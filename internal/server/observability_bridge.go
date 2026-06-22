package server

import (
	"context"

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
	s.logger.Info("Observability metrics bridge started")
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-events:
			if !ok {
				return
			}
			applyMetricEvent(mm, evt)
		}
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
