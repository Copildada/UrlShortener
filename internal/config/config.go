package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds application configuration
type Config struct {
	// Server config
	Port              string
	Env               string
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration

	// Database config
	DatabaseURL       string
	MaxDBConnections  int
	DBConnMaxLifetime time.Duration

	// Redis config
	RedisAddr          string
	RedisPassword      string
	RedisDB            int
	RedisCacheTTL      time.Duration

	// Analytics config
	AnalyticsStreamKey string
	BatchSize          int
	BatchFlushInterval time.Duration
}

// Load loads configuration from environment variables
func Load() *Config {
	return &Config{
		// Server
		Port:         getEnv("PORT", "8080"),
		Env:          getEnv("ENV", "development"),
		ReadTimeout:  getDurationEnv("READ_TIMEOUT", 10*time.Second),
		WriteTimeout: getDurationEnv("WRITE_TIMEOUT", 10*time.Second),

		// Database
		DatabaseURL:       getEnv("DATABASE_URL", "postgres://user:password@localhost:5432/urlshortener"),
		MaxDBConnections:  getIntEnv("MAX_DB_CONNECTIONS", 25),
		DBConnMaxLifetime: getDurationEnv("DB_CONN_MAX_LIFETIME", 5*time.Minute),

		// Redis
		RedisAddr:     getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		RedisDB:       getIntEnv("REDIS_DB", 0),
		RedisCacheTTL: getDurationEnv("REDIS_CACHE_TTL", 24*time.Hour),

		// Analytics
		AnalyticsStreamKey: getEnv("ANALYTICS_STREAM_KEY", "url:clicks"),
		BatchSize:         getIntEnv("ANALYTICS_BATCH_SIZE", 100),
		BatchFlushInterval: getDurationEnv("ANALYTICS_FLUSH_INTERVAL", 5*time.Second),
	}
}

// Helper functions to read environment variables with defaults
func getEnv(key, defaultVal string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultVal
}

func getIntEnv(key string, defaultVal int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultVal
}

func getDurationEnv(key string, defaultVal time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultVal
}
