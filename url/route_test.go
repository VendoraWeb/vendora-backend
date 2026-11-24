package url

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestCORSAllowedOrigins(t *testing.T) {
	t.Setenv("CORS_ORIGINS", "https://staging.vendorashop.my.id/ , , https://preview.vendora.id,")

	allowed := []string{
		"https://vendorashop.my.id",
		"https://www.vendorashop.my.id",
		"https://vendora-frontend-nu.vercel.app",
		"https://staging.vendorashop.my.id",
		"https://preview.vendora.id",
		"http://localhost",
		"http://127.0.0.1",
		"http://localhost:5500",
		"http://127.0.0.1:8080",
	}
	for _, o := range allowed {
		if !isAllowedOrigin(o) {
			t.Errorf("origin %q seharusnya diizinkan", o)
		}
	}

	blocked := []string{
		"",
		"https://evil.example.com",
		"http://vendorashop.my.id.evil.com",
		"javascript:alert(1)",
	}
	for _, o := range blocked {
		if isAllowedOrigin(o) {
			t.Errorf("origin %q seharusnya ditolak", o)
		}
	}
}

func TestSetupRoutesPublicAndAuthGuards(t *testing.T) {
	app := fiber.New()
	SetupRoutes(app)

	// 1. CORS Preflight + Expose-Headers untuk ekspor CSV
	pre := httptest.NewRequest("OPTIONS", "/api/admin/transactions/export", nil)
	pre.Header.Set("Origin", "https://www.vendorashop.my.id")
	pre.Header.Set("Access-Control-Request-Method", "GET")
	resp, err := app.Test(pre)
	if err != nil || resp.StatusCode != 204 {
		t.Fatalf("preflight status=%d err=%v", resp.StatusCode, err)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://www.vendorashop.my.id" {
		t.Errorf("unexpected Allow-Origin: %q", got)
	}

	// 2. Endpoint publik tetap terbuka tanpa login
	for _, path := range []string{"/api/couriers", "/api/rent/quote?days=30"} {
		r, err := app.Test(httptest.NewRequest("GET", path, nil))
		if err != nil || r.StatusCode != 200 {
			t.Errorf("GET %s = %d (err=%v), want 200", path, r.StatusCode, err)
		}
	}

	// 3. Seluruh endpoint privat wajib menolak tamu dengan 401 Unauthorized
	protected := []struct {
		method string
		path   string
	}{
		{"GET", "/api/me"},
		{"GET", "/api/shop/mine"},
		{"PUT", "/api/shop/507f1f77bcf86cd799439011"},
		{"POST", "/api/product"},
		{"POST", "/api/checkout"},
		{"GET", "/api/transactions"},
		{"GET", "/api/transaction/507f1f77bcf86cd799439011"},
		{"GET", "/api/wallet/summary"},
		{"GET", "/api/disputes"},
		{"GET", "/api/tickets"},
		{"GET", "/api/notifications"},
		{"GET", "/api/admin/verify"},
		{"GET", "/api/admin/products"},
		{"GET", "/api/admin/reports"},
		{"GET", "/api/chat/unread"},
	}
	for _, ep := range protected {
		r, err := app.Test(httptest.NewRequest(ep.method, ep.path, nil))
		if err != nil || r.StatusCode != 401 {
			t.Errorf("%s %s without token = %d (err=%v), want 401", ep.method, ep.path, r.StatusCode, err)
		}
	}
}
