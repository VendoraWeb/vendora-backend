package controller

import (
	"context"
	"log"
	"time"

	"vendora/config"
	"vendora/helper"
	"vendora/model"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type RentShopReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	OwnerID     string `json:"owner_id"` // Owner ID (role: seller)
	RentalDays  int    `json:"rental_days"` // Days of rental (e.g. 30 days, 365 days)
}

func RentShop(c *fiber.Ctx) error {
	var req RentShopReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request payload")
	}

	if req.Name == "" || req.OwnerID == "" || req.RentalDays <= 0 {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Name, owner_id, and positive rental_days are required")
	}

	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	ownerOID, err := primitive.ObjectIDFromHex(req.OwnerID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid Owner ID format")
	}

	// Verify owner is a Seller
	var owner model.User
	err = config.DB.Collection("users").FindOne(context.Background(), bson.M{"_id": ownerOID}).Decode(&owner)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Owner user not found")
	}

	if owner.Role != "seller" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Only users with the 'seller' role can rent a shop")
	}

	// Calculate rent fee (e.g., $10 per day or IDR 10,000 per day)
	rentalPrice := float64(req.RentalDays) * 15000 // Mock price logic: IDR 15,000/day
	expiryDate := time.Now().AddDate(0, 0, req.RentalDays)

	newShop := model.Shop{
		ID:            primitive.NewObjectID(),
		Name:          req.Name,
		Description:   req.Description,
		OwnerID:       ownerOID,
		Status:        "active",
		RentalPrice:   rentalPrice,
		RentalExpires: primitive.NewDateTimeFromTime(expiryDate),
		CreatedAt:     primitive.NewDateTimeFromTime(time.Now()),
	}

	_, err = config.DB.Collection("shops").InsertOne(context.Background(), newShop)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to register/rent shop space")
	}

	// Update data user di collection 'users' agar has_shop = true dan shop_id terisi
	_, err = config.DB.Collection("users").UpdateOne(
		context.Background(),
		bson.M{"_id": ownerOID},
		bson.M{"$set": bson.M{"has_shop": true, "shop_id": newShop.ID}},
	)
	if err != nil {
		log.Printf("Gagal memperbarui status toko user: %v", err)
	}

	return helper.SuccessResponse(c, fiber.StatusCreated, "Shop rented successfully! Space allocated.", newShop)
}

func ListShops(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	cursor, err := config.DB.Collection("shops").Find(context.Background(), bson.M{})
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to retrieve shops")
	}
	defer cursor.Close(context.Background())

	var shops []model.Shop = []model.Shop{}
	if err = cursor.All(context.Background(), &shops); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Error decoding shops data")
	}

	var responseShops []fiber.Map = []fiber.Map{}
	for _, s := range shops {
		var owner model.User
		err = config.DB.Collection("users").FindOne(context.Background(), bson.M{"_id": s.OwnerID}).Decode(&owner)
		phone := ""
		address := ""
		if err == nil {
			phone = owner.Phone
			address = owner.Address
		}
		responseShops = append(responseShops, fiber.Map{
			"id":             s.ID,
			"name":           s.Name,
			"description":    s.Description,
			"owner_id":       s.OwnerID,
			"status":         s.Status,
			"rental_price":   s.RentalPrice,
			"rental_expires": s.RentalExpires,
			"created_at":     s.CreatedAt,
			"owner_phone":    phone,
			"owner_address":  address,
		})
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Shops list retrieved successfully", responseShops)
}

func UpdateShopStatus(c *fiber.Ctx) error {
	shopID := c.Params("id")
	if shopID == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Shop ID param is required")
	}

	type StatusUpdate struct {
		Status string `json:"status"` // active, suspended
	}

	var req StatusUpdate
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request payload")
	}

	if req.Status != "active" && req.Status != "suspended" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Status must be active or suspended")
	}

	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	oid, err := primitive.ObjectIDFromHex(shopID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid Shop ID format")
	}

	result, err := config.DB.Collection("shops").UpdateOne(
		context.Background(),
		bson.M{"_id": oid},
		bson.M{"$set": bson.M{"status": req.Status}},
	)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to update shop status")
	}

	if result.MatchedCount == 0 {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Shop not found")
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Shop status updated successfully", nil)
}

type RenewShopReq struct {
	RentalDays int `json:"rental_days"`
}

// RenewShop extends the rental expiry of an existing shop
func RenewShop(c *fiber.Ctx) error {
	shopID := c.Params("id")
	if shopID == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Shop ID param is required")
	}

	var req RenewShopReq
	if err := c.BodyParser(&req); err != nil || req.RentalDays <= 0 {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "rental_days must be a positive integer")
	}

	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	oid, err := primitive.ObjectIDFromHex(shopID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid Shop ID format")
	}

	// Fetch current shop to get existing expiry
	var shop model.Shop
	err = config.DB.Collection("shops").FindOne(context.Background(), bson.M{"_id": oid}).Decode(&shop)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Shop not found")
	}

	// Extend from current expiry (or now if already expired)
	currentExpiry := shop.RentalExpires.Time()
	if currentExpiry.Before(time.Now()) {
		currentExpiry = time.Now()
	}
	newExpiry := currentExpiry.AddDate(0, 0, req.RentalDays)
	additionalCost := float64(req.RentalDays) * 15000

	result, err := config.DB.Collection("shops").UpdateOne(
		context.Background(),
		bson.M{"_id": oid},
		bson.M{"$set": bson.M{
			"rental_expires": primitive.NewDateTimeFromTime(newExpiry),
			"rental_price":   shop.RentalPrice + additionalCost,
		}},
	)
	if err != nil || result.MatchedCount == 0 {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to renew shop")
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Shop renewed successfully", fiber.Map{
		"new_expiry":      newExpiry.Format("2006-01-02"),
		"additional_cost": additionalCost,
	})
}


