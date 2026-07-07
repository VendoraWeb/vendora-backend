//go:build ignore

package main


import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	client, err := mongo.Connect(context.Background(), options.Client().ApplyURI("mongodb://localhost:27017"))
	if err != nil {
		panic(err)
	}
	defer client.Disconnect(context.Background())

	db := client.Database("vendora")
	col := db.Collection("messages")

	ownerOID := primitive.NewObjectID()
	buyerOID := primitive.NewObjectID()
	shopOID := primitive.NewObjectID()

	_, err = col.InsertOne(context.Background(), bson.M{
		"sender_id":   buyerOID,
		"receiver_id": ownerOID,
		"shop_id":     shopOID,
		"text":        "Haii",
		"created_at":  primitive.NewDateTimeFromTime(time.Now()),
	})

	_, err = col.InsertOne(context.Background(), bson.M{
		"sender_id":   ownerOID,
		"receiver_id": buyerOID,
		"shop_id":     shopOID,
		"text":        "Terima kasih!",
		"created_at":  primitive.NewDateTimeFromTime(time.Now()),
	})

	pipeline := []bson.M{
		{"$match": bson.M{"shop_id": shopOID}},
		{"$sort": bson.M{"created_at": -1}},
		{"$group": bson.M{
			"_id": bson.M{
				"$cond": []interface{}{
					bson.M{"$eq": []interface{}{"$sender_id", ownerOID}},
					"$receiver_id",
					"$sender_id",
				},
			},
			"last_message": bson.M{"$first": "$text"},
			"updated_at":   bson.M{"$first": "$created_at"},
		}},
	}

	cursor, err := col.Aggregate(context.Background(), pipeline)
	if err != nil {
		panic(err)
	}

	var results []bson.M
	cursor.All(context.Background(), &results)
	fmt.Printf("Results: %+v\n", results)
}
