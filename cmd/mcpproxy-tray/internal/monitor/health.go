package monitor

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"mcpproxy-go/cmd/mcpproxy-tray/internal/api"
	"mcpproxy-go/cmd/mcpproxy-tray/internal/state"
	"mcpproxy-go/internal/socket"
)

// HealthStatus represents the health status of the core service
type HealthStatus string

const (
	HealthStatusUnknown     HealthStatus = "unknown"
	HealthStatusStarting    HealthStatus = "starting"
	HealthStatusHealthy     HealthStatus = "healthy"
	HealthStatusUnhealthy   HealthStatus = "unhealthy"
	HealthStatusUnavailable HealthStatus = "unavailable"
)

// HealthCheck represents a health check result
type HealthCheck struct {
	Endpoint  string
	Status    HealthStatus
	Latency   time.Duration
	Error     error
	Timestamp time.Time
}

// HealthMonitor monitors the health of the core service
type HealthMonitor struct {
	baseURL      string
	logger       *zap.SugaredLogger
	stateMachine *state.Machine

	mu            sync.RWMutex
	currentStatus HealthStatus
	lastCheck     time.Time
	lastError     error

	// HTTP client for health checks
	httpClient *http.Client

	// Channels
	resultsCh  chan HealthCheck
	shutdownCh chan struct{}

	// Context for cancellation
	ctx    context.Context
	cancel context.CancelFunc

	// Shutdown synchronization
	stopOnce sync.Once
	stopped  bool

	// Configuration
	checkInterval    time.Duration
	timeout          time.Duration
	readinessTimeout time.Duration
}

// NewHealthMonitor creates a new health monitor
func NewHealthMonitor(endpoint string, logger *zap.SugaredLogger, stateMachine *state.Machine) *HealthMonitor {
	ctx, cancel := context.WithCancel(context.Background())

	// Create socket/TCP-aware HTTP client using the same dialer as API client
	// The timeout is set in the httpClient itself
	httpClient := api.CreateHTTPClient(endpoint, 10*time.Second, logger)

	// Transform the endpoint to get the proper base URL for HTTP requests
	// For Unix sockets: unix:///path/socket.sock -> http://localhost
	// For TCP: http://localhost:8080 -> http://localhost:8080
	_, transformedBaseURL, err := socket.CreateDialer(endpoint)
	if err != nil {
		// If CreateDialer fails, use the original endpoint (it's likely already HTTP)
		transformedBaseURL = endpoint
		if logger != nil {
			logger.Debug("Using original endpoint as base URL",
				"endpoint", endpoint,
				"error", err)
		}
	} else {
		if logger != nil {
			logger.Debug("Transformed endpoint for health checks",
				"original", endpoint,
				"transformed", transformedBaseURL)
		}
	}

	return &HealthMonitor{
		baseURL:       strings.TrimSuffix(transformedBaseURL, "/"),
		logger:        logger,
		stateMachine:  stateMachine,
		currentStatus: HealthStatusUnknown,
		httpClient:    httpClient,
		resultsCh:        make(chan HealthCheck, 10),
		shutdownCh:       make(chan struct{}),
		ctx:              ctx,
		cancel:           cancel,
		checkInterval:    10 * time.Second, // Reduced from 5s to 10s to lower system load
		timeout:          10 * time.Second, // Increased to match longer operations
		readinessTimeout: 60 * time.Second, // Increased to allow slow Docker container startups
	}
}

// Start starts the health monitoring
func (hm *HealthMonitor) Start() {
	hm.logger.Infow("Starting health monitor", "base_url", hm.baseURL)
	go hm.monitor()
}

// Stop stops the health monitoring (safe to call multiple times)
func (hm *HealthMonitor) Stop() {
	hm.stopOnce.Do(func() {
		hm.logger.Info("Stopping health monitor")
		hm.mu.Lock()
		hm.stopped = true
		hm.mu.Unlock()
		hm.cancel()
		close(hm.shutdownCh)
	})
}

// GetStatus returns the current health status
func (hm *HealthMonitor) GetStatus() HealthStatus {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.currentStatus
}

// GetLastCheck returns the time of the last health check
func (hm *HealthMonitor) GetLastCheck() time.Time {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.lastCheck
}

// GetLastError returns the last health check error
func (hm *HealthMonitor) GetLastError() error {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.lastError
}

// ResultsChannel returns a channel for receiving health check results
func (hm *HealthMonitor) ResultsChannel() <-chan HealthCheck {
	return hm.resultsCh
}

// WaitForReady waits for the service to become ready within the timeout
func (hm *HealthMonitor) WaitForReady() error {
	hm.logger.Infow("Waiting for core service to become ready", "timeout", hm.readinessTimeout)

	startTime := time.Now()
	ctx, cancel := context.WithTimeout(hm.ctx, hm.readinessTimeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			elapsed := time.Since(startTime)
			hm.logger.Error("Timeout waiting for core service to become ready",
				"elapsed", elapsed,
				"timeout", hm.readinessTimeout)
			return fmt.Errorf("timeout waiting for core service to become ready after %v", elapsed)

		case <-ticker.C:
			if hm.checkReadiness() {
				elapsed := time.Since(startTime)
				hm.logger.Infow("Core service is ready", "elapsed", elapsed)

				// Notify state machine
				if hm.stateMachine != nil {
					hm.stateMachine.SendEvent(state.EventCoreReady)
				}
				return nil
			}
		}
	}
}

// monitor runs the periodic health monitoring
func (hm *HealthMonitor) monitor() {
	defer close(hm.resultsCh)

	ticker := time.NewTicker(hm.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-hm.ctx.Done():
			hm.logger.Debug("Health monitor context cancelled")
			return

		case <-ticker.C:
			hm.performHealthCheck()
		}
	}
}

// performHealthCheck performs a single health check
func (hm *HealthMonitor) performHealthCheck() {
	startTime := time.Now()

	// Check liveness first (basic connectivity)
	livenessResult := hm.checkEndpoint("/healthz")

	// Check readiness (fully operational)
	readinessResult := hm.checkEndpoint("/readyz")

	// Determine overall status
	var status HealthStatus
	var checkError error

	if livenessResult.Status == HealthStatusHealthy {
		if readinessResult.Status == HealthStatusHealthy {
			status = HealthStatusHealthy
		} else {
			status = HealthStatusStarting
		}
	} else {
		status = HealthStatusUnavailable
		checkError = livenessResult.Error
	}

	hm.mu.Lock()
	previousStatus := hm.currentStatus
	hm.currentStatus = status
	hm.lastCheck = time.Now()
	hm.lastError = checkError
	hm.mu.Unlock()

	// Log status changes
	if status != previousStatus {
		if checkError != nil {
			hm.logger.Warnw("Health status changed",
				"from", previousStatus,
				"to", status,
				"error", checkError,
				"duration", time.Since(startTime))
		} else {
			hm.logger.Infow("Health status changed",
				"from", previousStatus,
				"to", status,
				"duration", time.Since(startTime))
		}
	}

	// Send result to channel
	result := HealthCheck{
		Endpoint:  "combined",
		Status:    status,
		Latency:   time.Since(startTime),
		Error:     checkError,
		Timestamp: time.Now(),
	}

	select {
	case hm.resultsCh <- result:
	default:
		// Channel full, drop result
		hm.logger.Debug("Health check results channel full, dropping result")
	}
}

// checkReadiness performs a quick readiness check
func (hm *HealthMonitor) checkReadiness() bool {
	result := hm.checkEndpoint("/readyz")
	return result.Status == HealthStatusHealthy
}

// checkEndpoint checks a specific health endpoint
func (hm *HealthMonitor) checkEndpoint(path string) HealthCheck {
	startTime := time.Now()
	url := hm.baseURL + path

	req, err := http.NewRequestWithContext(hm.ctx, "GET", url, http.NoBody)
	if err != nil {
		return HealthCheck{
			Endpoint:  path,
			Status:    HealthStatusUnavailable,
			Latency:   time.Since(startTime),
			Error:     fmt.Errorf("failed to create request: %w", err),
			Timestamp: time.Now(),
		}
	}

	resp, err := hm.httpClient.Do(req)
	if err != nil {
		status := HealthStatusUnavailable

		// Check if it's a connection error (service not started yet)
		if strings.Contains(err.Error(), "connection refused") ||
			strings.Contains(err.Error(), "no such host") {
			status = HealthStatusUnavailable
		}

		return HealthCheck{
			Endpoint:  path,
			Status:    status,
			Latency:   time.Since(startTime),
			Error:     err,
			Timestamp: time.Now(),
		}
	}
	defer resp.Body.Close()

	var status HealthStatus
	switch resp.StatusCode {
	case http.StatusOK:
		status = HealthStatusHealthy
	case http.StatusServiceUnavailable:
		status = HealthStatusStarting
	default:
		status = HealthStatusUnhealthy
	}

	return HealthCheck{
		Endpoint:  path,
		Status:    status,
		Latency:   time.Since(startTime),
		Timestamp: time.Now(),
	}
}

// IsHealthy returns true if the service is healthy
func (hm *HealthMonitor) IsHealthy() bool {
	return hm.GetStatus() == HealthStatusHealthy
}

// IsReady returns true if the service is ready
func (hm *HealthMonitor) IsReady() bool {
	status := hm.GetStatus()
	return status == HealthStatusHealthy
}

// IsStarting returns true if the service is starting up
func (hm *HealthMonitor) IsStarting() bool {
	status := hm.GetStatus()
	return status == HealthStatusStarting
}

// SetCheckInterval sets the health check interval
func (hm *HealthMonitor) SetCheckInterval(interval time.Duration) {
	hm.checkInterval = interval
}

// SetTimeout sets the health check timeout
func (hm *HealthMonitor) SetTimeout(timeout time.Duration) {
	hm.timeout = timeout
	hm.httpClient.Timeout = timeout
}

// SetReadinessTimeout sets the readiness wait timeout
func (hm *HealthMonitor) SetReadinessTimeout(timeout time.Duration) {
	hm.readinessTimeout = timeout
}
