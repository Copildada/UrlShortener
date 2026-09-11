package storage

import (
	"context"

	"github.com/joybabacopilot/url-shortener/internal/models"
)

// Store defines the interface for URL storage operations
type Store interface {
	// SaveURL creates a new URL mapping
	SaveURL(ctx context.Context, req *models.CreateURLRequest) (*models.URLMapping, error)

	// GetURLByShortCode retrieves a URL by its short code (HOT PATH)
	GetURLByShortCode(ctx context.Context, shortCode string) (*models.URLMapping, error)

	// RecordAnalytics logs an analytics event
	RecordAnalytics(ctx context.Context, event *models.AnalyticsEvent) error

	// IncrementClickCount increments the click count for a URL
	IncrementClickCount(ctx context.Context, shortCode string) error

	// GetAnalytics retrieves analytics data for a URL
	GetAnalytics(ctx context.Context, shortCode string) (map[string]interface{}, error)

	// CreateSchema initializes the database schema
	CreateSchema(ctx context.Context) error

	// Close closes the database connection
	Close() error
}
