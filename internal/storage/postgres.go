package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/lib/pq"

	"github.com/joybabacopilot/url-shortener/internal/encoding"
	"github.com/joybabacopilot/url-shortener/internal/models"
)

// PostgresStore handles all database operations for URL mappings
type PostgresStore struct {
	db      *sql.DB
	encoder *encoding.Base62
}

// NewPostgresStore creates a new PostgreSQL store
func NewPostgresStore(connStr string, maxConnections int) (*PostgresStore, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(maxConnections)
	db.SetMaxIdleConns(maxConnections / 2)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Verify connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	return &PostgresStore{
		db:      db,
		encoder: encoding.NewBase62(),
	}, nil
}

// CreateSchema creates the necessary tables if they don't exist
func (ps *PostgresStore) CreateSchema(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS url_mappings (
		id BIGSERIAL PRIMARY KEY,
		short_code VARCHAR(20) UNIQUE NOT NULL,
		original_url TEXT NOT NULL,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		expires_at TIMESTAMP,
		click_count BIGINT NOT NULL DEFAULT 0,
		last_clicked_at TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_short_code ON url_mappings(short_code);
	CREATE INDEX IF NOT EXISTS idx_expires_at ON url_mappings(expires_at);

	CREATE TABLE IF NOT EXISTS analytics_events (
		id BIGSERIAL PRIMARY KEY,
		short_code VARCHAR(20) NOT NULL,
		timestamp TIMESTAMP NOT NULL,
		user_agent TEXT,
		remote_addr VARCHAR(50),
		referer TEXT,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_analytics_short_code ON analytics_events(short_code);
	CREATE INDEX IF NOT EXISTS idx_analytics_timestamp ON analytics_events(timestamp);
	`

	_, err := ps.db.ExecContext(ctx, schema)
	return err
}

// SaveURL saves a new URL mapping to the database
func (ps *PostgresStore) SaveURL(ctx context.Context, req *models.CreateURLRequest) (*models.URLMapping, error) {
	var id int64
	var shortCode string

	// If custom code is provided, use it; otherwise generate one
	if req.CustomCode != "" {
		shortCode = req.CustomCode
	} else {
		// We'll use an insert + get approach to avoid race conditions
		// Insert and get the auto-generated ID
		err := ps.db.QueryRowContext(ctx,
			`INSERT INTO url_mappings (original_url, expires_at)
			 VALUES ($1, $2)
			 RETURNING id`,
			req.OriginalURL,
			req.ExpiresAt,
		).Scan(&id)

		if err != nil {
			return nil, fmt.Errorf("failed to insert URL: %w", err)
		}

		// Generate short code from ID using Base62
		shortCode = ps.encoder.Encode(id)

		// Update with generated short code
		_, err = ps.db.ExecContext(ctx,
			`UPDATE url_mappings SET short_code = $1 WHERE id = $2`,
			shortCode,
			id,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to update short code: %w", err)
		}
	}

	// Fetch and return the complete mapping
	return ps.GetURLByShortCode(ctx, shortCode)
}

// GetURLByShortCode retrieves a URL mapping by its short code (HOT PATH - heavily cached)
func (ps *PostgresStore) GetURLByShortCode(ctx context.Context, shortCode string) (*models.URLMapping, error) {
	var mapping models.URLMapping

	err := ps.db.QueryRowContext(ctx,
		`SELECT id, short_code, original_url, created_at, expires_at, click_count, last_clicked_at
		 FROM url_mappings
		 WHERE short_code = $1`,
		shortCode,
	).Scan(
		&mapping.ID,
		&mapping.ShortCode,
		&mapping.OriginalURL,
		&mapping.CreatedAt,
		&mapping.ExpiresAt,
		&mapping.ClickCount,
		&mapping.LastClickedAt,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("URL not found: %w", err)
		}
		return nil, fmt.Errorf("failed to query URL: %w", err)
	}

	// Check if URL has expired
	if mapping.ExpiresAt != nil && time.Now().After(*mapping.ExpiresAt) {
		return nil, fmt.Errorf("URL has expired")
	}

	return &mapping, nil
}

// RecordAnalytics logs an analytics event to the database (ASYNC PATH)
func (ps *PostgresStore) RecordAnalytics(ctx context.Context, event *models.AnalyticsEvent) error {
	_, err := ps.db.ExecContext(ctx,
		`INSERT INTO analytics_events (short_code, timestamp, user_agent, remote_addr, referer)
		 VALUES ($1, $2, $3, $4, $5)`,
		event.ShortCode,
		event.Timestamp,
		event.UserAgent,
		event.RemoteAddr,
		event.Referer,
	)
	if err != nil {
		return fmt.Errorf("failed to record analytics: %w", err)
	}

	return nil
}

// IncrementClickCount increments the click count for a URL (BATCH UPDATE - async)
func (ps *PostgresStore) IncrementClickCount(ctx context.Context, shortCode string) error {
	result, err := ps.db.ExecContext(ctx,
		`UPDATE url_mappings
		 SET click_count = click_count + 1, last_clicked_at = CURRENT_TIMESTAMP
		 WHERE short_code = $1`,
		shortCode,
	)
	if err != nil {
		return fmt.Errorf("failed to increment click count: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("URL not found for click count increment")
	}

	return nil
}

// GetAnalytics retrieves analytics data for a URL
func (ps *PostgresStore) GetAnalytics(ctx context.Context, shortCode string) (map[string]interface{}, error) {
	var totalClicks int64
	var lastClickedAt sql.NullTime

	err := ps.db.QueryRowContext(ctx,
		`SELECT click_count, last_clicked_at FROM url_mappings WHERE short_code = $1`,
		shortCode,
	).Scan(&totalClicks, &lastClickedAt)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("URL not found")
		}
		return nil, fmt.Errorf("failed to query analytics: %w", err)
	}

	analytics := map[string]interface{}{
		"short_code":      shortCode,
		"total_clicks":    totalClicks,
		"last_clicked_at": lastClickedAt.Time,
	}

	return analytics, nil
}

// Close closes the database connection
func (ps *PostgresStore) Close() error {
	return ps.db.Close()
}
