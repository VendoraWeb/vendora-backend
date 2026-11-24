package model

import "go.mongodb.org/mongo-driver/bson/primitive"

type Message struct {
	ID         primitive.ObjectID  `bson:"_id,omitempty" json:"id,omitempty"`
	SenderID   primitive.ObjectID  `bson:"sender_id" json:"sender_id"`
	ReceiverID primitive.ObjectID  `bson:"receiver_id" json:"receiver_id"`
	ShopID     primitive.ObjectID  `bson:"shop_id" json:"shop_id"`
	Text       string              `bson:"text" json:"text"`
	Image      string              `bson:"image,omitempty" json:"image,omitempty"` // ID foto di koleksi chat_media
	ReadAt     *primitive.DateTime `bson:"read_at,omitempty" json:"read_at,omitempty"`
	Auto       bool                `bson:"auto,omitempty" json:"auto,omitempty"` // salam otomatis toko (dikirim sistem atas nama ruko)
	CreatedAt  primitive.DateTime  `bson:"created_at" json:"created_at"`
}

// ChatMedia menyimpan foto yang dikirim lewat chat (sudah dikompres di browser).
type ChatMedia struct {
	ID        primitive.ObjectID `bson:"_id,omitempty"`
	ShopID    primitive.ObjectID `bson:"shop_id"`
	SenderID  primitive.ObjectID `bson:"sender_id"`
	Mime      string             `bson:"mime"`
	Data      []byte             `bson:"data"`
	CreatedAt primitive.DateTime `bson:"created_at"`
}
