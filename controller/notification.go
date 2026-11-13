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
	"go.mongodb.org/mongo-driver/mongo/options"
)

// PushUserNotification membuat notifikasi ke satu pengguna spesifik (mis. status pesanan / saldo cair).
func PushUserNotification(userID primitive.ObjectID, notifType, title, message, link string) {
	if config.DB == nil || userID.IsZero() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	notif := model.Notification{
		ID:        primitive.NewObjectID(),
		UserID:    userID,
		Type:      notifType,
		Title:     title,
		Message:   message,
		Link:      link,
		CreatedAt: primitive.NewDateTimeFromTime(time.Now()),
	}
	_, _ = config.DB.Collection("notifications").InsertOne(ctx, notif)
}

// PushBroadcastNotification mengirim notifikasi pengumuman/promo ke seluruh role tertentu ("buyer", "seller", atau "all").
func PushBroadcastNotification(targetRole, notifType, title, message, link, image string) {
	if config.DB == nil {
		return
	}
	if targetRole == "" {
		targetRole = "all"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	notif := model.Notification{
		ID:         primitive.NewObjectID(),
		TargetRole: targetRole,
		Type:       notifType,
		Title:      title,
		Message:    message,
		Link:       link,
		Image:      image,
		CreatedAt:  primitive.NewDateTimeFromTime(time.Now()),
	}
	_, _ = config.DB.Collection("notifications").InsertOne(ctx, notif)
}

// ListNotifications menangani GET /api/notifications?user_id=...&role=...
func ListNotifications(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	authUser, aerr := requireAuth(c)
	if authUser == nil {
		return aerr
	}

	userIDStr := strings.TrimSpace(c.Query("user_id"))
	if userIDStr != "" && authUser.Role != "admin" && userIDStr != authUser.ID.Hex() {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Tidak diizinkan membaca notifikasi pengguna lain")
	}

	userOID := authUser.ID
	role := strings.ToLower(strings.TrimSpace(authUser.Role))
	if role == "" {
		role = "buyer"
	}

	orFilters := []bson.M{
		{"target_role": "all"},
		{"target_role": role},
	}
	if !userOID.IsZero() {
		orFilters = append(orFilters, bson.M{"user_id": userOID})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(40)
	cursor, err := config.DB.Collection("notifications").Find(ctx, bson.M{"$or": orFilters}, opts)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to retrieve notifications")
	}
	defer cursor.Close(ctx)

	var list []model.Notification = []model.Notification{}
	if err := cursor.All(ctx, &list); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to decode notifications")
	}

	unreadCount := 0
	for i := range list {
		read := false
		if !userOID.IsZero() {
			for _, r := range list[i].ReadBy {
				if r == userOID {
					read = true
					break
				}
			}
		}
		list[i].IsRead = read
		if !read {
			unreadCount++
		}
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Notifications retrieved", fiber.Map{
		"notifications": list,
		"unread_count":  unreadCount,
	})
}

// CreateNotification menangani POST /api/notifications (Seller/Admin kirim pengumuman atau promo)
func CreateNotification(c *fiber.Ctx) error {
	authUser, aerr := requireAuth(c)
	if authUser == nil {
		return aerr
	}
	if authUser.Role != "admin" && authUser.Role != "seller" {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Hanya penjual atau admin yang bisa mengirim notifikasi")
	}
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	var req struct {
		UserID     string `json:"user_id"`
		TargetRole string `json:"target_role"` // "all", "buyer", "seller"
		Type       string `json:"type"`        // "promo", "system", "order"
		Title      string `json:"title"`
		Message    string `json:"message"`
		Link       string `json:"link"`
		Image      string `json:"image"`
	}
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request payload")
	}

	req.Title = strings.TrimSpace(req.Title)
	req.Message = strings.TrimSpace(req.Message)
	if req.Title == "" || req.Message == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Judul dan isi notifikasi wajib diisi")
	}
	if req.Type == "" {
		req.Type = "promo"
	}

	notif := model.Notification{
		ID:        primitive.NewObjectID(),
		Type:      req.Type,
		Title:     req.Title,
		Message:   req.Message,
		Link:      strings.TrimSpace(req.Link),
		Image:     strings.TrimSpace(req.Image),
		CreatedAt: primitive.NewDateTimeFromTime(time.Now()),
	}

	if req.UserID != "" && authUser.Role == "admin" {
		if oid, err := primitive.ObjectIDFromHex(req.UserID); err == nil {
			notif.UserID = oid
		}
	}
	if notif.UserID.IsZero() {
		// Penjual hanya boleh broadcast ke pembeli; admin bebas memilih target
		if authUser.Role != "admin" || req.TargetRole == "" {
			notif.TargetRole = "buyer"
		} else {
			notif.TargetRole = req.TargetRole
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := config.DB.Collection("notifications").InsertOne(ctx, notif); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal menyimpan notifikasi")
	}

	return helper.SuccessResponse(c, fiber.StatusCreated, "Notifikasi berhasil dikirim", notif)
}

// MarkNotificationRead menangani PUT /api/notifications/:id/read
func MarkNotificationRead(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	authUser, aerr := requireAuth(c)
	if authUser == nil {
		return aerr
	}

	id := c.Params("id")
	notifOID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid notification ID")
	}

	var req struct {
		UserID string `json:"user_id"`
	}
	_ = c.BodyParser(&req)
	if req.UserID == "" {
		req.UserID = c.Query("user_id")
	}
	if strings.TrimSpace(req.UserID) != "" && authUser.Role != "admin" && strings.TrimSpace(req.UserID) != authUser.ID.Hex() {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Tidak diizinkan menandai notifikasi pengguna lain")
	}
	userOID := authUser.ID

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _ = config.DB.Collection("notifications").UpdateOne(
		ctx,
		bson.M{"_id": notifOID},
		bson.M{"$addToSet": bson.M{"read_by": userOID}},
	)

	return helper.SuccessResponse(c, fiber.StatusOK, "Notification marked as read", nil)
}

// MarkAllNotificationsRead menangani PUT /api/notifications/read-all
func MarkAllNotificationsRead(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	authUser, aerr := requireAuth(c)
	if authUser == nil {
		return aerr
	}

	var req struct {
		UserID string `json:"user_id"`
	}
	_ = c.BodyParser(&req)
	if req.UserID == "" {
		req.UserID = c.Query("user_id")
	}
	if strings.TrimSpace(req.UserID) != "" && authUser.Role != "admin" && strings.TrimSpace(req.UserID) != authUser.ID.Hex() {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Tidak diizinkan menandai notifikasi pengguna lain")
	}
	userOID := authUser.ID

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _ = config.DB.Collection("notifications").UpdateMany(
		ctx,
		bson.M{},
		bson.M{"$addToSet": bson.M{"read_by": userOID}},
	)

	return helper.SuccessResponse(c, fiber.StatusOK, "All notifications marked as read", nil)
}
