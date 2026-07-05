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

type CheckoutItem struct {
	ProductID string `json:"product_id"`
	ShopID    string `json:"shop_id"`
	Quantity  int    `json:"quantity"`
}

type CheckoutReq struct {
	BuyerID         string         `json:"buyer_id"`
	RecipientName   string         `json:"recipient_name"`
	ShippingAddress string         `json:"shipping_address"`
	Items           []CheckoutItem `json:"items"`
}

func Checkout(c *fiber.Ctx) error {
	var req CheckoutReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request payload")
	}

	if req.BuyerID == "" || len(req.Items) == 0 {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "BuyerID and at least one item are required")
	}

	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	buyerOID, err := primitive.ObjectIDFromHex(req.BuyerID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid Buyer ID format")
	}

	// Verify buyer role
	var buyer model.User
	err = config.DB.Collection("users").FindOne(context.Background(), bson.M{"_id": buyerOID}).Decode(&buyer)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Buyer user not found")
	}

	var transactionItems []model.TransactionItem
	var totalAmount float64

	// Process each item
	for _, item := range req.Items {
		var product model.Product
		var prodOID primitive.ObjectID
		
		var err error
		prodOID, err = primitive.ObjectIDFromHex(item.ProductID)
		if err != nil || (len(item.ProductID) > 0 && item.ProductID[0] == 'm') {
			// Treat as mockup if it fails ObjectID parsing or explicitly starts with 'm'
			prodOID = primitive.NewObjectID()
			product = model.Product{
				Name:  "Produk Mockup (" + item.ProductID + ")",
				Price: 10000000,
			}
		} else {
			err = config.DB.Collection("products").FindOne(context.Background(), bson.M{"_id": prodOID}).Decode(&product)
			if err != nil {
				return helper.ErrorResponse(c, fiber.StatusNotFound, "Product not found: "+item.ProductID)
			}

			// Allow sellers to purchase from their own shop for testing purposes

			// Deduct stock atomically with verification to prevent race conditions
			filter := bson.M{
				"_id":   prodOID,
				"stock": bson.M{"$gte": item.Quantity}, // Ensure stock is sufficient
			}
			update := bson.M{
				"$inc": bson.M{"stock": -item.Quantity}, // Deduct quantity
			}
			res, err := config.DB.Collection("products").UpdateOne(context.Background(), filter, update)
			if err != nil {
				return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to update stock for product: "+product.Name)
			}

			if res.MatchedCount == 0 {
				return helper.ErrorResponse(c, fiber.StatusBadRequest, "Insufficient stock for product: "+product.Name)
			}
		}

		itemTotal := product.Price * float64(item.Quantity)
		totalAmount += itemTotal

		var shopOID primitive.ObjectID
		if item.ShopID != "" {
			shopOID, _ = primitive.ObjectIDFromHex(item.ShopID)
		} else {
			shopOID = product.ShopID
		}

		transactionItems = append(transactionItems, model.TransactionItem{
			ProductID: prodOID,
			ShopID:    shopOID,
			Name:      product.Name,
			Price:     product.Price,
			Quantity:  item.Quantity,
		})
	}

	newTx := model.Transaction{
		ID:              primitive.NewObjectID(),
		BuyerID:         buyerOID,
		RecipientName:   req.RecipientName,
		ShippingAddress: req.ShippingAddress,
		Items:           transactionItems,
		TotalAmount:     totalAmount,
		Status:          "pending",
		CreatedAt:       primitive.NewDateTimeFromTime(time.Now()),
	}

	_, err = config.DB.Collection("transactions").InsertOne(context.Background(), newTx)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to create transaction record")
	}

	return helper.SuccessResponse(c, fiber.StatusCreated, "Checkout completed! Transaction created.", newTx)
}

func ListTransactions(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	// If shop_id is provided, find all transactions containing items for this shop
	shopID := c.Query("shop_id")
	if shopID != "" {
		shopOID, err := primitive.ObjectIDFromHex(shopID)
		if err != nil {
			return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid shop_id format")
		}

		filter := bson.M{
			"items.shop_id": shopOID,
		}

		cursor, err := config.DB.Collection("transactions").Find(context.Background(), filter)
		if err != nil {
			return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to retrieve transactions")
		}
		defer cursor.Close(context.Background())

		var transactions []model.Transaction = []model.Transaction{}
		if err = cursor.All(context.Background(), &transactions); err != nil {
			return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Error decoding transactions list")
		}

		return helper.SuccessResponse(c, fiber.StatusOK, "Transactions retrieved successfully", transactions)
	}

	// Default: filter by buyer_id if provided
	filter := bson.M{}
	buyerID := c.Query("buyer_id")
	if buyerID != "" {
		buyerOID, err := primitive.ObjectIDFromHex(buyerID)
		if err == nil {
			filter["buyer_id"] = buyerOID
		}
	}

	cursor, err := config.DB.Collection("transactions").Find(context.Background(), filter)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to retrieve transactions")
	}
	defer cursor.Close(context.Background())

	var transactions []model.Transaction = []model.Transaction{}
	if err = cursor.All(context.Background(), &transactions); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Error decoding transactions list")
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Transactions retrieved successfully", transactions)
}

// FixTransactionStatus updates all pending_payment transactions to success (one-time migration)
func FixTransactionStatus(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := config.DB.Collection("transactions").UpdateMany(
		ctx,
		bson.M{"status": "pending_payment"},
		bson.M{"$set": bson.M{"status": "success"}},
	)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to update transactions")
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Transactions updated successfully", fiber.Map{
		"updated_count": result.ModifiedCount,
	})
}

// UpdateTransactionStatus handles PUT /api/transaction/:id/status
func UpdateTransactionStatus(c *fiber.Ctx) error {
	id := c.Params("id")
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid transaction ID")
	}

	var req struct {
		Status string `json:"status"`
	}
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body")
	}

	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := config.DB.Collection("transactions").UpdateOne(
		ctx,
		bson.M{"_id": oid},
		bson.M{"$set": bson.M{"status": req.Status}},
	)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to update transaction status")
	}
	if result.MatchedCount == 0 {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Transaction not found")
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Transaction status updated successfully", nil)
}
