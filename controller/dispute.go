package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"vendora/config"
	"vendora/helper"
	"vendora/model"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var disputeReasons = map[string]string{
	"not_received":     "Barang tidak sampai",
	"not_as_described": "Barang tidak sesuai deskripsi",
	"damaged":          "Barang rusak / cacat",
	"other":            "Alasan lain",
}

func shortTxID(id primitive.ObjectID) string {
	h := id.Hex()
	return strings.ToUpper(h[len(h)-6:])
}

// CreateDispute menangani POST /api/disputes (pembeli mengajukan komplain/retur).
// Syarat: pesanan milik pembeli, sudah dikirim (shipped), dana masih di Escrow.
// Efek: status pesanan jadi "disputed" sehingga dana beku & tidak selesai otomatis.
func CreateDispute(c *fiber.Ctx) error {
	user, aerr := requireAuth(c)
	if user == nil {
		return aerr
	}
	var req struct {
		TransactionID string   `json:"transaction_id"`
		Reason        string   `json:"reason"`
		Description   string   `json:"description"`
		Evidence      []string `json:"evidence"`
	}
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request payload")
	}
	txOID, err := primitive.ObjectIDFromHex(req.TransactionID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "ID pesanan tidak valid")
	}
	if _, ok := disputeReasons[req.Reason]; !ok {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Pilih alasan komplain")
	}
	req.Description = strings.TrimSpace(req.Description)
	if len([]rune(req.Description)) < 10 {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Jelaskan masalahnya minimal 10 karakter")
	}
	cleanEvidence := []string{}
	for _, raw := range req.Evidence {
		u := strings.TrimSpace(raw)
		lower := strings.ToLower(u)
		if (strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://")) && !strings.ContainsAny(u, " \"<>'\\") {
			cleanEvidence = append(cleanEvidence, u)
			if len(cleanEvidence) >= 5 {
				break
			}
		}
	}
	req.Evidence = cleanEvidence

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var tx model.Transaction
	if err := config.DB.Collection("transactions").FindOne(ctx, bson.M{"_id": txOID}).Decode(&tx); err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Pesanan tidak ditemukan")
	}
	if tx.BuyerID != user.ID {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Hanya pembeli pesanan ini yang bisa mengajukan komplain")
	}
	if tx.Status != "shipped" || tx.EscrowStatus != "held" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Komplain hanya bisa diajukan saat pesanan berstatus Dikirim dan dana masih di Escrow")
	}

	// Bekukan pesanan secara atomik (cegah dobel komplain / bentrok dengan auto-complete)
	res, err := config.DB.Collection("transactions").UpdateOne(ctx,
		bson.M{"_id": txOID, "status": "shipped"},
		bson.M{"$set": bson.M{"status": "disputed"}, "$unset": bson.M{"auto_complete_at": ""}},
	)
	if err != nil || res.ModifiedCount == 0 {
		return helper.ErrorResponse(c, fiber.StatusConflict, "Pesanan sudah berubah status. Muat ulang halaman.")
	}

	shopSet := map[primitive.ObjectID]bool{}
	var shopIDs []primitive.ObjectID
	for _, it := range tx.Items {
		if !it.ShopID.IsZero() && !shopSet[it.ShopID] {
			shopSet[it.ShopID] = true
			shopIDs = append(shopIDs, it.ShopID)
		}
	}

	d := model.Dispute{
		ID:            primitive.NewObjectID(),
		TransactionID: txOID,
		BuyerID:       user.ID,
		ShopIDs:       shopIDs,
		Reason:        req.Reason,
		Description:   req.Description,
		Evidence:      req.Evidence,
		Status:        "open",
		Amount:        ComputeOrderSubtotal(tx),
		CreatedAt:     primitive.NewDateTimeFromTime(time.Now()),
	}
	if _, err := config.DB.Collection("disputes").InsertOne(ctx, d); err != nil {
		// kembalikan status bila gagal menyimpan komplain
		_, _ = config.DB.Collection("transactions").UpdateOne(ctx, bson.M{"_id": txOID}, bson.M{"$set": bson.M{"status": "shipped", "auto_complete_at": tx.AutoCompleteAt}})
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal menyimpan komplain")
	}

	// Beri tahu penjual
	for _, sid := range shopIDs {
		var shop model.Shop
		if err := config.DB.Collection("shops").FindOne(ctx, bson.M{"_id": sid}).Decode(&shop); err == nil {
			PushUserNotification(shop.OwnerID, "dispute", "Pembeli Mengajukan Komplain",
				fmt.Sprintf("Pesanan #%s dikomplain: %s. Dana ditahan sampai admin memutuskan. Beri tanggapanmu di dashboard.", shortTxID(txOID), disputeReasons[req.Reason]), "seller.html")
		}
	}
	PushUserNotification(user.ID, "dispute", "Komplain Diterima",
		fmt.Sprintf("Komplain pesanan #%s sedang ditinjau admin. Dana kamu tetap aman di Escrow.", shortTxID(txOID)), "pesanan.html")

	return helper.SuccessResponse(c, fiber.StatusCreated, "Komplain diajukan. Dana ditahan sampai admin memutuskan.", d)
}

// ListDisputes menangani GET /api/disputes
//
//	admin  → semua komplain (?status=open untuk yang belum diputuskan)
//	buyer  → komplain miliknya
//	seller → komplain pada toko miliknya
func ListDisputes(c *fiber.Ctx) error {
	user, aerr := requireAuth(c)
	if user == nil {
		return aerr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	filter := bson.M{}
	switch user.Role {
	case "admin":
	case "seller":
		cur, _ := config.DB.Collection("shops").Find(ctx, bson.M{"owner_id": user.ID})
		var shops []model.Shop
		if cur != nil {
			_ = cur.All(ctx, &shops)
			cur.Close(ctx)
		}
		ids := []primitive.ObjectID{}
		for _, s := range shops {
			ids = append(ids, s.ID)
		}
		filter["$or"] = []bson.M{
			{"shop_ids": bson.M{"$in": ids}},
			{"buyer_id": user.ID},
		}
	default:
		filter["buyer_id"] = user.ID
	}
	if st := strings.TrimSpace(c.Query("status")); st != "" {
		filter["status"] = st
	}

	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(100)
	cur, err := config.DB.Collection("disputes").Find(ctx, filter, opts)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal memuat komplain")
	}
	var list []model.Dispute = []model.Dispute{}
	_ = cur.All(ctx, &list)
	cur.Close(ctx)

	// Lengkapi informasi nominal pesanan (Subtotal barang, Ongkir, dan Total termasuk ongkir) agar sinkron dengan halaman Transaksi
	var enriched []fiber.Map = []fiber.Map{}
	for _, d := range list {
		var tx model.Transaction
		sub := d.Amount
		ship := 0.0
		tot := d.Amount
		if err := config.DB.Collection("transactions").FindOne(ctx, bson.M{"_id": d.TransactionID}).Decode(&tx); err == nil {
			sub = ComputeOrderSubtotal(tx)
			ship = tx.ShippingCost
			tot = tx.TotalAmount
			if tot <= 0 {
				tot = sub + ship
			}
		}
		enriched = append(enriched, fiber.Map{
			"id":              d.ID,
			"transaction_id":  d.TransactionID,
			"buyer_id":        d.BuyerID,
			"shop_ids":        d.ShopIDs,
			"reason":          d.Reason,
			"description":     d.Description,
			"evidence":        d.Evidence,
			"seller_response": d.SellerResponse,
			"status":          d.Status,
			"admin_note":      d.AdminNote,
			"amount":          d.Amount,
			"subtotal_amount": sub,
			"shipping_cost":   ship,
			"total_amount":    tot,
			"created_at":      d.CreatedAt,
			"resolved_at":     d.ResolvedAt,
		})
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Disputes retrieved", enriched)
}

// ReplyDispute menangani PUT /api/dispute/:id/reply (penjual menanggapi komplain)
func ReplyDispute(c *fiber.Ctx) error {
	user, aerr := requireAuth(c)
	if user == nil {
		return aerr
	}
	oid, err := primitive.ObjectIDFromHex(c.Params("id"))
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "ID komplain tidak valid")
	}
	var req struct {
		Response string `json:"response"`
	}
	if err := c.BodyParser(&req); err != nil || strings.TrimSpace(req.Response) == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Tanggapan tidak boleh kosong")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	var d model.Dispute
	if err := config.DB.Collection("disputes").FindOne(ctx, bson.M{"_id": oid}).Decode(&d); err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Komplain tidak ditemukan")
	}
	if d.Status != "open" {
		return helper.ErrorResponse(c, fiber.StatusConflict, "Komplain sudah diputuskan")
	}
	n, _ := config.DB.Collection("shops").CountDocuments(ctx, bson.M{"_id": bson.M{"$in": d.ShopIDs}, "owner_id": user.ID})
	if n == 0 {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Hanya penjual terkait yang bisa menanggapi")
	}
	_, _ = config.DB.Collection("disputes").UpdateOne(ctx, bson.M{"_id": oid}, bson.M{"$set": bson.M{"seller_response": strings.TrimSpace(req.Response)}})
	return helper.SuccessResponse(c, fiber.StatusOK, "Tanggapan terkirim ke admin", nil)
}

// ResolveDispute menangani PUT /api/dispute/:id/resolve (khusus admin)
//
//	decision = "refund"  → dana Escrow dikembalikan ke pembeli, pesanan dibatalkan
//	decision = "release" → dana diteruskan ke penjual (dipotong pajak), pesanan selesai
func ResolveDispute(c *fiber.Ctx) error {
	user, aerr := requireAuth(c)
	if user == nil {
		return aerr
	}
	if user.Role != "admin" {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Hanya admin yang bisa memutuskan komplain")
	}
	oid, err := primitive.ObjectIDFromHex(c.Params("id"))
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "ID komplain tidak valid")
	}
	var req struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}
	if err := c.BodyParser(&req); err != nil || (req.Decision != "refund" && req.Decision != "release") {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "decision harus 'refund' atau 'release'")
	}
	req.Note = strings.TrimSpace(req.Note)
	if req.Note == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Tulis alasan keputusan untuk pembeli dan penjual")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	newStatus := "resolved_release"
	if req.Decision == "refund" {
		newStatus = "resolved_refund"
	}
	nowDT := primitive.NewDateTimeFromTime(time.Now())

	// Klaim atomik: hanya satu keputusan yang berlaku
	var d model.Dispute
	err = config.DB.Collection("disputes").FindOneAndUpdate(ctx,
		bson.M{"_id": oid, "status": "open"},
		bson.M{"$set": bson.M{"status": newStatus, "admin_note": req.Note, "resolved_at": nowDT}},
	).Decode(&d)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusConflict, "Komplain tidak ditemukan atau sudah diputuskan")
	}

	var tx model.Transaction
	if err := config.DB.Collection("transactions").FindOne(ctx, bson.M{"_id": d.TransactionID}).Decode(&tx); err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Pesanan terkait tidak ditemukan")
	}

	if req.Decision == "refund" {
		refundEscrowAndRestoreStock(ctx, tx, false)
		_, _ = config.DB.Collection("transactions").UpdateOne(ctx, bson.M{"_id": tx.ID},
			bson.M{"$set": bson.M{"status": "cancelled", "escrow_status": "refunded"}})
		PushUserNotification(tx.BuyerID, "dispute", "Komplain Dimenangkan — Dana Dikembalikan",
			fmt.Sprintf("Admin memutuskan refund untuk pesanan #%s. Catatan: %s", shortTxID(tx.ID), req.Note), "pesanan.html")
	} else {
		releaseEscrowFunds(ctx, tx)
		_, _ = config.DB.Collection("transactions").UpdateOne(ctx, bson.M{"_id": tx.ID},
			bson.M{"$set": bson.M{"status": "success", "escrow_status": "released", "released_at": nowDT}})
		PushUserNotification(tx.BuyerID, "dispute", "Komplain Ditolak — Dana Diteruskan ke Penjual",
			fmt.Sprintf("Admin memutuskan dana pesanan #%s diteruskan ke penjual. Catatan: %s", shortTxID(tx.ID), req.Note), "pesanan.html")
	}

	// Beri tahu penjual
	for _, sid := range d.ShopIDs {
		var shop model.Shop
		if err := config.DB.Collection("shops").FindOne(ctx, bson.M{"_id": sid}).Decode(&shop); err == nil {
			title, msg := "Komplain Diputuskan — Dana Diteruskan", "Admin memutuskan dana pesanan #%s diteruskan ke Saldo Aktif kamu. Catatan: %s"
			if req.Decision == "refund" {
				title, msg = "Komplain Diputuskan — Refund ke Pembeli", "Admin memutuskan dana pesanan #%s dikembalikan ke pembeli. Catatan: %s"
			}
			PushUserNotification(shop.OwnerID, "dispute", title, fmt.Sprintf(msg, shortTxID(tx.ID), req.Note), "seller.html")
		}
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Keputusan komplain tersimpan", fiber.Map{"status": newStatus})
}

