package model

import "go.mongodb.org/mongo-driver/bson/primitive"

// OptionValue adalah satu pilihan variasi, mis. "Burgundy" atau "512 GB".
type OptionValue struct {
	Label      string  `bson:"label" json:"label"`
	Swatch     string  `bson:"swatch,omitempty" json:"swatch,omitempty"`           // warna hex untuk bulatan warna
	Image      string  `bson:"image,omitempty" json:"image,omitempty"`             // foto yang tampil saat pilihan ini dipilih
	PriceDelta float64 `bson:"price_delta,omitempty" json:"price_delta,omitempty"` // tambahan harga dari harga dasar
}

// OptionGroup adalah satu jenis variasi, mis. "Warna", "Kapasitas", "Ukuran".
type OptionGroup struct {
	Name   string        `bson:"name" json:"name"`
	Values []OptionValue `bson:"values" json:"values"`
}

// Spec adalah satu baris spesifikasi produk.
type Spec struct {
	Label string `bson:"label" json:"label"`
	Value string `bson:"value" json:"value"`
}

// SizeGuide adalah tabel panduan ukuran (baju, sepatu).
type SizeGuide struct {
	Note    string     `bson:"note,omitempty" json:"note,omitempty"`
	Columns []string   `bson:"columns" json:"columns"`
	Rows    [][]string `bson:"rows" json:"rows"`
}

type Product struct {
	ID              primitive.ObjectID `bson:"_id,omitempty" json:"id,omitempty"`
	ShopID          primitive.ObjectID `bson:"shop_id" json:"shop_id"` // References Shop
	Name            string             `bson:"name" json:"name"`
	Description     string             `bson:"description" json:"description"`
	Category        string             `bson:"category,omitempty" json:"category,omitempty"`       // kunci kategori utama, mis. "elektronik"
	Subcategory     string             `bson:"subcategory,omitempty" json:"subcategory,omitempty"` // mis. "Handphone & Tablet"
	Price           float64            `bson:"price" json:"price"`
	PromoPrice      float64            `bson:"promo_price,omitempty" json:"promo_price,omitempty"`           // harga setelah diskon (0 = tidak sedang promo)
	DiscountPercent int                `bson:"discount_percent,omitempty" json:"discount_percent,omitempty"` // persentase diskon otomatis
	PromoLabel      string             `bson:"promo_label,omitempty" json:"promo_label,omitempty"`           // mis. "Flash Sale", "Promo Gajian"
	Stock           int                `bson:"stock" json:"stock"`
	Images          []string           `bson:"images" json:"images"`
	Options         []OptionGroup      `bson:"options,omitempty" json:"options,omitempty"`
	Specs           []Spec             `bson:"specs,omitempty" json:"specs,omitempty"`
	SizeGuide       *SizeGuide         `bson:"size_guide,omitempty" json:"size_guide,omitempty"`
	Hidden          bool               `bson:"hidden,omitempty" json:"hidden,omitempty"` // disembunyikan oleh admin (moderasi)
	ModerationNote  string             `bson:"moderation_note,omitempty" json:"moderation_note,omitempty"`
	CreatedAt       primitive.DateTime `bson:"created_at" json:"created_at"`
}

// EffectivePrice mengembalikan harga aktif produk (PromoPrice bila valid, jika tidak Price normal).
func (p Product) EffectivePrice() float64 {
	if p.PromoPrice > 0 && p.PromoPrice < p.Price {
		return p.PromoPrice
	}
	return p.Price
}
