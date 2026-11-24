package controller

import (
	"context"
	"errors"
	"log"
	"os"
	"regexp"
	"strconv"
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
	"golang.org/x/crypto/bcrypt"
)

const (
	otpCodeTTL       = 5 * time.Minute  // masa berlaku satu kode
	otpChallengeTTL  = 15 * time.Minute // masa berlaku satu sesi verifikasi
	otpResendWait    = 60 * time.Second // jeda minimal kirim ulang
	otpMaxSends      = 5                // maksimal kirim per sesi
	otpMaxAttempts   = 5                // maksimal salah per kode
	resetTokenTTL    = 10 * time.Minute
	minPasswordChars = 8
)

type channelInfo struct {
	Type   string `json:"type"`   // "email" | "phone"
	Masked string `json:"masked"` // tujuan yang disamarkan
	Label  string `json:"label"`
}

func otpCol() *mongo.Collection   { return config.DB.Collection("otp_challenges") }
func usersCol() *mongo.Collection { return config.DB.Collection("users") }

// phoneLabel: nama saluran nomor HP sesuai penyedia yang aktif.
func phoneLabel() string {
	switch {
	case os.Getenv("FONNTE_TOKEN") != "":
		return "WhatsApp"
	case os.Getenv("TWILIO_ACCOUNT_SID") != "":
		return "SMS"
	}
	return "WhatsApp / SMS"
}

// channelsFor mengembalikan saluran OTP yang tersedia untuk pengguna.
func channelsFor(u *model.User) []channelInfo {
	var out []channelInfo
	if helper.IsEmail(u.Email) {
		out = append(out, channelInfo{Type: "email", Masked: helper.MaskEmail(u.Email), Label: "Email"})
	}
	phone := u.PhoneE164
	if phone == "" {
		phone = helper.NormalizePhone(u.Phone)
	}
	if phone != "" {
		out = append(out, channelInfo{Type: "phone", Masked: helper.MaskPhone(phone), Label: phoneLabel()})
	}
	return out
}

// errSharedPhone: nomor HP yang sama tercatat di lebih dari satu akun (data lama).
var errSharedPhone = errors.New("nomor HP dipakai lebih dari satu akun")

// findUsersByIdentifier mengembalikan SEMUA akun yang cocok dengan email atau nomor HP.
func findUsersByIdentifier(ctx context.Context, identifier string) ([]model.User, error) {
	id := strings.TrimSpace(identifier)
	if id == "" {
		return nil, mongo.ErrNoDocuments
	}
	var out []model.User
	if strings.Contains(id, "@") {
		// email tidak peka huruf besar/kecil
		filter := bson.M{"email": bson.M{"$regex": "^" + regexp.QuoteMeta(id) + "$", "$options": "i"}}
		cur, err := usersCol().Find(ctx, filter)
		if err != nil {
			return nil, err
		}
		defer cur.Close(ctx)
		if err := cur.All(ctx, &out); err != nil {
			return nil, err
		}
	} else {
		phone := helper.NormalizePhone(id)
		if phone == "" {
			return nil, mongo.ErrNoDocuments
		}
		// Data lama menyimpan nomor dalam berbagai format ("0821-3419-6609"), jadi cocokkan setelah normalisasi
		cur, err := usersCol().Find(ctx, bson.M{"$or": bson.A{
			bson.M{"phone_e164": phone},
			bson.M{"phone": bson.M{"$nin": bson.A{"", nil}}},
		}})
		if err != nil {
			return nil, err
		}
		defer cur.Close(ctx)
		for cur.Next(ctx) {
			var cand model.User
			if cur.Decode(&cand) != nil {
				continue
			}
			if cand.PhoneE164 == phone || helper.NormalizePhone(cand.Phone) == phone {
				if cand.PhoneE164 == "" {
					// simpan versi ternormalisasi supaya pencarian berikutnya cepat
					_, _ = usersCol().UpdateOne(ctx, bson.M{"_id": cand.ID}, bson.M{"$set": bson.M{"phone_e164": phone}})
				}
				out = append(out, cand)
			}
		}
	}
	if len(out) == 0 {
		return nil, mongo.ErrNoDocuments
	}
	return out, nil
}

// findUserByIdentifier mengembalikan satu akun. Bila nomor HP dipakai beberapa akun,
// akun pertama tetap dikembalikan bersama errSharedPhone agar pemanggil bisa memutuskan.
func findUserByIdentifier(ctx context.Context, identifier string) (*model.User, error) {
	users, err := findUsersByIdentifier(ctx, identifier)
	if err != nil {
		return nil, err
	}
	if len(users) > 1 {
		return &users[0], errSharedPhone
	}
	return &users[0], nil
}

func newChallenge(ctx context.Context, userID primitive.ObjectID, purpose string) (*model.OTPChallenge, error) {
	now := time.Now()
	ch := &model.OTPChallenge{
		ID:        primitive.NewObjectID(),
		UserID:    userID,
		Purpose:   purpose,
		ExpiresAt: now.Add(otpChallengeTTL),
		CreatedAt: now,
	}
	_, err := otpCol().InsertOne(ctx, ch)
	return ch, err
}

func loadChallenge(ctx context.Context, id string) (*model.OTPChallenge, error) {
	oid, err := primitive.ObjectIDFromHex(strings.TrimSpace(id))
	if err != nil {
		return nil, mongo.ErrNoDocuments
	}
	var ch model.OTPChallenge
	if err := otpCol().FindOne(ctx, bson.M{"_id": oid}).Decode(&ch); err != nil {
		return nil, err
	}
	if ch.Used || time.Now().After(ch.ExpiresAt) {
		return nil, mongo.ErrNoDocuments
	}
	return &ch, nil
}

func dbReady(c *fiber.Ctx) bool {
	if config.DB == nil {
		_ = helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database belum terhubung")
		return false
	}
	return true
}

// ─── POST /api/login ─────────────────────────────────────────────────────────
// Masuk dengan email/nomor HP + kata sandi. Bila cocok, langsung mengembalikan sesi.
// (OTP dipakai pada alur lupa / ganti kata sandi.)
type LoginReq struct {
	Identifier string `json:"identifier"`
	Email      string `json:"email"` // kompatibel dengan klien lama
	Password   string `json:"password"`
}

func Login(c *fiber.Ctx) error {
	var req LoginReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Permintaan tidak valid")
	}
	identifier := strings.TrimSpace(req.Identifier)
	if identifier == "" {
		identifier = strings.TrimSpace(req.Email)
	}
	if identifier == "" || req.Password == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Email/nomor HP dan kata sandi wajib diisi")
	}
	if !dbReady(c) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	// Nomor HP bisa tercatat di beberapa akun (data lama) → pilih akun yang kata sandinya cocok
	candidates, _ := findUsersByIdentifier(ctx, identifier)
	var user *model.User
	for i := range candidates {
		if bcrypt.CompareHashAndPassword([]byte(candidates[i].Password), []byte(req.Password)) == nil {
			user = &candidates[i]
			break
		}
	}
	if user == nil {
		return helper.ErrorResponse(c, fiber.StatusUnauthorized, "Email/nomor HP atau kata sandi salah")
	}
	if user.Banned {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Akun ini diblokir. Hubungi admin Vendora.")
	}
	// Kata sandi benar → langsung masuk. OTP dipakai untuk lupa/ganti kata sandi.
	sessionToken, err := IssueSession(user.ID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal membuat sesi")
	}
	return helper.SuccessResponse(c, fiber.StatusOK, "Berhasil masuk", fiber.Map{
		"otp_required": false,
		"token":        sessionToken,
		"user":         user,
	})
}

// ─── POST /api/auth/otp/send ─────────────────────────────────────────────────
type SendOTPReq struct {
	ChallengeID string `json:"challenge_id"`
	Channel     string `json:"channel"` // "email" | "phone"
}

func SendOTP(c *fiber.Ctx) error {
	var req SendOTPReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Permintaan tidak valid")
	}
	if strings.TrimSpace(req.ChallengeID) == "" || (req.Channel != "email" && req.Channel != "phone") {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Pilih saluran email atau nomor HP")
	}
	if !dbReady(c) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ch, err := loadChallenge(ctx, req.ChallengeID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusGone, "Sesi verifikasi sudah berakhir. Ulangi dari awal.")
	}
	if ch.Verified {
		return helper.ErrorResponse(c, fiber.StatusConflict, "Kode sudah diverifikasi")
	}
	now := time.Now()
	if ch.SendCount >= otpMaxSends {
		return helper.ErrorResponse(c, fiber.StatusTooManyRequests, "Terlalu banyak permintaan kode. Ulangi dari awal beberapa saat lagi.")
	}
	// Jeda kirim ulang hanya untuk saluran yang sama; pindah saluran boleh langsung
	// (tetap dibatasi otpMaxSends per sesi).
	if wait := otpResendWait - now.Sub(ch.LastSentAt); ch.SendCount > 0 && req.Channel == ch.Channel && wait > 0 {
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"status":  fiber.StatusTooManyRequests,
			"message": "Tunggu sebentar sebelum meminta kode baru",
			"data":    fiber.Map{"retry_in": int(wait.Seconds()) + 1},
		})
	}

	var user model.User
	if err := usersCol().FindOne(ctx, bson.M{"_id": ch.UserID}).Decode(&user); err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Akun tidak ditemukan")
	}
	var dest, masked string
	switch req.Channel {
	case "email":
		if !helper.IsEmail(user.Email) {
			return helper.ErrorResponse(c, fiber.StatusBadRequest, "Akun ini tidak punya email yang valid")
		}
		dest, masked = user.Email, helper.MaskEmail(user.Email)
	case "phone":
		dest = user.PhoneE164
		if dest == "" {
			dest = helper.NormalizePhone(user.Phone)
		}
		if dest == "" {
			return helper.ErrorResponse(c, fiber.StatusBadRequest, "Akun ini tidak punya nomor HP yang valid")
		}
		masked = helper.MaskPhone(dest)
	default:
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Pilih saluran email atau nomor HP")
	}

	code, err := helper.GenerateOTP()
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal membuat kode")
	}

	var devMode bool
	if req.Channel == "email" {
		devMode, err = helper.SendEmailOTP(dest, user.Name, code, ch.Purpose)
	} else {
		devMode, err = helper.SendPhoneOTP(dest, code, ch.Purpose)
	}
	if errors.Is(err, helper.ErrNoProvider) {
		return helper.ErrorResponse(c, fiber.StatusServiceUnavailable,
			"Pengiriman OTP lewat "+map[string]string{"email": "email", "phone": "nomor HP"}[req.Channel]+" belum diaktifkan di server. Coba saluran lain atau hubungi admin.")
	}
	if err != nil {
		log.Printf("[OTP] gagal kirim %s ke %s: %v", req.Channel, masked, err)
		if req.Channel == "phone" {
			return helper.ErrorResponse(c, fiber.StatusBadGateway, "Kode belum bisa dikirim lewat "+phoneLabel()+" saat ini. Pilih email, atau coba lagi nanti.")
		}
		return helper.ErrorResponse(c, fiber.StatusBadGateway, "Kode belum bisa dikirim lewat email saat ini. Pilih saluran lain, atau coba lagi nanti.")
	}

	_, err = otpCol().UpdateOne(ctx, bson.M{"_id": ch.ID}, bson.M{
		"$set": bson.M{
			"code_hash":       helper.HashSecret("otp", ch.ID.Hex(), code),
			"code_expires_at": now.Add(otpCodeTTL),
			"channel":         req.Channel,
			"attempts":        0,
			"last_sent_at":    now,
			"dev_mode":        devMode,
		},
		"$inc": bson.M{"send_count": 1},
	})
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal menyimpan kode")
	}
	return helper.SuccessResponse(c, fiber.StatusOK, "Kode verifikasi dikirim", fiber.Map{
		"channel":         req.Channel,
		"sent_to":         masked,
		"resend_in":       int(otpResendWait.Seconds()),
		"code_expires_in": int(otpCodeTTL.Seconds()),
		"sends_left":      otpMaxSends - ch.SendCount - 1,
		"dev_mode":        devMode, // true: kode hanya dicetak di log server (pengembangan)
	})
}

// ─── POST /api/auth/otp/verify ───────────────────────────────────────────────
type VerifyOTPReq struct {
	ChallengeID string `json:"challenge_id"`
	Code        string `json:"code"`
}

func VerifyOTP(c *fiber.Ctx) error {
	var req VerifyOTPReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Permintaan tidak valid")
	}
	if strings.TrimSpace(req.ChallengeID) == "" || len(strings.TrimSpace(req.Code)) != 6 {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Masukkan 6 digit kode verifikasi")
	}
	if !dbReady(c) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	ch, err := loadChallenge(ctx, req.ChallengeID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusGone, "Sesi verifikasi sudah berakhir. Ulangi dari awal.")
	}
	if ch.CodeHash == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Minta kode terlebih dahulu")
	}
	if ch.Verified {
		return helper.ErrorResponse(c, fiber.StatusConflict, "Kode sudah dipakai")
	}
	if time.Now().After(ch.CodeExpiresAt) {
		return helper.ErrorResponse(c, fiber.StatusGone, "Kode sudah kedaluwarsa. Minta kode baru.")
	}
	if ch.Attempts >= otpMaxAttempts {
		return helper.ErrorResponse(c, fiber.StatusTooManyRequests, "Terlalu banyak percobaan salah. Minta kode baru.")
	}

	code := strings.TrimSpace(req.Code)
	if !helper.SecretEqual(helper.HashSecret("otp", ch.ID.Hex(), code), ch.CodeHash) {
		_, _ = otpCol().UpdateOne(ctx, bson.M{"_id": ch.ID}, bson.M{"$inc": bson.M{"attempts": 1}})
		left := otpMaxAttempts - ch.Attempts - 1
		msg := "Kode salah."
		if left > 0 {
			msg += " Sisa percobaan: " + strconv.Itoa(left) + "."
		} else {
			msg += " Minta kode baru."
		}
		return helper.ErrorResponse(c, fiber.StatusUnauthorized, msg)
	}

	var user model.User
	if err := usersCol().FindOne(ctx, bson.M{"_id": ch.UserID}).Decode(&user); err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Akun tidak ditemukan")
	}

	// Saluran yang berhasil diverifikasi ditandai di akun — kecuali mode pengembangan,
	// karena kodenya hanya dicetak di log dan tidak benar-benar sampai ke pengguna.
	verifiedField := map[string]string{"email": "email_verified", "phone": "phone_verified"}[ch.Channel]
	if verifiedField != "" && !ch.DevMode {
		_, _ = usersCol().UpdateOne(ctx, bson.M{"_id": user.ID}, bson.M{"$set": bson.M{verifiedField: true}})
	}

	if ch.Purpose == "reset" {
		token, err := helper.RandomToken(24)
		if err != nil {
			return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal membuat token reset")
		}
		_, _ = otpCol().UpdateOne(ctx, bson.M{"_id": ch.ID}, bson.M{"$set": bson.M{
			"verified":         true,
			"reset_token_hash": helper.HashSecret("reset", ch.ID.Hex(), token),
			"expires_at":       time.Now().Add(resetTokenTTL),
		}})
		return helper.SuccessResponse(c, fiber.StatusOK, "Kode benar. Silakan buat kata sandi baru.", fiber.Map{
			"reset_token": token,
		})
	}

	// Login selesai
	if user.Banned {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Akun ini diblokir. Hubungi admin Vendora.")
	}
	_, _ = otpCol().UpdateOne(ctx, bson.M{"_id": ch.ID}, bson.M{"$set": bson.M{"verified": true, "used": true}})
	sessionToken, err := IssueSession(user.ID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal membuat sesi")
	}
	if !ch.DevMode && ch.Channel == "email" {
		user.EmailVerified = true
	} else if !ch.DevMode && ch.Channel == "phone" {
		user.PhoneVerified = true
	}
	return helper.SuccessResponse(c, fiber.StatusOK, "Berhasil masuk", fiber.Map{
		"token": sessionToken,
		"user":  user,
	})
}

// ─── POST /api/auth/reset/start ──────────────────────────────────────────────
type ResetStartReq struct {
	Identifier string `json:"identifier"`
}

func ResetStart(c *fiber.Ctx) error {
	var req ResetStartReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Permintaan tidak valid")
	}
	if strings.TrimSpace(req.Identifier) == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Masukkan email atau nomor HP akun")
	}
	if !dbReady(c) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	user, err := findUserByIdentifier(ctx, req.Identifier)
	if errors.Is(err, errSharedPhone) {
		return helper.ErrorResponse(c, fiber.StatusConflict, "Nomor HP ini terdaftar di lebih dari satu akun. Gunakan email untuk mengatur ulang kata sandi.")
	}
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Akun dengan email/nomor tersebut tidak ditemukan")
	}
	channels := channelsFor(user)
	if len(channels) == 0 {
		return helper.ErrorResponse(c, fiber.StatusUnprocessableEntity, "Akun belum punya email atau nomor HP yang valid. Hubungi admin.")
	}
	ch, err := newChallenge(ctx, user.ID, "reset")
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal memulai reset kata sandi")
	}
	return helper.SuccessResponse(c, fiber.StatusOK, "Pilih ke mana kode dikirim", fiber.Map{
		"challenge_id": ch.ID.Hex(),
		"channels":     channels,
	})
}

// ─── POST /api/auth/reset/complete ───────────────────────────────────────────
type ResetCompleteReq struct {
	ChallengeID string `json:"challenge_id"`
	ResetToken  string `json:"reset_token"`
	NewPassword string `json:"new_password"`
}

func ResetComplete(c *fiber.Ctx) error {
	var req ResetCompleteReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Permintaan tidak valid")
	}
	if len(req.NewPassword) < minPasswordChars {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Kata sandi minimal 8 karakter")
	}
	if !dbReady(c) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	ch, err := loadChallenge(ctx, req.ChallengeID)
	if err != nil || ch.Purpose != "reset" || !ch.Verified ||
		!helper.SecretEqual(helper.HashSecret("reset", ch.ID.Hex(), req.ResetToken), ch.ResetTokenHash) {
		return helper.ErrorResponse(c, fiber.StatusUnauthorized, "Sesi reset tidak valid atau sudah berakhir. Ulangi dari awal.")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal mengamankan kata sandi")
	}
	if _, err := usersCol().UpdateOne(ctx, bson.M{"_id": ch.UserID}, bson.M{"$set": bson.M{"password": string(hash)}}); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal menyimpan kata sandi baru")
	}
	// tandai selesai + batalkan sesi verifikasi lain milik akun ini
	_, _ = otpCol().UpdateOne(ctx, bson.M{"_id": ch.ID}, bson.M{"$set": bson.M{"used": true}})
	_, _ = otpCol().UpdateMany(ctx, bson.M{"user_id": ch.UserID, "used": false}, bson.M{"$set": bson.M{"used": true}})
	// Kata sandi baru → keluarkan semua perangkat yang masih masuk dengan kata sandi lama
	revokeUserSessions(ctx, ch.UserID)
	return helper.SuccessResponse(c, fiber.StatusOK, "Kata sandi berhasil diubah. Silakan masuk.", nil)
}

// EnsureOTPIndexes membuat TTL index supaya sesi OTP lama terhapus otomatis.
func EnsureOTPIndexes() {
	if config.DB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := otpCol().Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "expires_at", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
		{Keys: bson.D{{Key: "user_id", Value: 1}}},
	})
	if err != nil {
		log.Printf("[OTP] gagal membuat index: %v", err)
	}
}
