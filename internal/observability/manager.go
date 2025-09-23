package observability

import (
	"context"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// Tool call status constants
const (
	StatusSuccess = "success"
	StatusError   = "error"
)

// Config holds configuration for observability features
type Config struct {
	Health  HealthConfig  `json:"health"`
	Metrics MetricsConfig `json:"metrics"`
	Tracing TracingConfig `json:"tracing"`
}

// HealthConfig holds configuration for health checks
type HealthConfig struct {
	Enabled bool          `json:"enabled"`
	Timeout time.Duration `json:"timeout"`
}

// MetricsConfig holds configuration for metrics
type MetricsConfig struct {
	Enabled bool `json:"enabled"`
}

// DefaultConfig returns a default observability configuration
func DefaultConfig(serviceName, serviceVersion string) Config {
	return Config{
		Health: HealthConfig{
			Enabled: true,
			Timeout: 5 * time.Second,
		},
		Metrics: MetricsConfig{
			Enabled: true,
		},
		Tracing: TracingConfig{
			Enabled:        false, // Disabled by default
			ServiceName:    serviceName,
			ServiceVersion: serviceVersion,
			JaegerEndpoint: "http://localhost:14268/api/traces",
			SampleRate:     0.1, // 10% sampling
		},
	}
}

// Manager coordinates all observability features
type Manager struct {
	logger  *zap.SugaredLogger
	config  Config
	health  *HealthManager
	metrics *MetricsManager
	tracing *TracingManager

	startTime time.Time
}

// NewManager creates a new observability manager
func NewManager(logger *zap.SugaredLogger, config Config) (*Manager, error) {
	manager := &Manager{
		logger:    logger,
		config:    config,
		startTime: time.Now(),
	}

	// Initialize health manager
	if config.Health.Enabled {
		manager.health = NewHealthManager(logger)
		manager.health.SetTimeout(config.Health.Timeout)
		logger.Info("Health checks enabled")
	}

	// Initialize metrics manager
	if config.Metrics.Enabled {
		manager.metrics = NewMetricsManager(logger)
		logger.Info("Prometheus metrics enabled")
	}

	// Initialize tracing manager
	if config.Tracing.Enabled {
		var err error
		manager.tracing, err = NewTracingManager(logger, config.Tracing)
		if err != nil {
			return nil, err
		}
	}

	return manager, nil
}

// Health returns the health manager
func (m *Manager) Health() *HealthManager {
	return m.health
}

// Metrics returns the metrics manager
func (m *Manager) Metrics() *MetricsManager {
	return m.metrics
}

// Tracing returns the tracing manager
func (m *Manager) Tracing() *TracingManager {
	return m.tracing
}

// RegisterHealthChecker registers a health checker
func (m *Manager) RegisterHealthChecker(checker HealthChecker) {
	if m.health != nil {
		m.health.AddHealthChecker(checker)
	}
}

// RegisterReadinessChecker registers a readiness checker
func (m *Manager) RegisterReadinessChecker(checker ReadinessChecker) {
	if m.health != nil {
		m.health.AddReadinessChecker(checker)
	}
}

// SetupHTTPHandlers sets up observability HTTP handlers
func (m *Manager) SetupHTTPHandlers(mux *http.ServeMux) {
	// Health endpoints
	if m.health != nil {
		mux.HandleFunc("/healthz", m.health.HealthzHandler())
		mux.HandleFunc("/readyz", m.health.ReadyzHandler())
	}

	// Metrics endpoint
	if m.metrics != nil {
		mux.Handle("/metrics", m.metrics.Handler())
	}
}

// HTTPMiddleware returns combined HTTP middleware for observability
func (m *Manager) HTTPMiddleware() func(http.Handler) http.Handler {
	middlewares := make([]func(http.Handler) http.Handler, 0)

	// Add metrics middleware
	if m.metrics != nil {
		middlewares = append(middlewares, m.metrics.HTTPMiddleware())
	}

	// Add tracing middleware
	if m.tracing != nil {
		middlewares = append(middlewares, m.tracing.HTTPMiddleware())
	}

	// Chain middlewares
	return func(next http.Handler) http.Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			next = middlewares[i](next)
		}
		return next
	}
}

// UpdateMetrics updates various metrics with current system state
func (m *Manager) UpdateMetrics() {
	if m.metrics == nil {
		return
	}

	// Update uptime
	m.metrics.SetUptime(m.startTime)

	// Additional metrics can be updated here by calling external providers
}

// Close gracefully shuts down observability components
func (m *Manager) Close(ctx context.Context) error {
	if m.tracing != nil {
		if err := m.tracing.Close(ctx); err != nil {
			m.logger.Errorw("Failed to close tracing manager", "error", err)
			return err
		}
	}
	return nil
}

// IsHealthy returns true if all health checks pass
func (m *Manager) IsHealthy() bool {
	if m.health == nil {
		return true // Consider healthy if health checks are disabled
	}
	return m.health.IsHealthy()
}

// IsReady returns true if all readiness checks pass
func (m *Manager) IsReady() bool {
	if m.health == nil {
		return true // Consider ready if readiness checks are disabled
	}
	return m.health.IsReady()
}

// RecordToolCall is a convenience method to record tool call metrics and tracing
func (m *Manager) RecordToolCall(ctx context.Context, serverName, toolName string, duration time.Duration, err error) {
	status := StatusSuccess
	if err != nil {
		status = StatusError
	}

	// Record metrics
	if m.metrics != nil {
		m.metrics.RecordToolCall(serverName, toolName, status, duration)
	}

	// Add tracing attributes
	if m.tracing != nil && err != nil {
		m.tracing.SetSpanError(ctx, err)
	}
}

// RecordStorageOperation is a convenience method to record storage operations
func (m *Manager) RecordStorageOperation(operation string, err error) {
	status := StatusSuccess
	if err != nil {
		status = StatusError
	}

	if m.metrics != nil {
		m.metrics.RecordStorageOperation(operation, status)
	}
}
