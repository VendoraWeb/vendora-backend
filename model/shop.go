package model

import "go.mongodb.org/mongo-driver/bson/primitive"

type Shop struct {
	ID            primitive.ObjectID `bson:"_id,omitempty" json:"id,omitempty"`
	Name          string             `bson:"name" json:"name"`
	Description   string             `bson:"description" json:"description"`
	OwnerID       primitive.ObjectID `bson:"owner_id" json:"owner_id"` // References User (role: seller)
	Status        string             `bson:"status" json:"status"`     // active, suspended, pending_payment
	RentalPrice   float64            `bson:"rental_price" json:"rental_price"` // Price per period (sewa ruko)
	RentalExpires primitive.DateTime `bson:"rental_expires" json:"rental_expires"`
	CreatedAt     primitive.DateTime `bson:"created_at" json:"created_at"`
}
