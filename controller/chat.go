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
	"go.mongodb.org/mongo-driver/mongo/options"
)

type SendMsgReq struct {
	SenderID   string `json:"sender_id"`
	ReceiverID string `json:"receiver_id"`
	ShopID     string `json:"shop_id"`
	Text       string `json:"text"`
}

func SendMessage(c *fiber.Ctx) error {
	var req SendMsgReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request payload")
	}

	if req.SenderID == "" || req.ShopID == "" || req.Text == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Missing required fields")
	}

	senderOID, err1 := primitive.ObjectIDFromHex(req.SenderID)
	shopOID, err2 := primitive.ObjectIDFromHex(req.ShopID)

	if err1 != nil || err2 != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid ID format")
	}

	var receiverOID primitive.ObjectID
	if req.ReceiverID == "" {
		// If ReceiverID is empty, assume Buyer sending to Shop Owner
		var shop model.Shop
		err := config.DB.Collection("shops").FindOne(context.Background(), bson.M{"_id": shopOID}).Decode(&shop)
		if err != nil {
			return helper.ErrorResponse(c, fiber.StatusNotFound, "Shop not found")
		}
		receiverOID = shop.OwnerID
	} else {
		recOID, err := primitive.ObjectIDFromHex(req.ReceiverID)
		if err != nil {
			return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid Receiver ID format")
		}
		receiverOID = recOID
	}

	msg := model.Message{
		ID:         primitive.NewObjectID(),
		SenderID:   senderOID,
		ReceiverID: receiverOID,
		ShopID:     shopOID,
		Text:       req.Text,
		CreatedAt:  primitive.NewDateTimeFromTime(time.Now()),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := config.DB.Collection("messages").InsertOne(ctx, msg)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to send message")
	}

	return helper.SuccessResponse(c, fiber.StatusCreated, "Message sent", msg)
}

func GetChatHistory(c *fiber.Ctx) error {
	buyerID := c.Query("buyer_id")
	shopID := c.Query("shop_id")

	if buyerID == "" || shopID == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "buyer_id and shop_id are required")
	}

	buyerOID, err1 := primitive.ObjectIDFromHex(buyerID)
	shopOID, err2 := primitive.ObjectIDFromHex(shopID)
	if err1 != nil || err2 != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid ID format")
	}

	// Get Shop Owner ID to filter messages between buyer and owner
	var shop model.Shop
	err := config.DB.Collection("shops").FindOne(context.Background(), bson.M{"_id": shopOID}).Decode(&shop)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Shop not found")
	}

	ownerOID := shop.OwnerID

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	filter := bson.M{
		"shop_id": shopOID,
		"$or": []bson.M{
			{"sender_id": buyerOID, "receiver_id": ownerOID},
			{"sender_id": ownerOID, "receiver_id": buyerOID},
		},
	}
	
	opts := options.Find().SetSort(bson.M{"created_at": 1}) // ascending
	cursor, err := config.DB.Collection("messages").Find(ctx, filter, opts)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to fetch messages")
	}
	defer cursor.Close(ctx)

	var messages []model.Message
	if err = cursor.All(ctx, &messages); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to parse messages")
	}

	if messages == nil {
		messages = []model.Message{}
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Messages retrieved", messages)
}

type ChatInboxItem struct {
	BuyerID     primitive.ObjectID `json:"buyer_id"`
	BuyerName   string             `json:"buyer_name"`
	BuyerAvatar string             `json:"buyer_avatar"`
	LastMsg     string             `json:"last_message"`
	UpdatedAt   primitive.DateTime `json:"updated_at"`
}

func GetSellerInbox(c *fiber.Ctx) error {
	shopID := c.Query("shop_id")
	if shopID == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "shop_id is required")
	}
	shopOID, err := primitive.ObjectIDFromHex(shopID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid shop_id")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var shop model.Shop
	if err := config.DB.Collection("shops").FindOne(ctx, bson.M{"_id": shopOID}).Decode(&shop); err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Shop not found")
	}
	ownerOID := shop.OwnerID

	// Fetch all messages for this shop
	opts := options.Find().SetSort(bson.M{"created_at": -1}) // sort newest first
	cursor, err := config.DB.Collection("messages").Find(ctx, bson.M{"shop_id": shopOID}, opts)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to fetch inbox")
	}
	defer cursor.Close(ctx)

	var messages []model.Message
	if err = cursor.All(ctx, &messages); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to parse inbox")
	}

	// Group in memory
	grouped := make(map[primitive.ObjectID]model.Message)
	for _, msg := range messages {
		var otherID primitive.ObjectID
		if msg.SenderID == ownerOID {
			otherID = msg.ReceiverID
		} else {
			otherID = msg.SenderID
		}
		
		// Since we sorted by newest first, the first time we see this otherID, it's their latest message
		if _, exists := grouped[otherID]; !exists {
			grouped[otherID] = msg
		}
	}

	var inbox []ChatInboxItem
	for buyerID, lastMsg := range grouped {
		// We don't skip if buyerID == ownerOID, in case they are testing with their own account
		
		var buyer model.User
		config.DB.Collection("users").FindOne(ctx, bson.M{"_id": buyerID}).Decode(&buyer)
		
		inbox = append(inbox, ChatInboxItem{
			BuyerID:     buyerID,
			BuyerName:   buyer.Name,
			BuyerAvatar: buyer.Avatar,
			LastMsg:     lastMsg.Text,
			UpdatedAt:   lastMsg.CreatedAt,
		})
	}
	
	if inbox == nil {
		inbox = []ChatInboxItem{}
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Inbox retrieved", inbox)
}

