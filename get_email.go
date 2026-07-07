//go:build ignore

package main


import (
	"context"
	"fmt"
	"log"
	"os"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	uri := os.Getenv("MONGO_URI")
	if uri == "" {
		uri = "mongodb+srv://nabilaa:nabil123@cluster0.grhnadb.mongodb.net/vendora_db?retryWrites=true&w=majority&appName=Cluster0"
	}
	client, err := mongo.Connect(context.TODO(), options.Client().ApplyURI(uri))
	if err != nil {
		log.Fatal(err)
	}
	db := client.Database("vendora_db")
	oid, _ := primitive.ObjectIDFromHex("6a4a4626b55724a59aecda91")
	var user bson.M
	db.Collection("users").FindOne(context.TODO(), bson.M{"_id": oid}).Decode(&user)
	fmt.Println("PUMA_EMAIL:", user["email"])
}
