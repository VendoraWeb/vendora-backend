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
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/crypto/bcrypt"
)

type RegisterReq struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"` // admin, seller, buyer
	ShopName string `json:"shop_name"`
}

type LoginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func Register(c *fiber.Ctx) error {
	var req RegisterReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request payload")
	}

	if req.Name == "" || req.Email == "" || req.Password == "" || req.Role == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "All fields (name, email, password, role) are required")
	}

	if req.Role != "admin" && req.Role != "seller" && req.Role != "buyer" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Role must be admin, seller, or buyer")
	}

	// Check if DB is initialized
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	collection := config.DB.Collection("users")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Check if email already exists
	var existingUser model.User
	err := collection.FindOne(ctx, bson.M{"email": req.Email}).Decode(&existingUser)
	if err == nil {
		return helper.ErrorResponse(c, fiber.StatusConflict, "Email already registered")
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to hash password")
	}

	newUser := model.User{
		ID:        primitive.NewObjectID(),
		Name:      req.Name,
		Email:     req.Email,
		Password:  string(hashedPassword),
		Role:      req.Role,
		ShopName:  req.ShopName,
		CreatedAt: primitive.NewDateTimeFromTime(time.Now()),
	}

	_, err = collection.InsertOne(ctx, newUser)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to register user")
	}

	return helper.SuccessResponse(c, fiber.StatusCreated, "User registered successfully", newUser)
}

func Login(c *fiber.Ctx) error {
	var req LoginReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request payload")
	}

	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	collection := config.DB.Collection("users")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var user model.User
	err := collection.FindOne(ctx, bson.M{"email": req.Email}).Decode(&user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return helper.ErrorResponse(c, fiber.StatusUnauthorized, "Invalid email or password")
		}
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database error")
	}

	// Compare hashed password using bcrypt
	err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password))
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusUnauthorized, "Invalid email or password")
	}

	// In a real application, you'd generate a JWT token here.
	// For this boilerplate, we'll return the user info and a mock token.
	response := fiber.Map{
		"token": "mock-jwt-token-for-" + user.Email,
		"user":  user,
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Login successful", response)
}
