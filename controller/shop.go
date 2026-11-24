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
	"go.mongodb.org/mongo-driver/mongo"
)

// ─── Ruko: sewa, verifikasi admin, masa sewa, perpanjangan ───────────────────

const (
	maxRentalDays = 365
	dailyRent     = 15000.0 // tarif sewa ruko virtual per hari (sama untuk semua ruko)
)

func shopsCol() *mongo.Collection    { return config.DB.Collection("shops") }
func productsCol() *mongo.Collection { return config.DB.Collection("products") }

func loadShop(ctx context.Context, id primitive.ObjectID) (*model.Shop, error) {
	var s model.Shop
	if err := shopsCol().FindOne(ctx, bson.M{"_id": id}).Decode(&s); err != nil {
		return nil, err
	}
	return &s, nil
}

func shopFromParam(c *fiber.Ctx, ctx context.Context) (*model.Shop, error) {
	oid, err := primitive.ObjectIDFromHex(c.Params("id"))
	if err != nil {
		return nil, err
	}
	return loadShop(ctx, oid)
}

// isShopOwner: pengguna ini pemilik ruko tersebut.
func isShopOwner(s *model.Shop, uid primitive.ObjectID) bool {
	return s != nil && uid != primitive.NilObjectID && s.OwnerID == uid
}

// effectiveStatus: "expired" bila sewa sudah habis, walau status tersimpan masih active.
func effectiveStatus(s *model.Shop) string {
	if s.Status == "" {
		return "active"
	}
	if s.Status == "active" && s.RentalExpires.Time().Before(time.Now()) {
		return "expired"
	}
	return s.Status
}

// EffectiveShopStatus: status ruko saat dibaca (active, pending, expired, suspended)
// tanpa mengubah field status di database.
func EffectiveShopStatus(s model.Shop) string { return effectiveStatus(&s) }

// activeShopFilter: ruko yang boleh tampil & berjualan (disetujui admin dan masa sewa berjalan).
func activeShopFilter() bson.M {
	return bson.M{"status": "active", "rental_expires": bson.M{"$gt": primitive.NewDateTimeFromTime(time.Now())}}
}

// shipFrom: hanya kota/kabupaten dari alamat pemilik (bagian terakhir), bukan alamat lengkap.
func shipFrom(address string) string {
	parts := []string{}
	for _, p := range strings.Split(address, ",") {
		if t := strings.TrimSpace(p); t != "" {
			parts = append(parts, t)
		}
	}
	if len(parts) < 2 {
		return ""
	}
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, ", ")
}

// publicShop: data ruko untuk publik; kontak & keuangan hanya untuk pemilik/admin.
func publicShop(ctx context.Context, s *model.Shop, viewer *model.User) fiber.Map {
	var owner model.User
	_ = usersCol().FindOne(ctx, bson.M{"_id": s.OwnerID}).Decode(&owner)
	out := fiber.Map{
		"id":               s.ID,
		"name":             s.Name,
		"description":      s.Description,
		"owner_id":         s.OwnerID,
		"owner_name":       owner.Name,
		"status":           effectiveStatus(s),
		"effective_status": effectiveStatus(s),
		"rental_expires":   s.RentalExpires,
		"created_at":       s.CreatedAt,
		"approved_at":      s.ApprovedAt,
		"ship_from":        shipFrom(owner.Address),
		"owner_avatar":     owner.Avatar,
	}
	switch {
	case viewer != nil && isShopOwner(s, viewer.ID):
		out["raw_status"] = s.Status
		out["rental_price"] = s.RentalPrice
		out["owner_phone"] = owner.Phone
		out["owner_address"] = owner.Address
	case isAdmin(viewer):
		// Admin melihat keuangan sewa, tapi kontak pemilik disamarkan (privasi pengguna)
		out["raw_status"] = s.Status
		out["rental_price"] = s.RentalPrice
		out["owner_phone"] = helper.MaskPhone(helper.NormalizePhone(owner.Phone))
		out["owner_address"] = helper.MaskAddress(owner.Address)
	}
	return out
}

// ─── Harga sewa ──────────────────────────────────────────────────────────────

type rentQuote struct {
	Days      int     `json:"days"`
	DailyRate float64 `json:"daily_rate"`
	Total     float64 `json:"total"`
}

func buildQuote(days int) rentQuote {
	return rentQuote{Days: days, DailyRate: dailyRent, Total: dailyRent * float64(days)}
}

// GET /api/rent/quote?days=30 — harga sewa ruko (baru maupun perpanjangan).
func RentQuote(c *fiber.Ctx) error {
	days := c.QueryInt("days", 30)
	if days < 1 || days > maxRentalDays {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Lama sewa 1–365 hari")
	}
	return helper.SuccessResponse(c, fiber.StatusOK, "ok", buildQuote(days))
}

// ─── Sewa ruko baru ──────────────────────────────────────────────────────────

type RentShopReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	RentalDays  int    `json:"rental_days"`
}

func RentShop(c *fiber.Ctx) error {
	var req RentShopReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Permintaan tidak valid")
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || req.RentalDays < 1 || req.RentalDays > maxRentalDays {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Nama ruko wajib diisi dan lama sewa 1–365 hari")
	}
	u := currentUser(c)
	if u.Role != "seller" {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Hanya akun penjual yang bisa menyewa ruko")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if u.ShopID != primitive.NilObjectID {
		if existing, err := loadShop(ctx, u.ShopID); err == nil && isShopOwner(existing, u.ID) {
			return helper.ErrorResponse(c, fiber.StatusConflict, "Kamu sudah punya ruko "+existing.Name)
		}
	}
	if n, _ := shopsCol().CountDocuments(ctx, bson.M{"name": bson.M{"$regex": "^" + regexp.QuoteMeta(req.Name) + "$", "$options": "i"}}); n > 0 {
		return helper.ErrorResponse(c, fiber.StatusConflict, "Nama ruko sudah dipakai. Pilih nama lain.")
	}

	q := buildQuote(req.RentalDays)
	now := time.Now()
	shop := model.Shop{
		ID:            primitive.NewObjectID(),
		Name:          req.Name,
		Description:   strings.TrimSpace(req.Description),
		OwnerID:       u.ID,
		Status:        "pending", // aktif setelah diverifikasi admin
		RentalPrice:   q.Total,
		RentalExpires: primitive.NewDateTimeFromTime(now.AddDate(0, 0, req.RentalDays)),
		CreatedAt:     primitive.NewDateTimeFromTime(now),
	}
	if _, err := shopsCol().InsertOne(ctx, shop); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal mendaftarkan ruko")
	}
	_, _ = usersCol().UpdateOne(ctx, bson.M{"_id": u.ID}, bson.M{"$set": bson.M{
		"has_shop": true, "shop_id": shop.ID, "shop_name": shop.Name}})

	return helper.SuccessResponse(c, fiber.StatusCreated, "Ruko berhasil disewa dan menunggu verifikasi admin", fiber.Map{
		"shop": publicShop(ctx, &shop, u), "quote": q})
}

// ─── Daftar & detail ruko ────────────────────────────────────────────────────

func ListShops(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database belum terhubung")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cursor, err := shopsCol().Find(ctx, bson.M{})
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal memuat ruko")
	}
	defer cursor.Close(ctx)
	shops := []model.Shop{}
	if err = cursor.All(ctx, &shops); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal membaca data ruko")
	}
	viewer := currentUser(c)
	out := []fiber.Map{}
	for i := range shops {
		out = append(out, publicShop(ctx, &shops[i], viewer))
	}
	return helper.SuccessResponse(c, fiber.StatusOK, "ok", out)
}

// GET /api/shop/mine — ruko milik pengguna yang sedang masuk.
func MyShop(c *fiber.Ctx) error {
	u := currentUser(c)
	if u == nil {
		return helper.ErrorResponse(c, fiber.StatusUnauthorized, "Sesi tidak valid")
	}
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database belum terhubung")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if u.ShopID == primitive.NilObjectID {
		return helper.SuccessResponse(c, fiber.StatusOK, "Belum punya ruko", nil)
	}
	s, err := loadShop(ctx, u.ShopID)
	if err != nil || !isShopOwner(s, u.ID) {
		return helper.SuccessResponse(c, fiber.StatusOK, "Belum punya ruko", nil)
	}
	return helper.SuccessResponse(c, fiber.StatusOK, "ok", publicShop(ctx, s, u))
}

// GET /api/shop/:id — detail satu ruko berdasarkan ID.
func GetShop(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database belum terhubung")
	}
	oid, err := primitive.ObjectIDFromHex(c.Params("id"))
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Format ID ruko tidak valid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := loadShop(ctx, oid)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Ruko tidak ditemukan")
	}
	return helper.SuccessResponse(c, fiber.StatusOK, "ok", publicShop(ctx, s, currentUser(c)))
}

// PUT /api/shop/:id — perbarui profil ruko (nama, deskripsi, kota) oleh pemilik atau admin.
func UpdateShop(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database belum terhubung")
	}
	u, aerr := requireAuth(c)
	if u == nil {
		return aerr
	}
	oid, err := primitive.ObjectIDFromHex(c.Params("id"))
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Format ID ruko tidak valid")
	}
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		City        string `json:"city"`
	}
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Permintaan tidak valid")
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Description = strings.TrimSpace(req.Description)
	req.City = strings.TrimSpace(req.City)
	if req.Name == "" && req.Description == "" && req.City == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Minimal satu kolom profil ruko harus diisi")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := loadShop(ctx, oid)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Ruko tidak ditemukan")
	}
	if !isShopOwner(s, u.ID) && !isAdmin(u) {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Hanya pemilik ruko atau admin yang boleh mengubah profil ruko")
	}

	set := bson.M{}
	if req.Name != "" {
		if len([]rune(req.Name)) < 2 || len([]rune(req.Name)) > 60 {
			return helper.ErrorResponse(c, fiber.StatusBadRequest, "Nama ruko harus 2–60 karakter")
		}
		if !strings.EqualFold(req.Name, s.Name) {
			if n, _ := shopsCol().CountDocuments(ctx, bson.M{
				"_id":  bson.M{"$ne": s.ID},
				"name": bson.M{"$regex": "^" + regexp.QuoteMeta(req.Name) + "$", "$options": "i"},
			}); n > 0 {
				return helper.ErrorResponse(c, fiber.StatusConflict, "Nama ruko sudah dipakai. Pilih nama lain.")
			}
		}
		set["name"] = req.Name
		s.Name = req.Name
	}
	if req.Description != "" {
		set["description"] = req.Description
		s.Description = req.Description
	}
	if req.City != "" {
		set["city"] = req.City
		s.City = req.City
	}

	if _, err := shopsCol().UpdateOne(ctx, bson.M{"_id": s.ID}, bson.M{"$set": set}); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal memperbarui profil ruko")
	}
	if req.Name != "" {
		_, _ = usersCol().UpdateOne(ctx, bson.M{"_id": s.OwnerID}, bson.M{"$set": bson.M{"shop_name": req.Name}})
	}
	return helper.SuccessResponse(c, fiber.StatusOK, "Profil ruko diperbarui", publicShop(ctx, s, u))
}

// ─── Admin: verifikasi / tangguhkan / hapus ──────────────────────────────────

func UpdateShopStatus(c *fiber.Ctx) error {
	var req struct {
		Status string `json:"status"`
	}
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Permintaan tidak valid")
	}
	if req.Status != "active" && req.Status != "suspended" && req.Status != "pending" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Status harus active, suspended, atau pending")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := shopFromParam(c, ctx)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Ruko tidak ditemukan")
	}
	set := bson.M{"status": req.Status}
	if req.Status == "active" && s.ApprovedAt == nil {
		now := time.Now()
		set["approved_at"] = primitive.NewDateTimeFromTime(now)
		// Jika ruko baru disetujui dari status pending, geser masa berlaku sewa agar penjual tidak rugi hari menunggu verifikasi
		if s.Status == "pending" && !s.CreatedAt.Time().IsZero() && !s.RentalExpires.Time().IsZero() {
			if dur := s.RentalExpires.Time().Sub(s.CreatedAt.Time()); dur > 0 {
				set["rental_expires"] = primitive.NewDateTimeFromTime(now.Add(dur))
			}
		}
	}
	if _, err := shopsCol().UpdateOne(ctx, bson.M{"_id": s.ID}, bson.M{"$set": set}); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal mengubah status ruko")
	}
	switch {
	case req.Status == "active" && s.Status == "pending":
		PushUserNotification(s.OwnerID, "shop", "Ruko "+s.Name+" disetujui admin",
			"Rukomu sudah tampil di katalog Vendora. Sekarang kamu bisa menambah produk.", "seller.html")
	case req.Status == "active" && s.Status == "suspended":
		PushUserNotification(s.OwnerID, "shop", "Ruko "+s.Name+" aktif kembali",
			"Penangguhan dicabut. Produkmu tampil lagi di katalog.", "seller.html")
	case req.Status == "suspended" && s.Status != "suspended":
		PushUserNotification(s.OwnerID, "shop", "Ruko "+s.Name+" ditangguhkan",
			"Produkmu disembunyikan dari katalog. Hubungi admin lewat Pusat bantuan untuk verifikasi ulang.", "bantuan.html#hubungi")
	}
	return helper.SuccessResponse(c, fiber.StatusOK, "Status ruko diperbarui", nil)
}

func DeleteShop(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := shopFromParam(c, ctx)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Ruko tidak ditemukan")
	}
	// Ruko yang masih punya pesanan berjalan (belum selesai/batal) tidak boleh dihapus
	active, _ := config.DB.Collection("transactions").CountDocuments(ctx, bson.M{
		"$or":    []bson.M{{"shop_id": s.ID}, {"items.shop_id": s.ID}},
		"status": bson.M{"$nin": []string{"success", "cancelled"}},
	})
	if active > 0 {
		return helper.ErrorResponse(c, fiber.StatusConflict, "Ruko tidak dapat dihapus karena masih memiliki pesanan aktif yang belum selesai atau dibatalkan.")
	}
	if _, err := shopsCol().DeleteOne(ctx, bson.M{"_id": s.ID}); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal menghapus ruko")
	}
	// Bersihkan produk milik ruko yang dihapus agar tidak tertinggal sebagai produk yatim
	_, _ = productsCol().DeleteMany(ctx, bson.M{"shop_id": s.ID})
	_, _ = usersCol().UpdateOne(ctx, bson.M{"_id": s.OwnerID},
		bson.M{"$set": bson.M{"has_shop": false, "shop_id": primitive.NilObjectID, "shop_name": ""}})
	return helper.SuccessResponse(c, fiber.StatusOK, "Ruko dihapus", nil)
}

// ─── Perpanjangan sewa ───────────────────────────────────────────────────────

type RenewShopReq struct {
	RentalDays int `json:"rental_days"`
}

// RenewShop menambah masa sewa: dari tanggal habis, atau dari hari ini bila sudah lewat.
func RenewShop(c *fiber.Ctx) error {
	var req RenewShopReq
	if err := c.BodyParser(&req); err != nil || req.RentalDays < 1 || req.RentalDays > maxRentalDays {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Lama perpanjangan 1–365 hari")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, err := shopFromParam(c, ctx)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Ruko tidak ditemukan")
	}
	u := currentUser(c)
	if !isShopOwner(s, u.ID) && !isAdmin(u) {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Hanya pemilik ruko yang bisa memperpanjang sewa")
	}
	q := buildQuote(req.RentalDays)
	start := s.RentalExpires.Time()
	if start.Before(time.Now()) {
		start = time.Now()
	}
	expires := start.AddDate(0, 0, req.RentalDays)
	set := bson.M{"rental_expires": primitive.NewDateTimeFromTime(expires)}
	if isShopOwner(s, u.ID) {
		// Pemilik membayar perpanjangan; perpanjangan oleh admin (mis. kompensasi) gratis
		set["rental_price"] = s.RentalPrice + q.Total
	}
	if _, err := shopsCol().UpdateOne(ctx, bson.M{"_id": s.ID}, bson.M{"$set": set}); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal memperpanjang sewa")
	}
	return helper.SuccessResponse(c, fiber.StatusOK, "Sewa diperpanjang", fiber.Map{"quote": q, "rental_expires": expires})
}
