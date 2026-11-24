package helper

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"regexp"
	"strings"
)

// otpSecret dipakai untuk HMAC kode OTP & token reset.
// Atur OTP_SECRET di environment. Bila kosong, diturunkan dari MONGO_URI
// supaya tetap stabil di semua instance serverless (bukan acak per proses).
func otpSecret() []byte {
	if s := os.Getenv("OTP_SECRET"); s != "" {
		return []byte(s)
	}
	sum := sha256.Sum256([]byte("vendora-otp:" + os.Getenv("MONGO_URI")))
	return sum[:]
}

// GenerateOTP menghasilkan kode angka 6 digit memakai crypto/rand.
func GenerateOTP() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// RandomToken menghasilkan token acak (hex) untuk reset kata sandi / sesi.
func RandomToken(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// HashSecret mengembalikan HMAC-SHA256(secret, value) dalam hex.
// scope membedakan kode OTP dari token reset untuk challenge yang sama.
func HashSecret(scope, challengeID, value string) string {
	mac := hmac.New(sha256.New, otpSecret())
	mac.Write([]byte(scope + ":" + challengeID + ":" + value))
	return hex.EncodeToString(mac.Sum(nil))
}

// SecretEqual membandingkan dua hash dengan waktu konstan.
func SecretEqual(a, b string) bool {
	return a != "" && b != "" && hmac.Equal([]byte(a), []byte(b))
}

var emailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// IsEmail mengecek format email sederhana.
func IsEmail(s string) bool { return emailRe.MatchString(strings.TrimSpace(s)) }

// NormalizePhone mengubah nomor Indonesia ke format 62xxxxxxxxxx.
// Menerima "0812-3456-7890", "+62 812 3456 7890", "812345678", dll.
// Mengembalikan "" bila tidak terlihat seperti nomor HP yang valid.
func NormalizePhone(raw string) string {
	var digits strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	d := digits.String()
	switch {
	case strings.HasPrefix(d, "62"):
		// sudah berkode negara
	case strings.HasPrefix(d, "0"):
		d = "62" + d[1:]
	case strings.HasPrefix(d, "8"):
		d = "62" + d
	default:
		return ""
	}
	// 62 + 8xx + 7–11 digit → total 10–15 digit, dan nomor seluler diawali 628
	if len(d) < 10 || len(d) > 15 || !strings.HasPrefix(d, "628") {
		return ""
	}
	return d
}

// MaskEmail: "nabilswallow@gmail.com" → "na**********@gmail.com"
func MaskEmail(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return "***"
	}
	name := parts[0]
	keep := 2
	if len(name) <= 2 {
		keep = 1
	}
	if len(name) < keep {
		keep = len(name)
	}
	return name[:keep] + strings.Repeat("*", max(len(name)-keep, 3)) + "@" + parts[1]
}

// MaskPhone: "6281234567890" → "+62 812-****-890"
func MaskPhone(e164 string) string {
	if len(e164) < 9 {
		return "***"
	}
	local := e164[2:]
	return "+62 " + local[:3] + "-****-" + local[len(local)-3:]
}
