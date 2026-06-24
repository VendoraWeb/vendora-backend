package model

import "go.mongodb.org/mongo-driver/bson/primitive"

type Product struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"id,omitempty"`
	ShopID      primitive.ObjectID `bson:"shop_id" json:"shop_id"` // References Shop
	Name        string             `bson:"name" json:"name"`
	Description string             `bson:"description" json:"description"`
	Price       float64            `bson:"price" json:"price"`
	Stock       int                `bson:"stock" json:"stock"`
	Images      []string           `bson:"images" json:"images"`
	CreatedAt   primitive.DateTime `bson:"created_at" json:"created_at"`
}
