package model

import "go.mongodb.org/mongo-driver/bson/primitive"

type User struct {
	ID       primitive.ObjectID `bson:"_id,omitempty" json:"id,omitempty"`
	Name     string             `bson:"name" json:"name"`
	Email    string             `bson:"email" json:"email"`
	Password string             `bson:"password" json:"-"` // Hidden in JSON responses
	Role     string             `bson:"role" json:"role"`  // admin, seller, buyer
	HasShop  bool               `bson:"has_shop" json:"has_shop"`
	ShopID   primitive.ObjectID `bson:"shop_id,omitempty" json:"shop_id,omitempty"`
	ShopName string             `bson:"shop_name,omitempty" json:"shop_name,omitempty"`
	Phone    string             `bson:"phone" json:"phone"`
	// PhoneE164 adalah nomor yang sudah dinormalisasi (628xxxxxxxxx) untuk pencarian & OTP.
	PhoneE164 string `bson:"phone_e164,omitempty" json:"-"`
	Address   string `bson:"address" json:"address"`
	Avatar    string `bson:"avatar" json:"avatar"`
	Banned    bool   `bson:"banned" json:"banned"`
	// Diset true setelah pengguna berhasil memasukkan OTP yang dikirim ke saluran tersebut.
	EmailVerified bool               `bson:"email_verified" json:"email_verified"`
	PhoneVerified bool               `bson:"phone_verified" json:"phone_verified"`
	CreatedAt     primitive.DateTime `bson:"created_at" json:"created_at"`
}
