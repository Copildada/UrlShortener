package handlers

import (
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/joybabacopilot/url-shortener/internal/analytics"
	"github.com/joybabacopilot/url-shortener/internal/cache"
	"github.com/joybabacopilot/url-shortener/internal/models"
	"github.com/joybabacopilot/url-shortener/internal/storage"
)

// RedirectHandler handles URL redirects - the HOT PATH
// Must respond in single-digit milliseconds
type RedirectHandler struct {
	store       storage.Store
	cache       *cache.RedisCache
	processor   *analytics.Processor
}

// NewRedirectHandler creates a new redirect handler
func NewRedirectHandler(store storage.Store, cache *cache.RedisCache, processor *analytics.Processor) *RedirectHandler {
	return &RedirectHandler{
		store:     store,
		cache:     cache,
		processor: processor,
	}
}

// Redirect handles short code redirects
// Strategy: Cache-first to avoid database hits on the hot path
// Analytics are logged asynchronously to Redis queue
func (h *RedirectHandler) Redirect(c fiber.Ctx) error {
	shortCode := c.Params("shortCode")

	// 1. Try cache first (read-heavy - should hit here 99% of the time)
	mapping, err := h.cache.Get(c.Context(), shortCode)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "cache error",
		})
	}

	// 2. If cache miss, query database
	if mapping == nil {
		mapping, err = h.store.GetURLByShortCode(c.Context(), shortCode)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "URL not found",
			})
		}

		// 3. Cache the result for future hits
		if err := h.cache.Set(c.Context(), mapping); err != nil {
			// Log but don't fail - redirect still works
			c.App().Config().ErrorHandler(c, err)
		}
	}

	// 4. Record analytics asynchronously (non-blocking)
	// This is queued to an async processor to avoid blocking the redirect
	event := &models.AnalyticsEvent{
		ShortCode:   shortCode,
		Timestamp:   time.Now(),
		UserAgent:   c.Get("User-Agent"),
		RemoteAddr:  c.IP(),
		Referer:     c.Get("Referer"),
	}

	if err := h.processor.RecordClick(event); err != nil {
		// Log error but continue - redirect should not fail due to analytics
		c.App().Config().ErrorHandler(c, err)
	}

	// 5. Redirect to original URL (HTTP 301 Moved Permanently)
	c.Set("Location", mapping.OriginalURL)
	return c.SendStatus(fiber.StatusMovedPermanently)
}

// GetStats returns analytics stats for a short code
func (h *RedirectHandler) GetStats(c fiber.Ctx) error {
	shortCode := c.Params("shortCode")

	// Verify URL exists
	_, err := h.store.GetURLByShortCode(c.Context(), shortCode)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "URL not found",
		})
	}

	// Get analytics data
	stats, err := h.store.GetAnalytics(c.Context(), shortCode)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to fetch analytics",
		})
	}

	return c.Status(fiber.StatusOK).JSON(stats)
}
