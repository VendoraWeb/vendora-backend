package model

import "go.mongodb.org/mongo-driver/bson/primitive"

// Notification adalah notifikasi aplikasi untuk pembeli, penjual, atau broadcast semua pengguna.
type Notification struct {
	ID         primitive.ObjectID   `bson:"_id,omitempty" json:"id,omitempty"`
	UserID     primitive.ObjectID   `bson:"user_id,omitempty" json:"user_id,omitempty"`         // kosong bila broadcast
	TargetRole string               `bson:"target_role,omitempty" json:"target_role,omitempty"` // "all", "buyer", "seller", atau kosong (spesifik user)
	Type       string               `bson:"type" json:"type"`                                   // "promo", "order", "escrow", "system"
	Title      string               `bson:"title" json:"title"`
	Message    string               `bson:"message" json:"message"`
	Link       string               `bson:"link,omitempty" json:"link,omitempty"`
	Image      string               `bson:"image,omitempty" json:"image,omitempty"`
	ReadBy     []primitive.ObjectID `bson:"read_by,omitempty" json:"-"`
	IsRead     bool                 `bson:"-" json:"is_read"`
	CreatedAt  primitive.DateTime   `bson:"created_at" json:"created_at"`
}
