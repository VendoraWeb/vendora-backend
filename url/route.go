package url

import (
	"vendora/controller"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
)

func SetupRoutes(app *fiber.App) {
	// Enable CORS for frontend access
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowHeaders: "Origin, Content-Type, Accept, Authorization",
		AllowMethods: "GET, POST, PUT, DELETE, OPTIONS",
	}))

	api := app.Group("/api")

	// Auth & User routes
	api.Post("/register", controller.Register)
	api.Post("/login", controller.Login)
	api.Put("/user/profile", controller.UpdateProfile)
	api.Get("/users", controller.ListUsers)
	api.Put("/user/:id/ban", controller.BanUser)

	// Shop routes (sewa ruko)
	api.Post("/shop/rent", controller.RentShop)
	api.Get("/shops", controller.ListShops)
	api.Put("/shop/:id/status", controller.UpdateShopStatus)
	api.Put("/shop/:id/renew", controller.RenewShop)

	// Product routes
	api.Post("/product", controller.AddProduct)
	api.Get("/products", controller.ListProducts)
	api.Get("/product/:id", controller.GetProduct)
	api.Put("/product/:id", controller.UpdateProduct)
	api.Delete("/product/:id", controller.DeleteProduct)

	// Checkout / Transactions routes
	api.Post("/checkout", controller.Checkout)
	api.Get("/transactions", controller.ListTransactions)
	api.Post("/fix-tx", controller.FixTransactionStatus)

	// Ticket / CS Support routes
	api.Post("/tickets", controller.CreateTicket)
	api.Get("/tickets", controller.ListTickets)
	api.Put("/ticket/:id/reply", controller.ReplyTicket)
}
