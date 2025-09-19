// Package observability provides health checks, metrics, and tracing capabilities
package observability

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// HealthChecker defines an interface for components that can report their health status
type HealthChecker interface {
	// HealthCheck returns nil if healthy, error if unhealthy
	HealthCheck(ctx context.Context) error
	// Name returns the name of the component being checked
	Name() string
}

// ReadinessChecker defines an interface for components that can report their readiness status
type ReadinessChecker interface {
	// ReadinessCheck returns nil if ready, error if not ready
	ReadinessCheck(ctx context.Context) error
	// Name returns the name of the component being checked
	Name() string
}

// HealthStatus represents the health status of a component
type HealthStatus struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // "healthy" or "unhealthy"
	Error   string `json:"error,omitempty"`
	Latency string `json:"latency,omitempty"`
}

// HealthResponse represents the overall health response
type HealthResponse struct {
	Status     string         `json:"status"` // "healthy" or "unhealthy"
	Timestamp  time.Time      `json:"timestamp"`
	Components []HealthStatus `json:"components"`
}

// ReadinessResponse represents the overall readiness response
type ReadinessResponse struct {
	Status     string         `json:"status"` // "ready" or "not_ready"
	Timestamp  time.Time      `json:"timestamp"`
	Components []HealthStatus `json:"components"`
}

// HealthManager manages health and readiness checks
type HealthManager struct {
	logger           *zap.SugaredLogger
	healthCheckers   []HealthChecker
	readinessCheckers []ReadinessChecker
	timeout          time.Duration
}

// NewHealthManager creates a new health manager
func NewHealthManager(logger *zap.SugaredLogger) *HealthManager {
	return &HealthManager{
		logger:            logger,
		healthCheckers:    make([]HealthChecker, 0),
		readinessCheckers: make([]ReadinessChecker, 0),
		timeout:           5 * time.Second, // Default timeout for health checks
	}
}

// AddHealthChecker registers a health checker
func (hm *HealthManager) AddHealthChecker(checker HealthChecker) {
	hm.healthCheckers = append(hm.healthCheckers, checker)
}

// AddReadinessChecker registers a readiness checker
func (hm *HealthManager) AddReadinessChecker(checker ReadinessChecker) {
	hm.readinessCheckers = append(hm.readinessCheckers, checker)
}

// SetTimeout sets the timeout for health checks
func (hm *HealthManager) SetTimeout(timeout time.Duration) {
	hm.timeout = timeout
}

// HealthzHandler returns an HTTP handler for the /healthz endpoint
func (hm *HealthManager) HealthzHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), hm.timeout)
		defer cancel()

		response := hm.checkHealth(ctx)

		// Set appropriate status code
		statusCode := http.StatusOK
		if response.Status != "healthy" {
			statusCode = http.StatusServiceUnavailable
		}

		hm.writeJSONResponse(w, statusCode, response)
	}
}

// ReadyzHandler returns an HTTP handler for the /readyz endpoint
func (hm *HealthManager) ReadyzHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), hm.timeout)
		defer cancel()

		response := hm.checkReadiness(ctx)

		// Set appropriate status code
		statusCode := http.StatusOK
		if response.Status != "ready" {
			statusCode = http.StatusServiceUnavailable
		}

		hm.writeJSONResponse(w, statusCode, response)
	}
}

// checkHealth performs all health checks
func (hm *HealthManager) checkHealth(ctx context.Context) HealthResponse {
	response := HealthResponse{
		Status:     "healthy",
		Timestamp:  time.Now(),
		Components: make([]HealthStatus, 0, len(hm.healthCheckers)),
	}

	for _, checker := range hm.healthCheckers {
		start := time.Now()
		status := HealthStatus{
			Name:   checker.Name(),
			Status: "healthy",
		}

		if err := checker.HealthCheck(ctx); err != nil {
			status.Status = "unhealthy"
			status.Error = err.Error()
			response.Status = "unhealthy"
			hm.logger.Warnw("Health check failed",
				"component", checker.Name(),
				"error", err)
		}

		status.Latency = time.Since(start).String()
		response.Components = append(response.Components, status)
	}

	return response
}

// checkReadiness performs all readiness checks
func (hm *HealthManager) checkReadiness(ctx context.Context) ReadinessResponse {
	response := ReadinessResponse{
		Status:     "ready",
		Timestamp:  time.Now(),
		Components: make([]HealthStatus, 0, len(hm.readinessCheckers)),
	}

	for _, checker := range hm.readinessCheckers {
		start := time.Now()
		status := HealthStatus{
			Name:   checker.Name(),
			Status: "ready",
		}

		if err := checker.ReadinessCheck(ctx); err != nil {
			status.Status = "not_ready"
			status.Error = err.Error()
			response.Status = "not_ready"
			hm.logger.Warnw("Readiness check failed",
				"component", checker.Name(),
				"error", err)
		}

		status.Latency = time.Since(start).String()
		response.Components = append(response.Components, status)
	}

	return response
}

// writeJSONResponse writes a JSON response
func (hm *HealthManager) writeJSONResponse(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		hm.logger.Errorw("Failed to encode health response", "error", err)
	}
}

// GetHealthStatus returns the current health status without HTTP context
func (hm *HealthManager) GetHealthStatus() HealthResponse {
	ctx, cancel := context.WithTimeout(context.Background(), hm.timeout)
	defer cancel()
	return hm.checkHealth(ctx)
}

// GetReadinessStatus returns the current readiness status without HTTP context
func (hm *HealthManager) GetReadinessStatus() ReadinessResponse {
	ctx, cancel := context.WithTimeout(context.Background(), hm.timeout)
	defer cancel()
	return hm.checkReadiness(ctx)
}

// IsHealthy returns true if all health checks pass
func (hm *HealthManager) IsHealthy() bool {
	return hm.GetHealthStatus().Status == "healthy"
}

// IsReady returns true if all readiness checks pass
func (hm *HealthManager) IsReady() bool {
	return hm.GetReadinessStatus().Status == "ready"
}