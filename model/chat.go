package model

import "go.mongodb.org/mongo-driver/bson/primitive"

type Message struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"id,omitempty"`
	SenderID   primitive.ObjectID `bson:"sender_id" json:"sender_id"`
	ReceiverID primitive.ObjectID `bson:"receiver_id" json:"receiver_id"`
	ShopID     primitive.ObjectID `bson:"shop_id" json:"shop_id"`
	Text       string             `bson:"text" json:"text"`
	CreatedAt  primitive.DateTime `bson:"created_at" json:"created_at"`
}
