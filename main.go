package main

import (
	"fmt"
	"log"
	"os"

	"vendora/config"
	"vendora/url"

	"github.com/gofiber/fiber/v2"
)

func main() {
	// Initialize database
	config.ConnectDB()

	app := fiber.New(fiber.Config{
		AppName: "Vendora E-Commerce Engine v1.0",
	})

	// Setup routes
	url.SetupRoutes(app)

	// Ping route for checking health
	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendString("Vendora E-Commerce Engine backend is running!")
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Printf("Backend starting on port %s...\n", port)
	log.Fatal(app.Listen(":" + port))
}
