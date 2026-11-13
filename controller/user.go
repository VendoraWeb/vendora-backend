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

type UpdateProfileReq struct {
	UserID  string `json:"user_id"`
	Email   string `json:"email"`
	Phone   string `json:"phone"`
	Address string `json:"address"`
	Avatar  string `json:"avatar"`
}

func UpdateProfile(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	authUser, aerr := requireAuth(c)
	if authUser == nil {
		return aerr
	}

	var req UpdateProfileReq
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if strings.TrimSpace(req.UserID) != "" && strings.TrimSpace(req.UserID) != authUser.ID.Hex() {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "Tidak diizinkan mengubah profil akun pengguna lain",
		})
	}

	objID := authUser.ID

	collection := config.DB.Collection("users")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	var current model.User
	if err := collection.FindOne(ctx, bson.M{"_id": objID}).Decode(&current); err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Akun tidak ditemukan"})
	}

	// Email & nomor HP dipakai untuk login + OTP, jadi harus valid dan unik.
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if !helper.IsEmail(req.Email) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Format email belum benar"})
	}
	phoneE164 := ""
	if strings.TrimSpace(req.Phone) != "" {
		phoneE164 = helper.NormalizePhone(req.Phone)
		if phoneE164 == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Nomor HP belum benar. Contoh: 0812 3456 7890"})
		}
	}
	if !strings.EqualFold(req.Email, current.Email) {
		other, err := findUserByIdentifier(ctx, req.Email)
		if err == nil && other != nil && other.ID != objID {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Email sudah dipakai akun lain"})
		}
	}
	currentPhone := current.PhoneE164
	if currentPhone == "" {
		currentPhone = helper.NormalizePhone(current.Phone)
	}
	if phoneE164 != "" && phoneE164 != currentPhone {
		others, _ := findUsersByIdentifier(ctx, phoneE164)
		for _, other := range others {
			if other.ID != objID {
				return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Nomor HP sudah dipakai akun lain"})
			}
		}
	}

	set := bson.M{
		"email":      req.Email,
		"phone":      strings.TrimSpace(req.Phone),
		"phone_e164": phoneE164,
		"address":    req.Address,
		"avatar":     req.Avatar,
	}
	// Ganti email/nomor → status terverifikasi kembali false sampai OTP berikutnya
	if !strings.EqualFold(req.Email, current.Email) {
		set["email_verified"] = false
	}
	if phoneE164 != currentPhone {
		set["phone_verified"] = false
	}
	update := bson.M{"$set": set}

	_, err := collection.UpdateOne(ctx, bson.M{"_id": objID}, update)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to update profile",
		})
	}

	return c.JSON(fiber.Map{
		"message": "Profile updated successfully",
		"data": map[string]string{
			"email":   req.Email,
			"phone":   req.Phone,
			"address": req.Address,
			"avatar":  req.Avatar,
		},
	})
}

// ListUsers returns all registered users (admin only)
func ListUsers(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Optional filter by role
	filter := bson.M{}
	if role := c.Query("role"); role != "" {
		filter["role"] = role
	}

	cursor, err := config.DB.Collection("users").Find(ctx, filter)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to retrieve users")
	}
	defer cursor.Close(ctx)

	var users []model.User
	if err = cursor.All(ctx, &users); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Error decoding users list")
	}

	// Privasi Data (Admin tidak boleh melihat email, nomor HP, atau alamat asli pengguna)
	for i := range users {
		if users[i].Role != "admin" {
			users[i].Email = helper.MaskEmail(users[i].Email)
			users[i].Phone = helper.MaskPhone(helper.NormalizePhone(users[i].Phone))
			users[i].Address = helper.MaskAddress(users[i].Address)
		}
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Users retrieved successfully", users)
}

type BanUserReq struct {
	Banned bool `json:"banned"`
}

// BanUser toggles the banned status of a user (admin only)
func BanUser(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	userID := c.Params("id")
	objID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid user ID")
	}

	var req BanUserReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	update := bson.M{
		"$set": bson.M{"banned": req.Banned},
	}

	res, err := config.DB.Collection("users").UpdateOne(ctx, bson.M{"_id": objID}, update)
	if err != nil || res.MatchedCount == 0 {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to update user status")
	}

	status := "unbanned"
	if req.Banned {
		status = "banned"
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "User "+status+" successfully", nil)
}
