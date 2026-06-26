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

type UpdateProfileReq struct {
	UserID  string `json:"user_id"`
	Phone   string `json:"phone"`
	Address string `json:"address"`
}

func UpdateProfile(c *fiber.Ctx) error {
	var req UpdateProfileReq
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	objID, err := primitive.ObjectIDFromHex(req.UserID)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid user ID",
		})
	}

	collection := config.DB.Collection("users")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	update := bson.M{
		"$set": bson.M{
			"phone":   req.Phone,
			"address": req.Address,
		},
	}

	_, err = collection.UpdateOne(ctx, bson.M{"_id": objID}, update)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to update profile",
		})
	}

	return c.JSON(fiber.Map{
		"message": "Profile updated successfully",
		"data": map[string]string{
			"phone":   req.Phone,
			"address": req.Address,
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
