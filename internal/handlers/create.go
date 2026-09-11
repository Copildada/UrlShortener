package handlers

import (
	"net/url"

	"github.com/gofiber/fiber/v3"

	"github.com/joybabacopilot/url-shortener/internal/cache"
	"github.com/joybabacopilot/url-shortener/internal/models"
	"github.com/joybabacopilot/url-shortener/internal/storage"
)

// CreateHandler handles URL shortening requests
type CreateHandler struct {
	store storage.Store
	cache *cache.RedisCache
}

// NewCreateHandler creates a new URL creation handler
func NewCreateHandler(store storage.Store, cache *cache.RedisCache) *CreateHandler {
	return &CreateHandler{
		store: store,
		cache: cache,
	}
}

// Create handles POST requests to create a shortened URL
func (h *CreateHandler) Create(c fiber.Ctx) error {
	var req models.CreateURLRequest

	// Parse request body
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	// Validate original URL
	if req.OriginalURL == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "original_url is required",
		})
	}

	// Ensure it's a valid URL
	if _, err := url.Parse(req.OriginalURL); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid URL format",
		})
	}

	// If custom code provided, validate it
	if req.CustomCode != "" && !isValidShortCode(req.CustomCode) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid custom code format",
		})
	}

	// Save URL to database
	mapping, err := h.store.SaveURL(c.Context(), &req)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to create shortened URL",
		})
	}

	// Cache the new mapping for future hits
	if err := h.cache.Set(c.Context(), mapping); err != nil {
		// Log but don't fail - the URL is already saved
		c.App().Config().ErrorHandler(c, err)
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":            mapping.ID,
		"short_code":    mapping.ShortCode,
		"original_url":  mapping.OriginalURL,
		"created_at":    mapping.CreatedAt,
		"expires_at":    mapping.ExpiresAt,
		"short_url":     c.BaseURL() + "/r/" + mapping.ShortCode,
	})
}

// isValidShortCode validates a custom short code
// Allows alphanumeric characters and some special chars
func isValidShortCode(code string) bool {
	if len(code) == 0 || len(code) > 20 {
		return false
	}

	for _, ch := range code {
		if !((ch >= '0' && ch <= '9') ||
			(ch >= 'a' && ch <= 'z') ||
			(ch >= 'A' && ch <= 'Z') ||
			ch == '-' || ch == '_') {
			return false
		}
	}

	return true
}
