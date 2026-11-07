package model

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// OTPChallenge menyimpan satu sesi verifikasi OTP (login atau reset kata sandi).
// Kode OTP dan token reset hanya disimpan dalam bentuk hash (HMAC-SHA256).
type OTPChallenge struct {
	ID             primitive.ObjectID `bson:"_id"`
	UserID         primitive.ObjectID `bson:"user_id"`
	Purpose        string             `bson:"purpose"` // "login" | "reset"
	Channel        string             `bson:"channel"` // "email" | "phone" (saluran terakhir yang dipakai)
	CodeHash       string             `bson:"code_hash"`
	CodeExpiresAt  time.Time          `bson:"code_expires_at"`
	Attempts       int                `bson:"attempts"`
	SendCount      int                `bson:"send_count"`
	LastSentAt     time.Time          `bson:"last_sent_at"`
	Verified       bool               `bson:"verified"`
	ResetTokenHash string             `bson:"reset_token_hash"`
	Used           bool               `bson:"used"`
	DevMode        bool               `bson:"dev_mode"`   // kode hanya dicetak di log (pengembangan)
	ExpiresAt      time.Time          `bson:"expires_at"` // dihapus otomatis oleh TTL index
	CreatedAt      time.Time          `bson:"created_at"`
}
