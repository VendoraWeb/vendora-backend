package controller

import (
	"context"
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

type ProductReq struct {
	ShopID      string               `json:"shop_id"`
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Category    *string              `json:"category"`
	Subcategory *string              `json:"subcategory"`
	Price       float64              `json:"price"`
	PromoPrice  *float64             `json:"promo_price"` // >0 = aktifkan promo; 0 = matikan promo
	PromoLabel  *string              `json:"promo_label"` // mis. "Flash Sale", "Diskon Spesial"
	Stock       *int                 `json:"stock"`
	Images      []string             `json:"images"`
	Options     *[]model.OptionGroup `json:"options"`    // dikirim = diganti; [] = hapus semua variasi
	Specs       *[]model.Spec        `json:"specs"`      // dikirim = diganti
	SizeGuide   *model.SizeGuide     `json:"size_guide"` // kolom kosong = hapus panduan ukuran
}

const (
	maxOptionGroups = 3
	maxOptionValues = 12
)

// cleanOptions merapikan variasi dari penjual: buang label kosong/duplikat,
// batasi jumlah, dan tolak tambahan harga negatif yang membuat harga <= 0.
func cleanOptions(groups []model.OptionGroup, basePrice float64) ([]model.OptionGroup, string) {
	out := []model.OptionGroup{}
	minDeltaSum := 0.0
	for _, g := range groups {
		g.Name = strings.TrimSpace(g.Name)
		if g.Name == "" {
			continue
		}
		seen := map[string]bool{}
		vals := []model.OptionValue{}
		groupMin := 0.0
		hasGroupMin := false
		for _, v := range g.Values {
			v.Label = strings.TrimSpace(v.Label)
			key := strings.ToLower(v.Label)
			if v.Label == "" || seen[key] {
				continue
			}
			if basePrice > 0 && basePrice+v.PriceDelta <= 0 {
				return nil, "Tambahan harga variasi \"" + v.Label + "\" membuat harga jadi nol atau minus"
			}
			if !hasGroupMin || v.PriceDelta < groupMin {
				groupMin = v.PriceDelta
				hasGroupMin = true
			}
			seen[key] = true
			vals = append(vals, v)
		}
		if len(vals) == 0 {
			continue
		}
		if len(vals) > maxOptionValues {
			return nil, "Maksimal 12 pilihan untuk variasi " + g.Name
		}
		if hasGroupMin && groupMin < 0 {
			minDeltaSum += groupMin
		}
		g.Values = vals
		out = append(out, g)
	}
	if basePrice > 0 && basePrice+minDeltaSum <= 0 {
		return nil, "Kombinasi potongan harga variasi membuat harga akhir jadi nol atau minus"
	}
	if len(out) > maxOptionGroups {
		return nil, "Maksimal 3 jenis variasi per produk"
	}
	return out, ""
}

func AddProduct(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	authUser, aerr := requireAuth(c)
	if authUser == nil {
		return aerr
	}

	var req ProductReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request payload")
	}

	stock := 0
	if req.Stock != nil {
		stock = *req.Stock
	}
	if req.ShopID == "" || strings.TrimSpace(req.Name) == "" || req.Price <= 0 || stock < 0 {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "ShopID, Name, positive Price and valid Stock are required")
	}

	shopOID, err := primitive.ObjectIDFromHex(req.ShopID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid Shop ID format")
	}

	// Verify shop exists and is active
	var shop model.Shop
	err = config.DB.Collection("shops").FindOne(context.Background(), bson.M{"_id": shopOID}).Decode(&shop)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Associated Shop not found")
	}

	if authUser.Role != "admin" && shop.OwnerID != authUser.ID {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Tidak diizinkan menambah produk ke ruko milik penjual lain")
	}

	if msg := sellBlockReason(&shop); msg != "" {
		return helper.ErrorResponse(c, fiber.StatusForbidden, msg)
	}

	newProduct := model.Product{
		ID:          primitive.NewObjectID(),
		ShopID:      shopOID,
		Name:        strings.TrimSpace(req.Name),
		Description: req.Description,
		Price:       req.Price,
		Stock:       stock,
		Images:      req.Images,
		CreatedAt:   primitive.NewDateTimeFromTime(time.Now()),
	}
	if req.Category != nil {
		newProduct.Category = strings.TrimSpace(*req.Category)
	}
	if req.Subcategory != nil {
		newProduct.Subcategory = strings.TrimSpace(*req.Subcategory)
	}
	if req.Options != nil {
		opts, msg := cleanOptions(*req.Options, req.Price)
		if msg != "" {
			return helper.ErrorResponse(c, fiber.StatusBadRequest, msg)
		}
		newProduct.Options = opts
	}
	if req.Specs != nil {
		newProduct.Specs = *req.Specs
	}
	if req.SizeGuide != nil && len(req.SizeGuide.Columns) > 0 {
		newProduct.SizeGuide = req.SizeGuide
	}
	if req.PromoPrice != nil && *req.PromoPrice > 0 {
		if *req.PromoPrice >= req.Price {
			return helper.ErrorResponse(c, fiber.StatusBadRequest, "Harga promo harus lebih kecil dari harga normal")
		}
		newProduct.PromoPrice = *req.PromoPrice
		pct := int(math.Round(((req.Price - *req.PromoPrice) / req.Price) * 100))
		if pct < 1 {
			pct = 1
		}
		newProduct.DiscountPercent = pct
		if req.PromoLabel != nil && strings.TrimSpace(*req.PromoLabel) != "" {
			newProduct.PromoLabel = strings.TrimSpace(*req.PromoLabel)
		} else {
			newProduct.PromoLabel = fmt.Sprintf("Hemat %d%%", pct)
		}
	}

	_, err = config.DB.Collection("products").InsertOne(context.Background(), newProduct)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to create product")
	}

	if newProduct.PromoPrice > 0 {
		img := ""
		if len(newProduct.Images) > 0 {
			img = newProduct.Images[0]
		}
		PushBroadcastNotification(
			"buyer",
			"promo",
			fmt.Sprintf("Promo Baru %s: Diskon %d%%!", shop.Name, newProduct.DiscountPercent),
			fmt.Sprintf("%s kini turun harga dari Rp %.0f menjadi Rp %.0f. Yuk cek sebelum kehabisan!", newProduct.Name, newProduct.Price, newProduct.PromoPrice),
			"index.html#catalog-anchor",
			img,
		)
	}

	return helper.SuccessResponse(c, fiber.StatusCreated, "Product added successfully", newProduct)
}

// sellBlockReason: alasan ruko belum/tidak boleh berjualan ("" = boleh).
func sellBlockReason(s *model.Shop) string {
	switch effectiveStatus(s) {
	case "active":
		return ""
	case "pending":
		return "Ruko masih menunggu verifikasi admin. Produk bisa ditambahkan setelah ruko disetujui."
	case "expired":
		return "Masa sewa ruko sudah habis. Perpanjang sewa untuk berjualan lagi."
	default:
		return "Ruko sedang ditangguhkan admin."
	}
}

func ListProducts(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	filter := bson.M{}
	if shopID := c.Query("shop_id"); shopID != "" {
		shopOID, err := primitive.ObjectIDFromHex(shopID)
		if err != nil {
			return helper.ErrorResponse(c, fiber.StatusBadRequest, "shop_id tidak valid")
		}
		filter["shop_id"] = shopOID
		// Pemilik ruko & admin melihat semua produknya; pembeli hanya produk tampil dari ruko yang aktif
		u := currentUser(c)
		shop, err := loadShop(ctx, shopOID)
		if err != nil {
			return helper.SuccessResponse(c, fiber.StatusOK, "Products list retrieved successfully", []model.Product{})
		}
		if u == nil || (!isAdmin(u) && !isShopOwner(shop, u.ID)) {
			if effectiveStatus(shop) != "active" {
				return helper.SuccessResponse(c, fiber.StatusOK, "Products list retrieved successfully", []model.Product{})
			}
			filter["hidden"] = bson.M{"$ne": true}
		}
	} else {
		// Katalog publik: tanpa produk yang disembunyikan moderator, hanya dari ruko yang
		// disetujui admin dan masa sewanya masih berjalan
		filter["hidden"] = bson.M{"$ne": true}
		cur, err := shopsCol().Find(ctx, activeShopFilter(), options.Find().SetProjection(bson.M{"_id": 1}))
		if err != nil {
			return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal memuat katalog")
		}
		ids := []primitive.ObjectID{}
		for cur.Next(ctx) {
			var row struct {
				ID primitive.ObjectID `bson:"_id"`
			}
			if cur.Decode(&row) == nil {
				ids = append(ids, row.ID)
			}
		}
		cur.Close(ctx)
		filter["shop_id"] = bson.M{"$in": ids}
	}

	cursor, err := productsCol().Find(ctx, filter)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to retrieve products")
	}
	defer cursor.Close(ctx)

	var products []model.Product = []model.Product{}
	if err = cursor.All(ctx, &products); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Error decoding products list")
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Products list retrieved successfully", products)
}

func GetProduct(c *fiber.Ctx) error {
	productID := c.Params("id")
	if productID == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Product ID param is required")
	}

	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	oid, err := primitive.ObjectIDFromHex(productID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid Product ID format")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var product model.Product
	err = config.DB.Collection("products").FindOne(ctx, bson.M{"_id": oid}).Decode(&product)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Product not found")
	}

	u := currentUser(c)
	shop, shopErr := loadShop(ctx, product.ShopID)
	canManage := u != nil && (isAdmin(u) || (shopErr == nil && isShopOwner(shop, u.ID)))
	if !canManage && (product.Hidden || shopErr != nil || effectiveStatus(shop) != "active") {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Product not found")
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Product details retrieved successfully", product)
}

func UpdateProduct(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	authUser, aerr := requireAuth(c)
	if authUser == nil {
		return aerr
	}

	productID := c.Params("id")
	if productID == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Product ID param is required")
	}

	var req ProductReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request payload")
	}

	oid, err := primitive.ObjectIDFromHex(productID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid Product ID format")
	}

	var currentProd model.Product
	if err := config.DB.Collection("products").FindOne(context.Background(), bson.M{"_id": oid}).Decode(&currentProd); err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Product not found")
	}

	var shop model.Shop
	if err := config.DB.Collection("shops").FindOne(context.Background(), bson.M{"_id": currentProd.ShopID}).Decode(&shop); err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Associated Shop not found")
	}
	if authUser.Role != "admin" && shop.OwnerID != authUser.ID {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Tidak diizinkan mengubah produk milik ruko lain")
	}

	updateFields := bson.M{}
	unsetFields := bson.M{}
	if strings.TrimSpace(req.Name) != "" {
		updateFields["name"] = strings.TrimSpace(req.Name)
	}
	if req.Description != "" {
		updateFields["description"] = req.Description
	}
	if req.Price > 0 {
		updateFields["price"] = req.Price
	}
	// Stok hanya diubah bila dikirim — sebelumnya stok jadi 0 saat field tidak ada
	if req.Stock != nil && *req.Stock >= 0 {
		updateFields["stock"] = *req.Stock
	}
	if len(req.Images) > 0 {
		updateFields["images"] = req.Images
	}
	if req.Category != nil {
		updateFields["category"] = strings.TrimSpace(*req.Category)
	}
	if req.Subcategory != nil {
		updateFields["subcategory"] = strings.TrimSpace(*req.Subcategory)
	}

	if req.Options != nil {
		price := req.Price
		if price <= 0 {
			price = currentProd.Price
		}
		opts, msg := cleanOptions(*req.Options, price)
		if msg != "" {
			return helper.ErrorResponse(c, fiber.StatusBadRequest, msg)
		}
		if len(opts) == 0 {
			unsetFields["options"] = ""
		} else {
			updateFields["options"] = opts
		}
	}

	// Dukungan edit harga promo / diskon produk oleh penjual
	var notifyPromo bool
	var promoDiscount int
	var promoPriceVal float64
	var basePriceVal float64
	if req.PromoPrice != nil {
		basePriceVal = req.Price
		if basePriceVal <= 0 {
			basePriceVal = currentProd.Price
		}
		if *req.PromoPrice <= 0 {
			unsetFields["promo_price"] = ""
			unsetFields["discount_percent"] = ""
			unsetFields["promo_label"] = ""
		} else {
			if basePriceVal > 0 && *req.PromoPrice >= basePriceVal {
				return helper.ErrorResponse(c, fiber.StatusBadRequest, "Harga promo harus lebih kecil dari harga normal")
			}
			promoPriceVal = *req.PromoPrice
			promoDiscount = int(math.Round(((basePriceVal - promoPriceVal) / basePriceVal) * 100))
			if promoDiscount < 1 {
				promoDiscount = 1
			}
			updateFields["promo_price"] = promoPriceVal
			updateFields["discount_percent"] = promoDiscount
			if req.PromoLabel != nil && strings.TrimSpace(*req.PromoLabel) != "" {
				updateFields["promo_label"] = strings.TrimSpace(*req.PromoLabel)
			} else {
				updateFields["promo_label"] = fmt.Sprintf("Hemat %d%%", promoDiscount)
			}
			if currentProd.PromoPrice != promoPriceVal {
				notifyPromo = true
			}
		}
	} else if req.PromoLabel != nil {
		lbl := strings.TrimSpace(*req.PromoLabel)
		if lbl == "" {
			unsetFields["promo_label"] = ""
		} else {
			updateFields["promo_label"] = lbl
		}
	}
	if req.Specs != nil {
		if len(*req.Specs) == 0 {
			unsetFields["specs"] = ""
		} else {
			updateFields["specs"] = *req.Specs
		}
	}
	if req.SizeGuide != nil {
		if len(req.SizeGuide.Columns) == 0 {
			unsetFields["size_guide"] = ""
		} else {
			updateFields["size_guide"] = req.SizeGuide
		}
	}

	if len(updateFields) == 0 && len(unsetFields) == 0 {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "No fields to update")
	}

	update := bson.M{}
	if len(updateFields) > 0 {
		update["$set"] = updateFields
	}
	if len(unsetFields) > 0 {
		update["$unset"] = unsetFields
	}
	result, err := config.DB.Collection("products").UpdateOne(
		context.Background(),
		bson.M{"_id": oid},
		update,
	)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to update product")
	}

	if result.MatchedCount == 0 {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Product not found")
	}

	if notifyPromo {
		prodName := currentProd.Name
		if n, ok := updateFields["name"].(string); ok && n != "" {
			prodName = n
		}
		img := ""
		if len(req.Images) > 0 {
			img = req.Images[0]
		} else if len(currentProd.Images) > 0 {
			img = currentProd.Images[0]
		}
		PushBroadcastNotification(
			"buyer",
			"promo",
			fmt.Sprintf("Flash Promo: Diskon %d%% untuk %s!", promoDiscount, prodName),
			fmt.Sprintf("Harga turun dari Rp %.0f menjadi Rp %.0f. Buruan serbu sekarang!", basePriceVal, promoPriceVal),
			"index.html#catalog-anchor",
			img,
		)
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Product updated successfully", nil)
}

func DeleteProduct(c *fiber.Ctx) error {
	user, aerr := requireAuth(c)
	if user == nil {
		return aerr
	}

	productID := c.Params("id")
	if productID == "" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Product ID param is required")
	}

	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	oid, err := primitive.ObjectIDFromHex(productID)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid Product ID format")
	}

	var prod model.Product
	if err := config.DB.Collection("products").FindOne(context.Background(), bson.M{"_id": oid}).Decode(&prod); err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Product not found")
	}

	// Hanya Admin atau Penjual pemilik ruko tempat produk tersebut berada yang boleh menghapus produk.
	if user.Role != "admin" {
		if user.Role != "seller" {
			return helper.ErrorResponse(c, fiber.StatusForbidden, "Akses ditolak: Anda tidak memiliki izin menghapus produk")
		}
		var shop model.Shop
		if err := config.DB.Collection("shops").FindOne(context.Background(), bson.M{"_id": prod.ShopID, "owner_id": user.ID}).Decode(&shop); err != nil {
			return helper.ErrorResponse(c, fiber.StatusForbidden, "Akses ditolak: Anda hanya dapat menghapus produk milik toko Anda sendiri")
		}
	}

	result, err := config.DB.Collection("products").DeleteOne(context.Background(), bson.M{"_id": oid})
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to delete product")
	}

	if result.DeletedCount == 0 {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Product not found")
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Product deleted successfully", nil)
}
