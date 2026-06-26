package model

import "go.mongodb.org/mongo-driver/bson/primitive"

type SupportTicket struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"id,omitempty"`
	SellerID   primitive.ObjectID `bson:"seller_id" json:"seller_id"`
	SellerName string             `bson:"seller_name" json:"seller_name"`
	Subject    string             `bson:"subject" json:"subject"`
	Message    string             `bson:"message" json:"message"`
	Reply      string             `bson:"reply" json:"reply"`
	Status     string             `bson:"status" json:"status"` // open, resolved
	CreatedAt  primitive.DateTime `bson:"created_at" json:"created_at"`
	UpdatedAt  primitive.DateTime `bson:"updated_at" json:"updated_at"`
}
