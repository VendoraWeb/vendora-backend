package config

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"vendora/model"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/crypto/bcrypt"
)

var DB *mongo.Database

func loadEnv() {
	file, err := os.Open(".env")
	if err != nil {
		return // Ignore error, fallback to environment
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			val = strings.Trim(val, `"'`)
			os.Setenv(key, val)
		}
	}
}

func ConnectDB() {
	loadEnv()

	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}

	dbName := os.Getenv("DB_NAME")
	if dbName == "" {
		dbName = "vendora_db"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		fmt.Printf("WARNING: Failed to connect to MongoDB: %v\n", err)
		return
	}

	// Ping database
	err = client.Ping(ctx, nil)
	if err != nil {
		fmt.Printf("WARNING: Failed to ping MongoDB: %v\n", err)
		return
	}

	fmt.Println("Successfully connected to MongoDB!")
	DB = client.Database(dbName)

	// Run Seed Data
	seedData()
}

func seedData() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	usersCol := DB.Collection("users")
	count, err := usersCol.CountDocuments(ctx, bson.M{})
	if err != nil || count > 0 {
		return // Already seeded or database connection error
	}

	fmt.Println("Seeding default database records...")

	hashPassword := func(pw string) string {
		h, _ := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
		return string(h)
	}

	// 1. Create Admin
	adminID := primitive.NewObjectID()
	adminUser := model.User{
		ID:        adminID,
		Name:      "Administrator System",
		Email:     "admin@vendora.com",
		Password:  hashPassword("admin123"),
		Role:      "admin",
		CreatedAt: primitive.NewDateTimeFromTime(time.Now()),
	}
	_, _ = usersCol.InsertOne(ctx, adminUser)

	// 2. Create Seller
	sellerID := primitive.NewObjectID()
	sellerUser := model.User{
		ID:        sellerID,
		Name:      "Nabila Store Official",
		Email:     "seller@vendora.com",
		Password:  hashPassword("seller123"),
		Role:      "seller",
		CreatedAt: primitive.NewDateTimeFromTime(time.Now()),
	}
	_, _ = usersCol.InsertOne(ctx, sellerUser)

	// 3. Create Buyer
	buyerID := primitive.NewObjectID()
	buyerUser := model.User{
		ID:        buyerID,
		Name:      "Nabila Buyer",
		Email:     "buyer@vendora.com",
		Password:  hashPassword("buyer123"),
		Role:      "buyer",
		CreatedAt: primitive.NewDateTimeFromTime(time.Now()),
	}
	_, _ = usersCol.InsertOne(ctx, buyerUser)

	// 4. Create Active Shop
	shopID := primitive.NewObjectID()
	newShop := model.Shop{
		ID:            shopID,
		Name:          "Nabila Premium Goods",
		Description:   "Pusat aksesoris, sepatu olahraga, dan gadget premium original berkualitas tinggi.",
		OwnerID:       sellerID,
		Status:        "active",
		RentalPrice:   450000,
		RentalExpires: primitive.NewDateTimeFromTime(time.Now().AddDate(0, 0, 30)),
		CreatedAt:     primitive.NewDateTimeFromTime(time.Now()),
	}
	_, _ = DB.Collection("shops").InsertOne(ctx, newShop)

	// 5. Create 3 products with real Unsplash image URLs
	products := []interface{}{
		model.Product{
			ID:          primitive.NewObjectID(),
			ShopID:      shopID,
			Name:        "Premium ANC Wireless Headphones",
			Description: "Headphone wireless high-fidelity dengan fitur Active Noise Cancelling dan daya tahan baterai hingga 40 jam.",
			Price:       1899000,
			Stock:       15,
			Images:      []string{"https://images.unsplash.com/photo-1505740420928-5e560c06d30e?w=800"},
			CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
		},
		model.Product{
			ID:          primitive.NewObjectID(),
			ShopID:      shopID,
			Name:        "AeroSport Runner Shoes Red",
			Description: "Sepatu lari didesain dengan sirkulasi udara optimal dan teknologi cushion responsif untuk kenyamanan ekstra.",
			Price:       1249000,
			Stock:       25,
			Images:      []string{"https://images.unsplash.com/photo-1542291026-7eec264c27ff?w=800"},
			CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
		},
		model.Product{
			ID:          primitive.NewObjectID(),
			ShopID:      shopID,
			Name:        "Minimalist Urban Leather Backpack",
			Description: "Tas ransel kulit sintetis berkualitas premium dengan slot laptop khusus, sangat cocok untuk kerja maupun kuliah.",
			Price:       899000,
			Stock:       10,
			Images:      []string{"https://images.unsplash.com/photo-1553062407-98eeb64c6a62?w=800"},
			CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
		},
	}
	_, _ = DB.Collection("products").InsertMany(ctx, products)
	fmt.Println("Seed data successfully injected!")
}
