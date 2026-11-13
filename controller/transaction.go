package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"vendora/config"
	"vendora/helper"
	"vendora/model"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// PlatformFeeRate adalah pajak/komisi platform tipis (2.5%) yang dipotong saat dana Escrow cair ke penjual.
const PlatformFeeRate = 0.025

type CourierOption struct {
	Code    string  `json:"code"`
	Name    string  `json:"name"`
	Service string  `json:"service"`
	Cost    float64 `json:"cost"`
	ETD     string  `json:"etd"`
}

var AvailableCouriers = []CourierOption{
	{Code: "JNE", Name: "JNE Express", Service: "JNE Reguler", Cost: 18000, ETD: "2-3 Hari"},
	{Code: "JNT", Name: "J&T Express", Service: "J&T EZ Regular", Cost: 19000, ETD: "2-3 Hari"},
	{Code: "SICEPAT", Name: "SiCepat Ekspres", Service: "SiCepat BEST", Cost: 22000, ETD: "1-2 Hari"},
	{Code: "INSTANT", Name: "Vendora Instant Courier", Service: "Instant Same-Day", Cost: 35000, ETD: "2-4 Jam"},
}

func findCourier(code string) CourierOption {
	code = strings.ToUpper(strings.TrimSpace(code))
	for _, c := range AvailableCouriers {
		if c.Code == code {
			return c
		}
	}
	return AvailableCouriers[0] // default JNE
}

// ListCouriers menangani GET /api/couriers
func ListCouriers(c *fiber.Ctx) error {
	return helper.SuccessResponse(c, fiber.StatusOK, "Available couriers retrieved", AvailableCouriers)
}

type CheckoutItem struct {
	ProductID string            `json:"product_id"`
	ShopID    string            `json:"shop_id"`
	Quantity  int               `json:"quantity"`
	Options   map[string]string `json:"options"` // variasi terpilih, mis. {"Warna":"Burgundy","Kapasitas":"512 GB"}
}

// resolveVariant mencocokkan pilihan pembeli dengan variasi produk.
// Harga dihitung di server (harga efektif/promo + tambahan tiap pilihan), bukan dari browser.
func resolveVariant(p model.Product, chosen map[string]string) (price float64, label string, image string, errMsg string) {
	price = p.EffectivePrice()
	if len(p.Images) > 0 {
		image = p.Images[0]
	}
	if len(p.Options) == 0 {
		return price, "", image, ""
	}
	labels := []string{}
	for _, g := range p.Options {
		want := strings.TrimSpace(chosen[g.Name])
		if want == "" {
			return 0, "", "", "Pilih " + strings.ToLower(g.Name) + " untuk " + p.Name + " dulu"
		}
		found := false
		for _, v := range g.Values {
			if strings.EqualFold(v.Label, want) {
				price += v.PriceDelta
				labels = append(labels, v.Label)
				if v.Image != "" {
					image = v.Image
				}
				found = true
				break
			}
		}
		if !found {
			return 0, "", "", g.Name + " \"" + want + "\" tidak tersedia untuk " + p.Name
		}
	}
	return price, strings.Join(labels, ", "), image, ""
}

// inferDestinationCoords menebak koordinat kota tujuan dari teks alamat pengiriman untuk visualisasi peta.
func inferDestinationCoords(address string) model.GeoLocation {
	lower := strings.ToLower(address)
	switch {
	case strings.Contains(lower, "surabaya"):
		return model.GeoLocation{Label: "Alamat Pembeli (Surabaya)", City: "Surabaya", Lat: -7.2575, Lng: 112.7521}
	case strings.Contains(lower, "yogyakarta") || strings.Contains(lower, "jogja"):
		return model.GeoLocation{Label: "Alamat Pembeli (Yogyakarta)", City: "Yogyakarta", Lat: -7.7956, Lng: 110.3695}
	case strings.Contains(lower, "semarang"):
		return model.GeoLocation{Label: "Alamat Pembeli (Semarang)", City: "Semarang", Lat: -6.9667, Lng: 110.4167}
	case strings.Contains(lower, "bogor"):
		return model.GeoLocation{Label: "Alamat Pembeli (Bogor)", City: "Bogor", Lat: -6.5971, Lng: 106.8060}
	case strings.Contains(lower, "depok"):
		return model.GeoLocation{Label: "Alamat Pembeli (Depok)", City: "Depok", Lat: -6.4025, Lng: 106.7942}
	case strings.Contains(lower, "tangerang"):
		return model.GeoLocation{Label: "Alamat Pembeli (Tangerang)", City: "Tangerang", Lat: -6.1783, Lng: 106.6319}
	case strings.Contains(lower, "bekasi"):
		return model.GeoLocation{Label: "Alamat Pembeli (Bekasi)", City: "Bekasi", Lat: -6.2383, Lng: 106.9756}
	case strings.Contains(lower, "jakarta"):
		return model.GeoLocation{Label: "Alamat Pembeli (Jakarta)", City: "Jakarta Selatan", Lat: -6.2615, Lng: 106.8106}
	default:
		return model.GeoLocation{Label: "Alamat Pembeli (Bandung)", City: "Bandung", Lat: -6.9175, Lng: 107.6191}
	}
}

func generatePaymentSecurity(txID primitive.ObjectID, buyerID primitive.ObjectID, total float64, method string) (token string, code string) {
	raw := fmt.Sprintf("%s:%s:%.2f:%s:%d", txID.Hex(), buyerID.Hex(), total, method, time.Now().UnixNano())
	sum := sha256.Sum256([]byte(raw))
	token = hex.EncodeToString(sum[:16])
	shortNum := strings.ToUpper(txID.Hex()[len(txID.Hex())-8:])
	switch strings.ToUpper(method) {
	case "VA_BCA":
		code = "88010" + shortNum
	case "VA_MANDIRI":
		code = "89022" + shortNum
	case "CARD":
		code = "AUTH-3DS-" + shortNum
	case "COD", "RETAIL":
		code = "VDR-PAY-" + shortNum
	default:
		code = "00020101021226590014ID.VENDORA.WWW011893600914" + shortNum + "5204541153033605802ID"
	}
	return token, code
}

type CheckoutReq struct {
	BuyerID         string         `json:"buyer_id"`
	RecipientName   string         `json:"recipient_name"`
	ShippingAddress string         `json:"shipping_address"`
	CourierCode     string         `json:"courier_code"`   // JNE, JNT, SICEPAT, INSTANT
	PaymentMethod   string         `json:"payment_method"` // QRIS, VA_BCA, CARD, RETAIL
	WaitPayment     bool           `json:"wait_payment"`   // true = buat invoice pending_payment dulu; false = bayar langsung terverifikasi
	Items           []CheckoutItem `json:"items"`
}

func Checkout(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	authUser, aerr := requireAuth(c)
	if authUser == nil {
		return aerr
	}

	var req CheckoutReq
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request payload")
	}

	if len(req.Items) == 0 {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Minimal satu barang diperlukan untuk checkout")
	}

	if strings.TrimSpace(req.BuyerID) != "" && strings.TrimSpace(req.BuyerID) != authUser.ID.Hex() {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Tidak diizinkan melakukan checkout atas nama pengguna lain")
	}

	buyerOID := authUser.ID

	// Verify buyer exists
	var buyer model.User
	err := config.DB.Collection("users").FindOne(context.Background(), bson.M{"_id": buyerOID}).Decode(&buyer)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Buyer user not found")
	}

	type deductedStock struct {
		prodOID primitive.ObjectID
		qty     int
	}
	var deducted []deductedStock
	rollbackDeducted := func() {
		for _, d := range deducted {
			_, _ = config.DB.Collection("products").UpdateOne(
				context.Background(),
				bson.M{"_id": d.prodOID},
				bson.M{"$inc": bson.M{"stock": d.qty}},
			)
		}
	}

	var transactionItems []model.TransactionItem
	var subtotalAmount float64
	shopSubtotals := map[primitive.ObjectID]float64{}

	// Process each item (hanya produk valid dari database, cabang mockup dihapus)
	for _, item := range req.Items {
		if item.Quantity < 1 {
			rollbackDeducted()
			return helper.ErrorResponse(c, fiber.StatusBadRequest, "Jumlah barang minimal 1")
		}

		prodOID, err := primitive.ObjectIDFromHex(strings.TrimSpace(item.ProductID))
		if err != nil || prodOID.IsZero() {
			rollbackDeducted()
			return helper.ErrorResponse(c, fiber.StatusBadRequest, "ID produk tidak valid: "+item.ProductID)
		}

		var product model.Product
		err = config.DB.Collection("products").FindOne(context.Background(), bson.M{"_id": prodOID}).Decode(&product)
		if err != nil {
			rollbackDeducted()
			return helper.ErrorResponse(c, fiber.StatusNotFound, "Product not found: "+item.ProductID)
		}
		if product.Hidden {
			rollbackDeducted()
			return helper.ErrorResponse(c, fiber.StatusForbidden, fmt.Sprintf("Checkout ditolak: Produk \"%s\" sedang disembunyikan oleh moderator.", product.Name))
		}

		// Selalu ambil ShopID dari dokumen produk di database (tidak pernah percaya shop_id dari klien)
		shopOID := product.ShopID
		if shopOID.IsZero() {
			rollbackDeducted()
			return helper.ErrorResponse(c, fiber.StatusBadRequest, "Produk \""+product.Name+"\" tidak memiliki toko yang valid.")
		}

		var itemShop model.Shop
		if err := config.DB.Collection("shops").FindOne(context.Background(), bson.M{"_id": shopOID}).Decode(&itemShop); err != nil {
			rollbackDeducted()
			return helper.ErrorResponse(c, fiber.StatusBadRequest, "Toko untuk produk \""+product.Name+"\" tidak ditemukan.")
		}
		if itemShop.OwnerID == buyerOID {
			rollbackDeducted()
			return helper.ErrorResponse(c, fiber.StatusForbidden, "Checkout ditolak: Anda tidak dapat membeli produk dari toko milik sendiri.")
		}

		effStatus := EffectiveShopStatus(itemShop)
		if effStatus == "suspended" {
			rollbackDeducted()
			return helper.ErrorResponse(c, fiber.StatusForbidden, fmt.Sprintf("Checkout ditolak: Toko \"%s\" sedang ditangguhkan oleh administrator.", itemShop.Name))
		}
		if effStatus == "expired" {
			rollbackDeducted()
			return helper.ErrorResponse(c, fiber.StatusForbidden, fmt.Sprintf("Checkout ditolak: Masa sewa ruko \"%s\" telah berakhir (jatuh tempo). Toko belum dapat menerima pesanan baru.", itemShop.Name))
		}

		unitPrice, variant, image, msg := resolveVariant(product, item.Options)
		if msg != "" {
			rollbackDeducted()
			return helper.ErrorResponse(c, fiber.StatusBadRequest, msg)
		}

		// Deduct stock atomically with verification to prevent race conditions
		filter := bson.M{
			"_id":   prodOID,
			"stock": bson.M{"$gte": item.Quantity},
		}
		update := bson.M{
			"$inc": bson.M{"stock": -item.Quantity},
		}
		res, err := config.DB.Collection("products").UpdateOne(context.Background(), filter, update)
		if err != nil {
			rollbackDeducted()
			return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to update stock for product: "+product.Name)
		}

		if res.MatchedCount == 0 {
			rollbackDeducted()
			return helper.ErrorResponse(c, fiber.StatusBadRequest, "Insufficient stock for product: "+product.Name)
		}
		deducted = append(deducted, deductedStock{prodOID: prodOID, qty: item.Quantity})

		itemTotal := unitPrice * float64(item.Quantity)
		subtotalAmount += itemTotal
		shopSubtotals[shopOID] += itemTotal

		transactionItems = append(transactionItems, model.TransactionItem{
			ProductID:     prodOID,
			ShopID:        shopOID,
			Name:          product.Name,
			Variant:       variant,
			Image:         image,
			OriginalPrice: product.Price,
			Price:         unitPrice,
			Quantity:      item.Quantity,
		})
	}

	// Hitung Kurir, Pajak Platform (2.5%), dan Escrow
	courier := findCourier(req.CourierCode)
	shippingCost := courier.Cost
	platformFee := math.Round(subtotalAmount * PlatformFeeRate)
	sellerNetAmount := subtotalAmount - platformFee
	totalAmount := subtotalAmount + shippingCost

	if req.PaymentMethod == "" {
		req.PaymentMethod = "QRIS"
	}

	txID := primitive.NewObjectID()
	now := time.Now()
	nowDT := primitive.NewDateTimeFromTime(now)
	expDT := primitive.NewDateTimeFromTime(now.Add(15 * time.Minute))
	payToken, payCode := generatePaymentSecurity(txID, buyerOID, totalAmount, req.PaymentMethod)

	origin := model.GeoLocation{
		Label: "Gudang Ruko Penjual (Jakarta Pusat)",
		City:  "Jakarta Pusat",
		Lat:   -6.1944,
		Lng:   106.8229,
	}
	dest := inferDestinationCoords(req.ShippingAddress)
	trackingNo := fmt.Sprintf("%s-%s", courier.Code, strings.ToUpper(txID.Hex()[len(txID.Hex())-8:]))

	initialStatus := "pending"
	escrowStatus := "held"
	var paidAtPtr *primitive.DateTime = &nowDT
	confirmByDT := primitive.NewDateTimeFromTime(now.Add(PendingConfirmDuration()))
	var confirmByPtr *primitive.DateTime = &confirmByDT
	if req.WaitPayment {
		initialStatus = "pending_payment"
		escrowStatus = "unpaid"
		paidAtPtr = nil
		confirmByPtr = nil
	}

	trackingEvents := []model.TrackingEvent{
		{
			Status:      "manifested",
			Title:       "Pesanan Dibuat & Terverifikasi Sistem Keamanan Vendora",
			Description: fmt.Sprintf("Menunggu penyerahan paket ke kurir %s (%s). Resi: %s", courier.Name, courier.Service, trackingNo),
			City:        origin.City,
			Lat:         origin.Lat,
			Lng:         origin.Lng,
			Timestamp:   nowDT,
		},
	}

	newTx := model.Transaction{
		ID:               txID,
		BuyerID:          buyerOID,
		RecipientName:    req.RecipientName,
		ShippingAddress:  req.ShippingAddress,
		Items:            transactionItems,
		SubtotalAmount:   subtotalAmount,
		ShippingCost:     shippingCost,
		PlatformFee:      platformFee,
		SellerNetAmount:  sellerNetAmount,
		TotalAmount:      totalAmount,
		EscrowStatus:     escrowStatus,
		PaymentMethod:    req.PaymentMethod,
		PaymentCode:      payCode,
		PaymentToken:     payToken,
		PaymentExpiresAt: &expDT,
		PaidAt:           paidAtPtr,
		CourierCode:      courier.Code,
		CourierService:   courier.Service,
		CourierETD:       courier.ETD,
		TrackingNumber:   trackingNo,
		RouteOrigin:      &origin,
		RouteDestination: &dest,
		CurrentLocation:  &origin,
		TrackingHistory:  trackingEvents,
		Status:           initialStatus,
		ConfirmBy:        confirmByPtr,
		CreatedAt:        nowDT,
	}

	_, err = config.DB.Collection("transactions").InsertOne(context.Background(), newTx)
	if err != nil {
		rollbackDeducted()
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to create transaction record")
	}

	// Jika langsung dibayar (Escrow Held), tambahkan ke EscrowBalance masing-masing toko & catat di WalletLedger
	if escrowStatus == "held" {
		holdEscrowFunds(newTx, shopSubtotals)
	}

	return helper.SuccessResponse(c, fiber.StatusCreated, "Checkout completed! Dana diamankan di Rekening Bersama (Escrow) Vendora.", newTx)
}

// holdEscrowFunds menahan uang pembayaran pembeli di Escrow Admin (belum bisa ditarik penjual sampai barang diterima).
func holdEscrowFunds(tx model.Transaction, shopSubtotals map[primitive.ObjectID]float64) {
	if config.DB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	for shopOID, gross := range shopSubtotals {
		if shopOID.IsZero() || gross <= 0 {
			continue
		}
		tax := math.Round(gross * PlatformFeeRate)
		net := gross - tax

		_, _ = config.DB.Collection("shops").UpdateOne(
			ctx,
			bson.M{"_id": shopOID},
			bson.M{"$inc": bson.M{"escrow_balance": net}},
		)

		ledger := model.WalletLedger{
			ID:            primitive.NewObjectID(),
			TransactionID: tx.ID,
			ShopID:        shopOID,
			Type:          "escrow_hold",
			GrossAmount:   gross,
			TaxAmount:     tax,
			NetAmount:     net,
			Description:   fmt.Sprintf("Dana ditahan di Escrow Admin untuk pesanan #%s (Pajak platform 2.5%%: Rp %.0f)", strings.ToUpper(tx.ID.Hex()[len(tx.ID.Hex())-6:]), tax),
			CreatedAt:     primitive.NewDateTimeFromTime(time.Now()),
		}
		_, _ = config.DB.Collection("wallet_ledgers").InsertOne(ctx, ledger)

		// Kirim notifikasi ke pemilik toko (Seller)
		var shop model.Shop
		if err := config.DB.Collection("shops").FindOne(ctx, bson.M{"_id": shopOID}).Decode(&shop); err == nil && !shop.OwnerID.IsZero() {
			PushUserNotification(
				shop.OwnerID,
				"order",
				"Pesanan Baru Masuk (Dana Dijamin Escrow)",
				fmt.Sprintf("Pesanan #%s senilai Rp %.0f telah dibayar via %s. Dana bersih Rp %.0f ditahan di Escrow dan akan cair otomatis saat pembeli menerima pesanan.", strings.ToUpper(tx.ID.Hex()[len(tx.ID.Hex())-6:]), gross, tx.PaymentMethod, net),
				"seller.html",
			)
		}
	}

	// Notifikasi ke Pembeli
	PushUserNotification(
		tx.BuyerID,
		"order",
		"Pembayaran Terverifikasi & Dijamin Escrow",
		fmt.Sprintf("Pembayaran Rp %.0f berhasil! Uang kamu ditahan dengan aman oleh Admin Vendora dan baru diteruskan ke penjual setelah paket %s (%s) kamu terima.", tx.TotalAmount, tx.CourierService, tx.TrackingNumber),
		"pesanan.html",
	)
}

// releaseEscrowFunds mencairkan dana dari Escrow ke Saldo Aktif Penjual (dipotong pajak 2.5% untuk Admin).
func releaseEscrowFunds(ctx context.Context, tx model.Transaction) {
	if tx.EscrowStatus == "released" {
		return
	}
	shopTotals := map[primitive.ObjectID]float64{}
	for _, item := range tx.Items {
		if !item.ShopID.IsZero() {
			shopTotals[item.ShopID] += item.Price * float64(item.Quantity)
		}
	}

	nowDT := primitive.NewDateTimeFromTime(time.Now())
	for shopOID, gross := range shopTotals {
		tax := math.Round(gross * PlatformFeeRate)
		net := gross - tax

		// Pindahkan dari escrow_balance ke balance (saldo aktif yang bisa ditarik penjual)
		var shop model.Shop
		_ = config.DB.Collection("shops").FindOne(ctx, bson.M{"_id": shopOID}).Decode(&shop)
		decEscrow := net
		if shop.EscrowBalance < decEscrow {
			decEscrow = shop.EscrowBalance
		}
		_, _ = config.DB.Collection("shops").UpdateOne(
			ctx,
			bson.M{"_id": shopOID},
			bson.M{
				"$inc": bson.M{
					"escrow_balance": -decEscrow,
					"balance":        net,
				},
			},
		)

		ledger := model.WalletLedger{
			ID:            primitive.NewObjectID(),
			TransactionID: tx.ID,
			ShopID:        shopOID,
			Type:          "escrow_release",
			GrossAmount:   gross,
			TaxAmount:     tax,
			NetAmount:     net,
			Description:   fmt.Sprintf("Pencairan Escrow pesanan #%s ke Saldo Aktif Penjual (Potongan pajak Admin 2.5%%: Rp %.0f)", strings.ToUpper(tx.ID.Hex()[len(tx.ID.Hex())-6:]), tax),
			CreatedAt:     nowDT,
		}
		_, _ = config.DB.Collection("wallet_ledgers").InsertOne(ctx, ledger)

		if !shop.OwnerID.IsZero() {
			PushUserNotification(
				shop.OwnerID,
				"escrow",
				"Dana Pesanan Cair ke Saldo Aktif!",
				fmt.Sprintf("Pembeli telah menerima pesanan #%s. Dana bersih Rp %.0f (setelah pajak platform 2.5%% Rp %.0f) kini masuk ke Saldo Aktif Ruko Anda.", strings.ToUpper(tx.ID.Hex()[len(tx.ID.Hex())-6:]), net, tax),
				"seller.html",
			)
		}
	}
}

// refundEscrowAndRestoreStock membatalkan dana Escrow (refund ke pembeli). Stok hanya dipulihkan
// bila restock = true (pesanan batal sebelum dikirim); untuk sengketa setelah barang dikirim, restock = false.
func refundEscrowAndRestoreStock(ctx context.Context, tx model.Transaction, restock bool) {
	for _, item := range tx.Items {
		if restock && !item.ProductID.IsZero() {
			_, _ = config.DB.Collection("products").UpdateOne(
				ctx,
				bson.M{"_id": item.ProductID},
				bson.M{"$inc": bson.M{"stock": item.Quantity}},
			)
		}
	}

	if tx.EscrowStatus == "held" {
		shopTotals := map[primitive.ObjectID]float64{}
		for _, item := range tx.Items {
			if !item.ShopID.IsZero() {
				shopTotals[item.ShopID] += item.Price * float64(item.Quantity)
			}
		}
		for shopOID, gross := range shopTotals {
			tax := math.Round(gross * PlatformFeeRate)
			net := gross - tax
			var shop model.Shop
			_ = config.DB.Collection("shops").FindOne(ctx, bson.M{"_id": shopOID}).Decode(&shop)
			decEscrow := net
			if shop.EscrowBalance < decEscrow {
				decEscrow = shop.EscrowBalance
			}
			_, _ = config.DB.Collection("shops").UpdateOne(
				ctx,
				bson.M{"_id": shopOID},
				bson.M{"$inc": bson.M{"escrow_balance": -decEscrow}},
			)

			ledger := model.WalletLedger{
				ID:            primitive.NewObjectID(),
				TransactionID: tx.ID,
				ShopID:        shopOID,
				Type:          "escrow_refund",
				GrossAmount:   gross,
				TaxAmount:     0,
				NetAmount:     gross,
				Description:   fmt.Sprintf("Pengembalian dana (Refund Escrow) ke pembeli untuk pesanan #%s yang dibatalkan", strings.ToUpper(tx.ID.Hex()[len(tx.ID.Hex())-6:])),
				CreatedAt:     primitive.NewDateTimeFromTime(time.Now()),
			}
			_, _ = config.DB.Collection("wallet_ledgers").InsertOne(ctx, ledger)
		}
	}
}

// PayTransaction menangani POST /api/payment/:id/pay (verifikasi pembayaran real-time dengan token keamanan)
func PayTransaction(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}
	authUser, aerr := requireAuth(c)
	if authUser == nil {
		return aerr
	}
	id := c.Params("id")
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid transaction ID")
	}

	var req struct {
		PaymentToken  string `json:"payment_token"`
		PaymentMethod string `json:"payment_method"`
	}
	_ = c.BodyParser(&req)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	var tx model.Transaction
	if err := config.DB.Collection("transactions").FindOne(ctx, bson.M{"_id": oid}).Decode(&tx); err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Transaksi tidak ditemukan")
	}

	if authUser.Role != "admin" && tx.BuyerID != authUser.ID {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Hanya pembeli pemilik pesanan yang dapat melakukan pembayaran")
	}

	if tx.Status == "cancelled" {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Transaksi sudah dibatalkan atau melewati batas waktu pembayaran")
	}
	if tx.EscrowStatus == "held" || tx.EscrowStatus == "released" {
		return helper.SuccessResponse(c, fiber.StatusOK, "Transaksi sudah terbayar dan diamankan di Escrow", tx)
	}

	if tx.PaymentToken != "" && strings.TrimSpace(req.PaymentToken) != tx.PaymentToken {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Token keamanan pembayaran wajib diisi dan harus sesuai")
	}

	now := time.Now()
	nowDT := primitive.NewDateTimeFromTime(now)
	confirmByDT := primitive.NewDateTimeFromTime(now.Add(PendingConfirmDuration()))
	method := tx.PaymentMethod
	if req.PaymentMethod != "" {
		method = req.PaymentMethod
	}

	_, err = config.DB.Collection("transactions").UpdateOne(
		ctx,
		bson.M{"_id": oid},
		bson.M{
			"$set": bson.M{
				"status":         "pending",
				"escrow_status":  "held",
				"payment_method": method,
				"paid_at":        nowDT,
				"confirm_by":     confirmByDT,
			},
		},
	)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal memverifikasi pembayaran")
	}

	shopSubtotals := map[primitive.ObjectID]float64{}
	for _, item := range tx.Items {
		if !item.ShopID.IsZero() {
			shopSubtotals[item.ShopID] += item.Price * float64(item.Quantity)
		}
	}
	tx.EscrowStatus = "held"
	tx.PaymentMethod = method
	holdEscrowFunds(tx, shopSubtotals)

	return helper.SuccessResponse(c, fiber.StatusOK, "Pembayaran berhasil diverifikasi secara real-time! Dana ditahan di Escrow.", tx)
}

// GetPaymentStatus menangani GET /api/payment/:id/status (pengecekan real-time status pembayaran & kadaluarsa)
func GetPaymentStatus(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}
	authUser, aerr := requireAuth(c)
	if authUser == nil {
		return aerr
	}
	id := c.Params("id")
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid transaction ID")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	var tx model.Transaction
	if err := config.DB.Collection("transactions").FindOne(ctx, bson.M{"_id": oid}).Decode(&tx); err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Transaction not found")
	}

	if authUser.Role != "admin" && tx.BuyerID != authUser.ID && !userSellsInTx(ctx, authUser, tx) {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Anda tidak memiliki akses ke status pembayaran pesanan ini")
	}

	// Auto-cancel bila pending_payment sudah lewat batas waktu 15 menit
	if tx.Status == "pending_payment" && tx.PaymentExpiresAt != nil && time.Now().After(tx.PaymentExpiresAt.Time()) {
		_, _ = config.DB.Collection("transactions").UpdateOne(
			ctx,
			bson.M{"_id": oid},
			bson.M{"$set": bson.M{"status": "cancelled", "escrow_status": "refunded"}},
		)
		refundEscrowAndRestoreStock(ctx, tx, true)
		tx.Status = "cancelled"
		tx.EscrowStatus = "refunded"
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Payment status retrieved", fiber.Map{
		"transaction_id":     tx.ID,
		"status":             tx.Status,
		"escrow_status":      tx.EscrowStatus,
		"payment_method":     tx.PaymentMethod,
		"payment_code":       tx.PaymentCode,
		"payment_expires_at": tx.PaymentExpiresAt,
		"paid_at":            tx.PaidAt,
		"subtotal_amount":    tx.SubtotalAmount,
		"shipping_cost":      tx.ShippingCost,
		"platform_fee":       tx.PlatformFee,
		"seller_net_amount":  tx.SellerNetAmount,
		"total_amount":       tx.TotalAmount,
	})
}

func ListTransactions(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}
	maybeSweepAutoComplete()

	// If shop_id is provided, find all transactions containing items for this shop (requires seller ownership or admin)
	shopID := strings.TrimSpace(c.Query("shop_id"))
	if shopID != "" {
		authUser, aerr := requireAuth(c)
		if authUser == nil {
			return aerr
		}
		shopOID, err := primitive.ObjectIDFromHex(shopID)
		if err != nil {
			return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid shop_id format")
		}
		if authUser.Role != "admin" {
			var shop model.Shop
			if err := config.DB.Collection("shops").FindOne(context.Background(), bson.M{"_id": shopOID}).Decode(&shop); err != nil {
				return helper.ErrorResponse(c, fiber.StatusNotFound, "Shop not found")
			}
			if shop.OwnerID != authUser.ID {
				return helper.ErrorResponse(c, fiber.StatusForbidden, "Akses ditolak: Anda bukan pemilik ruko ini.")
			}
		}

		filter := bson.M{
			"items.shop_id": shopOID,
		}

		cursor, err := config.DB.Collection("transactions").Find(context.Background(), filter)
		if err != nil {
			return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to retrieve transactions")
		}
		defer cursor.Close(context.Background())

		var transactions []model.Transaction = []model.Transaction{}
		if err = cursor.All(context.Background(), &transactions); err != nil {
			return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Error decoding transactions list")
		}
		for i := range transactions {
			transactions[i].PaymentToken = ""
		}

		return helper.SuccessResponse(c, fiber.StatusOK, "Transactions retrieved successfully", transactions)
	}

	// Filter by buyer_id if provided (requires matching buyer or admin)
	filter := bson.M{}
	buyerID := strings.TrimSpace(c.Query("buyer_id"))
	if buyerID != "" {
		authUser, aerr := requireAuth(c)
		if authUser == nil {
			return aerr
		}
		buyerOID, err := primitive.ObjectIDFromHex(buyerID)
		if err != nil {
			return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid buyer_id format")
		}
		if authUser.Role != "admin" && buyerOID != authUser.ID {
			return helper.ErrorResponse(c, fiber.StatusForbidden, "Akses ditolak: Anda hanya dapat melihat transaksi milik Anda sendiri.")
		}
		filter["buyer_id"] = buyerOID

		cursor, err := config.DB.Collection("transactions").Find(context.Background(), filter)
		if err != nil {
			return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to retrieve transactions")
		}
		defer cursor.Close(context.Background())

		var transactions []model.Transaction = []model.Transaction{}
		if err = cursor.All(context.Background(), &transactions); err != nil {
			return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Error decoding transactions list")
		}
		return helper.SuccessResponse(c, fiber.StatusOK, "Transactions retrieved successfully", transactions)
	}

	cursor, err := config.DB.Collection("transactions").Find(context.Background(), filter)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to retrieve transactions")
	}
	defer cursor.Close(context.Background())

	var transactions []model.Transaction = []model.Transaction{}
	if err = cursor.All(context.Background(), &transactions); err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Error decoding transactions list")
	}

	authUser := AuthUser(c)
	if authUser != nil && authUser.Role == "admin" {
		// Privasi Data: Jika Admin yang mengambil daftar semua transaksi, samarkan alamat & nama penerima pakai bintang ****
		for i := range transactions {
			transactions[i].ShippingAddress = helper.MaskAddress(transactions[i].ShippingAddress)
			transactions[i].RecipientName = helper.MaskAddress(transactions[i].RecipientName)
			transactions[i].PaymentToken = ""
		}
	} else {
		// Katalog publik (untuk perhitungan jumlah terjual): bersihkan seluruh informasi pribadi & finansial pembeli
		for i := range transactions {
			transactions[i].BuyerID = primitive.NilObjectID
			transactions[i].RecipientName = ""
			transactions[i].ShippingAddress = ""
			transactions[i].PaymentToken = ""
			transactions[i].TrackingNumber = ""
			transactions[i].TrackingHistory = nil
			transactions[i].RouteOrigin = nil
			transactions[i].RouteDestination = nil
			transactions[i].CurrentLocation = nil
		}
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Transactions retrieved successfully", transactions)
}

// FixTransactionStatus updates all pending_payment transactions to success (one-time migration)
func FixTransactionStatus(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := config.DB.Collection("transactions").UpdateMany(
		ctx,
		bson.M{"status": "pending_payment"},
		bson.M{"$set": bson.M{"status": "success"}},
	)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to update transactions")
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Transactions updated successfully", fiber.Map{
		"updated_count": result.ModifiedCount,
	})
}

// buildTrackingStateForStatus menyusun checkpoint kurir & koordinat peta otomatis saat status pesanan berubah.
func buildTrackingStateForStatus(tx model.Transaction, nextStatus string) (events []model.TrackingEvent, current *model.GeoLocation) {
	origin := tx.RouteOrigin
	if origin == nil {
		origin = &model.GeoLocation{Label: "Gudang Ruko Penjual (Jakarta Pusat)", City: "Jakarta Pusat", Lat: -6.1944, Lng: 106.8229}
	}
	dest := tx.RouteDestination
	if dest == nil {
		d := inferDestinationCoords(tx.ShippingAddress)
		dest = &d
	}
	events = tx.TrackingHistory
	nowDT := primitive.NewDateTimeFromTime(time.Now())
	courierName := tx.CourierService
	if courierName == "" {
		courierName = "JNE Reguler"
	}

	switch nextStatus {
	case "paid":
		current = origin
		events = append(events, model.TrackingEvent{
			Status:      "processing",
			Title:       "Penjual Sedang Mengemas Paket",
			Description: fmt.Sprintf("Paket sedang disiapkan dan menunggu penjemputan kurir %s.", courierName),
			City:        origin.City,
			Lat:         origin.Lat,
			Lng:         origin.Lng,
			Timestamp:   nowDT,
		})
	case "shipped":
		midLat := (origin.Lat + dest.Lat) / 2
		midLng := (origin.Lng + dest.Lng) / 2
		midLoc := model.GeoLocation{
			Label: fmt.Sprintf("Hub Transit %s (%s -> %s)", courierName, origin.City, dest.City),
			City:  "Hub Transit Kurir",
			Lat:   midLat,
			Lng:   midLng,
		}
		current = &midLoc
		events = append(events,
			model.TrackingEvent{
				Status:      "picked_up",
				Title:       fmt.Sprintf("Paket Diserahkan ke Kurir %s", courierName),
				Description: fmt.Sprintf("Nomor Resi %s telah aktif dan paket berangkat dari %s.", tx.TrackingNumber, origin.City),
				City:        origin.City,
				Lat:         origin.Lat,
				Lng:         origin.Lng,
				Timestamp:   nowDT,
			},
			model.TrackingEvent{
				Status:      "in_transit",
				Title:       "Paket Dalam Perjalanan (In Transit)",
				Description: fmt.Sprintf("Paket sedang bergerak menuju kota tujuan %s.", dest.City),
				City:        midLoc.Label,
				Lat:         midLat,
				Lng:         midLng,
				Timestamp:   nowDT,
			},
		)
	case "success":
		current = dest
		events = append(events, model.TrackingEvent{
			Status:      "delivered",
			Title:       "Paket Telah Diterima oleh Pembeli",
			Description: fmt.Sprintf("Paket diterima oleh %s. Dana Escrow dicairkan ke penjual.", tx.RecipientName),
			City:        dest.City,
			Lat:         dest.Lat,
			Lng:         dest.Lng,
			Timestamp:   nowDT,
		})
	default:
		current = tx.CurrentLocation
		if current == nil {
			current = origin
		}
	}
	return events, current
}

// ─── Aturan perubahan status pesanan ─────────────────────────────────────────
//
//	Pembeli : shipped → success (Pesanan Diterima) · pending_payment/pending → cancelled
//	Penjual : pending → paid · paid → shipped · pending/paid → cancelled
//	Admin   : bebas (untuk penyelesaian sengketa & penanganan khusus)
//	Pesanan "disputed" dibekukan: hanya admin lewat keputusan sengketa.
//
// Penjual TIDAK bisa menandai "success" sendiri, sehingga dana Escrow tidak bisa dicairkan sepihak.

// AutoCompleteDuration = berapa lama setelah barang dikirim pesanan otomatis selesai bila pembeli
// diam dan tidak komplain. Atur lewat env AUTO_COMPLETE_HOURS (default 168 jam = 7 hari).
func AutoCompleteDuration() time.Duration {
	if v := strings.TrimSpace(os.Getenv("AUTO_COMPLETE_HOURS")); v != "" {
		if h, err := strconv.ParseFloat(v, 64); err == nil && h > 0 {
			return time.Duration(h * float64(time.Hour))
		}
	}
	return 7 * 24 * time.Hour
}

// PendingConfirmDuration = batas waktu penjual wajib mengonfirmasi pesanan baru (status "pending").
// Atur lewat env PENDING_CONFIRM_TIMEOUT_HOURS (default 24 jam).
func PendingConfirmDuration() time.Duration {
	if v := strings.TrimSpace(os.Getenv("PENDING_CONFIRM_TIMEOUT_HOURS")); v != "" {
		if h, err := strconv.ParseFloat(v, 64); err == nil && h > 0 {
			return time.Duration(h * float64(time.Hour))
		}
	}
	return 24 * time.Hour
}

// ComputeOrderSubtotal menghitung subtotal barang TANPA ongkir sesuai urutan prioritas:
// 1) subtotal_amount (> 0)
// 2) sum(items[].price * quantity) (> 0)
// 3) max(0, total_amount - shipping_cost) sebagai opsi terakhir.
func ComputeOrderSubtotal(t model.Transaction) float64 {
	if t.SubtotalAmount > 0 {
		return t.SubtotalAmount
	}
	var itemSum float64
	for _, it := range t.Items {
		itemSum += it.Price * float64(it.Quantity)
	}
	if itemSum > 0 {
		return itemSum
	}
	sub := t.TotalAmount - t.ShippingCost
	if sub < 0 {
		return 0
	}
	return sub
}

// userSellsInTx: apakah user ini pemilik salah satu toko yang barangnya ada di pesanan.
func userSellsInTx(ctx context.Context, u *model.User, tx model.Transaction) bool {
	for _, it := range tx.Items {
		if it.ShopID.IsZero() {
			continue
		}
		n, _ := config.DB.Collection("shops").CountDocuments(ctx, bson.M{"_id": it.ShopID, "owner_id": u.ID})
		if n > 0 {
			return true
		}
	}
	return false
}

func transitionAllowed(role string, from, to string) bool {
	if role == "admin" {
		return true
	}
	if from == "disputed" || from == "success" || from == "cancelled" {
		return false
	}
	switch role {
	case "buyer":
		return (from == "shipped" && to == "success") ||
			((from == "pending_payment" || from == "pending") && to == "cancelled")
	case "seller":
		return ((from == "pending" || from == "pending_payment") && to == "paid") ||
			(from == "paid" && to == "shipped") ||
			((from == "pending" || from == "paid") && to == "cancelled")
	}
	return false
}

// applyStatusChange menjalankan efek samping (resi, peta, escrow, notifikasi) lalu menyimpan status.
func applyStatusChange(ctx context.Context, tx model.Transaction, newStatus, courierCode, trackingNumber string) (bson.M, error) {
	setFields := bson.M{"status": newStatus}
	if courierCode != "" {
		cOpt := findCourier(courierCode)
		setFields["courier_code"] = cOpt.Code
		setFields["courier_service"] = cOpt.Service
		tx.CourierCode = cOpt.Code
		tx.CourierService = cOpt.Service
	}
	if strings.TrimSpace(trackingNumber) != "" {
		setFields["tracking_number"] = strings.TrimSpace(trackingNumber)
		tx.TrackingNumber = strings.TrimSpace(trackingNumber)
	} else if tx.TrackingNumber == "" {
		code := tx.CourierCode
		if code == "" {
			code = "JNE"
		}
		tx.TrackingNumber = fmt.Sprintf("%s-%s", code, strings.ToUpper(tx.ID.Hex()[len(tx.ID.Hex())-8:]))
		setFields["tracking_number"] = tx.TrackingNumber
	}

	events, curLoc := buildTrackingStateForStatus(tx, newStatus)
	setFields["tracking_history"] = events
	if curLoc != nil {
		setFields["current_location"] = curLoc
	}
	if tx.RouteOrigin != nil {
		setFields["route_origin"] = tx.RouteOrigin
	}
	if tx.RouteDestination != nil {
		setFields["route_destination"] = tx.RouteDestination
	}

	now := time.Now()
	nowDT := primitive.NewDateTimeFromTime(now)
	switch {
	case newStatus == "shipped":
		// Mulai hitung mundur auto-complete
		due := primitive.NewDateTimeFromTime(now.Add(AutoCompleteDuration()))
		setFields["shipped_at"] = nowDT
		setFields["auto_complete_at"] = due
	case newStatus == "success" && tx.EscrowStatus != "released":
		setFields["escrow_status"] = "released"
		setFields["released_at"] = nowDT
		releaseEscrowFunds(ctx, tx)
	case newStatus == "cancelled" && tx.Status != "cancelled":
		restock := tx.Status != "shipped" && tx.Status != "disputed"
		if tx.EscrowStatus == "held" {
			setFields["escrow_status"] = "refunded"
		}
		refundEscrowAndRestoreStock(ctx, tx, restock)
	}

	if _, err := config.DB.Collection("transactions").UpdateOne(ctx, bson.M{"_id": tx.ID}, bson.M{"$set": setFields}); err != nil {
		return nil, err
	}

	switch newStatus {
	case "paid":
		PushUserNotification(tx.BuyerID, "order", "Pesanan Sedang Dikemas", fmt.Sprintf("Penjual sedang mengemas pesananmu. Kurir: %s.", tx.CourierService), "pesanan.html")
	case "shipped":
		PushUserNotification(tx.BuyerID, "order", "Pesanan Dikirim! Lacak di Peta", fmt.Sprintf("Paketmu dikirim dengan %s (Resi: %s). Jangan lupa klik 'Pesanan Diterima' setelah paket tiba. Bila tidak ada komplain, pesanan selesai otomatis dalam %d hari.", tx.CourierService, tx.TrackingNumber, int(AutoCompleteDuration().Hours()/24)), "pesanan.html")
	case "success":
		PushUserNotification(tx.BuyerID, "order", "Pesanan Selesai", "Terima kasih telah menerima pesanan. Dana Escrow telah diteruskan ke penjual.", "pesanan.html")
	case "cancelled":
		if tx.EscrowStatus == "held" {
			PushUserNotification(tx.BuyerID, "order", "Pesanan Dibatalkan & Dana Dikembalikan", "Pesanan dibatalkan dan dana Escrow dikembalikan 100% ke kamu.", "pesanan.html")
		} else {
			PushUserNotification(tx.BuyerID, "order", "Pesanan Dibatalkan", "Pesanan telah dibatalkan dan stok produk telah dikembalikan.", "pesanan.html")
		}
	}
	return setFields, nil
}

func changeStatusHandler(c *fiber.Ctx, forcedStatus string) error {
	user, err := requireAuth(c)
	if user == nil {
		return err
	}

	oid, perr := primitive.ObjectIDFromHex(c.Params("id"))
	if perr != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid transaction ID")
	}

	var req struct {
		Status         string `json:"status"`
		CourierCode    string `json:"courier_code"`
		TrackingNumber string `json:"tracking_number"`
	}
	if forcedStatus == "" {
		if err := c.BodyParser(&req); err != nil {
			return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body")
		}
	} else {
		req.Status = forcedStatus
	}
	req.Status = strings.TrimSpace(req.Status)
	valid := map[string]bool{"paid": true, "shipped": true, "success": true, "cancelled": true}
	if !valid[req.Status] {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Status tidak dikenal")
	}

	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var tx model.Transaction
	if err := config.DB.Collection("transactions").FindOne(ctx, bson.M{"_id": oid}).Decode(&tx); err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Transaction not found")
	}

	// Tentukan peran pengguna terhadap pesanan INI (bukan sekadar role akun)
	role := ""
	switch {
	case user.Role == "admin":
		role = "admin"
	case tx.BuyerID == user.ID:
		role = "buyer"
	case userSellsInTx(ctx, user, tx):
		role = "seller"
	}
	if role == "" {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Kamu tidak punya akses ke pesanan ini")
	}

	status := normalizeTxStatus(tx.Status)
	if tx.Status == "disputed" {
		return helper.ErrorResponse(c, fiber.StatusConflict, "Pesanan sedang disengketakan. Menunggu keputusan admin.")
	}
	if !transitionAllowed(role, status, req.Status) {
		msg := "Perubahan status ini tidak diizinkan untuk peranmu"
		if role == "seller" && req.Status == "success" {
			msg = "Hanya pembeli yang bisa menyatakan pesanan diterima. Dana cair setelah pembeli konfirmasi atau otomatis setelah batas waktu."
		}
		return helper.ErrorResponse(c, fiber.StatusForbidden, msg)
	}

	setFields, err := applyStatusChange(ctx, tx, req.Status, req.CourierCode, req.TrackingNumber)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Failed to update transaction status")
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Transaction status updated successfully", fiber.Map{
		"status":          req.Status,
		"escrow_status":   setFields["escrow_status"],
		"tracking_number": setFields["tracking_number"],
	})
}

// "pending_payment" lama dianggap "pending" untuk aturan transisi
func normalizeTxStatus(s string) string {
	if s == "processing" {
		return "paid"
	}
	return s
}

// UpdateTransactionStatus handles PUT /api/transaction/:id/status (butuh login)
func UpdateTransactionStatus(c *fiber.Ctx) error { return changeStatusHandler(c, "") }

// ConfirmOrderReceived menangani POST /api/transaction/:id/confirm-received.
// Hanya pembeli pemilik pesanan (atau admin) yang boleh; dana Escrow cair ke penjual.
func ConfirmOrderReceived(c *fiber.Ctx) error { return changeStatusHandler(c, "success") }

// AutoCompleteDueOrders menyelesaikan pesanan berstatus "shipped" yang melewati batas waktu
// tanpa konfirmasi maupun komplain: dana Escrow cair ke penjual. Aman dipanggil berulang.
func AutoCompleteDueOrders() int {
	if config.DB == nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cur, err := config.DB.Collection("transactions").Find(ctx, bson.M{
		"status":           "shipped",
		"auto_complete_at": bson.M{"$lte": primitive.NewDateTimeFromTime(time.Now())},
	})
	if err != nil {
		return 0
	}
	var due []model.Transaction
	_ = cur.All(ctx, &due)
	cur.Close(ctx)

	done := 0
	for _, tx := range due {
		// Klaim atomik supaya tidak diproses dua kali (mis. oleh dua instance)
		res, err := config.DB.Collection("transactions").UpdateOne(ctx,
			bson.M{"_id": tx.ID, "status": "shipped"},
			bson.M{"$set": bson.M{"status": "success_pending_release"}},
		)
		if err != nil || res.ModifiedCount == 0 {
			continue
		}
		setFields, err := applyStatusChange(ctx, tx, "success", "", "")
		if err != nil {
			_, _ = config.DB.Collection("transactions").UpdateOne(ctx, bson.M{"_id": tx.ID}, bson.M{"$set": bson.M{"status": "shipped"}})
			continue
		}
		_ = setFields
		_, _ = config.DB.Collection("transactions").UpdateOne(ctx, bson.M{"_id": tx.ID}, bson.M{"$set": bson.M{"auto_completed": true}})
		PushUserNotification(tx.BuyerID, "order", "Pesanan Selesai Otomatis",
			fmt.Sprintf("Pesanan %s otomatis dinyatakan selesai karena melewati batas konfirmasi tanpa komplain.", strings.ToUpper(tx.ID.Hex()[len(tx.ID.Hex())-6:])), "pesanan.html")
		done++
	}

	// Auto-cancel pesanan BARU berstatus "pending" yang memiliki field "confirm_by" dan sudah lewat batas konfirmasi penjual.
	// Catatan: Data historis lama (yang belum punya field "confirm_by") TIDAK disentuh oleh worker otomatis ini,
	// melainkan hanya dibatalkan lewat aksi manual Admin di Dashboard.
	curPending, err := config.DB.Collection("transactions").Find(ctx, bson.M{
		"status":     "pending",
		"confirm_by": bson.M{"$lte": primitive.NewDateTimeFromTime(time.Now())},
	})
	if err == nil {
		var staleNew []model.Transaction
		_ = curPending.All(ctx, &staleNew)
		curPending.Close(ctx)
		for _, tx := range staleNew {
			res, uerr := config.DB.Collection("transactions").UpdateOne(ctx,
				bson.M{"_id": tx.ID, "status": "pending"},
				bson.M{"$set": bson.M{"status": "cancelling_auto"}},
			)
			if uerr != nil || res.ModifiedCount == 0 {
				continue
			}
			tx.Status = "pending"
			if _, aerr := applyStatusChange(ctx, tx, "cancelled", "", ""); aerr != nil {
				_, _ = config.DB.Collection("transactions").UpdateOne(ctx, bson.M{"_id": tx.ID}, bson.M{"$set": bson.M{"status": "pending"}})
				continue
			}
			done++
		}
	}

	return done
}

// CancelStalePendingOrders menangani POST /api/admin/transactions/cancel-stale
// Membatalkan pesanan berstatus "pending" yang menunggu konfirmasi penjual lebih dari X jam (default 24 jam)
// berdasarkan konfirmasi manual Admin di Dashboard.
// Untuk transaksi lama dengan escrow_status kosong (""), fungsi ini HANYA mengubah status menjadi "cancelled"
// dan mengembalikan stok produk tanpa melakukan refund escrow.
func CancelStalePendingOrders(c *fiber.Ctx) error {
	user, aerr := requireAuth(c)
	if user == nil {
		return aerr
	}
	if user.Role != "admin" {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Hanya Admin yang dapat menjalankan pembatalan massal pesanan lewat batas waktu")
	}
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	var req struct {
		Hours float64 `json:"hours"`
	}
	_ = c.BodyParser(&req)
	timeout := PendingConfirmDuration()
	if req.Hours > 0 {
		timeout = time.Duration(req.Hours * float64(time.Hour))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cutoff := primitive.NewDateTimeFromTime(time.Now().Add(-timeout))
	cur, err := config.DB.Collection("transactions").Find(ctx, bson.M{
		"status":     "pending",
		"created_at": bson.M{"$lte": cutoff},
	})
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Gagal mencari pesanan yang lewat batas waktu")
	}
	var stale []model.Transaction
	_ = cur.All(ctx, &stale)
	cur.Close(ctx)

	cancelledCount := 0
	escrowRefundedCount := 0
	stockRestoredItems := 0

	for _, tx := range stale {
		setFields, err := applyStatusChange(ctx, tx, "cancelled", "", "")
		if err != nil {
			continue
		}
		cancelledCount++
		if setFields["escrow_status"] == "refunded" {
			escrowRefundedCount++
		}
		for _, it := range tx.Items {
			if !it.ProductID.IsZero() {
				stockRestoredItems += it.Quantity
			}
		}
	}

	return helper.SuccessResponse(c, fiber.StatusOK, fmt.Sprintf("%d pesanan yang menunggu > %.0f jam berhasil dibatalkan dan stok dikembalikan.", cancelledCount, timeout.Hours()), fiber.Map{
		"cancelled_count":       cancelledCount,
		"escrow_refunded_count": escrowRefundedCount,
		"stock_restored_items":  stockRestoredItems,
		"timeout_hours":         timeout.Hours(),
	})
}

var lastAutoSweep time.Time

// maybeSweepAutoComplete menjalankan penyelesaian otomatis secara lazy (maks. 1x/menit) —
// berguna di hosting serverless (Vercel) yang tidak punya proses latar belakang.
func maybeSweepAutoComplete() {
	if time.Since(lastAutoSweep) < time.Minute {
		return
	}
	lastAutoSweep = time.Now()
	go AutoCompleteDueOrders()
}

// StartAutoCompleteWorker menjalankan penyapu berkala untuk server biasa (go run / VPS).
func StartAutoCompleteWorker() {
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		AutoCompleteDueOrders()
		for range t.C {
			AutoCompleteDueOrders()
		}
	}()
}

// GetOrderTracking menangani GET /api/transaction/:id/track (Mengembalikan detail resi, kurir, timeline, dan koordinat peta untuk Leaflet)
func GetOrderTracking(c *fiber.Ctx) error {
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}
	authUser, aerr := requireAuth(c)
	if authUser == nil {
		return aerr
	}
	id := c.Params("id")
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid transaction ID")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	var tx model.Transaction
	if err := config.DB.Collection("transactions").FindOne(ctx, bson.M{"_id": oid}).Decode(&tx); err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Transaction not found")
	}

	if authUser.Role != "admin" && tx.BuyerID != authUser.ID && !userSellsInTx(ctx, authUser, tx) {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Akses ditolak: Anda tidak berhak melacak transaksi ini.")
	}
	if authUser.Role == "admin" && tx.BuyerID != authUser.ID {
		tx.RecipientName = helper.MaskAddress(tx.RecipientName)
		tx.ShippingAddress = helper.MaskAddress(tx.ShippingAddress)
	}

	// Pastikan pesanan lama yang belum punya field koordinat tetap mendapat default rute peta
	origin := tx.RouteOrigin
	if origin == nil {
		origin = &model.GeoLocation{Label: "Gudang Ruko Penjual (Jakarta Pusat)", City: "Jakarta Pusat", Lat: -6.1944, Lng: 106.8229}
	}
	dest := tx.RouteDestination
	if dest == nil {
		d := inferDestinationCoords(tx.ShippingAddress)
		dest = &d
	}
	current := tx.CurrentLocation
	if current == nil {
		switch tx.Status {
		case "shipped":
			current = &model.GeoLocation{
				Label: "Dalam Perjalanan (Hub Transit Kurir)",
				City:  "Purwakarta / Cipularang",
				Lat:   (origin.Lat + dest.Lat) / 2,
				Lng:   (origin.Lng + dest.Lng) / 2,
			}
		case "success":
			current = dest
		default:
			current = origin
		}
	}

	courierCode := tx.CourierCode
	if courierCode == "" {
		courierCode = "JNE"
	}
	courierService := tx.CourierService
	if courierService == "" {
		courierService = "JNE Reguler (2-3 Hari)"
	}
	trackingNumber := tx.TrackingNumber
	if trackingNumber == "" {
		trackingNumber = fmt.Sprintf("%s-%s", courierCode, strings.ToUpper(tx.ID.Hex()[len(tx.ID.Hex())-8:]))
	}

	history := tx.TrackingHistory
	if len(history) == 0 {
		history, _ = buildTrackingStateForStatus(tx, tx.Status)
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Tracking info retrieved", fiber.Map{
		"transaction_id":    tx.ID,
		"status":            tx.Status,
		"escrow_status":     tx.EscrowStatus,
		"courier_code":      courierCode,
		"courier_service":   courierService,
		"courier_etd":       tx.CourierETD,
		"tracking_number":   trackingNumber,
		"recipient_name":    tx.RecipientName,
		"shipping_address":  tx.ShippingAddress,
		"route_origin":      origin,
		"route_destination": dest,
		"current_location":  current,
		"tracking_history":  history,
	})
}

// GetWalletSummary menangani GET /api/wallet/summary (?shop_id=... untuk Penjual, atau tanpa shop_id untuk Admin)
func GetWalletSummary(c *fiber.Ctx) error {
	user, aerr := requireAuth(c)
	if user == nil {
		return aerr
	}
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	shopID := strings.TrimSpace(c.Query("shop_id"))
	if shopID == "" && user.Role != "admin" {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Ringkasan platform hanya untuk admin")
	}
	if shopID != "" {
		shopOID, err := primitive.ObjectIDFromHex(shopID)
		if err != nil {
			return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid shop_id")
		}

		var shop model.Shop
		if err := config.DB.Collection("shops").FindOne(ctx, bson.M{"_id": shopOID}).Decode(&shop); err != nil {
			return helper.ErrorResponse(c, fiber.StatusNotFound, "Shop not found")
		}
		if user.Role != "admin" && shop.OwnerID != user.ID {
			return helper.ErrorResponse(c, fiber.StatusForbidden, "Bukan ruko milikmu")
		}

		// Hitung ulang juga dari transaksi agar data lama tetap akurat
		cursor, err := config.DB.Collection("transactions").Find(ctx, bson.M{"items.shop_id": shopOID})
		var txs []model.Transaction
		if err == nil {
			_ = cursor.All(ctx, &txs)
			cursor.Close(ctx)
		}

		var calcEscrowHeld, calcReleasedGross, calcTaxPaid, calcReleasedNet float64
		for _, t := range txs {
			if t.Status == "cancelled" {
				continue
			}
			var shopGross float64
			for _, it := range t.Items {
				if it.ShopID == shopOID {
					shopGross += it.Price * float64(it.Quantity)
				}
			}
			tax := math.Round(shopGross * PlatformFeeRate)
			net := shopGross - tax
			if t.Status == "success" || t.EscrowStatus == "released" {
				calcReleasedGross += shopGross
				calcTaxPaid += tax
				calcReleasedNet += net
			} else if t.Status != "pending_payment" {
				calcEscrowHeld += net
			}
		}

		availableBalance := calcReleasedNet - shop.TotalWithdrawn
		if availableBalance < 0 {
			availableBalance = 0
		}

		// Ambil 20 mutasi terakhir
		opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(20)
		lCur, _ := config.DB.Collection("wallet_ledgers").Find(ctx, bson.M{"shop_id": shopOID}, opts)
		var ledgers []model.WalletLedger = []model.WalletLedger{}
		if lCur != nil {
			_ = lCur.All(ctx, &ledgers)
			lCur.Close(ctx)
		}

		return helper.SuccessResponse(c, fiber.StatusOK, "Seller wallet summary retrieved", fiber.Map{
			"shop_id":             shop.ID,
			"shop_name":           shop.Name,
			"platform_fee_rate":   PlatformFeeRate,
			"escrow_held_balance": calcEscrowHeld,
			"available_balance":   availableBalance,
			"total_released_net":  calcReleasedNet,
			"total_tax_paid":      calcTaxPaid,
			"total_withdrawn":     shop.TotalWithdrawn,
			"ledgers":             ledgers,
		})
	}

	// Ringkasan Keuangan Platform untuk Admin (Escrow yang ditahan, Pendapatan Pajak 2.5%, dan Pendapatan Sewa Ruko)
	cursor, err := config.DB.Collection("transactions").Find(ctx, bson.M{})
	var allTxs []model.Transaction
	if err == nil {
		_ = cursor.All(ctx, &allTxs)
		cursor.Close(ctx)
	}

	var totalEscrowHeld, totalPlatformTaxEarned, totalPendingTax, totalGMV float64
	var escrowOrderCount int
	for _, t := range allTxs {
		if t.Status == "cancelled" || t.Status == "pending_payment" {
			continue
		}
		sub := ComputeOrderSubtotal(t)
		tax := t.PlatformFee
		if tax <= 0 {
			tax = math.Round(sub * PlatformFeeRate)
		}
		totalGMV += t.TotalAmount
		if t.Status == "success" || t.EscrowStatus == "released" {
			totalPlatformTaxEarned += tax
		} else {
			totalEscrowHeld += (sub - tax)
			totalPendingTax += tax
			escrowOrderCount++
		}
	}

	return helper.SuccessResponse(c, fiber.StatusOK, "Platform escrow & tax summary retrieved", fiber.Map{
		"platform_fee_rate":         PlatformFeeRate,
		"total_gmv":                 totalGMV,
		"total_escrow_held":         totalEscrowHeld,
		"escrow_order_count":        escrowOrderCount,
		"total_platform_tax_earned": totalPlatformTaxEarned,
		"total_pending_tax":         totalPendingTax,
	})
}

var withdrawMu sync.Mutex

// WithdrawSellerBalance menangani POST /api/wallet/withdraw (Penjual mencairkan Saldo Aktif yang sudah lepas dari Escrow)
func WithdrawSellerBalance(c *fiber.Ctx) error {
	user, aerr := requireAuth(c)
	if user == nil {
		return aerr
	}
	if config.DB == nil {
		return helper.ErrorResponse(c, fiber.StatusInternalServerError, "Database connection not initialized")
	}

	var req struct {
		ShopID        string  `json:"shop_id"`
		Amount        float64 `json:"amount"`
		BankName      string  `json:"bank_name"`
		AccountNumber string  `json:"account_number"`
	}
	if err := c.BodyParser(&req); err != nil {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Invalid request body")
	}

	shopOID, err := primitive.ObjectIDFromHex(req.ShopID)
	if err != nil || req.Amount < 10000 {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, "Shop ID valid dan nominal penarikan minimal Rp 10.000 diperlukan")
	}

	// Kunci penarikan secara sinkron + filter atomik di database untuk mencegah race condition (penarikan ganda bersamaan)
	withdrawMu.Lock()
	defer withdrawMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	var shop model.Shop
	if err := config.DB.Collection("shops").FindOne(ctx, bson.M{"_id": shopOID}).Decode(&shop); err != nil {
		return helper.ErrorResponse(c, fiber.StatusNotFound, "Ruko tidak ditemukan")
	}
	if shop.OwnerID != user.ID {
		return helper.ErrorResponse(c, fiber.StatusForbidden, "Hanya pemilik ruko yang bisa menarik saldo")
	}

	// Hitung saldo tersedia yang sudah released
	cursor, _ := config.DB.Collection("transactions").Find(ctx, bson.M{"items.shop_id": shopOID})
	var txs []model.Transaction
	if cursor != nil {
		_ = cursor.All(ctx, &txs)
		cursor.Close(ctx)
	}
	var calcReleasedNet float64
	for _, t := range txs {
		if t.Status == "success" || t.EscrowStatus == "released" {
			var shopGross float64
			for _, it := range t.Items {
				if it.ShopID == shopOID {
					shopGross += it.Price * float64(it.Quantity)
				}
			}
			tax := math.Round(shopGross * PlatformFeeRate)
			calcReleasedNet += (shopGross - tax)
		}
	}
	available := calcReleasedNet - shop.TotalWithdrawn
	if req.Amount > available {
		return helper.ErrorResponse(c, fiber.StatusBadRequest, fmt.Sprintf("Saldo aktif yang bisa ditarik tidak mencukupi (Tersedia: Rp %.0f)", available))
	}

	updateFilter := bson.M{"_id": shopOID, "total_withdrawn": shop.TotalWithdrawn}
	if shop.TotalWithdrawn == 0 {
		updateFilter = bson.M{
			"_id": shopOID,
			"$or": []bson.M{
				{"total_withdrawn": 0},
				{"total_withdrawn": bson.M{"$exists": false}},
				{"total_withdrawn": nil},
			},
		}
	}

	res, err := config.DB.Collection("shops").UpdateOne(
		ctx,
		updateFilter,
		bson.M{
			"$inc": bson.M{"total_withdrawn": req.Amount},
			"$set": bson.M{"balance": available - req.Amount},
		},
	)
	if err != nil || res.ModifiedCount == 0 {
		return helper.ErrorResponse(c, fiber.StatusConflict, "Gagal memproses penarikan dana atau terdapat transaksi penarikan bersamaan")
	}

	ledger := model.WalletLedger{
		ID:            primitive.NewObjectID(),
		ShopID:        shopOID,
		Type:          "withdrawal",
		GrossAmount:   req.Amount,
		TaxAmount:     0,
		NetAmount:     req.Amount,
		BankName:      strings.TrimSpace(req.BankName),
		AccountNumber: helper.MaskAccount(req.AccountNumber),
		Description:   fmt.Sprintf("Penarikan Saldo Aktif ke %s (%s)", req.BankName, helper.MaskAccount(req.AccountNumber)),
		CreatedAt:     primitive.NewDateTimeFromTime(time.Now()),
	}
	_, _ = config.DB.Collection("wallet_ledgers").InsertOne(ctx, ledger)

	return helper.SuccessResponse(c, fiber.StatusOK, "Penarikan saldo berhasil diproses!", fiber.Map{
		"withdrawn_amount":  req.Amount,
		"remaining_balance": available - req.Amount,
		"ledger":            ledger,
	})
}
