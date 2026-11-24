package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"vendora/model"

	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func doJSON(t *testing.T, app *fiber.App, method, path string, body interface{}, token string) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := app.Test(req, 10000)
	if err != nil {
		t.Fatalf("app.Test %s %s: %v", method, path, err)
	}
	return resp
}

// ─── SKENARIO 01: Autentikasi, Validasi Registrasi & Middleware Sesi ─────────
func TestScenario01_AuthRegistrationAndSessionGuards(t *testing.T) {
	app := fiber.New()
	app.Post("/api/register", Register)
	app.Post("/api/login", Login)
	app.Post("/api/logout", Logout)
	app.Get("/api/me", RequireAuth, GetMe)
	app.Get("/api/seller-only", RequireRole("seller", "admin"), func(c *fiber.Ctx) error { return c.SendStatus(200) })
	app.Get("/api/admin-only", RequireAdmin, func(c *fiber.Ctx) error { return c.SendStatus(200) })

	badRegisters := []struct {
		name string
		body map[string]interface{}
	}{
		{"kolom wajib kosong", map[string]interface{}{"name": "", "email": "a@b.com", "password": "password123", "role": "buyer"}},
		{"format email salah", map[string]interface{}{"name": "Budi", "email": "bukan-email", "password": "password123", "role": "buyer"}},
		{"sandi kurang dari 8 karakter", map[string]interface{}{"name": "Budi", "email": "budi@vendora.com", "password": "123", "role": "buyer"}},
		{"pendaftaran role admin dari publik ditolak", map[string]interface{}{"name": "Hacker", "email": "h@vendora.com", "password": "password123", "role": "admin"}},
		{"nomor HP tidak valid", map[string]interface{}{"name": "Budi", "email": "budi@vendora.com", "password": "password123", "role": "buyer", "phone": "123"}},
	}
	for _, tc := range badRegisters {
		if resp := doJSON(t, app, "POST", "/api/register", tc.body, ""); resp.StatusCode != 400 {
			t.Errorf("%s: got %d, want 400", tc.name, resp.StatusCode)
		}
	}

	// Login tanpa kredensial → 400
	if resp := doJSON(t, app, "POST", "/api/login", map[string]string{"email": "", "password": ""}, ""); resp.StatusCode != 400 {
		t.Errorf("empty login got %d, want 400", resp.StatusCode)
	}

	// Logout tanpa token tetap 200
	if resp := doJSON(t, app, "POST", "/api/logout", nil, ""); resp.StatusCode != 200 {
		t.Errorf("logout got %d, want 200", resp.StatusCode)
	}

	// Token hash deterministik & aman
	h1 := hashSessionToken("vdr_sampletoken")
	h2 := hashSessionToken("vdr_sampletoken")
	if h1 != h2 || len(h1) != 64 {
		t.Errorf("hashSessionToken tidak konsisten: %q vs %q", h1, h2)
	}

	// Middleware role check dengan sesi injected di c.Locals
	roleApp := fiber.New()
	roleApp.Use(func(c *fiber.Ctx) error {
		role := c.Get("X-Test-Role")
		banned := c.Get("X-Test-Banned") == "1"
		if role != "" {
			c.Locals("authResolved", true)
			c.Locals("user", &model.User{ID: primitive.NewObjectID(), Name: "Tester", Role: role, Banned: banned})
		}
		return c.Next()
	})
	roleApp.Get("/auth", RequireAuth, func(c *fiber.Ctx) error { return c.SendStatus(200) })
	roleApp.Get("/seller", RequireRole("seller", "admin"), func(c *fiber.Ctx) error { return c.SendStatus(200) })
	roleApp.Get("/admin", RequireAdmin, func(c *fiber.Ctx) error { return c.SendStatus(200) })

	checkRole := func(path, role string, banned bool, want int) {
		req := httptest.NewRequest("GET", path, nil)
		if role != "" {
			req.Header.Set("X-Test-Role", role)
		}
		if banned {
			req.Header.Set("X-Test-Banned", "1")
		}
		r, _ := roleApp.Test(req)
		if r.StatusCode != want {
			t.Errorf("%s role=%q banned=%v -> %d, want %d", path, role, banned, r.StatusCode, want)
		}
	}
	checkRole("/auth", "", false, 401)
	checkRole("/auth", "buyer", true, 403)
	checkRole("/auth", "buyer", false, 200)
	checkRole("/seller", "buyer", false, 403)
	checkRole("/seller", "seller", false, 200)
	checkRole("/admin", "seller", false, 403)
	checkRole("/admin", "admin", false, 200)
}

// ─── SKENARIO 02: CRUD Ruko — Tarif Sewa, Status Efektif & Validasi ──────────
func TestScenario02_ShopRentalAndStatusLogic(t *testing.T) {
	app := fiber.New()
	app.Get("/api/rent/quote", RentQuote)
	app.Get("/api/shop/:id", GetShop)

	// Tarif sewa 30 hari = 30 * 15000 = 450000
	resp := doJSON(t, app, "GET", "/api/rent/quote?days=30", nil, "")
	if resp.StatusCode != 200 {
		t.Fatalf("GET /api/rent/quote?days=30 -> %d", resp.StatusCode)
	}
	if q := buildQuote(90); q.Total != 1350000 || q.DailyRate != dailyRent {
		t.Errorf("buildQuote(90) = %+v", q)
	}
	if resp := doJSON(t, app, "GET", "/api/rent/quote?days=0", nil, ""); resp.StatusCode != 400 {
		t.Errorf("days=0 got %d, want 400", resp.StatusCode)
	}
	if resp := doJSON(t, app, "GET", "/api/rent/quote?days=400", nil, ""); resp.StatusCode != 400 {
		t.Errorf("days=400 got %d, want 400", resp.StatusCode)
	}

	// Status efektif ruko & sellBlockReason
	future := primitive.NewDateTimeFromTime(time.Now().Add(48 * time.Hour))
	past := primitive.NewDateTimeFromTime(time.Now().Add(-48 * time.Hour))
	activeShop := model.Shop{Status: "active", RentalExpires: future}
	expiredShop := model.Shop{Status: "active", RentalExpires: past}
	pendingShop := model.Shop{Status: "pending", RentalExpires: future}
	suspendedShop := model.Shop{Status: "suspended", RentalExpires: future}
	legacyEmpty := model.Shop{Status: "", RentalExpires: future}

	if EffectiveShopStatus(activeShop) != "active" || sellBlockReason(&activeShop) != "" {
		t.Error("ruko aktif seharusnya boleh berjualan")
	}
	if EffectiveShopStatus(expiredShop) != "expired" || !strings.Contains(sellBlockReason(&expiredShop), "habis") {
		t.Error("ruko lewat jatuh tempo harus berstatus expired & diblokir")
	}
	if EffectiveShopStatus(pendingShop) != "pending" || !strings.Contains(sellBlockReason(&pendingShop), "verifikasi") {
		t.Error("ruko pending harus menunggu verifikasi")
	}
	if EffectiveShopStatus(suspendedShop) != "suspended" || !strings.Contains(sellBlockReason(&suspendedShop), "ditangguhkan") {
		t.Error("ruko suspended harus ditolak")
	}
	if EffectiveShopStatus(legacyEmpty) != "active" {
		t.Error("ruko status kosong legacy harus fallback ke active")
	}

	// Helper kota pengiriman (shipFrom) & kepemilikan
	if got := shipFrom("Jl. Sarijadi No. 10, Sukasari, Kota Bandung, Jawa Barat"); got != "Kota Bandung, Jawa Barat" {
		t.Errorf("shipFrom = %q", got)
	}
	if got := shipFrom("HanyaSatuBagian"); got != "" {
		t.Errorf("shipFrom single part = %q, want empty", got)
	}
	ownerID := primitive.NewObjectID()
	owned := &model.Shop{OwnerID: ownerID}
	if !isShopOwner(owned, ownerID) || isShopOwner(owned, primitive.NewObjectID()) || isShopOwner(nil, ownerID) {
		t.Error("isShopOwner logic tidak sesuai")
	}
}

// ─── SKENARIO 03: CRUD Produk — Variasi Multi-Grup & Proteksi Harga Negatif ──
func TestScenario03_ProductOptionsAndVariantPricing(t *testing.T) {
	// Celah multi-grup delta negatif harus ditolak!
	exploitGroups := []model.OptionGroup{
		{Name: "Grup 1", Values: []model.OptionValue{{Label: "A", PriceDelta: -60000}}},
		{Name: "Grup 2", Values: []model.OptionValue{{Label: "B", PriceDelta: -60000}}},
	}
	if _, msg := cleanOptions(exploitGroups, 100000); msg == "" {
		t.Error("akumulasi potongan harga antar grup yang membuat harga <= 0 harus ditolak")
	}

	// Maksimal 3 grup variasi
	tooManyGroups := []model.OptionGroup{
		{Name: "G1", Values: []model.OptionValue{{Label: "A"}}},
		{Name: "G2", Values: []model.OptionValue{{Label: "B"}}},
		{Name: "G3", Values: []model.OptionValue{{Label: "C"}}},
		{Name: "G4", Values: []model.OptionValue{{Label: "D"}}},
	}
	if _, msg := cleanOptions(tooManyGroups, 100000); msg == "" {
		t.Error(">3 grup variasi seharusnya ditolak")
	}

	// Harga promo + delta variasi negatif yang menghasilkan <= 0 ditolak oleh resolveVariant
	promoProd := model.Product{
		Name:       "Jaket Promo",
		Price:      500000,
		PromoPrice: 100000,
		Options: []model.OptionGroup{
			{Name: "Paket", Values: []model.OptionValue{{Label: "Hemat", PriceDelta: -120000}}},
		},
	}
	if _, _, _, msg := resolveVariant(promoProd, map[string]string{"Paket": "Hemat"}); msg == "" {
		t.Error("resolveVariant seharusnya menolak harga akhir <= 0 saat digabung dengan PromoPrice")
	}
}

// ─── SKENARIO 04: Checkout, Kurir, Koordinat Peta & Perhitungan Subtotal ─────
func TestScenario04_CheckoutCouriersAndSubtotalMath(t *testing.T) {
	app := fiber.New()
	app.Get("/api/couriers", ListCouriers)
	if resp := doJSON(t, app, "GET", "/api/couriers", nil, ""); resp.StatusCode != 200 {
		t.Fatalf("GET /api/couriers -> %d", resp.StatusCode)
	}

	if c := findCourier("instant"); c.Code != "INSTANT" || c.Cost != 35000 {
		t.Errorf("findCourier(instant) = %+v", c)
	}
	if c := findCourier("UNKNOWN"); c.Code != "JNE" {
		t.Errorf("findCourier(UNKNOWN) should fallback to JNE, got %+v", c)
	}

	cities := map[string]string{
		"Jl. Tunjungan Surabaya":    "Surabaya",
		"Malioboro Jogja":           "Yogyakarta",
		"Simpang Lima Semarang":     "Semarang",
		"Pajajaran Bogor":           "Bogor",
		"Margonda Depok":            "Depok",
		"BSD Tangerang":             "Tangerang",
		"Summarecon Bekasi":         "Bekasi",
		"Sudirman Jakarta":          "Jakarta Selatan",
		"Alamat Tanpa Kota Spesial": "Bandung",
	}
	for addr, wantCity := range cities {
		if loc := inferDestinationCoords(addr); loc.City != wantCity {
			t.Errorf("inferDestinationCoords(%q) = %q, want %q", addr, loc.City, wantCity)
		}
	}

	txID := primitive.NewObjectID()
	buyerID := primitive.NewObjectID()
	for method, prefix := range map[string]string{
		"VA_BCA":     "88010",
		"VA_MANDIRI": "89022",
		"CARD":       "AUTH-3DS-",
		"COD":        "VDR-PAY-",
		"QRIS":       "00020101021226590014ID.VENDORA.WWW",
	} {
		tok, code := generatePaymentSecurity(txID, buyerID, 250000, method)
		if len(tok) != 32 || !strings.HasPrefix(code, prefix) {
			t.Errorf("generatePaymentSecurity(%s) -> tok=%q code=%q", method, tok, code)
		}
	}

	// ComputeOrderSubtotal: 3 cabang (field eksplisit, jumlah item, total - ongkir)
	if got := ComputeOrderSubtotal(model.Transaction{SubtotalAmount: 500000}); got != 500000 {
		t.Errorf("explicit subtotal = %.0f", got)
	}
	if got := ComputeOrderSubtotal(model.Transaction{Items: []model.TransactionItem{{Price: 120000, Quantity: 2}, {Price: 50000, Quantity: 1}}}); got != 290000 {
		t.Errorf("items sum subtotal = %.0f", got)
	}
	if got := ComputeOrderSubtotal(model.Transaction{TotalAmount: 218000, ShippingCost: 18000}); got != 200000 {
		t.Errorf("total-shipping subtotal = %.0f", got)
	}
}

// ─── SKENARIO 05: Matriks Transisi Escrow & Timeline Tracking Kurir ──────────
func TestScenario05_EscrowTransitionsAndTrackingTimeline(t *testing.T) {
	// Pembuktian keamanan kritis: seller DILARANG mengubah pending_payment → paid
	if transitionAllowed("seller", "pending_payment", "paid") {
		t.Fatal("SECURITY REGRESSION: seller tidak boleh memaksa pesanan belum dibayar (pending_payment) menjadi paid!")
	}
	// Alur normal seller & buyer
	if !transitionAllowed("seller", "pending", "paid") || !transitionAllowed("seller", "paid", "shipped") {
		t.Error("alur normal seller pending → paid → shipped harus diizinkan")
	}
	if !transitionAllowed("buyer", "shipped", "success") || !transitionAllowed("buyer", "pending", "cancelled") {
		t.Error("alur normal buyer confirm-received & cancel harus diizinkan")
	}
	// Status terminal & disputed terkunci bagi buyer & seller
	for _, role := range []string{"buyer", "seller"} {
		for _, from := range []string{"success", "cancelled", "disputed"} {
			if transitionAllowed(role, from, "paid") || transitionAllowed(role, from, "cancelled") {
				t.Errorf("%s tidak boleh mengubah status terminal %s", role, from)
			}
		}
	}
	if !transitionAllowed("admin", "disputed", "cancelled") {
		t.Error("admin harus bisa memproses status")
	}

	// Timeline tracking untuk seluruh status
	baseTx := model.Transaction{
		ID:              primitive.NewObjectID(),
		CourierCode:     "JNE",
		CourierService:  "JNE Reguler",
		TrackingNumber:  "JNE-12345678",
		ShippingAddress: "Jl. Braga, Bandung",
	}
	for _, st := range []string{"paid", "shipped", "success"} {
		events, loc := buildTrackingStateForStatus(baseTx, st)
		if len(events) == 0 || loc == nil {
			t.Errorf("buildTrackingStateForStatus(%s) harus menghasilkan timeline & lokasi", st)
		}
	}
	for _, st := range []string{"pending", "disputed", "cancelled"} {
		_, loc := buildTrackingStateForStatus(baseTx, st)
		if loc == nil {
			t.Errorf("buildTrackingStateForStatus(%s) harus tetap mengembalikan lokasi origin", st)
		}
	}

	// Durasi auto-complete & confirm-by (env override & default)
	t.Setenv("AUTO_COMPLETE_HOURS", "120")
	if got := AutoCompleteDuration(); got != 120*time.Hour {
		t.Errorf("AutoCompleteDuration = %v, want 120h", got)
	}
	t.Setenv("PENDING_CONFIRM_TIMEOUT_HOURS", "12")
	if got := PendingConfirmDuration(); got != 12*time.Hour {
		t.Errorf("PendingConfirmDuration = %v, want 12h", got)
	}
}

// ─── SKENARIO 06: Validasi Komplain (Dispute) & Pertahanan XSS Bukti ─────────
func TestScenario06_DisputeValidationAndXSSDefense(t *testing.T) {
	oid := primitive.NewObjectID()
	if s := shortTxID(oid); len(s) != 6 || s != strings.ToUpper(oid.Hex()[len(oid.Hex())-6:]) {
		t.Errorf("shortTxID = %q", s)
	}
	if len(disputeReasons) < 4 {
		t.Error("disputeReasons harus berisi minimal 4 pilihan alasan")
	}

	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("authResolved", true)
		c.Locals("user", &model.User{ID: primitive.NewObjectID(), Role: c.Get("X-Role")})
		return c.Next()
	})
	app.Post("/api/disputes", CreateDispute)
	app.Put("/api/dispute/:id/reply", ReplyDispute)
	app.Put("/api/dispute/:id/resolve", ResolveDispute)

	// ID transaksi tidak valid → 400
	req := httptest.NewRequest("POST", "/api/disputes", strings.NewReader(`{"transaction_id":"invalid","reason":"damaged","description":"Barang rusak berat sekali"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Role", "buyer")
	if r, _ := app.Test(req); r.StatusCode != 400 {
		t.Errorf("CreateDispute invalid tx id -> %d, want 400", r.StatusCode)
	}

	// Deskripsi < 10 karakter → 400
	req2 := httptest.NewRequest("POST", "/api/disputes", strings.NewReader(`{"transaction_id":"`+oid.Hex()+`","reason":"damaged","description":"rusak"}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Role", "buyer")
	if r, _ := app.Test(req2); r.StatusCode != 400 {
		t.Errorf("CreateDispute short desc -> %d, want 400", r.StatusCode)
	}

	// ResolveDispute oleh non-admin → 403
	req3 := httptest.NewRequest("PUT", "/api/dispute/"+oid.Hex()+"/resolve", strings.NewReader(`{"decision":"refund","note":"ok"}`))
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("X-Role", "seller")
	if r, _ := app.Test(req3); r.StatusCode != 403 {
		t.Errorf("ResolveDispute non-admin -> %d, want 403", r.StatusCode)
	}

	// ResolveDispute dengan decision salah → 400
	req4 := httptest.NewRequest("PUT", "/api/dispute/"+oid.Hex()+"/resolve", strings.NewReader(`{"decision":"hack","note":"ok"}`))
	req4.Header.Set("Content-Type", "application/json")
	req4.Header.Set("X-Role", "admin")
	if r, _ := app.Test(req4); r.StatusCode != 400 {
		t.Errorf("ResolveDispute bad decision -> %d, want 400", r.StatusCode)
	}
}

// ─── SKENARIO 07: Notifikasi & Sanitasi Tautan Anti-XSS ──────────────────────
func TestScenario07_NotificationLinkSanitizationAndGuards(t *testing.T) {
	safe := map[string]string{
		"pesanan.html":                       "pesanan.html",
		"seller.html#ruko":                   "seller.html#ruko",
		"https://www.vendorashop.my.id/help": "https://www.vendorashop.my.id/help",
	}
	for in, want := range safe {
		if got := safeNotifLink(in); got != want {
			t.Errorf("safeNotifLink(%q) = %q, want %q", in, got, want)
		}
	}
	unsafe := []string{
		"",
		"javascript:alert(1)",
		"JAVASCRIPT:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"//evil.com/phish",
		`https://evil.com/" onclick="alert(1)`,
		"http://insecure.example.com",
	}
	for _, in := range unsafe {
		if got := safeNotifLink(in); got != "" {
			t.Errorf("safeNotifLink(%q) seharusnya diblokir, dapat %q", in, got)
		}
	}
}

// ─── SKENARIO 08: Chat — Hak Akses Percakapan & Filter Percakapan ────────────
func TestScenario08_ChatAccessControl(t *testing.T) {
	buyerID := primitive.NewObjectID()
	ownerID := primitive.NewObjectID()
	adminID := primitive.NewObjectID()
	strangerID := primitive.NewObjectID()
	shop := &model.Shop{ID: primitive.NewObjectID(), OwnerID: ownerID}

	if !canSeeConversation(&model.User{ID: buyerID, Role: "buyer"}, shop, buyerID) {
		t.Error("pembeli harus boleh melihat percakapannya sendiri")
	}
	if !canSeeConversation(&model.User{ID: ownerID, Role: "seller"}, shop, buyerID) {
		t.Error("pemilik ruko harus boleh melihat percakapan di rukonya")
	}
	if !canSeeConversation(&model.User{ID: adminID, Role: "admin"}, shop, buyerID) {
		t.Error("admin harus boleh melihat percakapan untuk moderasi")
	}
	if canSeeConversation(&model.User{ID: strangerID, Role: "buyer"}, shop, buyerID) {
		t.Error("pihak luar DILARANG melihat percakapan orang lain")
	}

	flt := conversationFilter(shop.ID, buyerID)
	if flt["shop_id"] != shop.ID {
		t.Errorf("conversationFilter shop_id mismatch: %+v", flt)
	}
}

// ─── SKENARIO 09: Tiket Bantuan & Validasi Balasan Kosong ────────────────────
func TestScenario09_TicketReplyValidation(t *testing.T) {
	app := fiber.New()
	app.Put("/api/ticket/:id/reply", ReplyTicket)

	// Balasan berisi spasi saja harus ditolak 400
	oid := primitive.NewObjectID().Hex()
	if resp := doJSON(t, app, "PUT", "/api/ticket/"+oid+"/reply", map[string]string{"reply": "   "}, ""); resp.StatusCode != 400 {
		t.Errorf("whitespace reply got %d, want 400", resp.StatusCode)
	}
	if resp := doJSON(t, app, "PUT", "/api/ticket/not-an-id/reply", map[string]string{"reply": "Halo"}, ""); resp.StatusCode != 400 {
		t.Errorf("bad ticket id got %d, want 400", resp.StatusCode)
	}
}

// ─── SKENARIO 10: Validasi OTP & Reset Sandi ─────────────────────────────────
func TestScenario10_OTPAndResetValidation(t *testing.T) {
	app := fiber.New()
	app.Post("/api/auth/otp/send", SendOTP)
	app.Post("/api/auth/otp/verify", VerifyOTP)
	app.Post("/api/auth/reset/start", ResetStart)
	app.Post("/api/auth/reset/complete", ResetComplete)

	if resp := doJSON(t, app, "POST", "/api/auth/otp/send", map[string]string{"challenge_id": "bad", "channel": "sms"}, ""); resp.StatusCode != 400 {
		t.Errorf("SendOTP bad input got %d, want 400", resp.StatusCode)
	}
	if resp := doJSON(t, app, "POST", "/api/auth/otp/verify", map[string]string{"challenge_id": "bad", "code": "12"}, ""); resp.StatusCode != 400 {
		t.Errorf("VerifyOTP bad code got %d, want 400", resp.StatusCode)
	}
	if resp := doJSON(t, app, "POST", "/api/auth/reset/start", map[string]string{"identifier": ""}, ""); resp.StatusCode != 400 {
		t.Errorf("ResetStart empty got %d, want 400", resp.StatusCode)
	}
	if resp := doJSON(t, app, "POST", "/api/auth/reset/complete", map[string]string{"challenge_id": "abc", "reset_token": "tok", "new_password": "short"}, ""); resp.StatusCode != 400 {
		t.Errorf("ResetComplete short password got %d, want 400", resp.StatusCode)
	}
}
