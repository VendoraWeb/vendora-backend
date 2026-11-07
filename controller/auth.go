package controller

import (
	"context"
	"regexp"
	"strings"
	"time"

	"vendora/config"
	"vendora/helper"
	"vendora/model"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"golang.org/x/crypto/bcrypt"
)

type RegisterReq struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Phone    string `json:"phone"` // opsional, untuk OTP lewat WhatsApp/SMS
	Password string `json:"password"`
	Role     string `json:"role"` // seller | buyer
	ShopName string `json:"shop_name"`
}

func Register(c *fiber.Ctx) error {
	var req RegisterReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Permintaan tidak valid")
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Name == "" || req.Email == "" || req.Password == "" || req.Role == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Nama, email, kata sandi, dan peran wajib diisi")
	}
	if !helper.IsEmail(req.Email) {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Format email belum benar")
	}
	if len(req.Password) < minPasswordChars {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Kata sandi minimal 8 karakter")
	}
	// Pendaftaran publik hanya untuk pembeli & penjual — akun admin tidak boleh dibuat dari luar.
	if req.Role != "seller" && req.Role != "buyer" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Peran harus pembeli atau penjual")
	}

	phoneE164 := ""
	if strings.TrimSpace(req.Phone) != "" {
		phoneE164 = helper.NormalizePhone(req.Phone)
		if phoneE164 == "" {
			return helper.ErrorResponse(c, fiber.StatusBadRequest, "Nomor HP belum benar. Contoh: 0812 3456 7890")
		}
	}

	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database belum terhubung")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	// Email unik (tidak peka huruf besar/kecil)
	emailFilter := bson.M{"email": bson.M{"$regex": "^" + regexp.QuoteMeta(req.Email) + "$", "$options": "i"}}
	if n, _ := usersCol().CountDocuments(ctx, emailFilter); n > 0 {
		return helper.ErrorResponse(c, fiber.StatusConflict, "Email sudah terdaftar")
	}
	// Nomor HP unik
	if phoneE164 != "" {
		if existing, _ := findUsersByIdentifier(ctx, phoneE164); len(existing) > 0 {
			return helper.ErrorResponse(c, fiber.StatusConflict, "Nomor HP sudah dipakai akun lain")
		}
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal mengamankan kata sandi")
	}

	newUser := model.User{
		ID:        primitive.NewObjectID(),
		Name:      req.Name,
		Email:     req.Email,
		Phone:     strings.TrimSpace(req.Phone),
		PhoneE164: phoneE164,
		Password:  string(hashedPassword),
		Role:      req.Role,
		ShopName:  req.ShopName,
		CreatedAt: primitive.NewDateTimeFromTime(time.Now()),
	}

	if _, err = usersCol().InsertOne(ctx, newUser); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal mendaftarkan akun")
	}

	return helper.SuccessResponse(c, fiber.StatusCreated, "Akun berhasil dibuat", newUser)
}
