package controller

import (
	"context"
	"strings"
	"time"

	"vendora/config"
	"vendora/helper"
	"vendora/model"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type ProductReq struct {
	ShopID      string               `json:"shop_id"`
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Category    *string              `json:"category"`
	Subcategory *string              `json:"subcategory"`
	Price       float64              `json:"price"`
	Stock       *int                 `json:"stock"`
	Images      []string             `json:"images"`
	Options     *[]model.OptionGroup `json:"options"`    // dikirim = diganti; [] = hapus semua variasi
	Specs       *[]model.Spec        `json:"specs"`      // dikirim = diganti
	SizeGuide   *model.SizeGuide     `json:"size_guide"` // kolom kosong = hapus panduan ukuran
}

const (
	maxOptionGroups = 3
	maxOptionValues = 12
)

// cleanOptions merapikan variasi dari penjual: buang label kosong/duplikat,
// batasi jumlah, dan tolak tambahan harga negatif yang membuat harga <= 0.
func cleanOptions(groups []model.OptionGroup, basePrice float64) ([]model.OptionGroup, string) {
	out := []model.OptionGroup{}
	for _, g := range groups {
		g.Name = strings.TrimSpace(g.Name)
		if g.Name == "" {
			continue
		}
		seen := map[string]bool{}
		vals := []model.OptionValue{}
		for _, v := range g.Values {
			v.Label = strings.TrimSpace(v.Label)
			key := strings.ToLower(v.Label)
			if v.Label == "" || seen[key] {
				continue
			}
			if basePrice > 0 && basePrice+v.PriceDelta <= 0 {
				return nil, "Tambahan harga variasi \"" + v.Label + "\" membuat harga jadi nol atau minus"
			}
			seen[key] = true
			vals = append(vals, v)
		}
		if len(vals) == 0 {
			continue
		}
		if len(vals) > maxOptionValues {
			return nil, "Maksimal 12 pilihan untuk variasi " + g.Name
		}
		g.Values = vals
		out = append(out, g)
	}
	if len(out) > maxOptionGroups {
		return nil, "Maksimal 3 jenis variasi per produk"
	}
	return out, ""
}

func AddProduct(c *fiber.Ctx) error {
	var req ProductReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request payload")
	}

	stock := 0
	if req.Stock != nil {
		stock = *req.Stock
	}
	if req.ShopID == "" || strings.TrimSpace(req.Name) == "" || req.Price <= 0 || stock < 0 {
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
		Name:        strings.TrimSpace(req.Name),
		Description: req.Description,
		Price:       req.Price,
		Stock:       stock,
		Images:      req.Images,
		CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
	}
	if req.Category != nil {
		newProduct.Category = strings.TrimSpace(*req.Category)
	}
	if req.Subcategory != nil {
		newProduct.Subcategory = strings.TrimSpace(*req.Subcategory)
	}
	if req.Options != nil {
		opts, msg := cleanOptions(*req.Options, req.Price)
		if msg != "" {
			return helper.ErrorResponse(c, fiber.StatusBadRequest, msg)
		}
		newProduct.Options = opts
	}
	if req.Specs != nil {
		newProduct.Specs = *req.Specs
	}
	if req.SizeGuide != nil && len(req.SizeGuide.Columns) > 0 {
		newProduct.SizeGuide = req.SizeGuide
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
	unsetFields := bson.M{}
	if strings.TrimSpace(req.Name) != "" {
		updateFields["name"] = strings.TrimSpace(req.Name)
	}
	if req.Description != "" {
		updateFields["description"] = req.Description
	}
	if req.Price > 0 {
		updateFields["price"] = req.Price
	}
	// Stok hanya diubah bila dikirim — sebelumnya stok jadi 0 saat field tidak ada
	if req.Stock != nil && *req.Stock >= 0 {
		updateFields["stock"] = *req.Stock
	}
	if len(req.Images) > 0 {
		updateFields["images"] = req.Images
	}
	if req.Category != nil {
		updateFields["category"] = strings.TrimSpace(*req.Category)
	}
	if req.Subcategory != nil {
		updateFields["subcategory"] = strings.TrimSpace(*req.Subcategory)
	}
	if req.Options != nil {
		price := req.Price
		if price <= 0 {
			var current model.Product
			if err := config.DB.Collection("products").FindOne(context.Background(), bson.M{"_id": oid}).Decode(&current); err == nil {
				price = current.Price
			}
		}
		opts, msg := cleanOptions(*req.Options, price)
		if msg != "" {
			return helper.ErrorResponse(c, fiber.StatusBadRequest, msg)
		}
		if len(opts) == 0 {
			unsetFields["options"] = ""
		} else {
			updateFields["options"] = opts
		}
	}
	if req.Specs != nil {
		if len(*req.Specs) == 0 {
			unsetFields["specs"] = ""
		} else {
			updateFields["specs"] = *req.Specs
		}
	}
	if req.SizeGuide != nil {
		if len(req.SizeGuide.Columns) == 0 {
			unsetFields["size_guide"] = ""
		} else {
			updateFields["size_guide"] = req.SizeGuide
		}
	}

	if len(updateFields) == 0 && len(unsetFields) == 0 {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "No fields to update")
	}

	update := bson.M{}
	if len(updateFields) > 0 {
		update["$set"] = updateFields
	}
	if len(unsetFields) > 0 {
		update["$unset"] = unsetFields
	}
	result, err := config.DB.Collection("products").UpdateOne(
		context.Background(),
		bson.M{"_id": oid},
		update,
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
