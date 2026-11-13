package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"vendora/config"
	"vendora/helper"
	"vendora/model"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

const sessionTTL = 7 * 24 * time.Hour

func hashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// IssueSession membuat token sesi baru ("vdr_...") dan menyimpan hash-nya di database,
// sehingga token bisa diverifikasi di setiap request (bukan sekadar string acak).
func IssueSession(userID primitive.ObjectID) (string, error) {
	raw, err := helper.RandomToken(24)
	if err != nil {
		return "", err
	}
	token := "vdr_" + raw
	if config.DB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err = config.DB.Collection("sessions").InsertOne(ctx, bson.M{
			"token_hash": hashSessionToken(token),
			"user_id":    userID,
			"created_at": time.Now(),
			"expires_at": time.Now().Add(sessionTTL),
		})
		if err != nil {
			return "", err
		}
	}
	return token, nil
}

// AuthUser membaca header "Authorization: Bearer vdr_..." lalu mengembalikan pengguna
// yang sah. Mengembalikan nil bila token tidak ada, salah, kedaluwarsa, atau akun diblokir.
func AuthUser(c *fiber.Ctx) *model.User {
	if config.DB == nil {
		return nil
	}
	h := strings.TrimSpace(c.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return nil
	}
	token := strings.TrimSpace(h[7:])
	if !strings.HasPrefix(token, "vdr_") {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var sess struct {
		UserID    primitive.ObjectID `bson:"user_id"`
		ExpiresAt time.Time          `bson:"expires_at"`
	}
	if err := config.DB.Collection("sessions").FindOne(ctx, bson.M{"token_hash": hashSessionToken(token)}).Decode(&sess); err != nil {
		return nil
	}
	if time.Now().After(sess.ExpiresAt) {
		return nil
	}
	var user model.User
	if err := config.DB.Collection("users").FindOne(ctx, bson.M{"_id": sess.UserID}).Decode(&user); err != nil {
		return nil
	}
	if user.Banned {
		return nil
	}
	return &user
}

// requireAuth mengembalikan pengguna atau langsung membalas 401.
func requireAuth(c *fiber.Ctx) (*model.User, error) {
	u := AuthUser(c)
	if u == nil {
		return nil, helper.ErrorResponse(c, fiber.StatusUnauthorized, "Sesi tidak valid atau sudah berakhir. Silakan masuk lagi.")
	}
	return u, nil
}

// RequireAdmin adalah middleware Fiber yang memastikan pengguna sudah login dan memiliki role "admin".
// Jika belum login -> 401 Unauthorized; jika bukan admin -> 403 Forbidden.
func RequireAdmin(c *fiber.Ctx) error {
	u, err := requireAuth(c)
	if u == nil {
		return err
	}
	if u.Role != "admin" {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Akses ditolak: Hanya administrator yang diizinkan mengakses sumber daya ini.")
	}
	c.Locals("auth_user", u)
	return c.Next()
}

// GetMe mengembalikan profil pengguna yang sedang masuk (GET /api/user/me).
func GetMe(c *fiber.Ctx) error {
	u, err := requireAuth(c)
	if u == nil {
		return err
	}
	return helper.SuccessResponse(c, fiber.StatusOK, "Current user retrieved", u)
}


