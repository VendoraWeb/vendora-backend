//go:build ignore

package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/crypto/bcrypt"
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

	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte("123456"), 10)

	res, err := db.Collection("users").UpdateOne(context.TODO(), bson.M{"email": "puma@vendora.com"}, bson.M{"$set": bson.M{"password": string(hashedPassword)}})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Modified %v document(s)\n", res.ModifiedCount)
}
