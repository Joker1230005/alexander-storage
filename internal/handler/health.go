// Package handler provides HTTP handlers for Alexander Storage API.
package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/prn-tf/alexander-storage/internal/storage"
)

// HealthChecker provides health check endpoints.
type HealthChecker struct {
	dbChecker      DatabaseChecker
	storageBackend storage.Backend
	logger         zerolog.Logger

	// Cached status for efficiency
	mu           sync.RWMutex
	cachedStatus *HealthStatus
	cacheExpiry  time.Time
	cacheTTL     time.Duration
}

// DatabaseChecker interface for database health checks.
type DatabaseChecker interface {
	Ping(ctx context.Context) error
}

// HealthCheckerConfig contains health checker configuration.
type HealthCheckerConfig struct {
	DatabaseChecker DatabaseChecker
	StorageBackend  storage.Backend
	Logger          zerolog.Logger
	CacheTTL        time.Duration
}

// NewHealthChecker creates a new health checker.
func NewHealthChecker(config HealthCheckerConfig) *HealthChecker {
	cacheTTL := config.CacheTTL
	if cacheTTL == 0 {
		cacheTTL = 5 * time.Second
	}

	return &HealthChecker{
		dbChecker:      config.DatabaseChecker,
		storageBackend: config.StorageBackend,
		logger:         config.Logger.With().Str("handler", "health").Logger(),
		cacheTTL:       cacheTTL,
	}
}

// HealthStatus represents the overall health status.
type HealthStatus struct {
	Status     string                      `json:"status"`
	Timestamp  time.Time                   `json:"timestamp"`
	Version    string                      `json:"version,omitempty"`
	Uptime     string                      `json:"uptime,omitempty"`
	Components map[string]*ComponentStatus `json:"components"`
}

// ComponentStatus represents the health of a single component.
type ComponentStatus struct {
	Status  string      `json:"status"`
	Latency string      `json:"latency,omitempty"`
	Error   string      `json:"error,omitempty"`
	Details interface{} `json:"details,omitempty"`
}

// Status constants
const (
	StatusHealthy   = "healthy"
	StatusDegraded  = "degraded"
	StatusUnhealthy = "unhealthy"
)

var startTime = time.Now()

// HandleLiveness handles liveness probe requests (Kubernetes /healthz).
// Returns 200 if the server is running (always succeeds if handler is called).
func (h *HealthChecker) HandleLiveness(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": StatusHealthy,
	})
}

// HandleReadiness handles readiness probe requests (Kubernetes /readyz).
// Returns 200 if the server is ready to accept traffic.
func (h *HealthChecker) HandleReadiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	status := h.checkComponents(ctx)

	w.Header().Set("Content-Type", "application/json")

	if status.Status == StatusHealthy || status.Status == StatusDegraded {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	json.NewEncoder(w).Encode(status)
}

// HandleHealth handles detailed health check requests.
// This is the main health endpoint with full component status.
func (h *HealthChecker) HandleHealth(w http.ResponseWriter, r *http.Request) {
	// Check for cached status
	h.mu.RLock()
	if h.cachedStatus != nil && time.Now().Before(h.cacheExpiry) {
		status := h.cachedStatus
		h.mu.RUnlock()

		h.writeHealthResponse(w, status)
		return
	}
	h.mu.RUnlock()

	// Perform health check
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	status := h.checkComponents(ctx)
	status.Uptime = time.Since(startTime).Round(time.Second).String()

	// Cache the result
	h.mu.Lock()
	h.cachedStatus = status
	h.cacheExpiry = time.Now().Add(h.cacheTTL)
	h.mu.Unlock()

	h.writeHealthResponse(w, status)
}

func (h *HealthChecker) writeHealthResponse(w http.ResponseWriter, status *HealthStatus) {
	w.Header().Set("Content-Type", "application/json")

	switch status.Status {
	case StatusHealthy:
		w.WriteHeader(http.StatusOK)
	case StatusDegraded:
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	json.NewEncoder(w).Encode(status)
}

// checkComponents checks all components and returns health status.
func (h *HealthChecker) checkComponents(ctx context.Context) *HealthStatus {
	status := &HealthStatus{
		Status:     StatusHealthy,
		Timestamp:  time.Now().UTC(),
		Components: make(map[string]*ComponentStatus),
	}

	// Check database
	dbStatus := h.checkDatabase(ctx)
	status.Components["database"] = dbStatus

	// Check storage
	storageStatus := h.checkStorage(ctx)
	status.Components["storage"] = storageStatus

	// Determine overall status
	for _, comp := range status.Components {
		if comp.Status == StatusUnhealthy {
			status.Status = StatusUnhealthy
			break
		}
		if comp.Status == StatusDegraded {
			status.Status = StatusDegraded
		}
	}

	return status
}

// checkDatabase checks database connectivity.
func (h *HealthChecker) checkDatabase(ctx context.Context) *ComponentStatus {
	if h.dbChecker == nil {
		return &ComponentStatus{
			Status: StatusUnhealthy,
			Error:  "database checker not configured",
		}
	}

	start := time.Now()
	err := h.dbChecker.Ping(ctx)
	latency := time.Since(start)

	if err != nil {
		h.logger.Warn().Err(err).Msg("Database health check failed")
		return &ComponentStatus{
			Status:  StatusUnhealthy,
			Latency: latency.String(),
			Error:   err.Error(),
		}
	}

	status := StatusHealthy
	if latency > 100*time.Millisecond {
		status = StatusDegraded
	}

	return &ComponentStatus{
		Status:  status,
		Latency: latency.String(),
	}
}

// checkStorage checks storage backend accessibility.
func (h *HealthChecker) checkStorage(ctx context.Context) *ComponentStatus {
	if h.storageBackend == nil {
		return &ComponentStatus{
			Status: StatusUnhealthy,
			Error:  "storage backend not configured",
		}
	}

	start := time.Now()

	// Try to check if storage is accessible
	// We'll use a simple operation that doesn't require a specific blob
	err := h.storageBackend.HealthCheck(ctx)
	latency := time.Since(start)

	if err != nil {
		h.logger.Warn().Err(err).Msg("Storage health check failed")
		return &ComponentStatus{
			Status:  StatusUnhealthy,
			Latency: latency.String(),
			Error:   err.Error(),
		}
	}

	status := StatusHealthy
	if latency > 500*time.Millisecond {
		status = StatusDegraded
	}

	return &ComponentStatus{
		Status:  status,
		Latency: latency.String(),
	}
}

// SimpleHealth returns a simple JSON health response.
// Used as a lightweight endpoint.
func SimpleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"healthy"}`))
}
