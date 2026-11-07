package controller

import (
	"context"
	"encoding/base64"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"vendora/config"
	"vendora/helper"
	"vendora/model"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Chat pembeli ↔ penjual.
// "Real-time" memakai polling singkat (±2 detik) dari browser, karena backend
// berjalan di Vercel (serverless) yang tidak mendukung WebSocket.

const (
	maxChatText  = 2000
	maxChatImage = 3 << 20 // 3 MB setelah didekode (foto sudah dikompres di browser)
)

var allowedChatMime = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
	"image/gif":  true,
}

type SendMsgReq struct {
	SenderID   string `json:"sender_id"`
	ReceiverID string `json:"receiver_id"`
	ShopID     string `json:"shop_id"`
	Text       string `json:"text"`
	Image      string `json:"image"` // data URL: data:image/jpeg;base64,....
}

// decodeDataURL memvalidasi & mendekode foto dari browser.
func decodeDataURL(s string) (mime string, data []byte, errMsg string) {
	const prefix = "data:"
	if !strings.HasPrefix(s, prefix) {
		return "", nil, "Format foto tidak dikenali"
	}
	comma := strings.IndexByte(s, ',')
	if comma < 0 {
		return "", nil, "Format foto tidak dikenali"
	}
	meta := s[len(prefix):comma] // image/jpeg;base64
	if !strings.HasSuffix(meta, ";base64") {
		return "", nil, "Format foto tidak dikenali"
	}
	mime = strings.TrimSuffix(meta, ";base64")
	if !allowedChatMime[mime] {
		return "", nil, "Hanya foto JPG, PNG, WEBP, atau GIF yang bisa dikirim"
	}
	payload := s[comma+1:]
	if base64.StdEncoding.DecodedLen(len(payload)) > maxChatImage+4 {
		return "", nil, "Ukuran foto maksimal 3 MB"
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", nil, "Foto rusak, coba pilih ulang"
	}
	if len(data) > maxChatImage {
		return "", nil, "Ukuran foto maksimal 3 MB"
	}
	// Pastikan isinya benar-benar gambar, bukan sekadar label MIME
	sniffed := http.DetectContentType(data)
	if !allowedChatMime[sniffed] {
		return "", nil, "File yang dikirim bukan foto"
	}
	return sniffed, data, ""
}

func SendMessage(c *fiber.Ctx) error {
	var req SendMsgReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request payload")
	}

	req.Text = strings.TrimSpace(req.Text)
	if req.SenderID == "" || req.ShopID == "" || (req.Text == "" && req.Image == "") {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Pesan atau foto wajib diisi")
	}
	if utf8.RuneCountInString(req.Text) > maxChatText {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Pesan terlalu panjang (maksimal 2000 karakter)")
	}
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database belum terhubung")
	}

	senderOID, err1 := primitive.ObjectIDFromHex(req.SenderID)
	shopOID, err2 := primitive.ObjectIDFromHex(req.ShopID)
	if err1 != nil || err2 != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid ID format")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var receiverOID primitive.ObjectID
	if req.ReceiverID == "" {
		// Pembeli mengirim ke pemilik toko
		var shop model.Shop
		if err := config.DB.Collection("shops").FindOne(ctx, bson.M{"_id": shopOID}).Decode(&shop); err != nil {
			return helper.ErrorResponse(c, fiber.StatusNotFound, "Shop not found")
		}
		receiverOID = shop.OwnerID
	} else {
		recOID, err := primitive.ObjectIDFromHex(req.ReceiverID)
		if err != nil {
			return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid Receiver ID format")
		}
		receiverOID = recOID
	}

	now := primitive.NewDateTimeFromTime(time.Now())
	msg := model.Message{
		ID:         primitive.NewObjectID(),
		SenderID:   senderOID,
		ReceiverID: receiverOID,
		ShopID:     shopOID,
		Text:       req.Text,
		CreatedAt:  now,
	}

	if req.Image != "" {
		mime, data, msgErr := decodeDataURL(req.Image)
		if msgErr != "" {
			return helper.ErrorResponse(c, fiber.StatusBadRequest, msgErr)
		}
		media := model.ChatMedia{
			ID:        primitive.NewObjectID(),
			ShopID:    shopOID,
			SenderID:  senderOID,
			Mime:      mime,
			Data:      data,
			CreatedAt: now,
		}
		if _, err := config.DB.Collection("chat_media").InsertOne(ctx, media); err != nil {
			return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal menyimpan foto")
		}
		msg.Image = media.ID.Hex()
	}

	if _, err := config.DB.Collection("messages").InsertOne(ctx, msg); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to send message")
	}

	return helper.SuccessResponse(c, fiber.StatusCreated, "Message sent", msg)
}

// GetChatMedia mengirim foto chat. ID foto acak (ObjectID) dan tidak bisa ditebak dari daftar.
func GetChatMedia(c *fiber.Ctx) error {
	oid, err := primitive.ObjectIDFromHex(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("ID foto tidak valid")
	}
	if config.DB == nil {
		return c.Status(fiber.StatusInternalServerError).SendString("Database belum terhubung")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	var media model.ChatMedia
	if err := config.DB.Collection("chat_media").FindOne(ctx, bson.M{"_id": oid}).Decode(&media); err != nil {
		return c.Status(fiber.StatusNotFound).SendString("Foto tidak ditemukan")
	}
	c.Set(fiber.HeaderContentType, media.Mime)
	c.Set(fiber.HeaderCacheControl, "private, max-age=31536000, immutable")
	c.Set("X-Content-Type-Options", "nosniff")
	return c.Send(media.Data)
}

// conversationFilter: semua pesan antara pembeli dan pemilik toko di satu toko.
func conversationFilter(shopOID, buyerOID, ownerOID primitive.ObjectID) bson.M {
	return bson.M{
		"shop_id": shopOID,
		"$or": []bson.M{
			{"sender_id": buyerOID, "receiver_id": ownerOID},
			{"sender_id": ownerOID, "receiver_id": buyerOID},
		},
	}
}

func shopOwner(ctx context.Context, shopOID primitive.ObjectID) (primitive.ObjectID, error) {
	var shop model.Shop
	if err := config.DB.Collection("shops").FindOne(ctx, bson.M{"_id": shopOID}).Decode(&shop); err != nil {
		return primitive.NilObjectID, err
	}
	return shop.OwnerID, nil
}

func GetChatHistory(c *fiber.Ctx) error {
	buyerID := c.Query("buyer_id")
	shopID := c.Query("shop_id")
	if buyerID == "" || shopID == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "buyer_id and shop_id are required")
	}

	buyerOID, err1 := primitive.ObjectIDFromHex(buyerID)
	shopOID, err2 := primitive.ObjectIDFromHex(shopID)
	if err1 != nil || err2 != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid ID format")
	}
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database belum terhubung")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	ownerOID, err := shopOwner(ctx, shopOID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Shop not found")
	}

	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}, {Key: "_id", Value: 1}}).SetLimit(500)
	cursor, err := config.DB.Collection("messages").Find(ctx, conversationFilter(shopOID, buyerOID, ownerOID), opts)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to fetch messages")
	}
	defer cursor.Close(ctx)

	messages := []model.Message{}
	if err = cursor.All(ctx, &messages); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to parse messages")
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Messages retrieved", messages)
}

type MarkReadReq struct {
	ReaderID string `json:"reader_id"`
	BuyerID  string `json:"buyer_id"`
	ShopID   string `json:"shop_id"`
}

// MarkChatRead menandai pesan untuk pembaca sebagai sudah dibaca (centang dua).
func MarkChatRead(c *fiber.Ctx) error {
	var req MarkReadReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request payload")
	}
	readerOID, err1 := primitive.ObjectIDFromHex(req.ReaderID)
	buyerOID, err2 := primitive.ObjectIDFromHex(req.BuyerID)
	shopOID, err3 := primitive.ObjectIDFromHex(req.ShopID)
	if err1 != nil || err2 != nil || err3 != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid ID format")
	}
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database belum terhubung")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	ownerOID, err := shopOwner(ctx, shopOID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Shop not found")
	}
	if readerOID != buyerOID && readerOID != ownerOID {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Bukan peserta percakapan ini")
	}

	filter := conversationFilter(shopOID, buyerOID, ownerOID)
	filter["receiver_id"] = readerOID
	filter["read_at"] = bson.M{"$exists": false}
	delete(filter, "$or")
	// Lawan bicara pembaca: pemilik toko bila pembaca pembeli, dan sebaliknya
	if readerOID == buyerOID {
		filter["sender_id"] = ownerOID
	} else {
		filter["sender_id"] = buyerOID
	}

	res, err := config.DB.Collection("messages").UpdateMany(ctx, filter,
		bson.M{"$set": bson.M{"read_at": primitive.NewDateTimeFromTime(time.Now())}})
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal menandai pesan")
	}
	return helper.SuccessResponse(c, fiber.StatusOK, "Pesan ditandai sudah dibaca", fiber.Map{"updated": res.ModifiedCount})
}

type ChatInboxItem struct {
	BuyerID     primitive.ObjectID `json:"buyer_id"`
	BuyerName   string             `json:"buyer_name"`
	BuyerAvatar string             `json:"buyer_avatar"`
	LastMsg     string             `json:"last_message"`
	LastIsImage bool               `json:"last_is_image"`
	LastFromMe  bool               `json:"last_from_me"`
	Unread      int                `json:"unread"`
	UpdatedAt   primitive.DateTime `json:"updated_at"`
}

type BuyerInboxItem struct {
	ShopID      primitive.ObjectID `json:"shop_id"`
	ShopName    string             `json:"shop_name"`
	OwnerID     primitive.ObjectID `json:"owner_id"`
	LastMsg     string             `json:"last_message"`
	LastIsImage bool               `json:"last_is_image"`
	LastFromMe  bool               `json:"last_from_me"`
	Unread      int                `json:"unread"`
	UpdatedAt   primitive.DateTime `json:"updated_at"`
}

// GetBuyerInbox: daftar toko yang pernah dichat seorang pembeli (seperti daftar chat WhatsApp).
func GetBuyerInbox(c *fiber.Ctx) error {
	buyerOID, err := primitive.ObjectIDFromHex(c.Query("buyer_id"))
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "buyer_id tidak valid")
	}
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database belum terhubung")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	opts := options.Find().SetSort(bson.M{"created_at": -1}).SetLimit(2000)
	cursor, err := config.DB.Collection("messages").Find(ctx, bson.M{
		"$or": []bson.M{{"sender_id": buyerOID}, {"receiver_id": buyerOID}},
	}, opts)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal memuat chat")
	}
	defer cursor.Close(ctx)
	var messages []model.Message
	if err = cursor.All(ctx, &messages); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal memuat chat")
	}

	byShop := map[primitive.ObjectID]*BuyerInboxItem{}
	order := []primitive.ObjectID{}
	for _, msg := range messages {
		item, ok := byShop[msg.ShopID]
		if !ok {
			item = &BuyerInboxItem{
				ShopID:      msg.ShopID,
				LastMsg:     msg.Text,
				LastIsImage: msg.Image != "",
				LastFromMe:  msg.SenderID == buyerOID,
				UpdatedAt:   msg.CreatedAt,
			}
			byShop[msg.ShopID] = item
			order = append(order, msg.ShopID)
		}
		if msg.ReceiverID == buyerOID && msg.ReadAt == nil {
			item.Unread++
		}
	}

	inbox := make([]BuyerInboxItem, 0, len(order))
	for _, shopID := range order {
		item := byShop[shopID]
		var shop model.Shop
		if err := config.DB.Collection("shops").FindOne(ctx, bson.M{"_id": shopID}).Decode(&shop); err != nil {
			continue // toko sudah dihapus
		}
		if shop.OwnerID == buyerOID {
			continue // percakapan di toko sendiri tampil di inbox penjual
		}
		item.ShopName = shop.Name
		item.OwnerID = shop.OwnerID
		inbox = append(inbox, *item)
	}
	return helper.SuccessResponse(c, fiber.StatusOK, "Inbox retrieved", inbox)
}

func GetSellerInbox(c *fiber.Ctx) error {
	if c.Query("buyer_id") != "" && c.Query("shop_id") == "" {
		return GetBuyerInbox(c)
	}
	shopID := c.Query("shop_id")
	if shopID == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "shop_id is required")
	}
	shopOID, err := primitive.ObjectIDFromHex(shopID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid shop_id")
	}
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database belum terhubung")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	ownerOID, err := shopOwner(ctx, shopOID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Shop not found")
	}

	opts := options.Find().SetSort(bson.M{"created_at": -1}).SetLimit(2000)
	cursor, err := config.DB.Collection("messages").Find(ctx, bson.M{"shop_id": shopOID}, opts)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to fetch inbox")
	}
	defer cursor.Close(ctx)

	var messages []model.Message
	if err = cursor.All(ctx, &messages); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to parse inbox")
	}

	// Kelompokkan per pembeli; pesan terbaru muncul pertama karena urutan menurun
	byBuyer := map[primitive.ObjectID]*ChatInboxItem{}
	order := []primitive.ObjectID{}
	for _, msg := range messages {
		other := msg.SenderID
		if msg.SenderID == ownerOID {
			other = msg.ReceiverID
		}
		item, ok := byBuyer[other]
		if !ok {
			item = &ChatInboxItem{
				BuyerID:     other,
				LastMsg:     msg.Text,
				LastIsImage: msg.Image != "",
				LastFromMe:  msg.SenderID == ownerOID,
				UpdatedAt:   msg.CreatedAt,
			}
			byBuyer[other] = item
			order = append(order, other)
		}
		if msg.ReceiverID == ownerOID && msg.ReadAt == nil {
			item.Unread++
		}
	}

	inbox := make([]ChatInboxItem, 0, len(order))
	for _, buyerID := range order {
		item := byBuyer[buyerID]
		var buyer model.User
		if err := config.DB.Collection("users").FindOne(ctx, bson.M{"_id": buyerID}).Decode(&buyer); err == nil {
			item.BuyerName = buyer.Name
			item.BuyerAvatar = buyer.Avatar
		}
		if item.BuyerName == "" {
			item.BuyerName = "Pembeli"
		}
		inbox = append(inbox, *item)
	}
	sort.SliceStable(inbox, func(i, j int) bool { return inbox[i].UpdatedAt > inbox[j].UpdatedAt })

	return helper.SuccessResponse(c, fiber.StatusOK, "Inbox retrieved", inbox)
}

// GetChatUnread: jumlah pesan belum dibaca untuk seorang pengguna (badge ikon chat).
func GetChatUnread(c *fiber.Ctx) error {
	userOID, err := primitive.ObjectIDFromHex(c.Query("user_id"))
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "user_id tidak valid")
	}
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database belum terhubung")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := config.DB.Collection("messages").CountDocuments(ctx, bson.M{
		"receiver_id": userOID,
		"read_at":     bson.M{"$exists": false},
	})
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal menghitung pesan")
	}
	return helper.SuccessResponse(c, fiber.StatusOK, "ok", fiber.Map{"unread": n})
}

// EnsureChatIndexes membuat indeks agar polling chat tetap cepat.
func EnsureChatIndexes() {
	if config.DB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = config.DB.Collection("messages").Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "shop_id", Value: 1}, {Key: "created_at", Value: -1}}},
		{Keys: bson.D{{Key: "receiver_id", Value: 1}, {Key: "read_at", Value: 1}}},
	})
}
