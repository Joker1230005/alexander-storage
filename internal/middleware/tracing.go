// Package middleware provides HTTP middleware for Alexander Storage.
package middleware

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/prn-tf/alexander-storage/internal/metrics"
)

// Context keys for tracing.
type contextKey string

const (
	// RequestIDKey is the context key for request ID.
	RequestIDKey contextKey = "request_id"

	// TraceIDKey is the context key for trace ID (for distributed tracing).
	TraceIDKey contextKey = "trace_id"

	// SpanIDKey is the context key for span ID.
	SpanIDKey contextKey = "span_id"

	// RequestStartKey is the context key for request start time.
	RequestStartKey contextKey = "request_start"
)

// Header names for tracing.
const (
	HeaderRequestID    = "X-Request-ID"
	HeaderTraceID      = "X-Trace-ID"
	HeaderSpanID       = "X-Span-ID"
	HeaderAmzRequestID = "x-amz-request-id"
	HeaderAmzID2       = "x-amz-id-2"
)

// Tracing provides request tracing and correlation ID middleware.
type Tracing struct {
	logger  zerolog.Logger
	metrics *metrics.Metrics
}

// NewTracing creates a new Tracing middleware.
func NewTracing(m *metrics.Metrics, logger zerolog.Logger) *Tracing {
	return &Tracing{
		logger:  logger.With().Str("component", "tracing").Logger(),
		metrics: m,
	}
}

// Middleware returns the tracing middleware.
func (t *Tracing) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Extract or generate request ID
		requestID := r.Header.Get(HeaderRequestID)
		if requestID == "" {
			requestID = generateID()
		}

		// Extract or generate trace ID
		traceID := r.Header.Get(HeaderTraceID)
		if traceID == "" {
			traceID = generateID()
		}

		// Generate new span ID for this request
		spanID := generateShortID()

		// Add to context
		ctx := r.Context()
		ctx = context.WithValue(ctx, RequestIDKey, requestID)
		ctx = context.WithValue(ctx, TraceIDKey, traceID)
		ctx = context.WithValue(ctx, SpanIDKey, spanID)
		ctx = context.WithValue(ctx, RequestStartKey, start)

		// Set response headers (S3-compatible)
		w.Header().Set(HeaderRequestID, requestID)
		w.Header().Set(HeaderAmzRequestID, requestID)
		w.Header().Set(HeaderAmzID2, traceID)

		// Create wrapped response writer to capture status and size
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		// Log request start
		t.logger.Debug().
			Str("request_id", requestID).
			Str("trace_id", traceID).
			Str("span_id", spanID).
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Str("remote_addr", r.RemoteAddr).
			Str("user_agent", r.UserAgent()).
			Msg("Request started")

		// Call next handler
		next.ServeHTTP(wrapped, r.WithContext(ctx))

		// Calculate duration
		duration := time.Since(start)

		// Normalize path for metrics (avoid high cardinality)
		metricPath := normalizePath(r.URL.Path)

		// Record metrics
		if t.metrics != nil {
			t.metrics.RecordHTTPRequest(
				r.Method,
				metricPath,
				http.StatusText(wrapped.statusCode),
				duration.Seconds(),
				int64(wrapped.bytesWritten),
			)
		}

		// Log request completion
		logger := t.logger.Info()
		if wrapped.statusCode >= 400 {
			logger = t.logger.Warn()
		}
		if wrapped.statusCode >= 500 {
			logger = t.logger.Error()
		}

		logger.
			Str("request_id", requestID).
			Str("trace_id", traceID).
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Int("status", wrapped.statusCode).
			Dur("duration", duration).
			Int("bytes", wrapped.bytesWritten).
			Msg("Request completed")
	})
}

// responseWriter wraps http.ResponseWriter to capture response details.
type responseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += n
	return n, err
}

// Flush implements http.Flusher for streaming responses.
func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// generateID generates a unique request ID.
func generateID() string {
	return uuid.New().String()
}

// generateShortID generates a short ID for spans.
func generateShortID() string {
	id := uuid.New()
	return id.String()[:8]
}

// normalizePath normalizes the request path for metrics to avoid high cardinality.
func normalizePath(path string) string {
	// For S3 API, we want to normalize bucket and object keys
	// /bucket-name -> /{bucket}
	// /bucket-name/object/key -> /{bucket}/{key}

	if path == "/" || path == "/health" || path == "/metrics" {
		return path
	}

	// Extract first path segment (bucket)
	parts := splitPath(path)
	if len(parts) == 0 {
		return "/"
	}

	if len(parts) == 1 {
		return "/{bucket}"
	}

	return "/{bucket}/{key}"
}

// splitPath splits a path into segments.
func splitPath(path string) []string {
	var parts []string
	start := 0

	// Skip leading slash
	if len(path) > 0 && path[0] == '/' {
		start = 1
	}

	for i := start; i < len(path); i++ {
		if path[i] == '/' {
			if i > start {
				parts = append(parts, path[start:i])
			}
			start = i + 1
		}
	}

	if start < len(path) {
		parts = append(parts, path[start:])
	}

	return parts
}

// GetRequestID extracts the request ID from context.
func GetRequestID(ctx context.Context) string {
	if v := ctx.Value(RequestIDKey); v != nil {
		return v.(string)
	}
	return ""
}

// GetTraceID extracts the trace ID from context.
func GetTraceID(ctx context.Context) string {
	if v := ctx.Value(TraceIDKey); v != nil {
		return v.(string)
	}
	return ""
}

// GetSpanID extracts the span ID from context.
func GetSpanID(ctx context.Context) string {
	if v := ctx.Value(SpanIDKey); v != nil {
		return v.(string)
	}
	return ""
}

// GetRequestStart extracts the request start time from context.
func GetRequestStart(ctx context.Context) time.Time {
	if v := ctx.Value(RequestStartKey); v != nil {
		return v.(time.Time)
	}
	return time.Time{}
}

// LoggerWithTrace returns a logger with trace context fields.
func LoggerWithTrace(ctx context.Context, logger zerolog.Logger) zerolog.Logger {
	return logger.With().
		Str("request_id", GetRequestID(ctx)).
		Str("trace_id", GetTraceID(ctx)).
		Str("span_id", GetSpanID(ctx)).
		Logger()
}

// MetricsMiddleware wraps handlers with metrics collection.
type MetricsMiddleware struct {
	metrics *metrics.Metrics
}

// NewMetricsMiddleware creates a new metrics middleware.
func NewMetricsMiddleware(m *metrics.Metrics) *MetricsMiddleware {
	return &MetricsMiddleware{metrics: m}
}

// Middleware returns the metrics middleware.
func (m *MetricsMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Track in-flight requests
		m.metrics.HTTPRequestsInFlight.Inc()
		defer m.metrics.HTTPRequestsInFlight.Dec()

		next.ServeHTTP(w, r)
	})
}
