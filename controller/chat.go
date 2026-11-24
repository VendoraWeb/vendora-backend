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
	ReceiverID string `json:"receiver_id"` // diisi pihak ruko: ID pembeli yang dibalas
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

// ─── Percakapan = (ruko, pembeli) ────────────────────────────────────────────
// Pemilik ruko membalas dari kotak masuk rukonya.
// Pengirim selalu pengguna yang sedang masuk — tidak bisa mengirim atas nama orang lain.

const greetingText = "Terima kasih sudah menghubungi kami! Pesanmu sudah diterima dan akan segera dibalas oleh penjual."

func conversationFilter(shopOID, buyerOID primitive.ObjectID) bson.M {
	return bson.M{"shop_id": shopOID, "$or": []bson.M{{"sender_id": buyerOID}, {"receiver_id": buyerOID}}}
}

func SendMessage(c *fiber.Ctx) error {
	var req SendMsgReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Permintaan tidak valid")
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.ShopID == "" || (req.Text == "" && req.Image == "") {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Pesan atau foto wajib diisi")
	}
	if utf8.RuneCountInString(req.Text) > maxChatText {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Pesan terlalu panjang (maksimal 2000 karakter)")
	}
	shopOID, err := primitive.ObjectIDFromHex(req.ShopID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Ruko tidak valid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	shop, err := loadShop(ctx, shopOID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Ruko tidak ditemukan")
	}
	me := currentUser(c)
	shopSide := isShopOwner(shop, me.ID)

	var buyerOID, receiverOID primitive.ObjectID
	if shopSide {
		receiverOID, err = primitive.ObjectIDFromHex(req.ReceiverID)
		if err != nil || isShopOwner(shop, receiverOID) {
			return helper.ErrorResponse(c, fiber.StatusBadRequest, "Pilih pembeli yang akan dibalas")
		}
		buyerOID = receiverOID
	} else {
		buyerOID, receiverOID = me.ID, shop.OwnerID
	}
	isFirst := false
	if !shopSide {
		n, _ := config.DB.Collection("messages").CountDocuments(ctx, conversationFilter(shopOID, buyerOID))
		isFirst = n == 0
	}

	now := time.Now()
	msg := model.Message{ID: primitive.NewObjectID(), SenderID: me.ID, ReceiverID: receiverOID, ShopID: shopOID,
		Text: req.Text, CreatedAt: primitive.NewDateTimeFromTime(now)}
	if req.Image != "" {
		mime, data, msgErr := decodeDataURL(req.Image)
		if msgErr != "" {
			return helper.ErrorResponse(c, fiber.StatusBadRequest, msgErr)
		}
		media := model.ChatMedia{ID: primitive.NewObjectID(), ShopID: shopOID, SenderID: me.ID, Mime: mime, Data: data,
			CreatedAt: primitive.NewDateTimeFromTime(now)}
		if _, err := config.DB.Collection("chat_media").InsertOne(ctx, media); err != nil {
			return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal menyimpan foto")
		}
		msg.Image = media.ID.Hex()
	}
	if _, err := config.DB.Collection("messages").InsertOne(ctx, msg); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Pesan gagal terkirim")
	}
	// Pesan pertama pembeli → salam otomatis toko (ditandai auto)
	if isFirst {
		_, _ = config.DB.Collection("messages").InsertOne(ctx, model.Message{ID: primitive.NewObjectID(), SenderID: shop.OwnerID,
			ReceiverID: buyerOID, ShopID: shopOID, Text: greetingText, Auto: true,
			CreatedAt: primitive.NewDateTimeFromTime(now.Add(time.Second))})
	}
	return helper.SuccessResponse(c, fiber.StatusCreated, "Pesan terkirim", msg)
}

// canSeeConversation: pembeli itu sendiri, pemilik ruko, atau admin.
func canSeeConversation(me *model.User, shop *model.Shop, buyerOID primitive.ObjectID) bool {
	return me.ID == buyerOID || isAdmin(me) || isShopOwner(shop, me.ID)
}

func GetChatHistory(c *fiber.Ctx) error {
	buyerOID, err1 := primitive.ObjectIDFromHex(c.Query("buyer_id"))
	shopOID, err2 := primitive.ObjectIDFromHex(c.Query("shop_id"))
	if err1 != nil || err2 != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "buyer_id dan shop_id wajib diisi")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shop, err := loadShop(ctx, shopOID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Ruko tidak ditemukan")
	}
	if !canSeeConversation(currentUser(c), shop, buyerOID) {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Kamu bukan peserta percakapan ini")
	}
	// Ambil 500 pesan terbaru lalu balik ke urutan kronologis agar pesan ke-501+ tidak terpotong
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}).SetLimit(500)
	cursor, err := config.DB.Collection("messages").Find(ctx, conversationFilter(shopOID, buyerOID), opts)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal memuat pesan")
	}
	defer cursor.Close(ctx)
	messages := []model.Message{}
	if err = cursor.All(ctx, &messages); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal membaca pesan")
	}
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return helper.SuccessResponse(c, fiber.StatusOK, "ok", messages)
}

type MarkReadReq struct {
	BuyerID string `json:"buyer_id"`
	ShopID  string `json:"shop_id"`
}

// MarkChatRead: tandai pesan dari pihak lawan sebagai dibaca (centang biru).
func MarkChatRead(c *fiber.Ctx) error {
	var req MarkReadReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Permintaan tidak valid")
	}
	buyerOID, err1 := primitive.ObjectIDFromHex(req.BuyerID)
	shopOID, err2 := primitive.ObjectIDFromHex(req.ShopID)
	if err1 != nil || err2 != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "ID tidak valid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	shop, err := loadShop(ctx, shopOID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Ruko tidak ditemukan")
	}
	me := currentUser(c)
	filter := bson.M{"shop_id": shopOID, "read_at": bson.M{"$exists": false}}
	switch {
	case me.ID == buyerOID:
		filter["receiver_id"] = buyerOID
	case isShopOwner(shop, me.ID):
		filter["sender_id"] = buyerOID
	default:
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Kamu bukan peserta percakapan ini")
	}
	res, err := config.DB.Collection("messages").UpdateMany(ctx, filter,
		bson.M{"$set": bson.M{"read_at": primitive.NewDateTimeFromTime(time.Now())}})
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal menandai pesan")
	}
	return helper.SuccessResponse(c, fiber.StatusOK, "ok", fiber.Map{"updated": res.ModifiedCount})
}

type ChatInboxItem struct {
	BuyerID     primitive.ObjectID `json:"buyer_id"`
	BuyerName   string             `json:"buyer_name"`
	BuyerAvatar string             `json:"buyer_avatar"`
	LastMsg     string             `json:"last_message"`
	LastIsImage bool               `json:"last_is_image"`
	LastFromMe  bool               `json:"last_from_me"` // dibalas pihak ruko
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

// GetBuyerInbox: daftar ruko yang pernah dichat pengguna sebagai pembeli.
func GetBuyerInbox(c *fiber.Ctx) error {
	me := currentUser(c)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	opts := options.Find().SetSort(bson.M{"created_at": -1}).SetLimit(2000)
	cursor, err := config.DB.Collection("messages").Find(ctx, bson.M{"$or": []bson.M{{"sender_id": me.ID}, {"receiver_id": me.ID}}}, opts)
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
			item = &BuyerInboxItem{ShopID: msg.ShopID, LastMsg: msg.Text, LastIsImage: msg.Image != "",
				LastFromMe: msg.SenderID == me.ID, UpdatedAt: msg.CreatedAt}
			byShop[msg.ShopID] = item
			order = append(order, msg.ShopID)
		}
		if msg.ReceiverID == me.ID && msg.ReadAt == nil {
			item.Unread++
		}
	}
	inbox := make([]BuyerInboxItem, 0, len(order))
	for _, shopID := range order {
		shop, err := loadShop(ctx, shopID)
		if err != nil || isShopOwner(shop, me.ID) {
			continue // ruko dihapus, atau ruko sendiri (tampil di kotak masuk ruko)
		}
		item := byShop[shopID]
		item.ShopName, item.OwnerID = shop.Name, shop.OwnerID
		inbox = append(inbox, *item)
	}
	return helper.SuccessResponse(c, fiber.StatusOK, "ok", inbox)
}

// GetSellerInbox: kotak masuk ruko untuk pemiliknya.
func GetSellerInbox(c *fiber.Ctx) error {
	if c.Query("shop_id") == "" {
		return GetBuyerInbox(c)
	}
	shopOID, err := primitive.ObjectIDFromHex(c.Query("shop_id"))
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "shop_id tidak valid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shop, err := loadShop(ctx, shopOID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Ruko tidak ditemukan")
	}
	me := currentUser(c)
	if !isShopOwner(shop, me.ID) && !isAdmin(me) {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Kamu bukan pemilik ruko ini")
	}
	opts := options.Find().SetSort(bson.M{"created_at": -1}).SetLimit(2000)
	cursor, err := config.DB.Collection("messages").Find(ctx, bson.M{"shop_id": shopOID}, opts)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal memuat kotak masuk")
	}
	defer cursor.Close(ctx)
	var messages []model.Message
	if err = cursor.All(ctx, &messages); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal memuat kotak masuk")
	}
	byBuyer := map[primitive.ObjectID]*ChatInboxItem{}
	order := []primitive.ObjectID{}
	for _, msg := range messages {
		buyer := msg.SenderID
		if msg.SenderID == shop.OwnerID {
			buyer = msg.ReceiverID
		}
		if buyer == shop.OwnerID {
			continue // pesan ke diri sendiri tidak ditampilkan
		}
		item, ok := byBuyer[buyer]
		if !ok {
			item = &ChatInboxItem{BuyerID: buyer, LastMsg: msg.Text, LastIsImage: msg.Image != "",
				LastFromMe: msg.SenderID == shop.OwnerID, UpdatedAt: msg.CreatedAt}
			byBuyer[buyer] = item
			order = append(order, buyer)
		}
		if msg.SenderID == buyer && msg.ReadAt == nil {
			item.Unread++
		}
	}
	inbox := make([]ChatInboxItem, 0, len(order))
	for _, buyerID := range order {
		item := byBuyer[buyerID]
		var buyer model.User
		if err := usersCol().FindOne(ctx, bson.M{"_id": buyerID}).Decode(&buyer); err == nil {
			item.BuyerName, item.BuyerAvatar = buyer.Name, buyer.Avatar
		}
		if item.BuyerName == "" {
			item.BuyerName = "Pembeli"
		}
		inbox = append(inbox, *item)
	}
	sort.SliceStable(inbox, func(i, j int) bool { return inbox[i].UpdatedAt > inbox[j].UpdatedAt })
	return helper.SuccessResponse(c, fiber.StatusOK, "ok", inbox)
}

// GetChatUnread: jumlah pesan belum dibaca untuk badge tombol chat.
func GetChatUnread(c *fiber.Ctx) error {
	me := currentUser(c)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	col := config.DB.Collection("messages")
	asBuyer := bson.M{"receiver_id": me.ID, "read_at": bson.M{"$exists": false}}
	total := int64(0)
	if me.ShopID != primitive.NilObjectID {
		if shop, err := loadShop(ctx, me.ShopID); err == nil && isShopOwner(shop, me.ID) {
			asBuyer["shop_id"] = bson.M{"$ne": shop.ID}
			n, _ := col.CountDocuments(ctx, bson.M{"shop_id": shop.ID, "sender_id": bson.M{"$ne": shop.OwnerID},
				"read_at": bson.M{"$exists": false}})
			total += n
		}
	}
	n, _ := col.CountDocuments(ctx, asBuyer)
	total += n
	return helper.SuccessResponse(c, fiber.StatusOK, "ok", fiber.Map{"unread": total})
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
