package model

import "go.mongodb.org/mongo-driver/bson/primitive"

type TransactionItem struct {
	ProductID primitive.ObjectID `bson:"product_id" json:"product_id"`
	Name      string             `bson:"name" json:"name"`
	Price     float64            `bson:"price" json:"price"`
	Quantity  int                `bson:"quantity" json:"quantity"`
}

type Transaction struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"id,omitempty"`
	BuyerID     primitive.ObjectID `bson:"buyer_id" json:"buyer_id"` // References User (role: buyer)
	Items       []TransactionItem  `bson:"items" json:"items"`
	TotalAmount float64            `bson:"total_amount" json:"total_amount"`
	Status      string             `bson:"status" json:"status"` // pending_payment, paid, success, cancelled
	CreatedAt   primitive.DateTime `bson:"created_at" json:"created_at"`
}
