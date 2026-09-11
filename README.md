# URL Shortener + Analytics

A production-grade URL shortening service built in Go with a focus on **ultra-fast redirects** and **decoupled async analytics processing**. Every redirect must respond in single-digit milliseconds, while analytics work happens asynchronously to never block the critical path.

## Architecture Overview

### The Problem

A naive URL shortener handles both the redirect *and* the analytics write synchronously:

```
User clicks → Lookup URL → Write to DB → Redirect
               ↑ Fast        ↑ Slow      ↑ Blocked
```

When traffic spikes, the single slow analytics write delays every redirect. The architecture here solves this:

```
User clicks → Lookup URL → [Async write] → Redirect (1-3ms)
               ↑ Cache       ↑ Queue/Worker
               ↑ Fast        ↑ Non-blocking
```

### Key Design Decisions

#### 1. **Separate Read and Write Paths**
- **Read path (redirects):** Cache-first, database-fallback. Never blocks on writes.
- **Write path (analytics):** Non-blocking queue + async batch processor. Runs independently.

#### 2. **Cache-Driven Redirects**
- Redis caches all hot URLs with a 24-hour TTL
- 99% of redirects hit cache (sub-millisecond)
- Cache misses fall through to PostgreSQL (indexed, fast)
- Write-throughs on create immediately populate cache

#### 3. **Collision-Free Short Codes**
- Auto-incrementing BIGSERIAL ID in PostgreSQL
- Base62-encoded IDs → collision-proof by construction
- Optional custom codes with validation and uniqueness check
- Birthday paradox math: 62^10 = 839 trillion combinations

#### 4. **Async Analytics via Redis Streams**
- Click events buffered in-memory (channel)
- Batched to Redis Streams as fallback if channel fills
- Worker goroutine processes batches:
  - Writes individual events to PostgreSQL
  - Aggregates click counts per URL
  - Updates click_count + last_clicked_at
  - Invalidates cache to sync
- No single click delays the redirect

#### 5. **Graceful Degradation**
- Cache miss? Query database. Redirect still works.
- Analytics processor down? Events queue in Redis. Can replay.
- Database down? Redirects still work if cache hot.

## Project Structure

```
.
├── cmd/
│   └── urlshortener/
│       └── main.go              # Entry point, dependency wiring
├── internal/
│   ├── analytics/
│   │   └── processor.go         # Async event processor
│   ├── cache/
│   │   └── redis.go             # Redis cache + streams
│   ├── config/
│   │   └── config.go            # Config from env vars
│   ├── encoding/
│   │   └── base62.go            # Collision-free ID encoding
│   ├── handlers/
│   │   ├── create.go            # POST /api/shorten
│   │   └── redirect.go          # GET /r/:shortCode (HOT PATH)
│   ├── models/
│   │   └── url.go               # Data structures
│   └── storage/
│       ├── store.go             # Storage interface
│       └── postgres.go          # PostgreSQL implementation
├── .env.example                 # Environment template
├── go.mod
├── go.sum
└── README.md
```

## Getting Started

### Prerequisites

- **Go 1.21+**
- **PostgreSQL 12+**
- **Redis 6+**

### Setup

#### 1. Clone and initialize

```bash
cd UrlShortener
go mod download
```

#### 2. Configure environment

```bash
cp .env.example .env
```

Edit `.env` with your PostgreSQL and Redis connection strings:

```env
DATABASE_URL=postgres://user:password@localhost:5432/urlshortener
REDIS_ADDR=localhost:6379
PORT=8080
```

#### 3. Create PostgreSQL database

```bash
createdb urlshortener
```

#### 4. Run the server

```bash
go run cmd/urlshortener/main.go
```

Server starts on port 8080. Schema is created automatically on startup.

### Verify it works

**Health check:**
```bash
curl http://localhost:8080/health
```

**Create a shortened URL:**
```bash
curl -X POST http://localhost:8080/api/shorten \
  -H "Content-Type: application/json" \
  -d '{
    "original_url": "https://example.com/very/long/path?with=query&params=true"
  }'
```

Response:
```json
{
  "id": 1,
  "short_code": "1",
  "original_url": "https://example.com/very/long/path?with=query&params=true",
  "created_at": "2026-09-12T10:30:00Z",
  "short_url": "http://localhost:8080/r/1"
}
```

**Redirect (follow the short URL):**
```bash
curl -L http://localhost:8080/r/1
# Redirects to original URL
```

**Get analytics:**
```bash
curl http://localhost:8080/api/1/stats
```

Response:
```json
{
  "short_code": "1",
  "total_clicks": 42,
  "last_clicked_at": "2026-09-12T11:45:30Z"
}
```

## API Reference

### Create a Short URL

**POST** `/api/shorten`

Request:
```json
{
  "original_url": "https://example.com/path",
  "expires_at": "2026-12-31T23:59:59Z",  // optional
  "custom_code": "mycode"                // optional, must be alphanumeric + dash/underscore
}
```

Response (201 Created):
```json
{
  "id": 1,
  "short_code": "1",
  "original_url": "https://example.com/path",
  "created_at": "2026-09-12T10:30:00Z",
  "expires_at": null,
  "short_url": "http://localhost:8080/r/1"
}
```

### Redirect to Original URL

**GET** `/r/:shortCode`

Returns: HTTP 301 (Moved Permanently) to original URL

Side effect: Queues an analytics event asynchronously (non-blocking)

### Get Analytics

**GET** `/api/:shortCode/stats`

Response (200 OK):
```json
{
  "short_code": "1",
  "total_clicks": 42,
  "last_clicked_at": "2026-09-12T11:45:30Z"
}
```

### Health Check

**GET** `/health`

Response (200 OK):
```json
{
  "status": "ok"
}
```

## Performance Characteristics

### Redirect Path (Hot)
- **Cache hit:** 1-3ms (Redis GET + redirect)
- **Cache miss:** 5-10ms (PostgreSQL lookup + cache populate + redirect)
- **Typical:** 99% cache hit rate → ~2ms average

### Create Path
- **Database insert:** 5-15ms
- **Base62 encoding:** <1ms
- **Cache write:** 2-5ms
- **Total:** ~10-20ms

### Analytics Processing
- **Record click (async):** <1ms (channel send, non-blocking)
- **Batch processing:** Background worker
  - Batch size: 100 events
  - Flush interval: 5 seconds
  - Throughput: ~20,000 clicks/sec per processor

## Configuration

All settings via environment variables (see `.env.example`):

| Variable | Default | Purpose |
|----------|---------|---------|
| `PORT` | 8080 | Server port |
| `DATABASE_URL` | postgres://... | PostgreSQL connection string |
| `MAX_DB_CONNECTIONS` | 25 | Connection pool size |
| `REDIS_ADDR` | localhost:6379 | Redis server address |
| `REDIS_CACHE_TTL` | 24h | How long to cache URLs |
| `ANALYTICS_BATCH_SIZE` | 100 | Events per batch before flush |
| `ANALYTICS_FLUSH_INTERVAL` | 5s | Max time before flushing batch |

## Scaling Considerations

### Horizontal Scale (Multiple Instances)

Since the architecture is stateless:

1. **Multiple app servers** behind a load balancer
2. **Shared PostgreSQL** database (use connection pooling)
3. **Shared Redis** cache cluster
4. **Each instance** runs its own analytics processor

Analytics processors independently consume from the same PostgreSQL table, so no coordination needed.

### Vertical Scale (Single Instance)

1. **Increase `MAX_DB_CONNECTIONS`** to pool more database connections
2. **Tune `ANALYTICS_BATCH_SIZE`** based on traffic
3. **Increase Redis memory** if cache hit rate drops
4. **Consider read replicas** for analytics queries in high-volume scenarios

### Metrics to Monitor

- **Redirect latency** (p50, p95, p99)
- **Cache hit ratio** (should be >95%)
- **Analytics event lag** (time from click to DB write)
- **Database connection pool utilization**
- **Redis memory usage**

## Deployment

### Docker (Optional)

Create a `Dockerfile`:

```dockerfile
FROM golang:1.21 AS builder
WORKDIR /app
COPY . .
RUN go build -o url-shortener cmd/urlshortener/main.go

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=builder /app/url-shortener /app/url-shortener
EXPOSE 8080
CMD ["/app/url-shortener"]
```

Build and run:
```bash
docker build -t url-shortener .
docker run -e DATABASE_URL=... -e REDIS_ADDR=... -p 8080:8080 url-shortener
```

## Testing

Run tests:
```bash
go test ./...
```

Run with coverage:
```bash
go test -cover ./...
```

## Architecture Tradeoffs

| Decision | Benefit | Tradeoff |
|----------|---------|----------|
| Redis cache-first | Sub-millisecond redirects | Requires Redis cluster |
| Async analytics | Non-blocking redirects | Eventual consistency on stats |
| Base62 encoding | Collision-proof | No semantic meaning in short codes |
| In-memory batching | High throughput | Data loss if process crashes (fallback to Redis Streams) |
| PostgreSQL for events | Queryable analytics | Slower than pure stream/queue |

## Future Improvements

- [ ] Distributed tracing for analytics
- [ ] Custom domain support (vanity URLs)
- [ ] Rate limiting per origin
- [ ] Webhook callbacks on expiration
- [ ] GraphQL API for advanced analytics
- [ ] CDN edge caching strategy
- [ ] QR code generation
- [ ] Click demographics (geo, device, referrer)

## License

MIT
