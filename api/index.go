package api

import (
	"net/http"

	"vendora/config"
	"vendora/url"

	"github.com/gofiber/adaptor/v2"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
)

var handler http.HandlerFunc

func init() {
	config.ConnectDB()

	app := fiber.New(fiber.Config{
		AppName: "Vendora E-Commerce Engine v1.0",
	})

	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowHeaders: "Origin, Content-Type, Accept, Authorization",
		AllowMethods: "GET, POST, PUT, DELETE, OPTIONS, HEAD, PATCH",
	}))

	// Setup routes exactly as main.go does
	url.SetupRoutes(app)

	// Ping route for Vercel health check
	app.Get("/api/ping", func(c *fiber.Ctx) error {
		return c.SendString("Vendora Vercel Backend is running 24/7!")
	})

	// Adapt fiber app to http.HandlerFunc
	handler = adaptor.FiberApp(app)
}

// Handler is the entrypoint for Vercel Serverless Function
func Handler(w http.ResponseWriter, r *http.Request) {
	// Reconstruct the RequestURI to ensure Fiber adaptor doesn't lose the query string on Vercel
	if r.URL.RawQuery != "" {
		r.RequestURI = r.URL.Path + "?" + r.URL.RawQuery
	} else {
		r.RequestURI = r.URL.Path
	}
	handler(w, r)
}
