package helper

import (
	"regexp"
	"testing"
)

func TestNormalizePhone(t *testing.T) {
	cases := map[string]string{
		"0812-3456-7890":      "6281234567890",
		"+62 812 3456 7890":   "6281234567890",
		"6281234567890":       "6281234567890",
		"81234567890":         "6281234567890",
		"(0821) 3419 6609":    "6282134196609",
		"08123":               "", // terlalu pendek
		"021-5551234":         "", // telepon rumah, bukan seluler
		"abc":                 "",
		"":                    "",
		"0812345678901234567": "", // terlalu panjang
	}
	for in, want := range cases {
		if got := NormalizePhone(in); got != want {
			t.Errorf("NormalizePhone(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMasking(t *testing.T) {
	if got := MaskEmail("nabilswallow@gmail.com"); got != "na**********@gmail.com" {
		t.Errorf("MaskEmail = %q", got)
	}
	if got := MaskEmail("a@b.co"); got != "a***@b.co" {
		t.Errorf("MaskEmail short = %q", got)
	}
	if got := MaskPhone("6281234567890"); got != "+62 812-****-890" {
		t.Errorf("MaskPhone = %q", got)
	}
}

func TestOTPCodeAndHash(t *testing.T) {
	re := regexp.MustCompile(`^\d{6}$`)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		code, err := GenerateOTP()
		if err != nil || !re.MatchString(code) {
			t.Fatalf("GenerateOTP = %q, %v", code, err)
		}
		seen[code] = true
	}
	if len(seen) < 190 {
		t.Errorf("kode OTP kurang acak: hanya %d unik dari 200", len(seen))
	}

	h := HashSecret("otp", "abc", "123456")
	if !SecretEqual(h, HashSecret("otp", "abc", "123456")) {
		t.Error("hash yang sama harus cocok")
	}
	if SecretEqual(h, HashSecret("otp", "abc", "123457")) {
		t.Error("kode berbeda tidak boleh cocok")
	}
	if SecretEqual(h, HashSecret("otp", "xyz", "123456")) {
		t.Error("challenge berbeda tidak boleh cocok")
	}
	if SecretEqual(h, HashSecret("reset", "abc", "123456")) {
		t.Error("scope berbeda tidak boleh cocok")
	}
	if SecretEqual("", "") {
		t.Error("hash kosong tidak boleh dianggap cocok")
	}
}
