# URL Shortener Architecture

## System Design

This document details the architectural decisions and implementation strategy for the URL shortener service.

### Core Problem Statement

**A naive URL shortener blocks redirects on analytics writes.** When traffic spikes, the slowest dependency (database analytics insert) becomes the bottleneck and delays every redirect.

```
NAIVE ARCHITECTURE (BLOCKED PATH):
┌─────────────────────────────────────────────────────────────┐
│ Request: GET /r/abc123                                      │
├─────────────────────────────────────────────────────────────┤
│ 1. Query cache/database for URL mapping    (~2-5ms)         │
│ 2. Record click to database                (~20-50ms) ← SLOW│
│ 3. Increment click counter in database     (~10-20ms) ← SLOW│
│ 4. Return HTTP 301 redirect                (~1ms)           │
├─────────────────────────────────────────────────────────────┤
│ TOTAL LATENCY: 33-75ms (blocked!)                           │
└─────────────────────────────────────────────────────────────┘

Problem: Under load, all redirects wait for database writes
Cost: Every analytics event delays the next user's redirect
```

## Solution: Decouple Paths

```
OPTIMIZED ARCHITECTURE (ASYNC ANALYTICS):
┌──────────────────────────────────┐        ┌──────────────────────┐
│ REQUEST PATH (HOT)               │        │ WORKER PATH (COLD)   │
├──────────────────────────────────┤        ├──────────────────────┤
│ 1. Redis cache lookup   (~1ms)   │        │ Process batch        │
│ 2. Queue analytics      (<1ms)   │───────→│ - Write events       │
│ 3. Return redirect      (~1ms)   │        │ - Update counters    │
├──────────────────────────────────┤        │ - Invalidate cache   │
│ TOTAL: 2-3ms per redirect        │        └──────────────────────┘
└──────────────────────────────────┘
        (non-blocking)                    (background, batched)

Key insight: Redirect doesn't wait for analytics processing
Cost: Zero impact on user-facing latency
Trade-off: Stats are eventually consistent (5-10s lag)
```

## Component Architecture

### 1. Request Routing (Fiber)

**File:** `cmd/urlshortener/main.go`

Registers HTTP routes with Fiber.

### 2. Redirect Handler (HOT PATH)

**File:** `internal/handlers/redirect.go`

The most performance-critical component. Strategy:
1. Extract shortCode from URL
2. Try Redis cache (99% hit rate expected)
3. Queue analytics event (non-blocking)
4. Return HTTP 301 to client

**Latency targets:**
- Cache hit: 1-3ms
- Cache miss: 5-10ms
- Analytics queue: <1ms

### 3. Analytics Processor (COLD PATH)

**File:** `internal/analytics/processor.go`

Background worker that batches and processes analytics:
- Accumulate events in memory
- Flush every 100 events OR 5 seconds
- Write to PostgreSQL
- Invalidate cache to sync

**Throughput:** ~20,000 clicks/sec

### 4. Caching Layer (Redis)

**File:** `internal/cache/redis.go`

Two-part Redis usage:
- URL Lookup Cache: Popular URLs cached for 24 hours
- Analytics Queue: Fallback stream for backpressure handling

### 5. Storage Layer (PostgreSQL)

**File:** `internal/storage/postgres.go`

Two separate tables:
- `url_mappings`: For redirect lookups (indexed on short_code)
- `analytics_events`: For individual click records (indexed on timestamp)

This separation prevents read-write contention.

### 6. ID Generation (Base62 Encoding)

**File:** `internal/encoding/base62.go`

Collision-free short code generation:
- Auto-increment BIGSERIAL ID in PostgreSQL
- Base62 encode the ID
- 62^6 = 56 trillion unique codes available
- No collision probability

## Performance Characteristics

### Latency Budget (Redirect)

```
Cache hit path: 1-3ms
- Network + Fiber routing: <1ms
- Redis lookup: 1-2ms
- Analytics queue: <1ms
- Response encoding: <1ms

Cache miss path: 5-10ms
- PostgreSQL query: 3-5ms
- JSON parsing: <1ms
- Redis cache write: 2-3ms
```

### Throughput

Single instance:
- Redirect (cache hit): 5000-10,000 req/s
- Redirect (cache miss): 1000-2000 req/s
- Analytics processing: 20,000 events/s

## Failure Modes & Recovery

### Redis Cache Down
- Redirects slower but working (fall through to DB)
- No data loss (cache is read-through)

### Analytics Processor Crashes
- Clicks buffer in memory and Redis Streams
- Can replay from Redis on restart
- Redirects unaffected

### PostgreSQL Slow/Down
- Cache hits stay fast (2ms)
- Cache misses timeout gracefully
- Analytics queue backs up but doesn't block redirects

### Traffic Spike (100x normal)
- Cache hit rate stays >95%
- Only 5% of spike hits database
- Response time maintained at 2-3ms
- Graceful degradation, no errors

## Deployment

### Single Instance (Development)
- Fiber on localhost:8080
- PostgreSQL local or remote
- Redis local or remote

### Horizontal Scale (Production)
- Multiple instances behind load balancer
- Shared PostgreSQL database
- Shared Redis cluster
- Each instance runs independent analytics processor

## Configuration

Key environment variables:

| Variable | Default | Purpose |
|----------|---------|---------|
| `PORT` | 8080 | Server port |
| `DATABASE_URL` | postgres://... | PostgreSQL connection |
| `MAX_DB_CONNECTIONS` | 25 | Connection pool size |
| `REDIS_ADDR` | localhost:6379 | Redis server |
| `ANALYTICS_BATCH_SIZE` | 100 | Events per batch |
| `ANALYTICS_FLUSH_INTERVAL` | 5s | Max time before flush |

## Monitoring & Metrics

Key metrics to monitor:

1. **Redirect Latency** (p50, p95, p99)
   - Target: p99 < 10ms
   - Alert if > 20ms

2. **Cache Hit Ratio**
   - Target: > 95%
   - Alert if < 80%

3. **Analytics Event Lag**
   - Target: < 10s
   - Alert if > 30s

4. **Error Rate**
   - Target: < 0.1%
   - Alert if > 1%

5. **Database Connection Pool**
   - Target: < 70% usage
   - Alert if > 90%

## Architecture Tradeoffs

| Decision | Benefit | Tradeoff |
|----------|---------|----------|
| Redis cache-first | Sub-millisecond redirects | Requires Redis cluster |
| Async analytics | Non-blocking redirects | Stats eventually consistent (5-10s lag) |
| Base62 encoding | Collision-proof | No semantic meaning in short codes |
| Batching | High throughput | Potential data loss on crash (mitigated by Redis queue) |
| Separate tables | No read-write contention | Slightly more complex schema |
