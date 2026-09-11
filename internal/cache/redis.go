package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/joybabacopilot/url-shortener/internal/models"
)

// RedisCache provides caching for URL lookups using Redis
type RedisCache struct {
	client *redis.Client
	ttl    time.Duration
}

// NewRedisCache creates a new Redis cache instance
func NewRedisCache(addr, password string, db int, ttl time.Duration) (*RedisCache, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &RedisCache{
		client: client,
		ttl:    ttl,
	}, nil
}

// Get retrieves a cached URL mapping by short code
func (rc *RedisCache) Get(ctx context.Context, shortCode string) (*models.URLMapping, error) {
	key := cacheKey(shortCode)
	val, err := rc.client.Get(ctx, key).Result()

	if err != nil {
		if err == redis.Nil {
			return nil, nil // Cache miss - not an error
		}
		return nil, fmt.Errorf("failed to get from cache: %w", err)
	}

	var mapping models.URLMapping
	if err := json.Unmarshal([]byte(val), &mapping); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cached URL: %w", err)
	}

	return &mapping, nil
}

// Set stores a URL mapping in the cache
func (rc *RedisCache) Set(ctx context.Context, mapping *models.URLMapping) error {
	key := cacheKey(mapping.ShortCode)
	data, err := json.Marshal(mapping)
	if err != nil {
		return fmt.Errorf("failed to marshal URL for cache: %w", err)
	}

	if err := rc.client.Set(ctx, key, data, rc.ttl).Err(); err != nil {
		return fmt.Errorf("failed to set cache: %w", err)
	}

	return nil
}

// Invalidate removes a URL mapping from the cache
func (rc *RedisCache) Invalidate(ctx context.Context, shortCode string) error {
	key := cacheKey(shortCode)
	if err := rc.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("failed to invalidate cache: %w", err)
	}
	return nil
}

// GetAnalyticsQueue retrieves pending analytics events from a Redis Stream
// This is used for async analytics processing
func (rc *RedisCache) GetAnalyticsQueue(ctx context.Context, streamKey string, count int64) ([]map[string]interface{}, error) {
	// Use XREAD to get events from the stream
	results, err := rc.client.XRead(ctx, &redis.XReadArgs{
		Streams: []string{streamKey, "0"},
		Count:   count,
	}).Result()

	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("failed to read analytics queue: %w", err)
	}

	var events []map[string]interface{}
	if len(results) > 0 {
		for _, message := range results[0].Messages {
			events = append(events, message.Values)
		}
	}

	return events, nil
}

// PushAnalyticsEvent adds an analytics event to the Redis Stream (async queue)
func (rc *RedisCache) PushAnalyticsEvent(ctx context.Context, streamKey string, event *models.AnalyticsEvent) error {
	data := map[string]interface{}{
		"short_code":   event.ShortCode,
		"timestamp":    event.Timestamp.Unix(),
		"user_agent":   event.UserAgent,
		"remote_addr":  event.RemoteAddr,
		"referer":      event.Referer,
	}

	if err := rc.client.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		Values: data,
	}).Err(); err != nil {
		return fmt.Errorf("failed to push analytics event: %w", err)
	}

	return nil
}

// TrimAnalyticsStream removes old events from the stream (housekeeping)
func (rc *RedisCache) TrimAnalyticsStream(ctx context.Context, streamKey string, maxLen int64) error {
	if err := rc.client.XTrimMaxLen(ctx, streamKey, maxLen).Err(); err != nil {
		return fmt.Errorf("failed to trim analytics stream: %w", err)
	}
	return nil
}

// Close closes the Redis connection
func (rc *RedisCache) Close() error {
	return rc.client.Close()
}

// cacheKey generates a consistent cache key for a short code
func cacheKey(shortCode string) string {
	return fmt.Sprintf("url:%s", shortCode)
}
