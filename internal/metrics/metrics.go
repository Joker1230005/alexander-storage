// Package metrics provides Prometheus metrics for Alexander Storage.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics contains all Prometheus metrics for the storage server.
type Metrics struct {
	// HTTP Metrics
	HTTPRequestsTotal    *prometheus.CounterVec
	HTTPRequestDuration  *prometheus.HistogramVec
	HTTPRequestsInFlight prometheus.Gauge
	HTTPResponseSize     *prometheus.HistogramVec

	// Storage Metrics
	StorageOperationsTotal   *prometheus.CounterVec
	StorageOperationDuration *prometheus.HistogramVec
	StorageBytesTotal        *prometheus.CounterVec
	BlobsTotal               prometheus.Gauge
	BlobsSize                prometheus.Gauge

	// Object Metrics
	ObjectsTotal   *prometheus.GaugeVec
	ObjectsSize    *prometheus.GaugeVec
	BucketsTotal   prometheus.Gauge
	VersionsTotal  prometheus.Gauge
	MultipartTotal prometheus.Gauge

	// Database Metrics
	DBConnectionsTotal    *prometheus.GaugeVec
	DBQueryDuration       *prometheus.HistogramVec
	DBTransactionsTotal   *prometheus.CounterVec
	DBTransactionDuration *prometheus.HistogramVec

	// Cache Metrics
	CacheHitsTotal   *prometheus.CounterVec
	CacheMissesTotal *prometheus.CounterVec
	CacheEvictions   prometheus.Counter

	// Auth Metrics
	AuthAttemptsTotal *prometheus.CounterVec
	AuthFailuresTotal *prometheus.CounterVec

	// Garbage Collection Metrics
	GCRunsTotal    prometheus.Counter
	GCBlobsDeleted prometheus.Counter
	GCBytesFreed   prometheus.Counter
	GCDuration     prometheus.Histogram
	GCOrphanBlobs  prometheus.Gauge
	GCLastRunTime  prometheus.Gauge

	// Rate Limiting Metrics
	RateLimitedRequests *prometheus.CounterVec
}

// namespace for all Alexander metrics
const namespace = "alexander"

// New creates and registers all Prometheus metrics.
func New() *Metrics {
	m := &Metrics{
		// HTTP Metrics
		HTTPRequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "http",
				Name:      "requests_total",
				Help:      "Total number of HTTP requests.",
			},
			[]string{"method", "path", "status"},
		),
		HTTPRequestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: "http",
				Name:      "request_duration_seconds",
				Help:      "HTTP request duration in seconds.",
				Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
			},
			[]string{"method", "path"},
		),
		HTTPRequestsInFlight: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: "http",
				Name:      "requests_in_flight",
				Help:      "Current number of HTTP requests being processed.",
			},
		),
		HTTPResponseSize: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: "http",
				Name:      "response_size_bytes",
				Help:      "HTTP response size in bytes.",
				Buckets:   prometheus.ExponentialBuckets(100, 10, 8), // 100B to 10GB
			},
			[]string{"method", "path"},
		),

		// Storage Metrics
		StorageOperationsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "storage",
				Name:      "operations_total",
				Help:      "Total number of storage operations.",
			},
			[]string{"operation", "status"},
		),
		StorageOperationDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: "storage",
				Name:      "operation_duration_seconds",
				Help:      "Storage operation duration in seconds.",
				Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
			},
			[]string{"operation"},
		),
		StorageBytesTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "storage",
				Name:      "bytes_total",
				Help:      "Total bytes processed by storage operations.",
			},
			[]string{"operation"},
		),
		BlobsTotal: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: "storage",
				Name:      "blobs_total",
				Help:      "Total number of unique blobs in storage.",
			},
		),
		BlobsSize: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: "storage",
				Name:      "blobs_size_bytes",
				Help:      "Total size of all blobs in bytes.",
			},
		),

		// Object Metrics
		ObjectsTotal: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: "objects",
				Name:      "total",
				Help:      "Total number of objects.",
			},
			[]string{"bucket"},
		),
		ObjectsSize: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: "objects",
				Name:      "size_bytes",
				Help:      "Total size of objects in bytes.",
			},
			[]string{"bucket"},
		),
		BucketsTotal: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: "objects",
				Name:      "buckets_total",
				Help:      "Total number of buckets.",
			},
		),
		VersionsTotal: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: "objects",
				Name:      "versions_total",
				Help:      "Total number of object versions.",
			},
		),
		MultipartTotal: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: "objects",
				Name:      "multipart_uploads_total",
				Help:      "Total number of in-progress multipart uploads.",
			},
		),

		// Database Metrics
		DBConnectionsTotal: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: "db",
				Name:      "connections",
				Help:      "Number of database connections by state.",
			},
			[]string{"state"},
		),
		DBQueryDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: "db",
				Name:      "query_duration_seconds",
				Help:      "Database query duration in seconds.",
				Buckets:   []float64{.0001, .0005, .001, .005, .01, .025, .05, .1, .25, .5, 1},
			},
			[]string{"query"},
		),
		DBTransactionsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "db",
				Name:      "transactions_total",
				Help:      "Total number of database transactions.",
			},
			[]string{"status"},
		),
		DBTransactionDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: "db",
				Name:      "transaction_duration_seconds",
				Help:      "Database transaction duration in seconds.",
				Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
			},
			[]string{"status"},
		),

		// Cache Metrics
		CacheHitsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "cache",
				Name:      "hits_total",
				Help:      "Total number of cache hits.",
			},
			[]string{"cache"},
		),
		CacheMissesTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "cache",
				Name:      "misses_total",
				Help:      "Total number of cache misses.",
			},
			[]string{"cache"},
		),
		CacheEvictions: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "cache",
				Name:      "evictions_total",
				Help:      "Total number of cache evictions.",
			},
		),

		// Auth Metrics
		AuthAttemptsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "auth",
				Name:      "attempts_total",
				Help:      "Total number of authentication attempts.",
			},
			[]string{"method"},
		),
		AuthFailuresTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "auth",
				Name:      "failures_total",
				Help:      "Total number of authentication failures.",
			},
			[]string{"method", "reason"},
		),

		// Garbage Collection Metrics
		GCRunsTotal: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "gc",
				Name:      "runs_total",
				Help:      "Total number of garbage collection runs.",
			},
		),
		GCBlobsDeleted: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "gc",
				Name:      "blobs_deleted_total",
				Help:      "Total number of blobs deleted by garbage collection.",
			},
		),
		GCBytesFreed: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "gc",
				Name:      "bytes_freed_total",
				Help:      "Total bytes freed by garbage collection.",
			},
		),
		GCDuration: promauto.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: "gc",
				Name:      "duration_seconds",
				Help:      "Garbage collection run duration in seconds.",
				Buckets:   []float64{.1, .5, 1, 5, 10, 30, 60, 120},
			},
		),
		GCOrphanBlobs: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: "gc",
				Name:      "orphan_blobs",
				Help:      "Current number of orphan blobs pending garbage collection.",
			},
		),
		GCLastRunTime: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: "gc",
				Name:      "last_run_timestamp_seconds",
				Help:      "Timestamp of the last garbage collection run.",
			},
		),

		// Rate Limiting Metrics
		RateLimitedRequests: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: "ratelimit",
				Name:      "requests_total",
				Help:      "Total number of rate limited requests.",
			},
			[]string{"limit_type"},
		),
	}

	return m
}

// Handler returns the Prometheus metrics HTTP handler.
func Handler() http.Handler {
	return promhttp.Handler()
}

// RecordHTTPRequest records HTTP request metrics.
func (m *Metrics) RecordHTTPRequest(method, path, status string, duration float64, size int64) {
	m.HTTPRequestsTotal.WithLabelValues(method, path, status).Inc()
	m.HTTPRequestDuration.WithLabelValues(method, path).Observe(duration)
	m.HTTPResponseSize.WithLabelValues(method, path).Observe(float64(size))
}

// RecordStorageOperation records storage operation metrics.
func (m *Metrics) RecordStorageOperation(operation, status string, duration float64, bytes int64) {
	m.StorageOperationsTotal.WithLabelValues(operation, status).Inc()
	m.StorageOperationDuration.WithLabelValues(operation).Observe(duration)
	if bytes > 0 {
		m.StorageBytesTotal.WithLabelValues(operation).Add(float64(bytes))
	}
}

// RecordAuthAttempt records an authentication attempt.
func (m *Metrics) RecordAuthAttempt(method string, success bool, reason string) {
	m.AuthAttemptsTotal.WithLabelValues(method).Inc()
	if !success {
		m.AuthFailuresTotal.WithLabelValues(method, reason).Inc()
	}
}

// RecordCacheAccess records a cache access.
func (m *Metrics) RecordCacheAccess(cache string, hit bool) {
	if hit {
		m.CacheHitsTotal.WithLabelValues(cache).Inc()
	} else {
		m.CacheMissesTotal.WithLabelValues(cache).Inc()
	}
}

// RecordGCRun records a garbage collection run.
func (m *Metrics) RecordGCRun(duration float64, blobsDeleted int, bytesFreed int64) {
	m.GCRunsTotal.Inc()
	m.GCDuration.Observe(duration)
	m.GCBlobsDeleted.Add(float64(blobsDeleted))
	m.GCBytesFreed.Add(float64(bytesFreed))
}

// RecordRateLimited records a rate limited request.
func (m *Metrics) RecordRateLimited(limitType string) {
	m.RateLimitedRequests.WithLabelValues(limitType).Inc()
}
