package server

import (
	"context"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/observability"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// runMetricsBridge subscribes to the runtime event bus and projects events onto
// Prometheus metrics (MCP-32). It runs until ctx is cancelled. Tool-call latency
// and OTLP spans are recorded inline at the call site; this bridge owns the
// gauge/counter series that are naturally event-driven (upstream health and
// quarantine state changes), keeping the metrics decoupled from business logic.
func (s *Server) runMetricsBridge(ctx context.Context, mm *observability.MetricsManager) {
	if mm == nil || s.runtime == nil {
		return
	}
	events := s.runtime.SubscribeEvents()
	defer s.runtime.UnsubscribeEvents(events)

	// Spec 093 FR-013: rejections are counted at the rejection site, not from
	// the (lossy) event bus.
	s.runtime.SetRejectionMetricSink(recordRejectionMetric(mm))
	defer s.runtime.SetRejectionMetricSink(nil)

	s.logger.Info("Observability metrics bridge started")

	// Spec 093 FR-013: queue depth is a level, not an event, so it is sampled
	// rather than written on every acquire/release — the gauges exist to show
	// SUSTAINED saturation, and per-call writes would drag a metrics mutex onto
	// the tool-call hot path.
	depthTicker := time.NewTicker(concurrencyDepthSampleInterval)
	defer depthTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-depthTicker.C:
			s.sampleConcurrencyDepth(mm)
		case evt, ok := <-events:
			if !ok {
				return
			}
			applyMetricEvent(mm, evt)
		}
	}
}

// concurrencyDepthSampleInterval is how often the limiter gauges are refreshed.
const concurrencyDepthSampleInterval = 10 * time.Second

// sampleConcurrencyDepth publishes the live occupancy of every limiter scope.
// Series are reset first so a removed server stops reporting a stale depth.
func (s *Server) sampleConcurrencyDepth(mm *observability.MetricsManager) {
	if s.runtime == nil {
		return
	}
	global, servers := s.runtime.ConcurrencyStats()
	mm.ResetConcurrencyDepth()
	mm.SetConcurrencyDepth("global", "", global.Running, global.Queued)
	for name, st := range servers {
		mm.SetConcurrencyDepth("server", name, st.Running, st.Queued)
	}
}

// applyMetricEvent translates a single runtime event into metric updates. It is
// defensive against malformed payloads (best-effort observability must never
// panic the daemon).
func applyMetricEvent(mm *observability.MetricsManager, evt runtime.Event) {
	switch evt.Type {
	case runtime.EventTypeServersChanged:
		stats, ok := evt.Payload["stats"].(*contracts.ServerStats)
		if !ok || stats == nil {
			return
		}
		mm.SetServerStats(stats.TotalServers, stats.ConnectedServers, stats.QuarantinedServers)
		mm.SetToolsTotal(stats.TotalTools)
		mm.SetDockerContainers(stats.DockerContainers)

	case runtime.EventTypeActivityQuarantineChange:
		action := "lifted"
		if q, _ := evt.Payload["quarantined"].(bool); q {
			action = "quarantined"
		}
		mm.RecordQuarantineEvent("server", action)

	case runtime.EventTypeActivityToolQuarantineChange:
		action, _ := evt.Payload["action"].(string)
		if action == "" {
			action = "unknown"
		}
		mm.RecordQuarantineEvent("tool", action)

	}
}

// recordRejectionMetric counts one shed. It is installed as the runtime's
// synchronous rejection sink rather than driven off the event bus (spec 093
// FR-013): publishEvent drops events for a subscriber that falls behind, so a
// burst of sheds — the only load where the counter is interesting — is exactly
// when bus-driven counting would lose increments. The sink still sees every
// origin, because it hangs off the limiter's own observer.
func recordRejectionMetric(mm *observability.MetricsManager) runtime.RejectionMetricSink {
	return func(server, reason, scope string) {
		if reason == "" {
			reason = "unknown"
		}
		if scope == "" {
			scope = "unknown"
		}
		mm.RecordToolCallRejected(server, reason, scope)
	}
}
