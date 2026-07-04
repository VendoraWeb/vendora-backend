package model

import "go.mongodb.org/mongo-driver/bson/primitive"

type User struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id,omitempty"`
	Name      string             `bson:"name" json:"name"`
	Email     string             `bson:"email" json:"email"`
	Password  string             `bson:"password" json:"-"` // Hidden in JSON responses
	Role      string             `bson:"role" json:"role"`   // admin, seller, buyer
	HasShop   bool               `bson:"has_shop" json:"has_shop"`
	ShopID    primitive.ObjectID `bson:"shop_id,omitempty" json:"shop_id,omitempty"`
	ShopName  string             `bson:"shop_name,omitempty" json:"shop_name,omitempty"`
	Phone     string             `bson:"phone" json:"phone"`
	Address   string             `bson:"address" json:"address"`
	Avatar    string             `bson:"avatar" json:"avatar"`
	CreatedAt primitive.DateTime `bson:"created_at" json:"created_at"`
}
