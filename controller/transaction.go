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
	Quantity  int    `json:"quantity"`
}

type CheckoutReq struct {
	BuyerID string         `json:"buyer_id"`
	Items   []CheckoutItem `json:"items"`
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
		prodOID, err := primitive.ObjectIDFromHex(item.ProductID)
		if err != nil {
			return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid Product ID: "+item.ProductID)
		}

		var product model.Product
		err = config.DB.Collection("products").FindOne(context.Background(), bson.M{"_id": prodOID}).Decode(&product)
		if err != nil {
			return helper.ErrorResponse(c, fiber.StatusNotFound, "Product not found: "+item.ProductID)
		}

		// Prevent sellers from purchasing products from their own shop
		if buyer.Role == "seller" && buyer.HasShop {
			if product.ShopID == buyer.ShopID {
				return helper.ErrorResponse(c, fiber.StatusBadRequest, "Cannot checkout: You cannot purchase products from your own shop")
			}
		}

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

		itemTotal := product.Price * float64(item.Quantity)
		totalAmount += itemTotal

		transactionItems = append(transactionItems, model.TransactionItem{
			ProductID: prodOID,
			Name:      product.Name,
			Price:     product.Price,
			Quantity:  item.Quantity,
		})
	}

	newTx := model.Transaction{
		ID:          primitive.NewObjectID(),
		BuyerID:     buyerOID,
		Items:       transactionItems,
		TotalAmount: totalAmount,
		Status:      "success",
		CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
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

	// If shop_id is provided, find all product IDs belonging to that shop first
	shopID := c.Query("shop_id")
	if shopID != "" {
		shopOID, err := primitive.ObjectIDFromHex(shopID)
		if err != nil {
			return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid shop_id format")
		}

		// Get all product IDs from this shop
		prodCursor, err := config.DB.Collection("products").Find(
			context.Background(),
			bson.M{"shop_id": shopOID},
		)
		if err != nil {
			return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to retrieve shop products")
		}
		defer prodCursor.Close(context.Background())

		var products []struct {
			ID primitive.ObjectID `bson:"_id"`
		}
		if err = prodCursor.All(context.Background(), &products); err != nil {
			return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Error decoding products")
		}

		productIDs := make([]primitive.ObjectID, 0, len(products))
		for _, p := range products {
			productIDs = append(productIDs, p.ID)
		}

		// Find all transactions that contain at least one product from this shop
		filter := bson.M{
			"items.product_id": bson.M{"$in": productIDs},
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
