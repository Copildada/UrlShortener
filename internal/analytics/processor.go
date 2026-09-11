package analytics

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/joybabacopilot/url-shortener/internal/cache"
	"github.com/joybabacopilot/url-shortener/internal/models"
	"github.com/joybabacopilot/url-shortener/internal/storage"
)

// Processor handles asynchronous analytics event processing
// Decouples the redirect request from database updates to keep redirects fast
type Processor struct {
	store              storage.Store
	cache              *cache.RedisCache
	analyticsStreamKey string
	batchSize          int
	batchFlushInterval time.Duration
	events             chan *models.AnalyticsEvent
	done               chan struct{}
	wg                 sync.WaitGroup
}

// NewProcessor creates a new analytics processor
func NewProcessor(store storage.Store, cache *cache.RedisCache, streamKey string, batchSize int, flushInterval time.Duration) *Processor {
	return &Processor{
		store:              store,
		cache:              cache,
		analyticsStreamKey: streamKey,
		batchSize:          batchSize,
		batchFlushInterval: flushInterval,
		events:             make(chan *models.AnalyticsEvent, batchSize*2), // Buffer 2x batch size
		done:               make(chan struct{}),
	}
}

// Start begins the async analytics processor
func (p *Processor) Start(ctx context.Context) {
	p.wg.Add(1)
	go p.processLoop(ctx)
	log.Println("Analytics processor started")
}

// Stop gracefully shuts down the processor, processing any remaining events
func (p *Processor) Stop(ctx context.Context) {
	log.Println("Analytics processor shutting down...")
	close(p.events)

	// Wait for the processor to finish or context to cancel
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("Analytics processor stopped gracefully")
	case <-ctx.Done():
		log.Println("Analytics processor shutdown context expired")
	}
}

// RecordClick records a click event asynchronously
// This is called from the redirect handler and returns immediately
func (p *Processor) RecordClick(event *models.AnalyticsEvent) error {
	select {
	case p.events <- event:
		return nil
	case <-p.done:
		return fmt.Errorf("processor is shutting down")
	default:
		// Channel is full, push to Redis Stream as fallback
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return p.cache.PushAnalyticsEvent(ctx, p.analyticsStreamKey, event)
	}
}

// processLoop is the main event processing loop
func (p *Processor) processLoop(ctx context.Context) {
	defer p.wg.Done()

	batch := make([]*models.AnalyticsEvent, 0, p.batchSize)
	ticker := time.NewTicker(p.batchFlushInterval)
	defer ticker.Stop()

	for {
		select {
		case event, ok := <-p.events:
			if !ok {
				// Channel closed - flush remaining events and exit
				if len(batch) > 0 {
					p.processBatch(ctx, batch)
				}
				close(p.done)
				return
			}

			batch = append(batch, event)

			// If batch is full, process it immediately
			if len(batch) >= p.batchSize {
				p.processBatch(ctx, batch)
				batch = make([]*models.AnalyticsEvent, 0, p.batchSize)
				// Reset ticker
				ticker.Stop()
				ticker = time.NewTicker(p.batchFlushInterval)
			}

		case <-ticker.C:
			// Flush batch on timeout, even if not full
			if len(batch) > 0 {
				p.processBatch(ctx, batch)
				batch = make([]*models.AnalyticsEvent, 0, p.batchSize)
			}

		case <-ctx.Done():
			// Context cancelled - flush and exit
			if len(batch) > 0 {
				p.processBatch(ctx, batch)
			}
			close(p.done)
			return
		}
	}
}

// processBatch processes a batch of analytics events
func (p *Processor) processBatch(ctx context.Context, events []*models.AnalyticsEvent) {
	if len(events) == 0 {
		return
	}

	// Group events by short code for efficient batch updates
	clickCounts := make(map[string]int64)
	for _, event := range events {
		// Record individual event
		if err := p.store.RecordAnalytics(ctx, event); err != nil {
			log.Printf("Failed to record analytics event for %s: %v", event.ShortCode, err)
			continue
		}

		// Count clicks per URL
		clickCounts[event.ShortCode]++
	}

	// Batch update click counts
	for shortCode, count := range clickCounts {
		// Update click count (this could be optimized with a batch update in the store)
		for i := int64(0); i < count; i++ {
			if err := p.store.IncrementClickCount(ctx, shortCode); err != nil {
				log.Printf("Failed to increment click count for %s: %v", shortCode, err)
			}
		}

		// Invalidate cache to ensure fresh data on next request
		if err := p.cache.Invalidate(ctx, shortCode); err != nil {
			log.Printf("Failed to invalidate cache for %s: %v", shortCode, err)
		}
	}

	log.Printf("Processed batch of %d analytics events", len(events))
}
