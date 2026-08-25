package main

import (
	"log"
	"os"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/proxy"
	"github.com/google/uuid"
)

func main() {
	// Initialize Fiber app
	app := fiber.New(fiber.Config{
		AppName: "Vertex BFF v1.0",
	})

	// Middleware
	// Extract or generate Request ID
	app.Use(func(c *fiber.Ctx) error {
		reqID := c.Get("X-Request-Id")
		if reqID == "" {
			reqID = uuid.New().String()
			c.Request().Header.Set("X-Request-Id", reqID)
		}
		c.Set("X-Request-Id", reqID)
		return c.Next()
	})

	// JSON Logger for ELK stack
	app.Use(logger.New(logger.Config{
		Format: `{"time":"${time}","level":"info","method":"${method}","path":"${path}","status":${status},"latency":"${latency}","request_id":"${header:X-Request-Id}","source_system":"${header:X-Source-System}","device_id":"${header:X-Device-Id}","ip":"${ip}"}` + "\n",
		TimeFormat: "2006-01-02T15:04:05.999Z07:00",
	}))

	// Health check
	app.Get("/", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status":  "success",
			"message": "Welcome to Vertex BFF API",
		})
	})

	// Proxy routes to Microservices
	petServiceURL := os.Getenv("PET_SERVICE_URL")
	if petServiceURL == "" {
		petServiceURL = "http://localhost:8081" // Fallback for local testing without docker
	}

	app.All("/api/pets", func(c *fiber.Ctx) error {
		return proxy.Do(c, petServiceURL+"/api/v1/pets")
	})
	app.All("/api/pets/*", func(c *fiber.Ctx) error {
		return proxy.Do(c, petServiceURL+"/api/v1/pets/"+c.Params("*"))
	})
	
	// Master Data Proxy
	app.All("/api/master-data/*", func(c *fiber.Ctx) error {
		return proxy.Do(c, petServiceURL+"/api/v1/master-data/"+c.Params("*"))
	})

	// Auth Proxy
	authServiceURL := os.Getenv("AUTH_SERVICE_URL")
	if authServiceURL == "" {
		authServiceURL = "http://localhost:4000"
	}
	app.All("/api/auth/*", func(c *fiber.Ctx) error {
		return proxy.Do(c, authServiceURL+"/api/v1/auth/"+c.Params("*"))
	})

	// Start server on port 3000
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	log.Fatal(app.Listen(":" + port))
}
