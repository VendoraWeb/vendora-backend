package helper

import (
	"strings"
	"unicode/utf8"
)

// MaskAddress menyamarkan alamat rumah pembeli/penjual untuk menjaga privasi dari Admin
func MaskAddress(address string) string {
	address = strings.TrimSpace(address)
	if address == "" {
		return "****"
	}
	words := strings.Fields(address)
	out := make([]string, len(words))
	for i, w := range words {
		rc := utf8.RuneCountInString(w)
		if rc <= 2 {
			out[i] = "**"
		} else if i == 0 {
			out[i] = maskRunes(w, 2, 0)
		} else {
			out[i] = maskRunes(w, 1, 0)
		}
	}
	return strings.Join(out, " ")
}

func maskRunes(s string, keepStart, keepEnd int) string {
	runes := []rune(s)
	n := len(runes)
	if n == 0 {
		return ""
	}
	if keepStart+keepEnd >= n {
		if n <= 1 {
			return "*"
		}
		return string(runes[0]) + strings.Repeat("*", n-1)
	}
	stars := n - keepStart - keepEnd
	if stars < 4 {
		stars = 4
	}
	if stars > 6 {
		stars = 6
	}
	prefix := string(runes[:keepStart])
	suffix := ""
	if keepEnd > 0 {
		suffix = string(runes[n-keepEnd:])
	}
	return prefix + strings.Repeat("*", stars) + suffix
}

// MaskAccount menyamarkan nomor rekening, menyisakan 3 digit terakhir.
func MaskAccount(acc string) string {
	acc = strings.TrimSpace(acc)
	if acc == "" {
		return "****"
	}
	return maskRunes(acc, 0, 3)
}
