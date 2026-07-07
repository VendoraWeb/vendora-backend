//go:build ignore

package main


import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	godotenv.Load(".env")
	uri := os.Getenv("MONGO_URI")
	dbName := os.Getenv("DB_NAME")
	client, err := mongo.Connect(context.TODO(), options.Client().ApplyURI(uri))
	if err != nil {
		log.Fatal(err)
	}
	db := client.Database(dbName)
	coll := db.Collection("transactions")
	opts := options.Find().SetSort(bson.D{{"_id", -1}}).SetLimit(5)
	cursor, err := coll.Find(context.TODO(), bson.D{}, opts)
	if err != nil {
		log.Fatal(err)
	}
	var results []bson.M
	if err = cursor.All(context.TODO(), &results); err != nil {
		log.Fatal(err)
	}
	b, _ := json.MarshalIndent(results, "", "  ")
	fmt.Println(string(b))
}
