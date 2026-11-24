package helper

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestMaskAddressAndAccount(t *testing.T) {
	if got := MaskAddress(""); got != "****" {
		t.Errorf("MaskAddress('') = %q, want ****", got)
	}
	if got := MaskAddress("   "); got != "****" {
		t.Errorf("MaskAddress(spaces) = %q, want ****", got)
	}
	masked := MaskAddress("Jl. Merdeka No 12 Bandung")
	if strings.Contains(masked, "Merdeka") || strings.Contains(masked, "Bandung") {
		t.Errorf("MaskAddress leaked plaintext: %q", masked)
	}
	if got := MaskAddress("A B"); got != "** **" {
		t.Errorf("MaskAddress short words = %q, want '** **'", got)
	}
	if got := maskRunes("", 1, 0); got != "" {
		t.Errorf("maskRunes empty = %q", got)
	}
	if got := maskRunes("X", 2, 0); got != "*" {
		t.Errorf("maskRunes single = %q", got)
	}
	if got := maskRunes("AB", 2, 0); got != "A*" {
		t.Errorf("maskRunes short = %q", got)
	}
	if got := maskRunes("SuperLongWordForMasking", 2, 0); !strings.HasPrefix(got, "Su******") {
		t.Errorf("maskRunes cap 6 stars = %q", got)
	}

	if got := MaskAccount(""); got != "****" {
		t.Errorf("MaskAccount('') = %q, want ****", got)
	}
	if got := MaskAccount("1234567890"); !strings.HasSuffix(got, "890") || strings.Contains(got, "1234") {
		t.Errorf("MaskAccount = %q, expected masked prefix with 890 suffix", got)
	}
}

func TestEmailAndRandomToken(t *testing.T) {
	if !IsEmail("user@vendora.com") || !IsEmail("  nabil.test+1@ulbi.ac.id ") {
		t.Error("valid emails should pass IsEmail")
	}
	for _, bad := range []string{"", "not-an-email", "a@b", "@domain.com", "a b@c.com"} {
		if IsEmail(bad) {
			t.Errorf("IsEmail(%q) should be false", bad)
		}
	}

	tok1, err := RandomToken(24)
	if err != nil || len(tok1) != 48 {
		t.Fatalf("RandomToken(24) = %q, err=%v", tok1, err)
	}
	tok2, _ := RandomToken(24)
	if tok1 == tok2 {
		t.Error("RandomToken should generate unique values")
	}
}

func TestOTPMessageAndDevProviders(t *testing.T) {
	if msg := OTPMessage("123456", "reset"); !strings.Contains(msg, "123456") || !strings.Contains(msg, "mengatur ulang kata sandi") {
		t.Errorf("unexpected reset OTPMessage: %q", msg)
	}
	if msg := OTPMessage("654321", "login"); !strings.Contains(msg, "654321") || !strings.Contains(msg, "masuk ke akun") {
		t.Errorf("unexpected login OTPMessage: %q", msg)
	}

	// Tanpa provider dan tanpa OTP_DEV_LOG → ErrNoProvider
	t.Setenv("RESEND_API_KEY", "")
	t.Setenv("SMTP_HOST", "")
	t.Setenv("FONNTE_TOKEN", "")
	t.Setenv("TWILIO_ACCOUNT_SID", "")
	t.Setenv("OTP_DEV_LOG", "false")
	if _, err := SendEmailOTP("test@vendora.com", "Nabil", "111222", "reset"); err != ErrNoProvider {
		t.Errorf("expected ErrNoProvider for email, got %v", err)
	}
	if _, err := SendPhoneOTP("6281234567890", "111222", "reset"); err != ErrNoProvider {
		t.Errorf("expected ErrNoProvider for phone, got %v", err)
	}

	// Dengan OTP_DEV_LOG=true → sukses dalam mode dev tanpa jaringan
	t.Setenv("OTP_DEV_LOG", "true")
	if dev, err := SendEmailOTP("test@vendora.com", "Nabil <script>", "111222", "reset"); err != nil || !dev {
		t.Errorf("expected dev mode email OTP, got dev=%v err=%v", dev, err)
	}
	if dev, err := SendPhoneOTP("6281234567890", "111222", "reset"); err != nil || !dev {
		t.Errorf("expected dev mode phone OTP, got dev=%v err=%v", dev, err)
	}

	// Cek emailFrom fallback & HTML escape
	t.Setenv("OTP_EMAIL_FROM", "")
	t.Setenv("SMTP_FROM", "noreply@vendorashop.my.id")
	if got := emailFrom(); got != "Vendora <noreply@vendorashop.my.id>" {
		t.Errorf("emailFrom SMTP_FROM = %q", got)
	}
	t.Setenv("SMTP_FROM", "")
	t.Setenv("SMTP_USER", "bot@vendorashop.my.id")
	if got := emailFrom(); got != "Vendora <bot@vendorashop.my.id>" {
		t.Errorf("emailFrom SMTP_USER = %q", got)
	}
	t.Setenv("SMTP_USER", "")
	if got := emailFrom(); got != "Vendora <onboarding@resend.dev>" {
		t.Errorf("emailFrom default = %q", got)
	}
	if escaped := emailHTML("", "999888", "login"); !strings.Contains(escaped, "Pelanggan Vendora") || !strings.Contains(escaped, "999888") {
		t.Errorf("emailHTML default name failed")
	}
	if escaped := htmlEscape(`<a href="x">&'`); escaped != `&lt;a href=&quot;x&quot;&gt;&amp;&#39;` {
		t.Errorf("htmlEscape = %q", escaped)
	}
}

func TestHTTPResponses(t *testing.T) {
	app := fiber.New()
	app.Get("/ok", func(c *fiber.Ctx) error {
		return SuccessResponse(c, fiber.StatusOK, "berhasil", fiber.Map{"x": 1})
	})
	app.Get("/err", func(c *fiber.Ctx) error {
		return ErrorResponse(c, fiber.StatusBadRequest, "data tidak valid")
	})

	resp, err := app.Test(httptest.NewRequest("GET", "/ok", nil))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("/ok status=%d err=%v", resp.StatusCode, err)
	}
	var body ApiResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Status != 200 || body.Message != "berhasil" {
		t.Fatalf("unexpected /ok body: %+v err=%v", body, err)
	}

	resp2, err := app.Test(httptest.NewRequest("GET", "/err", nil))
	if err != nil || resp2.StatusCode != 400 {
		t.Fatalf("/err status=%d err=%v", resp2.StatusCode, err)
	}
	var body2 ApiResponse
	if err := json.NewDecoder(resp2.Body).Decode(&body2); err != nil || body2.Status != 400 || body2.Message != "data tidak valid" {
		t.Fatalf("unexpected /err body: %+v err=%v", body2, err)
	}
}
