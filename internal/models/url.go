package models

import "time"

// URLMapping represents a shortened URL and its metadata
type URLMapping struct {
	ID           int64      `json:"id"`
	ShortCode    string     `json:"short_code"`
	OriginalURL  string     `json:"original_url"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    *time.Time `json:"expires_at"`
	ClickCount   int64      `json:"click_count"`
	LastClickedAt *time.Time `json:"last_clicked_at"`
}

// CreateURLRequest represents the request to create a shortened URL
type CreateURLRequest struct {
	OriginalURL string     `json:"original_url" form:"original_url"`
	ExpiresAt   *time.Time `json:"expires_at" form:"expires_at"`
	CustomCode  string     `json:"custom_code" form:"custom_code"`
}

// AnalyticsEvent represents a click event to be processed asynchronously
type AnalyticsEvent struct {
	ShortCode    string    `json:"short_code"`
	Timestamp    time.Time `json:"timestamp"`
	UserAgent    string    `json:"user_agent"`
	RemoteAddr   string    `json:"remote_addr"`
	Referer      string    `json:"referer"`
}
