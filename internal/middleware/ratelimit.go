// Package middleware provides HTTP middleware for Alexander Storage.
package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/prn-tf/alexander-storage/internal/metrics"
)

// RateLimiter implements token bucket rate limiting.
type RateLimiter struct {
	// Configuration
	requestsPerSecond float64
	burstSize         int
	enabled           bool

	// Per-client buckets
	buckets sync.Map // map[string]*bucket

	// Metrics
	metrics *metrics.Metrics
	logger  zerolog.Logger

	// Cleanup
	cleanupInterval time.Duration
	stopCleanup     chan struct{}
}

// bucket represents a token bucket for a single client.
type bucket struct {
	tokens     float64
	lastRefill time.Time
	mu         sync.Mutex
}

// RateLimiterConfig holds rate limiter configuration.
type RateLimiterConfig struct {
	// RequestsPerSecond is the rate of token refill.
	RequestsPerSecond float64

	// BurstSize is the maximum number of tokens (burst capacity).
	BurstSize int

	// Enabled determines if rate limiting is active.
	Enabled bool

	// CleanupInterval is how often to clean up stale buckets.
	CleanupInterval time.Duration
}

// DefaultRateLimiterConfig returns sensible defaults.
func DefaultRateLimiterConfig() RateLimiterConfig {
	return RateLimiterConfig{
		RequestsPerSecond: 100,
		BurstSize:         200,
		Enabled:           true,
		CleanupInterval:   5 * time.Minute,
	}
}

// NewRateLimiter creates a new rate limiter.
func NewRateLimiter(config RateLimiterConfig, m *metrics.Metrics, logger zerolog.Logger) *RateLimiter {
	rl := &RateLimiter{
		requestsPerSecond: config.RequestsPerSecond,
		burstSize:         config.BurstSize,
		enabled:           config.Enabled,
		metrics:           m,
		logger:            logger.With().Str("component", "ratelimiter").Logger(),
		cleanupInterval:   config.CleanupInterval,
		stopCleanup:       make(chan struct{}),
	}

	if config.Enabled {
		go rl.cleanupLoop()
	}

	return rl
}

// Middleware returns the rate limiting middleware.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.enabled {
			next.ServeHTTP(w, r)
			return
		}

		// Get client identifier (IP address or access key)
		clientID := rl.getClientID(r)

		// Check rate limit
		if !rl.allow(clientID) {
			rl.logger.Warn().
				Str("client_id", clientID).
				Str("path", r.URL.Path).
				Msg("Rate limit exceeded")

			if rl.metrics != nil {
				rl.metrics.RecordRateLimited("request")
			}

			w.Header().Set("Content-Type", "application/xml")
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<Error>
    <Code>SlowDown</Code>
    <Message>Please reduce your request rate.</Message>
</Error>`))
			return
		}

		next.ServeHTTP(w, r)
	})
}

// getClientID extracts the client identifier from the request.
func (rl *RateLimiter) getClientID(r *http.Request) string {
	// Try to get X-Forwarded-For header first (for proxied requests)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}

	// Fall back to remote address
	return r.RemoteAddr
}

// allow checks if a request is allowed under the rate limit.
func (rl *RateLimiter) allow(clientID string) bool {
	b := rl.getBucket(clientID)

	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()

	// Refill tokens based on time elapsed
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens += elapsed * rl.requestsPerSecond

	// Cap at burst size
	if b.tokens > float64(rl.burstSize) {
		b.tokens = float64(rl.burstSize)
	}

	b.lastRefill = now

	// Check if we have at least 1 token
	if b.tokens >= 1 {
		b.tokens--
		return true
	}

	return false
}

// getBucket gets or creates a bucket for the client.
func (rl *RateLimiter) getBucket(clientID string) *bucket {
	if b, ok := rl.buckets.Load(clientID); ok {
		return b.(*bucket)
	}

	// Create new bucket with full tokens
	b := &bucket{
		tokens:     float64(rl.burstSize),
		lastRefill: time.Now(),
	}

	actual, _ := rl.buckets.LoadOrStore(clientID, b)
	return actual.(*bucket)
}

// cleanupLoop periodically removes stale buckets.
func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.cleanup()
		case <-rl.stopCleanup:
			return
		}
	}
}

// cleanup removes buckets that haven't been accessed recently.
func (rl *RateLimiter) cleanup() {
	threshold := time.Now().Add(-rl.cleanupInterval)
	deleted := 0

	rl.buckets.Range(func(key, value interface{}) bool {
		b := value.(*bucket)
		b.mu.Lock()
		if b.lastRefill.Before(threshold) {
			rl.buckets.Delete(key)
			deleted++
		}
		b.mu.Unlock()
		return true
	})

	if deleted > 0 {
		rl.logger.Debug().
			Int("deleted", deleted).
			Msg("Cleaned up stale rate limit buckets")
	}
}

// Stop stops the rate limiter's background cleanup.
func (rl *RateLimiter) Stop() {
	if rl.enabled {
		close(rl.stopCleanup)
	}
}

// BandwidthLimiter limits bandwidth per client.
type BandwidthLimiter struct {
	bytesPerSecond int64
	enabled        bool
	buckets        sync.Map
	metrics        *metrics.Metrics
	logger         zerolog.Logger
}

// BandwidthLimiterConfig holds bandwidth limiter configuration.
type BandwidthLimiterConfig struct {
	// BytesPerSecond is the bandwidth limit per client.
	BytesPerSecond int64

	// Enabled determines if bandwidth limiting is active.
	Enabled bool
}

// NewBandwidthLimiter creates a new bandwidth limiter.
func NewBandwidthLimiter(config BandwidthLimiterConfig, m *metrics.Metrics, logger zerolog.Logger) *BandwidthLimiter {
	return &BandwidthLimiter{
		bytesPerSecond: config.BytesPerSecond,
		enabled:        config.Enabled,
		metrics:        m,
		logger:         logger.With().Str("component", "bandwidth_limiter").Logger(),
	}
}

// AllowBytes checks if the specified bytes can be transferred.
func (bl *BandwidthLimiter) AllowBytes(clientID string, bytes int64) bool {
	if !bl.enabled {
		return true
	}

	// Similar token bucket implementation for bytes
	b := bl.getBucket(clientID)

	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()

	// Refill tokens based on time elapsed
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens += elapsed * float64(bl.bytesPerSecond)

	// Cap at burst size (1 second worth)
	if b.tokens > float64(bl.bytesPerSecond) {
		b.tokens = float64(bl.bytesPerSecond)
	}

	b.lastRefill = now

	// Check if we have enough tokens
	if b.tokens >= float64(bytes) {
		b.tokens -= float64(bytes)
		return true
	}

	if bl.metrics != nil {
		bl.metrics.RecordRateLimited("bandwidth")
	}

	return false
}

func (bl *BandwidthLimiter) getBucket(clientID string) *bucket {
	if b, ok := bl.buckets.Load(clientID); ok {
		return b.(*bucket)
	}

	b := &bucket{
		tokens:     float64(bl.bytesPerSecond),
		lastRefill: time.Now(),
	}

	actual, _ := bl.buckets.LoadOrStore(clientID, b)
	return actual.(*bucket)
}
