package server

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/observability"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// MCP-32: the metrics bridge translates runtime events into Prometheus series.

// metricValue scrapes the registry and returns the value of the named metric
// whose labels are a superset of want. Returns -1 when not found.
func metricValue(t *testing.T, reg *prometheus.Registry, name string, want map[string]string) float64 {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)
	for _, mf := range families {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			matched := true
			for k, v := range want {
				if labels[k] != v {
					matched = false
					break
				}
			}
			if !matched {
				continue
			}
			if c := m.GetCounter(); c != nil {
				return c.GetValue()
			}
			if g := m.GetGauge(); g != nil {
				return g.GetValue()
			}
		}
	}
	return -1
}

func TestApplyMetricEvent_ServersChangedUpdatesGauges(t *testing.T) {
	mm := observability.NewMetricsManager(zap.NewNop().Sugar())

	applyMetricEvent(mm, runtime.Event{
		Type: runtime.EventTypeServersChanged,
		Payload: map[string]any{
			"stats": &contracts.ServerStats{
				TotalServers:       5,
				ConnectedServers:   3,
				QuarantinedServers: 1,
				TotalTools:         42,
				DockerContainers:   2,
			},
		},
	})

	reg := mm.Registry()
	assert.InDelta(t, 5.0, metricValue(t, reg, "mcpproxy_servers_total", nil), 1e-9)
	assert.InDelta(t, 3.0, metricValue(t, reg, "mcpproxy_servers_connected", nil), 1e-9)
	assert.InDelta(t, 1.0, metricValue(t, reg, "mcpproxy_servers_quarantined", nil), 1e-9)
	assert.InDelta(t, 42.0, metricValue(t, reg, "mcpproxy_tools_total", nil), 1e-9)
	assert.InDelta(t, 2.0, metricValue(t, reg, "mcpproxy_docker_containers_active", nil), 1e-9)
}

func TestApplyMetricEvent_ServerQuarantineChangeCounts(t *testing.T) {
	mm := observability.NewMetricsManager(zap.NewNop().Sugar())

	applyMetricEvent(mm, runtime.Event{
		Type:    runtime.EventTypeActivityQuarantineChange,
		Payload: map[string]any{"server_name": "github", "quarantined": true},
	})
	applyMetricEvent(mm, runtime.Event{
		Type:    runtime.EventTypeActivityQuarantineChange,
		Payload: map[string]any{"server_name": "github", "quarantined": false},
	})

	reg := mm.Registry()
	assert.InDelta(t, 1.0, metricValue(t, reg, "mcpproxy_quarantine_events_total", map[string]string{"scope": "server", "action": "quarantined"}), 1e-9)
	assert.InDelta(t, 1.0, metricValue(t, reg, "mcpproxy_quarantine_events_total", map[string]string{"scope": "server", "action": "lifted"}), 1e-9)
}

func TestApplyMetricEvent_ToolQuarantineChangeCounts(t *testing.T) {
	mm := observability.NewMetricsManager(zap.NewNop().Sugar())

	applyMetricEvent(mm, runtime.Event{
		Type:    runtime.EventTypeActivityToolQuarantineChange,
		Payload: map[string]any{"server_name": "github", "tool_name": "x", "action": "changed"},
	})

	assert.InDelta(t, 1.0, metricValue(t, mm.Registry(), "mcpproxy_quarantine_events_total", map[string]string{"scope": "tool", "action": "changed"}), 1e-9)
}

func TestApplyMetricEvent_IgnoresUnrelatedAndMalformed(t *testing.T) {
	mm := observability.NewMetricsManager(zap.NewNop().Sugar())

	// Must not panic on unrelated events or malformed payloads.
	applyMetricEvent(mm, runtime.Event{Type: runtime.EventTypeConfigReloaded})
	applyMetricEvent(mm, runtime.Event{Type: runtime.EventTypeServersChanged, Payload: map[string]any{"stats": "not-a-struct"}})
	applyMetricEvent(mm, runtime.Event{Type: runtime.EventTypeActivityQuarantineChange, Payload: nil})
}
