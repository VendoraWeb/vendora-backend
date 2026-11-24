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

type CreateTicketReq struct {
	SellerID string `json:"seller_id"`
	Subject  string `json:"subject"`
	Message  string `json:"message"`
}

func CreateTicket(c *fiber.Ctx) error {
	var req CreateTicketReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Permintaan tidak valid")
	}

	req.Subject, req.Message = strings.TrimSpace(req.Subject), strings.TrimSpace(req.Message)
	if req.Subject == "" || req.Message == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Topik dan pesan wajib diisi")
	}
	if len([]rune(req.Message)) > 2000 || len([]rune(req.Subject)) > 120 {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Pesan terlalu panjang")
	}
	// Tiket selalu atas nama pengguna yang sedang masuk
	sellerOID := currentUser(c).ID
	var err error

	// Find seller user to get name
	var seller model.User
	err = config.DB.Collection("users").FindOne(context.Background(), bson.M{"_id": sellerOID}).Decode(&seller)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Akun tidak ditemukan")
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
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal mengirim pertanyaan")
	}

	return helper.SuccessResponse(c, fiber.StatusCreated, "Pertanyaan terkirim", newTicket)
}

func ListTickets(c *fiber.Ctx) error {
	// Admin melihat semua tiket; pengguna lain hanya tiketnya sendiri
	filter := bson.M{}
	if u := currentUser(c); !isAdmin(u) {
		filter["seller_id"] = u.ID
	} else if sellerID := c.Query("seller_id"); sellerID != "" {
		if sellerOID, err := primitive.ObjectIDFromHex(sellerID); err == nil {
			filter["seller_id"] = sellerOID
		}
	}

	cursor, err := config.DB.Collection("tickets").Find(context.Background(), filter)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal memuat tiket")
	}
	defer cursor.Close(context.Background())

	var tickets []model.SupportTicket = []model.SupportTicket{}
	if err = cursor.All(context.Background(), &tickets); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal membaca tiket")
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Daftar tiket", tickets)
}

func ReplyTicket(c *fiber.Ctx) error {
	ticketID := c.Params("id")
	if ticketID == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "ID tiket wajib diisi")
	}

	type ReplyReq struct {
		Reply string `json:"reply"`
	}

	var req ReplyReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Permintaan tidak valid")
	}

	req.Reply = strings.TrimSpace(req.Reply)
	if req.Reply == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Balasan tidak boleh kosong")
	}

	oid, err := primitive.ObjectIDFromHex(ticketID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "ID tiket tidak valid")
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
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal membalas tiket")
	}

	if result.MatchedCount == 0 {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Tiket tidak ditemukan")
	}

	var t model.SupportTicket
	if config.DB.Collection("tickets").FindOne(context.Background(), bson.M{"_id": oid}).Decode(&t) == nil {
		PushUserNotification(t.SellerID, "ticket", "Tim Vendora membalas: "+t.Subject, req.Reply, "bantuan.html#hubungi")
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Tiket dibalas", nil)
}
