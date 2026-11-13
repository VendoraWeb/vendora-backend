package url

import (
	"vendora/controller"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
)

func SetupRoutes(app *fiber.App) {
	controller.EnsureOTPIndexes()
	controller.EnsureChatIndexes()

	// Enable CORS for frontend access (satu pintu di SetupRoutes)
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowHeaders: "Origin, Content-Type, Accept, Authorization",
		AllowMethods: "GET, POST, HEAD, PUT, PATCH, DELETE, OPTIONS",
	}))

	api := app.Group("/api")

	// Auth & User routes
	api.Post("/register", controller.Register)
	api.Post("/login", controller.Login) // email/nomor HP + kata sandi

	// OTP (login & reset kata sandi)
	api.Post("/auth/otp/send", controller.SendOTP)
	api.Post("/auth/otp/verify", controller.VerifyOTP)
	api.Post("/auth/reset/start", controller.ResetStart)
	api.Post("/auth/reset/complete", controller.ResetComplete)
	api.Put("/user/profile", controller.UpdateProfile)
	api.Get("/user/me", controller.GetMe)
	api.Get("/users", controller.RequireAdmin, controller.ListUsers) // khusus Admin & otomatis di-masking (****)
	api.Put("/user/:id/ban", controller.RequireAdmin, controller.BanUser)

	// Shop routes (sewa ruko)
	api.Post("/shop/rent", controller.RentShop)
	api.Get("/shops", controller.ListShops)
	api.Put("/shop/:id/status", controller.RequireAdmin, controller.UpdateShopStatus)
	api.Put("/shop/:id/renew", controller.RenewShop)
	api.Delete("/shop/:id", controller.RequireAdmin, controller.DeleteShop)

	// Product routes (mendukung promo_price, discount_percent, promo_label)
	api.Post("/product", controller.AddProduct)
	api.Get("/products", controller.ListProducts)
	api.Get("/product/:id", controller.GetProduct)
	api.Put("/product/:id", controller.UpdateProduct)
	api.Delete("/product/:id", controller.DeleteProduct) // dicek di handler: Admin atau Seller pemilik produk

	// Checkout, Real-Time Payment, Escrow, & Tracking Kurir/Peta
	api.Get("/couriers", controller.ListCouriers)
	api.Post("/checkout", controller.Checkout)
	api.Get("/transactions", controller.ListTransactions)
	api.Put("/transaction/:id/status", controller.UpdateTransactionStatus)
	api.Post("/transaction/:id/confirm-received", controller.ConfirmOrderReceived)
	api.Get("/transaction/:id/track", controller.GetOrderTracking)

	// Semua endpoint di bawah /api/admin/* wajib melewati middleware RequireAdmin
	adminAPI := api.Group("/admin", controller.RequireAdmin)
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

	// Laporan pelanggaran produk / toko dari pembeli
	api.Post("/reports", controller.CreateReport)

	// Komplain / Retur (dana beku di Escrow sampai admin memutuskan)
	api.Post("/disputes", controller.CreateDispute)
	api.Get("/disputes", controller.ListDisputes)
	api.Put("/dispute/:id/reply", controller.ReplyDispute)
	api.Put("/dispute/:id/resolve", controller.RequireAdmin, controller.ResolveDispute) // khusus Admin (keputusan sengketa)

	// Real-Time Payment Verification & Escrow Wallet
	api.Get("/payment/:id/status", controller.GetPaymentStatus)
	api.Post("/payment/:id/pay", controller.PayTransaction)
	api.Get("/wallet/summary", controller.GetWalletSummary)
	api.Post("/wallet/withdraw", controller.WithdrawSellerBalance)

	// In-App Notifications (Promo, Status Pesanan, Saldo Cair)
	api.Get("/notifications", controller.ListNotifications)
	api.Post("/notifications", controller.CreateNotification)
	api.Put("/notifications/read-all", controller.MarkAllNotificationsRead)
	api.Put("/notifications/:id/read", controller.MarkNotificationRead)

	// Ticket / CS Support routes
	api.Post("/tickets", controller.CreateTicket)
	api.Get("/tickets", controller.ListTickets)
	api.Put("/ticket/:id/reply", controller.RequireAdmin, controller.ReplyTicket) // khusus Admin (balas tiket CS)

	// Chat routes
	api.Post("/chat/send", controller.SendMessage) // teks dan/atau foto
	api.Post("/chat/read", controller.MarkChatRead)
	api.Get("/chat/unread", controller.GetChatUnread)
	api.Get("/chat/media/:id", controller.GetChatMedia)
	api.Get("/chat/history", controller.GetChatHistory)
	api.Get("/chat/inbox", controller.GetSellerInbox)
}
