package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/joybabacopilot/url-shortener/internal/analytics"
	"github.com/joybabacopilot/url-shortener/internal/cache"
	"github.com/joybabacopilot/url-shortener/internal/config"
	"github.com/joybabacopilot/url-shortener/internal/handlers"
	"github.com/joybabacopilot/url-shortener/internal/storage"

	"github.com/gofiber/fiber/v3"
)

func main() {
	// Load environment variables from .env file if it exists
	_ = godotenv.Load()

	// Load configuration
	cfg := config.Load()

	// Initialize dependencies
	log.Println("Initializing URL shortener...")

	// 1. Initialize PostgreSQL store
	store, err := storage.NewPostgresStore(cfg.DatabaseURL, cfg.MaxDBConnections)
	if err != nil {
		log.Fatalf("Failed to initialize store: %v", err)
	}
	defer store.Close()

	// Create schema
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := store.CreateSchema(ctx); err != nil {
		log.Fatalf("Failed to create schema: %v", err)
	}
	cancel()

	// 2. Initialize Redis cache
	redisCache, err := cache.NewRedisCache(
		cfg.RedisAddr,
		cfg.RedisPassword,
		cfg.RedisDB,
		cfg.RedisCacheTTL,
	)
	if err != nil {
		log.Fatalf("Failed to initialize cache: %v", err)
	}
	defer redisCache.Close()

	// 3. Initialize async analytics processor
	processor := analytics.NewProcessor(
		store,
		redisCache,
		cfg.AnalyticsStreamKey,
		cfg.BatchSize,
		cfg.BatchFlushInterval,
	)

	// Start the processor
	processor.Start(context.Background())

	// 4. Initialize handlers
	redirectHandler := handlers.NewRedirectHandler(store, redisCache, processor)
	createHandler := handlers.NewCreateHandler(store, redisCache)

	// 5. Create Fiber app
	app := fiber.New(fiber.Config{
		AppName:      "URL Shortener",
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	})

	// 6. Register routes
	// Health check endpoint
	app.Get("/health", func(c fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// API endpoints
	app.Post("/api/shorten", createHandler.Create)
	app.Get("/api/:shortCode/stats", redirectHandler.GetStats)

	// Redirect endpoint (HOT PATH - must be fast)
	app.Get("/r/:shortCode", redirectHandler.Redirect)

	// 7. Setup graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan

		log.Println("Shutdown signal received")

		// Graceful shutdown
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Stop analytics processor
		processor.Stop(shutdownCtx)

		// Shutdown server
		if err := app.Shutdown(); err != nil {
			log.Printf("Error shutting down server: %v", err)
		}
	}()

	// 8. Start server
	log.Printf("Starting server on port %s", cfg.Port)
	if err := app.Listen(":" + cfg.Port); err != nil && err.Error() != "http: Server closed" {
		log.Fatalf("Server error: %v", err)
	}

	log.Println("Server stopped")
}
