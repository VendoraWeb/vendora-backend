package model

import "go.mongodb.org/mongo-driver/bson/primitive"

// Report mencatat laporan pelanggaran produk atau toko dari pembeli/pengguna (koleksi reports).
type Report struct {
	ID           primitive.ObjectID  `bson:"_id,omitempty" json:"id,omitempty"`
	ReporterID   primitive.ObjectID  `bson:"reporter_id,omitempty" json:"reporter_id,omitempty"`
	ReporterName string              `bson:"reporter_name" json:"reporter_name"`
	TargetType   string              `bson:"target_type" json:"target_type"` // product | shop
	ProductID    primitive.ObjectID  `bson:"product_id,omitempty" json:"product_id,omitempty"`
	ProductName  string              `bson:"product_name,omitempty" json:"product_name,omitempty"`
	ShopID       primitive.ObjectID  `bson:"shop_id,omitempty" json:"shop_id,omitempty"`
	ShopName     string              `bson:"shop_name,omitempty" json:"shop_name,omitempty"`
	Reason       string              `bson:"reason" json:"reason"`
	Description  string              `bson:"description" json:"description"`
	Status       string              `bson:"status" json:"status"` // open | resolved | dismissed
	AdminNote    string              `bson:"admin_note,omitempty" json:"admin_note,omitempty"`
	CreatedAt    primitive.DateTime  `bson:"created_at" json:"created_at"`
	ResolvedAt   *primitive.DateTime `bson:"resolved_at,omitempty" json:"resolved_at,omitempty"`
}

