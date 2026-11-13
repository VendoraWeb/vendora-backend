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

type CreateTicketReq struct {
	SellerID string `json:"seller_id"`
	Subject  string `json:"subject"`
	Message  string `json:"message"`
}

func CreateTicket(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}
	authUser, aerr := requireAuth(c)
	if authUser == nil {
		return aerr
	}

	var req CreateTicketReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request payload")
	}

	if req.Subject == "" || req.Message == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Subject and Message are required")
	}

	if req.SellerID != "" && req.SellerID != authUser.ID.Hex() {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Tidak diizinkan membuat tiket atas nama pengguna lain")
	}
	sellerOID := authUser.ID

	// Find seller user to get name
	var seller model.User
	err := config.DB.Collection("users").FindOne(context.Background(), bson.M{"_id": sellerOID}).Decode(&seller)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Seller not found")
	}

	newTicket := model.SupportTicket{
		ID:         primitive.NewObjectID(),
		SellerID:   sellerOID,
		SellerName: seller.Name,
		Subject:    req.Subject,
		Message:    req.Message,
		Reply:      "",
		Status:     "open",
		CreatedAt:  primitive.NewDateTimeFromTime(time.Now()),
		UpdatedAt:  primitive.NewDateTimeFromTime(time.Now()),
	}

	_, err = config.DB.Collection("tickets").InsertOne(context.Background(), newTicket)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to create support ticket")
	}

	return helper.SuccessResponse(c, fiber.StatusCreated, "Ticket created successfully", newTicket)
}

func ListTickets(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}
	authUser, aerr := requireAuth(c)
	if authUser == nil {
		return aerr
	}

	filter := bson.M{}
	sellerID := c.Query("seller_id")
	if authUser.Role != "admin" {
		if sellerID != "" && sellerID != authUser.ID.Hex() {
			return helper.ErrorResponse(c, fiber.StatusForbidden, "Tidak diizinkan membaca tiket pengguna lain")
		}
		filter["seller_id"] = authUser.ID
	} else if sellerID != "" {
		sellerOID, err := primitive.ObjectIDFromHex(sellerID)
		if err == nil {
			filter["seller_id"] = sellerOID
		}
	}

	cursor, err := config.DB.Collection("tickets").Find(context.Background(), filter)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to retrieve tickets")
	}
	defer cursor.Close(context.Background())

	var tickets []model.SupportTicket = []model.SupportTicket{}
	if err = cursor.All(context.Background(), &tickets); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to decode tickets list")
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Tickets retrieved successfully", tickets)
}

func ReplyTicket(c *fiber.Ctx) error {
	ticketID := c.Params("id")
	if ticketID == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Ticket ID is required")
	}

	type ReplyReq struct {
		Reply string `json:"reply"`
	}

	var req ReplyReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request payload")
	}

	if req.Reply == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Reply content cannot be empty")
	}

	oid, err := primitive.ObjectIDFromHex(ticketID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid Ticket ID format")
	}

	update := bson.M{
		"$set": bson.M{
			"reply":      req.Reply,
			"status":     "resolved",
			"updated_at": primitive.NewDateTimeFromTime(time.Now()),
		},
	}

	result, err := config.DB.Collection("tickets").UpdateOne(context.Background(), bson.M{"_id": oid}, update)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to reply support ticket")
	}

	if result.MatchedCount == 0 {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Ticket not found")
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Ticket replied and resolved successfully", nil)
}
