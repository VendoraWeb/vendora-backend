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
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ─── Sesi login ─────────────────────────────────────────────────────────────
// Token acak "vdr_..." diberikan ke browser saat masuk; yang disimpan di database
// hanya hash SHA-256-nya. Setiap permintaan yang butuh login membawa header
// "Authorization: Bearer <token>". Sesi kedaluwarsa otomatis (indeks TTL).
//
// Dua gaya pemeriksaan didukung:
//   - middleware rute: AuthOptional (dipasang di grup /api) → RequireAuth / RequireRole / RequireAdmin
//   - di dalam handler: requireAuth(c) / AuthUser(c)
// Keduanya membaca sesi yang sama; hasilnya disimpan di c.Locals sehingga database
// hanya dibaca sekali per permintaan.

const sessionTTL = 30 * 24 * time.Hour

type session struct {
	ID        primitive.ObjectID `bson:"_id,omitempty"`
	TokenHash string             `bson:"token_hash"`
	UserID    primitive.ObjectID `bson:"user_id"`
	CreatedAt time.Time          `bson:"created_at"`
	ExpiresAt time.Time          `bson:"expires_at"`
}

func sessionsCol() *mongo.Collection { return config.DB.Collection("sessions") }

func hashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// IssueSession membuat sesi baru dan mengembalikan token untuk browser.
func IssueSession(userID primitive.ObjectID) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return issueSession(ctx, userID)
}

func issueSession(ctx context.Context, userID primitive.ObjectID) (string, error) {
	raw, err := helper.RandomToken(32)
	if err != nil {
		return "", err
	}
	token := "vdr_" + raw
	if config.DB == nil {
		return token, nil
	}
	now := time.Now()
	_, err = sessionsCol().InsertOne(ctx, session{
		TokenHash: hashSessionToken(token),
		UserID:    userID,
		CreatedAt: now,
		ExpiresAt: now.Add(sessionTTL),
	})
	return token, err
}

// revokeUserSessions mengeluarkan pengguna dari semua perangkat (mis. setelah ganti kata sandi).
func revokeUserSessions(ctx context.Context, userID primitive.ObjectID) {
	_, _ = sessionsCol().DeleteMany(ctx, bson.M{"user_id": userID})
}

func bearerToken(c *fiber.Ctx) string {
	h := strings.TrimSpace(c.Get(fiber.HeaderAuthorization))
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// resolveSession membaca token sekali per permintaan dan menyimpan hasilnya di c.Locals.
func resolveSession(c *fiber.Ctx) *model.User {
	if done, _ := c.Locals("authResolved").(bool); done {
		u, _ := c.Locals("user").(*model.User)
		return u
	}
	c.Locals("authResolved", true)

	token := bearerToken(c)
	if !strings.HasPrefix(token, "vdr_") || config.DB == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var s session
	if err := sessionsCol().FindOne(ctx, bson.M{"token_hash": hashSessionToken(token)}).Decode(&s); err != nil || time.Now().After(s.ExpiresAt) {
		return nil
	}
	var u model.User
	if err := usersCol().FindOne(ctx, bson.M{"_id": s.UserID}).Decode(&u); err != nil {
		return nil
	}
	c.Locals("user", &u)
	c.Locals("sessionID", s.ID)
	return &u
}

// AuthOptional membaca token bila ada. Tidak menolak permintaan; penolakan dilakukan
// RequireAuth / RequireRole / RequireAdmin atau requireAuth di dalam handler.
func AuthOptional(c *fiber.Ctx) error {
	resolveSession(c)
	return c.Next()
}

// currentUser mengembalikan pengguna yang sedang masuk (nil bila tamu).
func currentUser(c *fiber.Ctx) *model.User { return resolveSession(c) }

// AuthUser mengembalikan pengguna yang sah, atau nil bila token tidak ada, salah,
// kedaluwarsa, atau akun diblokir.
func AuthUser(c *fiber.Ctx) *model.User {
	u := resolveSession(c)
	if u == nil || u.Banned {
		return nil
	}
	return u
}

// requireAuth mengembalikan pengguna atau langsung membalas 401/403.
func requireAuth(c *fiber.Ctx) (*model.User, error) {
	u := resolveSession(c)
	if u == nil {
		return nil, helper.ErrorResponse(c, fiber.StatusUnauthorized, "Sesi tidak valid atau sudah berakhir. Silakan masuk lagi.")
	}
	if u.Banned {
		return nil, helper.ErrorResponse(c, fiber.StatusForbidden, "Akun ini diblokir. Hubungi admin Vendora.")
	}
	return u, nil
}

// RequireAuth menolak permintaan tanpa sesi yang sah.
func RequireAuth(c *fiber.Ctx) error {
	if u, err := requireAuth(c); u == nil {
		return err
	}
	return c.Next()
}

// RequireRole membatasi rute untuk peran tertentu.
func RequireRole(roles ...string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		u, err := requireAuth(c)
		if u == nil {
			return err
		}
		for _, r := range roles {
			if u.Role == r {
				return c.Next()
			}
		}
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Kamu tidak punya akses ke fitur ini.")
	}
}

// RequireAdmin: belum login → 401, bukan admin → 403.
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

func isAdmin(u *model.User) bool { return u != nil && u.Role == "admin" }

// GetMe: profil pengguna yang sedang masuk (GET /api/me dan /api/user/me).
func GetMe(c *fiber.Ctx) error {
	u, err := requireAuth(c)
	if u == nil {
		return err
	}
	return helper.SuccessResponse(c, fiber.StatusOK, "Current user retrieved", u)
}

// POST /api/logout — hapus sesi ini.
func Logout(c *fiber.Ctx) error {
	resolveSession(c)
	if id, ok := c.Locals("sessionID").(primitive.ObjectID); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = sessionsCol().DeleteOne(ctx, bson.M{"_id": id})
	}
	return helper.SuccessResponse(c, fiber.StatusOK, "Berhasil keluar", nil)
}

// EnsureSessionIndexes: token unik + hapus otomatis saat kedaluwarsa.
func EnsureSessionIndexes() {
	if config.DB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = sessionsCol().Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "token_hash", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "expires_at", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
		{Keys: bson.D{{Key: "user_id", Value: 1}}},
	})
}
