package observability

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

// MetricsManager manages Prometheus metrics
type MetricsManager struct {
	logger   *zap.SugaredLogger
	registry *prometheus.Registry

	// Core metrics
	uptime             prometheus.Gauge
	httpRequests       *prometheus.CounterVec
	httpDuration       *prometheus.HistogramVec
	serversTotal       prometheus.Gauge
	serversConnected   prometheus.Gauge
	serversQuarantined prometheus.Gauge
	toolsTotal         prometheus.Gauge
	toolCalls          *prometheus.CounterVec
	toolDuration       *prometheus.HistogramVec
	indexSize          prometheus.Gauge
	storageOps         *prometheus.CounterVec
	dockerContainers   prometheus.Gauge

	// Supervisor metrics (Phase 5)
	reconciliations       *prometheus.CounterVec
	reconciliationLatency prometheus.Histogram
	serverStateChanges    *prometheus.CounterVec

	// Actor metrics (Phase 5)
	actorStateTransitions *prometheus.CounterVec
	actorConnectLatency   *prometheus.HistogramVec
	actorRetries          *prometheus.CounterVec
	actorFailures         *prometheus.CounterVec
}

// NewMetricsManager creates a new metrics manager
func NewMetricsManager(logger *zap.SugaredLogger) *MetricsManager {
	registry := prometheus.NewRegistry()

	mm := &MetricsManager{
		logger:   logger,
		registry: registry,
	}

	mm.initMetrics()
	mm.registerMetrics()

	return mm
}

// initMetrics initializes all Prometheus metrics
func (mm *MetricsManager) initMetrics() {
	// System metrics
	mm.uptime = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mcpproxy_uptime_seconds",
		Help: "Time since the application started",
	})

	// HTTP metrics
	mm.httpRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mcpproxy_http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	mm.httpDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "mcpproxy_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path", "status"},
	)

	// Server metrics
	mm.serversTotal = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mcpproxy_servers_total",
		Help: "Total number of configured servers",
	})

	mm.serversConnected = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mcpproxy_servers_connected",
		Help: "Number of connected servers",
	})

	mm.serversQuarantined = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mcpproxy_servers_quarantined",
		Help: "Number of quarantined servers",
	})

	// Tool metrics
	mm.toolsTotal = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mcpproxy_tools_total",
		Help: "Total number of available tools",
	})

	mm.toolCalls = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mcpproxy_tool_calls_total",
			Help: "Total number of tool calls",
		},
		[]string{"server", "tool", "status"},
	)

	mm.toolDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "mcpproxy_tool_call_duration_seconds",
			Help:    "Tool call duration in seconds",
			Buckets: []float64{0.001, 0.01, 0.1, 0.5, 1, 2, 5, 10, 30},
		},
		[]string{"server", "tool", "status"},
	)

	// Storage metrics
	mm.indexSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mcpproxy_index_documents_total",
		Help: "Number of documents in the search index",
	})

	mm.storageOps = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mcpproxy_storage_operations_total",
			Help: "Total number of storage operations",
		},
		[]string{"operation", "status"},
	)

	// Docker metrics
	mm.dockerContainers = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mcpproxy_docker_containers_active",
		Help: "Number of active Docker containers",
	})

	// Supervisor metrics (Phase 5)
	mm.reconciliations = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mcpproxy_supervisor_reconciliations_total",
			Help: "Total number of reconciliation cycles",
		},
		[]string{"result"}, // result: success, failed
	)

	mm.reconciliationLatency = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "mcpproxy_supervisor_reconciliation_duration_seconds",
			Help:    "Time taken to complete a reconciliation cycle",
			Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5},
		},
	)

	mm.serverStateChanges = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mcpproxy_supervisor_state_changes_total",
			Help: "Total number of server state changes",
		},
		[]string{"server", "from_state", "to_state"},
	)

	// Actor metrics (Phase 5)
	mm.actorStateTransitions = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mcpproxy_actor_state_transitions_total",
			Help: "Total number of actor state transitions",
		},
		[]string{"server", "from_state", "to_state"},
	)

	mm.actorConnectLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "mcpproxy_actor_connect_duration_seconds",
			Help:    "Time taken for actor to connect to server",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
		},
		[]string{"server", "result"}, // result: success, failed
	)

	mm.actorRetries = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mcpproxy_actor_retries_total",
			Help: "Total number of connection retry attempts",
		},
		[]string{"server"},
	)

	mm.actorFailures = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mcpproxy_actor_failures_total",
			Help: "Total number of actor failures",
		},
		[]string{"server", "error_type"},
	)
}

// registerMetrics registers all metrics with the registry
func (mm *MetricsManager) registerMetrics() {
	mm.registry.MustRegister(
		mm.uptime,
		mm.httpRequests,
		mm.httpDuration,
		mm.serversTotal,
		mm.serversConnected,
		mm.serversQuarantined,
		mm.toolsTotal,
		mm.toolCalls,
		mm.toolDuration,
		mm.indexSize,
		mm.storageOps,
		mm.dockerContainers,
		// Phase 5 metrics
		mm.reconciliations,
		mm.reconciliationLatency,
		mm.serverStateChanges,
		mm.actorStateTransitions,
		mm.actorConnectLatency,
		mm.actorRetries,
		mm.actorFailures,
	)

	// Also register Go runtime metrics
	mm.registry.MustRegister(collectors.NewGoCollector())
	mm.registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
}

// Handler returns an HTTP handler for the /metrics endpoint
func (mm *MetricsManager) Handler() http.Handler {
	return promhttp.HandlerFor(mm.registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}

// Metric update methods

// SetUptime sets the uptime metric
func (mm *MetricsManager) SetUptime(startTime time.Time) {
	mm.uptime.Set(time.Since(startTime).Seconds())
}

// RecordHTTPRequest records an HTTP request
func (mm *MetricsManager) RecordHTTPRequest(method, path, status string, duration time.Duration) {
	mm.httpRequests.WithLabelValues(method, path, status).Inc()
	mm.httpDuration.WithLabelValues(method, path, status).Observe(duration.Seconds())
}

// SetServerStats updates server-related metrics
func (mm *MetricsManager) SetServerStats(total, connected, quarantined int) {
	mm.serversTotal.Set(float64(total))
	mm.serversConnected.Set(float64(connected))
	mm.serversQuarantined.Set(float64(quarantined))
}

// SetToolsTotal sets the total number of tools
func (mm *MetricsManager) SetToolsTotal(total int) {
	mm.toolsTotal.Set(float64(total))
}

// RecordToolCall records a tool call
func (mm *MetricsManager) RecordToolCall(server, tool, status string, duration time.Duration) {
	mm.toolCalls.WithLabelValues(server, tool, status).Inc()
	mm.toolDuration.WithLabelValues(server, tool, status).Observe(duration.Seconds())
}

// SetIndexSize sets the search index size
func (mm *MetricsManager) SetIndexSize(size uint64) {
	mm.indexSize.Set(float64(size))
}

// RecordStorageOperation records a storage operation
func (mm *MetricsManager) RecordStorageOperation(operation, status string) {
	mm.storageOps.WithLabelValues(operation, status).Inc()
}

// SetDockerContainers sets the number of active Docker containers
func (mm *MetricsManager) SetDockerContainers(count int) {
	mm.dockerContainers.Set(float64(count))
}

// Registry returns the Prometheus registry for custom metrics
func (mm *MetricsManager) Registry() *prometheus.Registry {
	return mm.registry
}

// HTTPMiddleware returns middleware that records HTTP metrics
func (mm *MetricsManager) HTTPMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap the response writer to capture status code
			ww := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			// Call the next handler
			next.ServeHTTP(ww, r)

			// Record metrics
			duration := time.Since(start)
			mm.RecordHTTPRequest(r.Method, r.URL.Path, http.StatusText(ww.statusCode), duration)
		})
	}
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// StatsUpdater defines an interface for components that can provide metrics
type StatsUpdater interface {
	UpdateMetrics(mm *MetricsManager)
}

// UpdateFromStatsProvider updates metrics from a stats provider
func (mm *MetricsManager) UpdateFromStatsProvider(provider StatsUpdater) {
	provider.UpdateMetrics(mm)
}

// Phase 5: Supervisor and Actor metrics

// RecordReconciliation records a reconciliation cycle
func (mm *MetricsManager) RecordReconciliation(result string, duration time.Duration) {
	mm.reconciliations.WithLabelValues(result).Inc()
	mm.reconciliationLatency.Observe(duration.Seconds())
}

// RecordServerStateChange records a server state change
func (mm *MetricsManager) RecordServerStateChange(server, fromState, toState string) {
	mm.serverStateChanges.WithLabelValues(server, fromState, toState).Inc()
}

// RecordActorStateTransition records an actor state transition
func (mm *MetricsManager) RecordActorStateTransition(server, fromState, toState string) {
	mm.actorStateTransitions.WithLabelValues(server, fromState, toState).Inc()
}

// RecordActorConnect records an actor connection attempt
func (mm *MetricsManager) RecordActorConnect(server, result string, duration time.Duration) {
	mm.actorConnectLatency.WithLabelValues(server, result).Observe(duration.Seconds())
}

// RecordActorRetry records an actor retry attempt
func (mm *MetricsManager) RecordActorRetry(server string) {
	mm.actorRetries.WithLabelValues(server).Inc()
}

// RecordActorFailure records an actor failure
func (mm *MetricsManager) RecordActorFailure(server, errorType string) {
	mm.actorFailures.WithLabelValues(server, errorType).Inc()
}
