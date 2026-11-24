package controller

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"math"
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

// ListAdminProducts menangani GET /api/admin/products (melihat seluruh produk lintas toko termasuk yang jatuh tempo / ditangguhkan / disembunyikan)
func ListAdminProducts(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	shopCur, _ := config.DB.Collection("shops").Find(ctx, bson.M{})
	var shops []model.Shop
	if shopCur != nil {
		_ = shopCur.All(ctx, &shops)
		shopCur.Close(ctx)
	}
	shopMap := map[primitive.ObjectID]model.Shop{}
	for _, s := range shops {
		shopMap[s.ID] = s
	}

	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
	prodCur, err := config.DB.Collection("products").Find(ctx, bson.M{}, opts)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal memuat daftar produk")
	}
	defer prodCur.Close(ctx)

	var prods []model.Product = []model.Product{}
	_ = prodCur.All(ctx, &prods)

	var out []fiber.Map = []fiber.Map{}
	for _, p := range prods {
		shop := shopMap[p.ShopID]
		effShopStatus := "active"
		shopName := "Toko tidak diketahui"
		if !shop.ID.IsZero() {
			shopName = shop.Name
			effShopStatus = EffectiveShopStatus(shop)
		}
		out = append(out, fiber.Map{
			"id":                p.ID,
			"shop_id":           p.ShopID,
			"shop_name":         shopName,
			"shop_status":       effShopStatus,
			"name":              p.Name,
			"description":       p.Description,
			"category":          p.Category,
			"subcategory":       p.Subcategory,
			"price":             p.Price,
			"promo_price":       p.PromoPrice,
			"discount_percent":  p.DiscountPercent,
			"promo_label":       p.PromoLabel,
			"stock":             p.Stock,
			"images":            p.Images,
			"hidden":            p.Hidden,
			"moderation_note":   p.ModerationNote,
			"created_at":        p.CreatedAt,
		})
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "All products retrieved for moderation", out)
}

// ModerateProductVisibility menangani PUT /api/admin/product/:id/visibility (sembunyikan / tampilkan kembali produk oleh admin)
func ModerateProductVisibility(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	oid, err := primitive.ObjectIDFromHex(c.Params("id"))
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "ID produk tidak valid")
	}

	var req struct {
		Hidden bool   `json:"hidden"`
		Reason string `json:"reason"`
	}
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Payload tidak valid")
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Hidden && req.Reason == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Alasan penyembunyian produk wajib diisi")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	var prod model.Product
	if err := config.DB.Collection("products").FindOne(ctx, bson.M{"_id": oid}).Decode(&prod); err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Produk tidak ditemukan")
	}

	_, err = config.DB.Collection("products").UpdateOne(ctx, bson.M{"_id": oid}, bson.M{
		"$set": bson.M{
			"hidden":          req.Hidden,
			"moderation_note": req.Reason,
		},
	})
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal memperbarui status visibilitas produk")
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Status produk diperbarui", fiber.Map{
		"id":              prod.ID,
		"hidden":          req.Hidden,
		"moderation_note": req.Reason,
	})
}

// ExportTransactionsCSV menangani GET /api/admin/transactions/export (dilindungi RequireAdmin)
// Menghasilkan file CSV dengan UTF-8 BOM agar langsung terbuka rapi di Microsoft Excel.
func ExportTransactionsCSV(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	statusFilter := strings.ToLower(strings.TrimSpace(c.Query("status")))
	searchQuery := strings.ToLower(strings.TrimSpace(c.Query("q")))

	// Muat pemetaan user & shop
	userCur, _ := config.DB.Collection("users").Find(ctx, bson.M{})
	var users []model.User
	if userCur != nil {
		_ = userCur.All(ctx, &users)
		userCur.Close(ctx)
	}
	userMap := map[primitive.ObjectID]string{}
	for _, u := range users {
		userMap[u.ID] = u.Name
	}

	shopCur, _ := config.DB.Collection("shops").Find(ctx, bson.M{})
	var shops []model.Shop
	if shopCur != nil {
		_ = shopCur.All(ctx, &shops)
		shopCur.Close(ctx)
	}
	shopMap := map[primitive.ObjectID]string{}
	for _, s := range shops {
		shopMap[s.ID] = s.Name
	}

	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
	txCur, err := config.DB.Collection("transactions").Find(ctx, bson.M{}, opts)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal memuat data transaksi untuk ekspor")
	}
	defer txCur.Close(ctx)

	var allTxs []model.Transaction
	_ = txCur.All(ctx, &allTxs)

	normSt := func(s string) string {
		if s == "pending_payment" {
			return "pending"
		}
		if s == "processing" {
			return "paid"
		}
		if s == "success_pending_release" {
			return "success"
		}
		if s == "cancelling_auto" {
			return "cancelled"
		}
		return s
	}
	statusLabel := map[string]string{
		"pending":   "Menunggu",
		"paid":      "Dikemas",
		"shipped":   "Dikirim",
		"disputed":  "Komplain",
		"success":   "Selesai",
		"cancelled": "Dibatalkan",
	}

	safeCSVCell := func(s string) string {
		if s != "" {
			switch s[0] {
			case '=', '+', '-', '@', '\t', '\r':
				return "'" + s
			}
		}
		return s
	}

	var buf bytes.Buffer
	// Tulis UTF-8 BOM (\xEF\xBB\xBF) agar Microsoft Excel mengenali encoding UTF-8 dengan benar
	buf.Write([]byte{0xEF, 0xBB, 0xBF})

	w := csv.NewWriter(&buf)
	_ = w.Write([]string{
		"ID Pesanan",
		"Tanggal",
		"Pembeli",
		"Toko",
		"Status",
		"Subtotal (Rp)",
		"Ongkir (Rp)",
		"Pajak 2.5% (Rp)",
		"Dana Bersih Penjual (Rp)",
	})

	for _, t := range allTxs {
		stKey := normSt(t.Status)
		if statusFilter != "" && stKey != statusFilter {
			continue
		}

		buyerName := userMap[t.BuyerID]
		if buyerName == "" {
			buyerName = t.RecipientName
		}
		if buyerName == "" {
			buyerName = "Pembeli"
		}

		shopNamesSet := map[string]bool{}
		var shopNames []string
		var itemNames []string
		for _, it := range t.Items {
			itemNames = append(itemNames, it.Name)
			sn := shopMap[it.ShopID]
			if sn != "" && !shopNamesSet[sn] {
				shopNamesSet[sn] = true
				shopNames = append(shopNames, sn)
			}
		}
		shopStr := strings.Join(shopNames, ", ")
		if shopStr == "" {
			shopStr = "-"
		}

		if searchQuery != "" {
			haystack := strings.ToLower(t.ID.Hex() + " " + buyerName + " " + shopStr + " " + strings.Join(itemNames, " "))
			if !strings.Contains(haystack, searchQuery) {
				continue
			}
		}

		subtotal := ComputeOrderSubtotal(t)
		shipping := t.ShippingCost
		tax := t.PlatformFee
		if tax <= 0 {
			tax = math.Round(subtotal * PlatformFeeRate)
		}
		netSeller := t.SellerNetAmount
		if netSeller <= 0 {
			netSeller = subtotal - tax
		}

		stText := statusLabel[stKey]
		if stText == "" {
			stText = t.Status
		}

		dateStr := t.CreatedAt.Time().In(time.Local).Format("2006-01-02 15:04:05")
		_ = w.Write([]string{
			t.ID.Hex(),
			dateStr,
			safeCSVCell(helper.MaskAddress(buyerName)),
			safeCSVCell(shopStr),
			stText,
			fmt.Sprintf("%.0f", subtotal),
			fmt.Sprintf("%.0f", shipping),
			fmt.Sprintf("%.0f", tax),
			fmt.Sprintf("%.0f", netSeller),
		})
	}
	w.Flush()

	filename := fmt.Sprintf("laporan_transaksi_vendora_%s.csv", time.Now().Format("20060102_1504"))
	c.Set("Content-Type", "text/csv; charset=utf-8")
	c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	return c.Send(buf.Bytes())
}

// CreateReport menangani POST /api/reports (pembeli/pengguna melaporkan produk atau toko yang melanggar)
func CreateReport(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	var req struct {
		TargetType  string `json:"target_type"` // product | shop
		ProductID   string `json:"product_id"`
		ShopID      string `json:"shop_id"`
		Reason      string `json:"reason"`
		Description string `json:"description"`
	}
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Payload laporan tidak valid")
	}

	req.Reason = strings.TrimSpace(req.Reason)
	req.Description = strings.TrimSpace(req.Description)
	if req.Reason == "" || len([]rune(req.Description)) < 5 {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Pilih alasan dan tulis deskripsi laporan (minimal 5 karakter)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	var reporterID primitive.ObjectID
	reporterName := "Pengunjung / Pembeli"
	if u := AuthUser(c); u != nil {
		reporterID = u.ID
		if u.Name != "" {
			reporterName = u.Name
		}
	}

	targetType := strings.ToLower(strings.TrimSpace(req.TargetType))
	if targetType != "shop" {
		targetType = "product"
	}

	var prodOID, shopOID primitive.ObjectID
	var prodName, shopName string

	if req.ProductID != "" {
		if oid, err := primitive.ObjectIDFromHex(req.ProductID); err == nil {
			prodOID = oid
			var p model.Product
			if err := config.DB.Collection("products").FindOne(ctx, bson.M{"_id": oid}).Decode(&p); err == nil {
				prodName = p.Name
				shopOID = p.ShopID
			}
		}
	}
	if req.ShopID != "" && shopOID.IsZero() {
		if oid, err := primitive.ObjectIDFromHex(req.ShopID); err == nil {
			shopOID = oid
		}
	}
	if !shopOID.IsZero() {
		var s model.Shop
		if err := config.DB.Collection("shops").FindOne(ctx, bson.M{"_id": shopOID}).Decode(&s); err == nil {
			shopName = s.Name
		}
	}

	rep := model.Report{
		ID:           primitive.NewObjectID(),
		ReporterID:   reporterID,
		ReporterName: reporterName,
		TargetType:   targetType,
		ProductID:    prodOID,
		ProductName:  prodName,
		ShopID:       shopOID,
		ShopName:     shopName,
		Reason:       req.Reason,
		Description:  req.Description,
		Status:       "open",
		CreatedAt:    primitive.NewDateTimeFromTime(time.Now()),
	}

	if _, err := config.DB.Collection("reports").InsertOne(ctx, rep); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal menyimpan laporan")
	}

	return helper.SuccessResponse(c, fiber.StatusCreated, "Laporan berhasil dikirim ke tim moderator Admin", rep)
}

// ListAdminReports menangani GET /api/admin/reports (dilindungi RequireAdmin)
func ListAdminReports(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
	cur, err := config.DB.Collection("reports").Find(ctx, bson.M{}, opts)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal memuat daftar laporan")
	}
	defer cur.Close(ctx)

	var reports []model.Report = []model.Report{}
	_ = cur.All(ctx, &reports)

	return helper.SuccessResponse(c, fiber.StatusOK, "Reports retrieved", reports)
}

// ResolveAdminReport menangani PUT /api/admin/report/:id/resolve (dilindungi RequireAdmin)
func ResolveAdminReport(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	oid, err := primitive.ObjectIDFromHex(c.Params("id"))
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "ID laporan tidak valid")
	}

	var req struct {
		Status    string `json:"status"` // resolved | dismissed
		Note      string `json:"note"`
		AdminNote string `json:"admin_note"`
	}
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Payload tidak valid")
	}
	if req.Status != "resolved" && req.Status != "dismissed" {
		req.Status = "resolved"
	}
	note := strings.TrimSpace(req.Note)
	if note == "" {
		note = strings.TrimSpace(req.AdminNote)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	var rep model.Report
	if err := config.DB.Collection("reports").FindOne(ctx, bson.M{"_id": oid}).Decode(&rep); err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Laporan tidak ditemukan")
	}

	nowDT := primitive.NewDateTimeFromTime(time.Now())
	_, err = config.DB.Collection("reports").UpdateOne(ctx, bson.M{"_id": oid}, bson.M{
		"$set": bson.M{
			"status":      req.Status,
			"admin_note":  note,
			"resolved_at": nowDT,
		},
	})
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal memperbarui laporan")
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Status laporan diperbarui", nil)
}

