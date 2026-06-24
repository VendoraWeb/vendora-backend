package controller

import (
	"context"
	"time"

	"vendora/config"
	"vendora/helper"
	"vendora/model"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type ProductReq struct {
	ShopID      string   `json:"shop_id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Price       float64  `json:"price"`
	Stock       int      `json:"stock"`
	Images      []string `json:"images"`
}

func AddProduct(c *fiber.Ctx) error {
	var req ProductReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request payload")
	}

	if req.ShopID == "" || req.Name == "" || req.Price <= 0 || req.Stock < 0 {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "ShopID, Name, positive Price and valid Stock are required")
	}

	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	shopOID, err := primitive.ObjectIDFromHex(req.ShopID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid Shop ID format")
	}

	// Verify shop exists and is active
	var shop model.Shop
	err = config.DB.Collection("shops").FindOne(context.Background(), bson.M{"_id": shopOID}).Decode(&shop)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Associated Shop not found")
	}

	if shop.Status != "active" {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Cannot add product: Shop is inactive or suspended")
	}

	newProduct := model.Product{
		ID:          primitive.NewObjectID(),
		ShopID:      shopOID,
		Name:        req.Name,
		Description: req.Description,
		Price:       req.Price,
		Stock:       req.Stock,
		Images:      req.Images,
		CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
	}

	_, err = config.DB.Collection("products").InsertOne(context.Background(), newProduct)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to create product")
	}

	return helper.SuccessResponse(c, fiber.StatusCreated, "Product added successfully", newProduct)
}

func ListProducts(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	filter := bson.M{}
	shopID := c.Query("shop_id")
	if shopID != "" {
		shopOID, err := primitive.ObjectIDFromHex(shopID)
		if err == nil {
			filter["shop_id"] = shopOID
		}
	} else {
		// Filter out products from suspended shops in the buyer catalog
		activeCursor, err := config.DB.Collection("shops").Find(context.Background(), bson.M{"status": "active"})
		if err == nil {
			var activeShops []model.Shop
			if err = activeCursor.All(context.Background(), &activeShops); err == nil {
				var activeShopOIDs []primitive.ObjectID
				for _, shop := range activeShops {
					activeShopOIDs = append(activeShopOIDs, shop.ID)
				}
				if len(activeShopOIDs) == 0 {
					return helper.SuccessResponse(c, fiber.StatusOK, "Products list retrieved successfully", []model.Product{})
				}
				filter["shop_id"] = bson.M{"$in": activeShopOIDs}
			}
		}
	}

	cursor, err := config.DB.Collection("products").Find(context.Background(), filter)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to retrieve products")
	}
	defer cursor.Close(context.Background())

	var products []model.Product = []model.Product{}
	if err = cursor.All(context.Background(), &products); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Error decoding products list")
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Products list retrieved successfully", products)
}

func GetProduct(c *fiber.Ctx) error {
	productID := c.Params("id")
	if productID == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Product ID param is required")
	}

	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	oid, err := primitive.ObjectIDFromHex(productID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid Product ID format")
	}

	var product model.Product
	err = config.DB.Collection("products").FindOne(context.Background(), bson.M{"_id": oid}).Decode(&product)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Product not found")
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Product details retrieved successfully", product)
}

func UpdateProduct(c *fiber.Ctx) error {
	productID := c.Params("id")
	if productID == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Product ID param is required")
	}

	var req ProductReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request payload")
	}

	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	oid, err := primitive.ObjectIDFromHex(productID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid Product ID format")
	}

	updateFields := bson.M{}
	if req.Name != "" {
		updateFields["name"] = req.Name
	}
	if req.Description != "" {
		updateFields["description"] = req.Description
	}
	if req.Price > 0 {
		updateFields["price"] = req.Price
	}
	if req.Stock >= 0 {
		updateFields["stock"] = req.Stock
	}
	if len(req.Images) > 0 {
		updateFields["images"] = req.Images
	}

	if len(updateFields) == 0 {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "No fields to update")
	}

	result, err := config.DB.Collection("products").UpdateOne(
		context.Background(),
		bson.M{"_id": oid},
		bson.M{"$set": updateFields},
	)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to update product")
	}

	if result.MatchedCount == 0 {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Product not found")
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Product updated successfully", nil)
}

func DeleteProduct(c *fiber.Ctx) error {
	productID := c.Params("id")
	if productID == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Product ID param is required")
	}

	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	oid, err := primitive.ObjectIDFromHex(productID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid Product ID format")
	}

	result, err := config.DB.Collection("products").DeleteOne(context.Background(), bson.M{"_id": oid})
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to delete product")
	}

	if result.DeletedCount == 0 {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Product not found")
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Product deleted successfully", nil)
}
