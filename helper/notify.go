package helper

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"strings"
	"time"
)

// ErrNoProvider: belum ada penyedia pengiriman yang dikonfigurasi untuk saluran ini.
var ErrNoProvider = errors.New("no OTP provider configured")

var httpClient = &http.Client{Timeout: 12 * time.Second}

// Konfigurasi lewat environment variable:
//
//   Email (pilih salah satu):
//     RESEND_API_KEY, OTP_EMAIL_FROM            → Resend (https://resend.com)
//     SMTP_HOST, SMTP_PORT, SMTP_USER, SMTP_PASS, SMTP_FROM
//                                               → SMTP biasa, mis. Gmail + App Password
//   Nomor HP (pilih salah satu):
//     FONNTE_TOKEN                              → WhatsApp lewat Fonnte (https://fonnte.com)
//     TWILIO_ACCOUNT_SID, TWILIO_AUTH_TOKEN, TWILIO_FROM
//                                               → SMS lewat Twilio
//   Pengembangan lokal:
//     OTP_DEV_LOG=true  → bila penyedia belum diatur, kode dicetak di log server
//                         (JANGAN aktifkan di production).

func devLogEnabled() bool { return strings.EqualFold(os.Getenv("OTP_DEV_LOG"), "true") }

func purposeText(purpose string) string {
	if purpose == "reset" {
		return "mengatur ulang kata sandi"
	}
	return "masuk ke akun"
}

// OTPMessage adalah teks pendek untuk WhatsApp/SMS.
func OTPMessage(code, purpose string) string {
	return fmt.Sprintf("Kode verifikasi Vendora untuk %s: %s\nBerlaku 5 menit. Jangan berikan kode ini kepada siapa pun, termasuk yang mengaku dari Vendora.",
		purposeText(purpose), code)
}

// SendEmailOTP mengirim kode ke email. Mengembalikan devMode=true bila kode hanya dicetak ke log.
func SendEmailOTP(to, name, code, purpose string) (devMode bool, err error) {
	subject := fmt.Sprintf("%s adalah kode verifikasi Vendora kamu", code)
	html := emailHTML(name, code, purpose)
	text := OTPMessage(code, purpose)

	switch {
	case os.Getenv("RESEND_API_KEY") != "":
		return false, sendResend(to, subject, html, text)
	case os.Getenv("SMTP_HOST") != "":
		return false, sendSMTP(to, subject, html, text)
	case devLogEnabled():
		log.Printf("[OTP DEV] email ke %s (%s): %s", to, purpose, code)
		return true, nil
	}
	return false, ErrNoProvider
}

// SendPhoneOTP mengirim kode ke nomor HP (format 62xxxxxxxxxx).
func SendPhoneOTP(phoneE164, code, purpose string) (devMode bool, err error) {
	msg := OTPMessage(code, purpose)
	switch {
	case os.Getenv("FONNTE_TOKEN") != "":
		return false, sendFonnte(phoneE164, msg)
	case os.Getenv("TWILIO_ACCOUNT_SID") != "":
		return false, sendTwilio("+"+phoneE164, msg)
	case devLogEnabled():
		log.Printf("[OTP DEV] HP %s (%s): %s", phoneE164, purpose, code)
		return true, nil
	}
	return false, ErrNoProvider
}

func emailFrom() string {
	if f := os.Getenv("OTP_EMAIL_FROM"); f != "" {
		return f
	}
	// SMTP_FROM berisi alamat email saja (sama seperti di Proyek1_Vendora)
	if f := strings.TrimSpace(os.Getenv("SMTP_FROM")); f != "" {
		if strings.Contains(f, "<") {
			return f
		}
		return "Vendora <" + f + ">"
	}
	if u := os.Getenv("SMTP_USER"); u != "" {
		return "Vendora <" + u + ">"
	}
	return "Vendora <onboarding@resend.dev>"
}

// ── Resend ───────────────────────────────────────────────────────────────────
func sendResend(to, subject, html, text string) error {
	body, _ := json.Marshal(map[string]interface{}{
		"from": emailFrom(), "to": []string{to}, "subject": subject, "html": html, "text": text,
	})
	req, _ := http.NewRequest("POST", "https://api.resend.com/emails", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+os.Getenv("RESEND_API_KEY"))
	req.Header.Set("Content-Type", "application/json")
	return doRequest(req, "resend")
}

// ── SMTP (mis. Gmail: smtp.gmail.com:587 + App Password) ────────────────────
func sendSMTP(to, subject, html, text string) error {
	host := os.Getenv("SMTP_HOST")
	port := os.Getenv("SMTP_PORT")
	if port == "" {
		port = "587"
	}
	user := os.Getenv("SMTP_USER")
	pass := strings.ReplaceAll(os.Getenv("SMTP_PASS"), " ", "") // App Password Gmail sering disalin dengan spasi
	from := emailFrom()
	fromAddr := from
	if i := strings.Index(from, "<"); i >= 0 {
		fromAddr = strings.Trim(from[i:], "<>")
	}

	boundary := "vendora-otp-boundary"
	var msg strings.Builder
	msg.WriteString("From: " + from + "\r\n")
	msg.WriteString("To: " + to + "\r\n")
	msg.WriteString("Subject: " + subject + "\r\n")
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: multipart/alternative; boundary=" + boundary + "\r\n\r\n")
	msg.WriteString("--" + boundary + "\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + text + "\r\n")
	msg.WriteString("--" + boundary + "\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n" + html + "\r\n")
	msg.WriteString("--" + boundary + "--\r\n")

	var auth smtp.Auth
	if user != "" {
		auth = smtp.PlainAuth("", user, pass, host)
	}
	return smtp.SendMail(host+":"+port, auth, fromAddr, []string{to}, []byte(msg.String()))
}

// ── Fonnte (WhatsApp) ────────────────────────────────────────────────────────
func sendFonnte(phoneE164, message string) error {
	form := url.Values{}
	form.Set("target", phoneE164)
	form.Set("message", message)
	form.Set("countryCode", "62")
	req, _ := http.NewRequest("POST", "https://api.fonnte.com/send", strings.NewReader(form.Encode()))
	req.Header.Set("Authorization", os.Getenv("FONNTE_TOKEN"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fonnte: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	// Fonnte membalas 200 juga saat gagal; cek field "status" (bisa bool atau teks)
	var out struct {
		Status interface{} `json:"status"`
		Reason string      `json:"reason"`
	}
	_ = json.Unmarshal(raw, &out)
	failed := false
	switch v := out.Status.(type) {
	case bool:
		failed = !v
	case string:
		failed = strings.EqualFold(v, "false")
	}
	if resp.StatusCode >= 300 || failed {
		return fmt.Errorf("fonnte: %s", strings.TrimSpace(out.Reason+" "+string(raw)))
	}
	return nil
}

// ── Twilio (SMS) ─────────────────────────────────────────────────────────────
func sendTwilio(to, message string) error {
	sid := os.Getenv("TWILIO_ACCOUNT_SID")
	form := url.Values{}
	form.Set("To", to)
	form.Set("From", os.Getenv("TWILIO_FROM"))
	form.Set("Body", message)
	req, _ := http.NewRequest("POST", "https://api.twilio.com/2010-04-01/Accounts/"+sid+"/Messages.json", strings.NewReader(form.Encode()))
	req.SetBasicAuth(sid, os.Getenv("TWILIO_AUTH_TOKEN"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return doRequest(req, "twilio")
}

func doRequest(req *http.Request, name string) error {
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("%s: HTTP %d %s", name, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

func emailHTML(name, code, purpose string) string {
	if name == "" {
		name = "Pelanggan Vendora"
	}
	return fmt.Sprintf(`<!doctype html><html><body style="margin:0;background:#f5f5f3;font-family:Arial,Helvetica,sans-serif;color:#111">
<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="padding:32px 12px"><tr><td align="center">
<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="max-width:480px;background:#ffffff;border:1px solid #ebebe8;border-radius:10px">
<tr><td style="background:#0e0e0d;border-radius:10px 10px 0 0;padding:20px 28px;color:#ede6dc;font-size:15px;letter-spacing:6px;font-weight:600">VENDORA</td></tr>
<tr><td style="padding:28px">
<p style="margin:0 0 12px;font-size:15px">Halo %s,</p>
<p style="margin:0 0 20px;font-size:15px;line-height:1.5;color:#444">Gunakan kode berikut untuk %s di Vendora:</p>
<p style="margin:0 0 20px;font-size:34px;font-weight:700;letter-spacing:10px;font-family:'Courier New',monospace">%s</p>
<p style="margin:0 0 8px;font-size:13px;color:#6b6b6b;line-height:1.5">Kode berlaku 5 menit. Jangan berikan kode ini kepada siapa pun, termasuk yang mengaku dari Vendora.</p>
<p style="margin:0;font-size:13px;color:#6b6b6b;line-height:1.5">Tidak merasa meminta kode ini? Abaikan email ini dan segera ganti kata sandimu.</p>
</td></tr></table></td></tr></table></body></html>`, htmlEscape(name), purposeText(purpose), code)
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}
