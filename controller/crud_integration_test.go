package controller_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"vendora/config"
	"vendora/controller"
	"vendora/url"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// readEnvMongoURI membaca MONGO_URI dari env atau backend/.env hanya untuk test integrasi terisolasi.
func readEnvMongoURI() string {
	if u := strings.TrimSpace(os.Getenv("VENDORA_TEST_MONGO_URI")); u != "" {
		return u
	}
	if os.Getenv("VENDORA_INTEGRATION_TEST") != "1" {
		return ""
	}
	if u := strings.TrimSpace(os.Getenv("MONGO_URI")); u != "" {
		return u
	}
	for _, p := range []string{"../.env", ".env"} {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if strings.HasPrefix(line, "MONGO_URI=") {
				f.Close()
				return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "MONGO_URI=")), `"'`)
			}
		}
		f.Close()
	}
	return ""
}

// setupIntegrationApp menyiapkan database MongoDB sementara yang 100% terisolasi
// (TIDAK PERNAH menyentuh vendora_db produksi) dan otomatis dihapus di t.Cleanup.
func setupIntegrationApp(t *testing.T) (*fiber.App, *mongo.Database) {
	t.Helper()
	uri := readEnvMongoURI()
	if uri == "" {
		t.Skip("Lewati uji integrasi DB (aktifkan dengan VENDORA_INTEGRATION_TEST=1)")
	}

	testDBName := fmt.Sprintf("vdr_test_%d", time.Now().UnixNano()%1000000000)
	if testDBName == "vendora_db" || !strings.HasPrefix(testDBName, "vdr_test_") {
		t.Fatal("SAFETY GUARD: dilarang menjalankan pengujian di database produksi vendora_db!")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("gagal koneksi MongoDB test: %v", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		t.Fatalf("gagal ping MongoDB test: %v", err)
	}

	prevDB := config.DB
	testDB := client.Database(testDBName)
	config.DB = testDB

	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer dropCancel()
		_ = testDB.Drop(dropCtx)
		_ = client.Disconnect(dropCtx)
		config.DB = prevDB
	})

	app := fiber.New(fiber.Config{BodyLimit: 10 * 1024 * 1024})
	url.SetupRoutes(app)
	return app, testDB
}

func apiReq(t *testing.T, app *fiber.App, method, path string, body interface{}, token string) (int, map[string]interface{}, string) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode JSON: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := app.Test(req, 15000)
	if err != nil {
		t.Fatalf("%s %s error: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]interface{}
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, string(raw)
}

func registerAndLogin(t *testing.T, app *fiber.App, db *mongo.Database, name, email, phone, password, role string) (userID string, token string) {
	t.Helper()
	regRole := role
	if role == "admin" {
		regRole = "buyer"
	}
	st, body, raw := apiReq(t, app, "POST", "/api/register", map[string]string{
		"name": name, "email": email, "phone": phone, "password": password, "role": regRole,
	}, "")
	if st != http.StatusCreated {
		t.Fatalf("register %s failed (%d): %s", email, st, raw)
	}
	data := body["data"].(map[string]interface{})
	userID = data["id"].(string)

	if role == "admin" {
		oid, _ := primitive.ObjectIDFromHex(userID)
		_, err := db.Collection("users").UpdateOne(context.Background(), bson.M{"_id": oid}, bson.M{"$set": bson.M{"role": "admin"}})
		if err != nil {
			t.Fatalf("promote admin: %v", err)
		}
	}

	st, lbody, lraw := apiReq(t, app, "POST", "/api/login", map[string]string{
		"email": email, "password": password,
	}, "")
	if st != http.StatusOK {
		t.Fatalf("login %s failed (%d): %s", email, st, lraw)
	}
	ldata := lbody["data"].(map[string]interface{})
	token = ldata["token"].(string)
	return userID, token
}

func samplePNGDataURL(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(0, 0, color.RGBA{G: 200, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}

// ─── SKENARIO 11–16: UJI INTEGRASI CRUD PENUH END-TO-END (PROYEK 2) ──────────
func TestScenario11To16_FullEndToEndCRUDAndEscrow(t *testing.T) {
	app, db := setupIntegrationApp(t)

	// ── SKENARIO 11: CRUD User, Auth, Profil, Masking Admin & Ban ──
	adminID, adminTok := registerAndLogin(t, app, db, "Admin Test", "admin@test.vendora.id", "08110000001", "AdminPass123!", "admin")
	sellerID, sellerTok := registerAndLogin(t, app, db, "Toko Seller", "seller@test.vendora.id", "08110000002", "SellerPass123!", "seller")
	buyerID, buyerTok := registerAndLogin(t, app, db, "Pembeli Satu", "buyer@test.vendora.id", "08110000003", "BuyerPass123!", "buyer")
	banTargetID, banTargetTok := registerAndLogin(t, app, db, "User Nakal", "nakal@test.vendora.id", "08110000004", "NakalPass123!", "buyer")
	_ = adminID
	_ = sellerID

	if st, _, _ := apiReq(t, app, "GET", "/api/me", nil, buyerTok); st != 200 {
		t.Fatalf("GET /api/me -> %d", st)
	}
	if st, _, _ := apiReq(t, app, "GET", "/api/admin/verify", nil, adminTok); st != 200 {
		t.Fatalf("GET /api/admin/verify -> %d", st)
	}
	if st, _, raw := apiReq(t, app, "PUT", "/api/user/profile", map[string]string{
		"name": "Pembeli Updated", "email": "buyer@test.vendora.id", "phone": "08110000003", "address": "Jl. Merdeka No 10, Kota Bandung",
	}, buyerTok); st != 200 {
		t.Fatalf("PUT /api/user/profile -> %d: %s", st, raw)
	}
	if st, _, _ := apiReq(t, app, "GET", "/api/users", nil, adminTok); st != 200 {
		t.Fatalf("GET /api/users -> %d", st)
	}
	// Ban user mencabut sesi aktifnya (401/403)
	if st, _, _ := apiReq(t, app, "PUT", "/api/user/"+banTargetID+"/ban", map[string]bool{"banned": true}, adminTok); st != 200 {
		t.Fatalf("PUT /api/user/:id/ban -> %d", st)
	}
	if st, _, _ := apiReq(t, app, "GET", "/api/me", nil, banTargetTok); st != 401 && st != 403 {
		t.Fatalf("sesi user yang diban seharusnya ditolak, dapat %d", st)
	}

	// ── SKENARIO 12: CRUD Ruko Lengkap (Create → Read → Update → Approve → Renew) ──
	st, rentBody, raw := apiReq(t, app, "POST", "/api/shop/rent", map[string]interface{}{
		"name": "Ruko Gadget Jaya", "description": "Ruko elektronik resmi", "rental_days": 30,
	}, sellerTok)
	if st != 201 {
		t.Fatalf("POST /api/shop/rent -> %d: %s", st, raw)
	}
	shopData := rentBody["data"].(map[string]interface{})["shop"].(map[string]interface{})
	shopID := shopData["id"].(string)
	if shopData["status"] != "pending" {
		t.Fatalf("ruko baru harus pending, dapat %v", shopData["status"])
	}

	// Read mine & detail by ID
	if st, _, _ := apiReq(t, app, "GET", "/api/shop/mine", nil, sellerTok); st != 200 {
		t.Fatalf("GET /api/shop/mine -> %d", st)
	}
	if st, _, _ := apiReq(t, app, "GET", "/api/shop/"+shopID, nil, ""); st != 200 {
		t.Fatalf("GET /api/shop/:id -> %d", st)
	}
	// Update profil ruko
	if st, _, raw := apiReq(t, app, "PUT", "/api/shop/"+shopID, map[string]string{
		"name": "Ruko Gadget Premium", "description": "Deskripsi diperbarui", "city": "Bandung",
	}, sellerTok); st != 200 {
		t.Fatalf("PUT /api/shop/:id -> %d: %s", st, raw)
	}
	// Ruko pending belum boleh tambah produk (403)
	if st, _, _ := apiReq(t, app, "POST", "/api/product", map[string]interface{}{
		"shop_id": shopID, "name": "Produk Pending", "price": 100000, "stock": 5,
	}, sellerTok); st != 403 {
		t.Fatalf("ruko pending seharusnya ditolak 403 saat tambah produk, dapat %d", st)
	}
	// Admin menyetujui ruko → active
	if st, _, raw := apiReq(t, app, "PUT", "/api/shop/"+shopID+"/status", map[string]string{"status": "active"}, adminTok); st != 200 {
		t.Fatalf("PUT /api/shop/:id/status -> %d: %s", st, raw)
	}
	// Perpanjang sewa ruko 30 hari
	if st, _, raw := apiReq(t, app, "PUT", "/api/shop/"+shopID+"/renew", map[string]int{"rental_days": 30}, sellerTok); st != 200 {
		t.Fatalf("PUT /api/shop/:id/renew -> %d: %s", st, raw)
	}
	if st, _, _ := apiReq(t, app, "GET", "/api/shops", nil, ""); st != 200 {
		t.Fatalf("GET /api/shops -> %d", st)
	}

	// ── SKENARIO 13: CRUD Produk Lengkap + Moderasi Visibilitas Admin ──
	st, prodBody, raw := apiReq(t, app, "POST", "/api/product", map[string]interface{}{
		"shop_id": shopID, "name": "Headphone ANC Pro", "description": "Audio jernih",
		"category": "elektronik", "price": 1000000, "promo_price": 800000, "stock": 10,
		"images": []string{"https://example.com/hp.jpg"},
		"options": []map[string]interface{}{
			{"name": "Warna", "values": []map[string]interface{}{{"label": "Hitam", "price_delta": 0}, {"label": "Silver", "price_delta": 50000}}},
		},
	}, sellerTok)
	if st != 201 {
		t.Fatalf("POST /api/product -> %d: %s", st, raw)
	}
	prodID := prodBody["data"].(map[string]interface{})["id"].(string)

	if st, _, _ := apiReq(t, app, "GET", "/api/products", nil, ""); st != 200 {
		t.Fatalf("GET /api/products -> %d", st)
	}
	if st, _, _ := apiReq(t, app, "GET", "/api/product/"+prodID, nil, ""); st != 200 {
		t.Fatalf("GET /api/product/:id -> %d", st)
	}
	if st, _, raw := apiReq(t, app, "PUT", "/api/product/"+prodID, map[string]interface{}{
		"name": "Headphone ANC Pro Gen 2", "stock": 12,
	}, sellerTok); st != 200 {
		t.Fatalf("PUT /api/product/:id -> %d: %s", st, raw)
	}
	// Moderasi: sembunyikan → publik 404 → tampilkan kembali → publik 200
	if st, _, _ := apiReq(t, app, "GET", "/api/admin/products", nil, adminTok); st != 200 {
		t.Fatalf("GET /api/admin/products -> %d", st)
	}
	if st, _, _ := apiReq(t, app, "PUT", "/api/admin/product/"+prodID+"/visibility", map[string]interface{}{"hidden": true, "reason": "Uji moderasi"}, adminTok); st != 200 {
		t.Fatalf("hide product -> %d", st)
	}
	if st, _, _ := apiReq(t, app, "GET", "/api/product/"+prodID, nil, ""); st != 404 {
		t.Fatalf("produk tersembunyi seharusnya 404 untuk publik, dapat %d", st)
	}
	if st, _, _ := apiReq(t, app, "PUT", "/api/admin/product/"+prodID+"/visibility", map[string]interface{}{"hidden": false, "reason": ""}, adminTok); st != 200 {
		t.Fatalf("unhide product -> %d", st)
	}

	// ── SKENARIO 14: Checkout → Tracking → Escrow Release → Wallet Withdraw → Export CSV ──
	st, coBody, raw := apiReq(t, app, "POST", "/api/checkout", map[string]interface{}{
		"recipient_name": "Pembeli Satu", "shipping_address": "Jl. Merdeka No 10, Bandung",
		"courier_code": "JNE", "payment_method": "QRIS",
		"items": []map[string]interface{}{
			{"product_id": prodID, "quantity": 2, "options": map[string]string{"Warna": "Silver"}},
		},
	}, buyerTok)
	if st != 201 {
		t.Fatalf("POST /api/checkout -> %d: %s", st, raw)
	}
	txList := coBody["data"].(map[string]interface{})["transactions"].([]interface{})
	txID := txList[0].(map[string]interface{})["id"].(string)

	if st, _, _ := apiReq(t, app, "GET", "/api/transactions?mine=1", nil, buyerTok); st != 200 {
		t.Fatalf("GET /api/transactions?mine=1 -> %d", st)
	}
	if st, _, _ := apiReq(t, app, "GET", "/api/transaction/"+txID, nil, buyerTok); st != 200 {
		t.Fatalf("GET /api/transaction/:id -> %d", st)
	}
	if st, _, _ := apiReq(t, app, "GET", "/api/transaction/"+txID+"/track", nil, buyerTok); st != 200 {
		t.Fatalf("GET /api/transaction/:id/track -> %d", st)
	}
	if st, _, raw := apiReq(t, app, "PUT", "/api/transaction/"+txID+"/status", map[string]string{"status": "paid"}, sellerTok); st != 200 {
		t.Fatalf("pending → paid -> %d: %s", st, raw)
	}
	if st, _, raw := apiReq(t, app, "PUT", "/api/transaction/"+txID+"/status", map[string]string{"status": "shipped"}, sellerTok); st != 200 {
		t.Fatalf("paid → shipped -> %d: %s", st, raw)
	}
	if st, _, raw := apiReq(t, app, "POST", "/api/transaction/"+txID+"/confirm-received", nil, buyerTok); st != 200 {
		t.Fatalf("confirm-received -> %d: %s", st, raw)
	}
	if st, _, _ := apiReq(t, app, "GET", "/api/wallet/summary?shop_id="+shopID, nil, sellerTok); st != 200 {
		t.Fatalf("GET /api/wallet/summary -> %d", st)
	}
	if st, _, raw := apiReq(t, app, "POST", "/api/wallet/withdraw", map[string]interface{}{
		"shop_id": shopID, "amount": 50000, "bank_name": "BCA", "account_number": "1234567890",
	}, sellerTok); st != 200 {
		t.Fatalf("POST /api/wallet/withdraw -> %d: %s", st, raw)
	}
	if st, _, _ := apiReq(t, app, "GET", "/api/products/sold", nil, ""); st != 200 {
		t.Fatalf("GET /api/products/sold -> %d", st)
	}
	if st, _, csvRaw := apiReq(t, app, "GET", "/api/admin/transactions/export", nil, adminTok); st != 200 || !strings.Contains(csvRaw, txID) {
		t.Fatalf("GET /api/admin/transactions/export -> %d", st)
	}

	// ── SKENARIO 15: Alur wait_payment → Keamanan Token & Pembatalan Restock ──
	st, wpBody, raw := apiReq(t, app, "POST", "/api/checkout", map[string]interface{}{
		"recipient_name": "Pembeli Satu", "shipping_address": "Jl. Merdeka No 10, Bandung",
		"courier_code": "JNT", "payment_method": "VA_BCA", "wait_payment": true,
		"items": []map[string]interface{}{
			{"product_id": prodID, "quantity": 1, "options": map[string]string{"Warna": "Hitam"}},
		},
	}, buyerTok)
	if st != 201 {
		t.Fatalf("wait_payment checkout -> %d: %s", st, raw)
	}
	wpTx := wpBody["data"].(map[string]interface{})["transactions"].([]interface{})[0].(map[string]interface{})
	wpTxID := wpTx["id"].(string)
	wpToken := wpTx["payment_token"].(string)
	if wpToken == "" {
		t.Fatal("wait_payment: true wajib mengembalikan payment_token")
	}
	// Seller tidak boleh memaksa pending_payment → paid
	if st, _, _ := apiReq(t, app, "PUT", "/api/transaction/"+wpTxID+"/status", map[string]string{"status": "paid"}, sellerTok); st != 403 {
		t.Fatalf("seller seharusnya ditolak 403 untuk pending_payment → paid, dapat %d", st)
	}
	// Bayar dengan token salah ditolak 403; token benar sukses 200
	if st, _, _ := apiReq(t, app, "POST", "/api/payment/"+wpTxID+"/pay", map[string]string{"payment_token": "salah"}, buyerTok); st != 403 {
		t.Fatalf("wrong payment_token -> %d, want 403", st)
	}
	if st, _, raw := apiReq(t, app, "POST", "/api/payment/"+wpTxID+"/pay", map[string]string{"payment_token": wpToken}, buyerTok); st != 200 {
		t.Fatalf("valid payment_token -> %d: %s", st, raw)
	}
	if st, _, _ := apiReq(t, app, "GET", "/api/payment/"+wpTxID+"/status", nil, buyerTok); st != 200 {
		t.Fatalf("GET /api/payment/:id/status -> %d", st)
	}
	// Batalkan pesanan yang belum dikemas → status cancelled + refund + restock
	if st, _, raw := apiReq(t, app, "PUT", "/api/transaction/"+wpTxID+"/status", map[string]string{"status": "cancelled"}, buyerTok); st != 200 {
		t.Fatalf("cancel pending order -> %d: %s", st, raw)
	}

	// ── SKENARIO 16: Dispute, Tiket CS, Notifikasi, Chat, Laporan & Delete Produk/Ruko ──
	// 16a. Dispute (checkout → paid → shipped → dispute dengan XSS evidence → reply → resolve refund)
	_, dCoBody, _ := apiReq(t, app, "POST", "/api/checkout", map[string]interface{}{
		"recipient_name": "Pembeli Satu", "shipping_address": "Jl. Merdeka No 10, Bandung",
		"courier_code": "SICEPAT", "payment_method": "QRIS",
		"items": []map[string]interface{}{{"product_id": prodID, "quantity": 1, "options": map[string]string{"Warna": "Hitam"}}},
	}, buyerTok)
	dTxID := dCoBody["data"].(map[string]interface{})["transactions"].([]interface{})[0].(map[string]interface{})["id"].(string)
	apiReq(t, app, "PUT", "/api/transaction/"+dTxID+"/status", map[string]string{"status": "paid"}, sellerTok)
	apiReq(t, app, "PUT", "/api/transaction/"+dTxID+"/status", map[string]string{"status": "shipped"}, sellerTok)

	st, dispBody, raw := apiReq(t, app, "POST", "/api/disputes", map[string]interface{}{
		"transaction_id": dTxID, "reason": "damaged",
		"description": "Kemasan penyok dan produk rusak saat tiba",
		"evidence":    []string{"javascript:alert(1)", "https://example.com/bukti1.jpg"},
	}, buyerTok)
	if st != 201 {
		t.Fatalf("POST /api/disputes -> %d: %s", st, raw)
	}
	dispData := dispBody["data"].(map[string]interface{})
	dispID := dispData["id"].(string)
	if evs := dispData["evidence"].([]interface{}); len(evs) != 1 || evs[0] != "https://example.com/bukti1.jpg" {
		t.Fatalf("bukti javascript: seharusnya dibuang, dapat: %+v", evs)
	}
	if st, _, _ := apiReq(t, app, "PUT", "/api/dispute/"+dispID+"/reply", map[string]string{"response": "Kami sudah kirim dengan bubble wrap"}, sellerTok); st != 200 {
		t.Fatalf("PUT /api/dispute/:id/reply -> %d", st)
	}
	if st, _, _ := apiReq(t, app, "GET", "/api/disputes", nil, adminTok); st != 200 {
		t.Fatalf("GET /api/disputes -> %d", st)
	}
	if st, _, raw := apiReq(t, app, "PUT", "/api/dispute/"+dispID+"/resolve", map[string]string{"decision": "refund", "note": "Bukti valid"}, adminTok); st != 200 {
		t.Fatalf("PUT /api/dispute/:id/resolve -> %d: %s", st, raw)
	}

	// 16b. Tiket Bantuan
	st, tktBody, raw := apiReq(t, app, "POST", "/api/tickets", map[string]string{
		"subject": "Tanya Pengiriman", "message": "Halo admin, berapa lama pengiriman reguler?",
	}, buyerTok)
	if st != 201 {
		t.Fatalf("POST /api/tickets -> %d: %s", st, raw)
	}
	tktID := tktBody["data"].(map[string]interface{})["id"].(string)
	if st, _, _ := apiReq(t, app, "GET", "/api/tickets", nil, adminTok); st != 200 {
		t.Fatalf("GET /api/tickets -> %d", st)
	}
	if st, _, _ := apiReq(t, app, "PUT", "/api/ticket/"+tktID+"/reply", map[string]string{"reply": "Estimasi 2-3 hari kerja ya!"}, adminTok); st != 200 {
		t.Fatalf("PUT /api/ticket/:id/reply -> %d", st)
	}

	// 16c. Notifikasi (Create → List → Read → Read-All)
	st, nBody, raw := apiReq(t, app, "POST", "/api/notifications", map[string]string{
		"title": "Flash Sale Akhir Pekan", "message": "Diskon hingga 20% semua audio!", "link": "index.html",
	}, sellerTok)
	if st != 201 {
		t.Fatalf("POST /api/notifications -> %d: %s", st, raw)
	}
	notifID := nBody["data"].(map[string]interface{})["id"].(string)
	if st, _, _ := apiReq(t, app, "GET", "/api/notifications", nil, buyerTok); st != 200 {
		t.Fatalf("GET /api/notifications -> %d", st)
	}
	if st, _, _ := apiReq(t, app, "PUT", "/api/notifications/"+notifID+"/read", nil, buyerTok); st != 200 {
		t.Fatalf("PUT /api/notifications/:id/read -> %d", st)
	}
	if st, _, _ := apiReq(t, app, "PUT", "/api/notifications/read-all", nil, buyerTok); st != 200 {
		t.Fatalf("PUT /api/notifications/read-all -> %d", st)
	}

	// 16d. Chat (Send teks + foto → Media → Seller balas → History → Inbox → Unread → Read)
	st, msgBody, raw := apiReq(t, app, "POST", "/api/chat/send", map[string]string{
		"shop_id": shopID, "text": "Apakah stok ready?", "image": samplePNGDataURL(t),
	}, buyerTok)
	if st != 201 {
		t.Fatalf("POST /api/chat/send -> %d: %s", st, raw)
	}
	imgID := msgBody["data"].(map[string]interface{})["image"].(string)
	if st, _, _ := apiReq(t, app, "GET", "/api/chat/media/"+imgID, nil, ""); st != 200 {
		t.Fatalf("GET /api/chat/media/:id -> %d", st)
	}
	if st, _, raw := apiReq(t, app, "POST", "/api/chat/send", map[string]string{
		"shop_id": shopID, "receiver_id": buyerID, "text": "Ready kak, silakan checkout!",
	}, sellerTok); st != 201 {
		t.Fatalf("seller reply POST /api/chat/send -> %d: %s", st, raw)
	}
	if st, _, _ := apiReq(t, app, "GET", "/api/chat/history?buyer_id="+buyerID+"&shop_id="+shopID, nil, buyerTok); st != 200 {
		t.Fatalf("GET /api/chat/history -> %d", st)
	}
	if st, _, _ := apiReq(t, app, "GET", "/api/chat/inbox?shop_id="+shopID, nil, sellerTok); st != 200 {
		t.Fatalf("GET /api/chat/inbox (seller) -> %d", st)
	}
	if st, _, _ := apiReq(t, app, "GET", "/api/chat/inbox?buyer_id="+buyerID, nil, buyerTok); st != 200 {
		t.Fatalf("GET /api/chat/inbox (buyer) -> %d", st)
	}
	if st, _, _ := apiReq(t, app, "GET", "/api/chat/unread", nil, sellerTok); st != 200 {
		t.Fatalf("GET /api/chat/unread -> %d", st)
	}
	if st, _, _ := apiReq(t, app, "POST", "/api/chat/read", map[string]string{"buyer_id": buyerID, "shop_id": shopID}, sellerTok); st != 200 {
		t.Fatalf("POST /api/chat/read -> %d", st)
	}

	// 16e. Laporan Moderasi Admin (Create → List → Resolve dengan note)
	st, repBody, raw := apiReq(t, app, "POST", "/api/reports", map[string]string{
		"target_type": "product", "product_id": prodID, "shop_id": shopID,
		"reason": "misleading", "description": "Uji laporan produk",
	}, buyerTok)
	if st != 201 {
		t.Fatalf("POST /api/reports -> %d: %s", st, raw)
	}
	repID := repBody["data"].(map[string]interface{})["id"].(string)
	if st, _, _ := apiReq(t, app, "GET", "/api/admin/reports", nil, adminTok); st != 200 {
		t.Fatalf("GET /api/admin/reports -> %d", st)
	}
	if st, _, _ := apiReq(t, app, "PUT", "/api/admin/report/"+repID+"/resolve", map[string]string{"status": "resolved", "note": "Sudah dicek admin"}, adminTok); st != 200 {
		t.Fatalf("PUT /api/admin/report/:id/resolve -> %d", st)
	}

	// 16f. Uji Auto-Complete / Auto-Cancel Worker + OTP Reset Flow (dev mode)
	_, acBody, _ := apiReq(t, app, "POST", "/api/checkout", map[string]interface{}{
		"recipient_name": "Pembeli Satu", "shipping_address": "Jl. Merdeka No 10, Bandung",
		"courier_code": "JNE", "payment_method": "QRIS",
		"items": []map[string]interface{}{{"product_id": prodID, "quantity": 1, "options": map[string]string{"Warna": "Hitam"}}},
	}, buyerTok)
	acTxID := acBody["data"].(map[string]interface{})["transactions"].([]interface{})[0].(map[string]interface{})["id"].(string)
	acOID, _ := primitive.ObjectIDFromHex(acTxID)
	_, _ = db.Collection("transactions").UpdateOne(context.Background(), bson.M{"_id": acOID}, bson.M{"$set": bson.M{
		"confirm_by": primitive.NewDateTimeFromTime(time.Now().Add(-1 * time.Hour)),
	}})
	if processed := controller.AutoCompleteDueOrders(); processed < 1 {
		t.Fatalf("AutoCompleteDueOrders seharusnya membatalkan pesanan pending kadaluarsa, dapat %d", processed)
	}

	t.Setenv("OTP_DEV_LOG", "true")
	st, rsBody, raw := apiReq(t, app, "POST", "/api/auth/reset/start", map[string]string{"identifier": "buyer@test.vendora.id"}, "")
	if st != 200 {
		t.Fatalf("POST /api/auth/reset/start -> %d: %s", st, raw)
	}
	chID := rsBody["data"].(map[string]interface{})["challenge_id"].(string)
	if st, _, raw := apiReq(t, app, "POST", "/api/auth/otp/send", map[string]string{"challenge_id": chID, "channel": "email"}, ""); st != 200 {
		t.Fatalf("POST /api/auth/otp/send -> %d: %s", st, raw)
	}
	if st, _, _ := apiReq(t, app, "POST", "/api/auth/otp/verify", map[string]string{"challenge_id": chID, "code": "000000"}, ""); st != 401 {
		t.Fatalf("POST /api/auth/otp/verify wrong code -> %d, want 401", st)
	}

	// 16g. Delete Produk & Delete Ruko (setelah tidak ada pesanan aktif)
	if st, _, raw := apiReq(t, app, "DELETE", "/api/product/"+prodID, nil, sellerTok); st != 200 {
		t.Fatalf("DELETE /api/product/:id -> %d: %s", st, raw)
	}
	if st, _, raw := apiReq(t, app, "DELETE", "/api/shop/"+shopID, nil, adminTok); st != 200 {
		t.Fatalf("DELETE /api/shop/:id -> %d: %s", st, raw)
	}
}
