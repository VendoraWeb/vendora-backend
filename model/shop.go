package model

import "go.mongodb.org/mongo-driver/bson/primitive"

type Shop struct {
	ID             primitive.ObjectID  `bson:"_id,omitempty" json:"id,omitempty"`
	Name           string              `bson:"name" json:"name"`
	Description    string              `bson:"description" json:"description"`
	OwnerID        primitive.ObjectID  `bson:"owner_id" json:"owner_id"`         // References User (role: seller)
	Status         string              `bson:"status" json:"status"`             // pending (menunggu verifikasi admin), active, suspended
	RentalPrice    float64             `bson:"rental_price" json:"rental_price"` // total sewa yang sudah dibayarkan
	RentalExpires  primitive.DateTime  `bson:"rental_expires" json:"rental_expires"`
	Balance        float64             `bson:"balance" json:"balance"`                                     // Saldo aktif penjual (sudah cair dari Escrow & dipotong pajak)
	EscrowBalance  float64             `bson:"escrow_balance" json:"escrow_balance"`                       // Saldo tertahan di Escrow sampai pembeli terima barang
	TotalWithdrawn float64             `bson:"total_withdrawn,omitempty" json:"total_withdrawn,omitempty"` // Total dana yang sudah dicairkan ke rekening penjual
	City           string              `bson:"city,omitempty" json:"city,omitempty"`                       // Kota asal pengiriman ruko (mis. "Jakarta Pusat")
	Lat            float64             `bson:"lat,omitempty" json:"lat,omitempty"`                         // Koordinat peta asal toko
	Lng            float64             `bson:"lng,omitempty" json:"lng,omitempty"`
	ApprovedAt     *primitive.DateTime `bson:"approved_at,omitempty" json:"approved_at,omitempty"`
	CreatedAt      primitive.DateTime  `bson:"created_at" json:"created_at"`
}
