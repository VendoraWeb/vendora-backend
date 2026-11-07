package controller

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"vendora/model"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 200, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDecodeDataURL(t *testing.T) {
	raw := tinyPNG(t)
	ok := "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)

	mime, data, msg := decodeDataURL(ok)
	if msg != "" || mime != "image/png" || !bytes.Equal(data, raw) {
		t.Fatalf("foto PNG valid ditolak: mime=%q msg=%q", mime, msg)
	}

	cases := map[string]string{
		"bukan data URL":        "https://contoh.com/a.png",
		"MIME bukan gambar":     "data:text/html;base64," + base64.StdEncoding.EncodeToString([]byte("<script>alert(1)</script>")),
		"label PNG isi HTML":    "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("<html><body>x</body></html>")),
		"base64 rusak":          "data:image/png;base64,@@@",
		"tanpa penanda base64":  "data:image/png," + string(raw),
		"lebih dari batas 3 MB": "data:image/jpeg;base64," + strings.Repeat("A", (maxChatImage/3+10)*4),
	}
	for name, in := range cases {
		if _, _, msg := decodeDataURL(in); msg == "" {
			t.Errorf("%s: seharusnya ditolak", name)
		}
	}
}

func TestResolveVariant(t *testing.T) {
	p := model.Product{
		Name: "iPhone 18 Pro", Price: 27499000, Images: []string{"utama.jpg"},
		Options: []model.OptionGroup{
			{Name: "Warna", Values: []model.OptionValue{{Label: "Burgundy", Image: "burgundy.jpg"}, {Label: "Silver"}}},
			{Name: "Kapasitas", Values: []model.OptionValue{{Label: "256 GB"}, {Label: "512 GB", PriceDelta: 5500000}}},
		},
	}
	price, label, img, msg := resolveVariant(p, map[string]string{"Warna": "burgundy", "Kapasitas": "512 GB"})
	if msg != "" || price != 32999000 || label != "Burgundy, 512 GB" || img != "burgundy.jpg" {
		t.Fatalf("variasi valid salah hitung: %v %q %q %q", price, label, img, msg)
	}
	if _, _, _, msg := resolveVariant(p, map[string]string{"Warna": "Burgundy"}); msg == "" {
		t.Error("kapasitas kosong seharusnya ditolak")
	}
	if _, _, _, msg := resolveVariant(p, map[string]string{"Warna": "Ungu", "Kapasitas": "256 GB"}); msg == "" {
		t.Error("warna yang tidak dijual seharusnya ditolak")
	}
	plain := model.Product{Name: "Topi", Price: 299000}
	if price, label, _, msg := resolveVariant(plain, nil); msg != "" || price != 299000 || label != "" {
		t.Error("produk tanpa variasi harus memakai harga dasar")
	}
}

func TestCleanOptions(t *testing.T) {
	in := []model.OptionGroup{
		{Name: " Ukuran ", Values: []model.OptionValue{{Label: "S"}, {Label: " s "}, {Label: ""}, {Label: "M"}}},
		{Name: "", Values: []model.OptionValue{{Label: "x"}}},
	}
	out, msg := cleanOptions(in, 100000)
	if msg != "" || len(out) != 1 || out[0].Name != "Ukuran" || len(out[0].Values) != 2 {
		t.Fatalf("hasil cleanOptions tidak sesuai: %+v %q", out, msg)
	}
	if _, msg := cleanOptions([]model.OptionGroup{{Name: "Paket", Values: []model.OptionValue{{Label: "Diskon", PriceDelta: -200000}}}}, 100000); msg == "" {
		t.Error("tambahan harga yang membuat harga minus seharusnya ditolak")
	}
}
