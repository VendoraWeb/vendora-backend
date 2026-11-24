package url

import (
	"os"
	"strings"

	"vendora/controller"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
)

// Asal (origin) frontend yang boleh memanggil API. Bisa ditambah lewat env CORS_ORIGINS (pisahkan koma).
func allowedOriginSet() map[string]bool {
	set := map[string]bool{
		"https://vendorashop.my.id":             true,
		"https://www.vendorashop.my.id":         true,
		"https://vendora-frontend-nu.vercel.app": true,
	}
	if extra := strings.TrimSpace(os.Getenv("CORS_ORIGINS")); extra != "" {
		for _, item := range strings.Split(extra, ",") {
			if o := strings.TrimRight(strings.TrimSpace(item), "/"); o != "" {
				set[o] = true
			}
		}
	}
	return set
}

// Pengembangan lokal: frontend dari localhost / 127.0.0.1 di port mana pun.
func isLocalOrigin(origin string) bool {
	for _, p := range []string{"http://localhost:", "http://127.0.0.1:"} {
		if strings.HasPrefix(origin, p) {
			return true
		}
	}
	return origin == "http://localhost" || origin == "http://127.0.0.1"
}

func isAllowedOrigin(origin string) bool {
	o := strings.TrimRight(strings.TrimSpace(origin), "/")
	if o == "" {
		return false
	}
	return allowedOriginSet()[o] || isLocalOrigin(o)
}

func SetupRoutes(app *fiber.App) {
	controller.EnsureOTPIndexes()
	controller.EnsureChatIndexes()
	controller.EnsureSessionIndexes()

	// CORS satu pintu (main.go & api/index.go tidak memasang CORS sendiri)
	app.Use(cors.New(cors.Config{
		AllowOriginsFunc: isAllowedOrigin,
		AllowHeaders:     "Origin, Content-Type, Accept, Authorization",
		ExposeHeaders:    "Content-Disposition",
		AllowMethods:     "GET, POST, HEAD, PUT, PATCH, DELETE, OPTIONS",
	}))

	// Sesi login dibaca sekali per permintaan; rute di bawah menolak tamu lewat auth/admin/seller
	api := app.Group("/api", controller.AuthOptional)
	auth := controller.RequireAuth
	admin := controller.RequireAdmin
	seller := controller.RequireRole("seller", "admin")

	// ── Akun ──
	api.Post("/register", controller.Register)
	api.Post("/login", controller.Login) // email/nomor HP + kata sandi
	api.Post("/logout", controller.Logout)
	api.Get("/me", auth, controller.GetMe)
	api.Get("/user/me", auth, controller.GetMe)
	api.Post("/auth/otp/send", controller.SendOTP)
	api.Post("/auth/otp/verify", controller.VerifyOTP)
	api.Post("/auth/reset/start", controller.ResetStart)
	api.Post("/auth/reset/complete", controller.ResetComplete)
	api.Put("/user/profile", auth, controller.UpdateProfile)
	api.Get("/users", admin, controller.ListUsers) // data pribadi disamarkan (****)
	api.Put("/user/:id/ban", admin, controller.BanUser)

	// ── Notifikasi (lonceng): status pesanan, escrow, komplain, promo, masa sewa ruko ──
	api.Get("/notifications", auth, controller.ListNotifications)
	api.Post("/notifications", auth, controller.CreateNotification) // penjual: promo ke pembeli; admin: bebas
	api.Put("/notifications/read-all", auth, controller.MarkAllNotificationsRead)
	api.Put("/notifications/:id/read", auth, controller.MarkNotificationRead)

	// ── Ruko ──
	api.Get("/shops", controller.ListShops)
	api.Get("/shop/mine", auth, controller.MyShop)
	api.Get("/shop/:id", controller.GetShop)
	api.Get("/rent/quote", controller.RentQuote)
	api.Post("/shop/rent", seller, controller.RentShop)
	api.Put("/shop/:id", auth, controller.UpdateShop)
	api.Put("/shop/:id/renew", auth, controller.RenewShop) // pemilik ruko atau admin (dicek di handler)
	api.Put("/shop/:id/status", admin, controller.UpdateShopStatus)
	api.Delete("/shop/:id", admin, controller.DeleteShop)

	// ── Produk (mendukung promo_price, discount_percent, promo_label) ──
	api.Get("/products", controller.ListProducts)
	api.Get("/products/sold", controller.SoldCounts)
	api.Get("/product/:id", controller.GetProduct)
	api.Post("/product", seller, controller.AddProduct)
	api.Put("/product/:id", seller, controller.UpdateProduct)
	api.Delete("/product/:id", seller, controller.DeleteProduct) // admin atau pemilik ruko produk itu

	// ── Pesanan: checkout, escrow, kurir & pelacakan peta ──
	api.Get("/couriers", controller.ListCouriers)
	api.Post("/checkout", auth, controller.Checkout)
	api.Get("/transactions", auth, controller.ListTransactions)
	api.Get("/transaction/:id", auth, controller.GetTransaction)
	api.Put("/transaction/:id/status", auth, controller.UpdateTransactionStatus)
	api.Post("/transaction/:id/confirm-received", auth, controller.ConfirmOrderReceived)
	api.Get("/transaction/:id/track", auth, controller.GetOrderTracking)

	// Pembayaran real-time & dompet escrow penjual
	api.Get("/payment/:id/status", auth, controller.GetPaymentStatus)
	api.Post("/payment/:id/pay", auth, controller.PayTransaction)
	api.Get("/wallet/summary", auth, controller.GetWalletSummary)
	api.Post("/wallet/withdraw", auth, controller.WithdrawSellerBalance)

	// Komplain / retur (dana beku di escrow sampai admin memutuskan)
	api.Post("/disputes", auth, controller.CreateDispute)
	api.Get("/disputes", auth, controller.ListDisputes)
	api.Put("/dispute/:id/reply", auth, controller.ReplyDispute)
	api.Put("/dispute/:id/resolve", admin, controller.ResolveDispute)

	// Laporan pelanggaran produk / toko (boleh tanpa masuk)
	api.Post("/reports", controller.CreateReport)

	// ── Konsol admin: semua rute /api/admin/* wajib admin ──
	adminAPI := api.Group("/admin", admin)
	adminAPI.Get("/verify", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": 200, "message": "Admin session verified", "data": c.Locals("auth_user")})
	})
	adminAPI.Post("/transactions/cancel-stale", controller.CancelStalePendingOrders)
	adminAPI.Get("/transactions/export", controller.ExportTransactionsCSV)
	adminAPI.Get("/products", controller.ListAdminProducts)
	adminAPI.Put("/product/:id/visibility", controller.ModerateProductVisibility)
	adminAPI.Delete("/product/:id", controller.DeleteProduct)
	adminAPI.Put("/shop/:id/status", controller.UpdateShopStatus)
	adminAPI.Delete("/shop/:id", controller.DeleteShop)
	adminAPI.Get("/users", controller.ListUsers)
	adminAPI.Put("/user/:id/ban", controller.BanUser)
	adminAPI.Put("/dispute/:id/resolve", controller.ResolveDispute)
	adminAPI.Put("/ticket/:id/reply", controller.ReplyTicket)
	adminAPI.Get("/reports", controller.ListAdminReports)
	adminAPI.Put("/report/:id/resolve", controller.ResolveAdminReport)

	// ── Bantuan (tiket CS) ──
	api.Post("/tickets", auth, controller.CreateTicket)
	api.Get("/tickets", auth, controller.ListTickets)
	api.Put("/ticket/:id/reply", admin, controller.ReplyTicket)

	// ── Chat ──
	api.Post("/chat/send", auth, controller.SendMessage) // teks dan/atau foto
	api.Post("/chat/read", auth, controller.MarkChatRead)
	api.Get("/chat/unread", auth, controller.GetChatUnread)
	api.Get("/chat/media/:id", controller.GetChatMedia) // ID acak; <img> tidak bisa mengirim header login
	api.Get("/chat/history", auth, controller.GetChatHistory)
	api.Get("/chat/inbox", auth, controller.GetSellerInbox)
}
