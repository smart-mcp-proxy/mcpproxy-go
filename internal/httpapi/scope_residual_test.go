package httpapi

import (
	"context"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	rt "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/stretchr/testify/assert"
)

func TestResidualStatsFailClosed(t *testing.T) {
	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"alpha"}})
	for _, shape := range []interface{}{nil, "hidden-sentinel", []string{"hidden-sentinel"}} {
		stats := map[string]interface{}{"servers": shape, "total_servers": 99, "total_tools": 123}
		assert.Empty(t, filterUpstreamStatsServers(ctx, stats))
		assert.Equal(t, stats, filterUpstreamStatsServers(context.Background(), stats))
		assert.Equal(t, 99, stats["total_servers"])
	}
}

func TestResidualStatsContributingTools(t *testing.T) {
	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"*"}})
	stats := map[string]interface{}{"servers": map[string]interface{}{
		"enabled":     map[string]interface{}{"enabled": true, "quarantined": false, "tool_count": 3},
		"disabled":    map[string]interface{}{"enabled": false, "quarantined": false, "tool_count": 7},
		"quarantined": map[string]interface{}{"enabled": true, "quarantined": true, "tool_count": 11},
	}, "total_tools": 3}
	assert.Equal(t, 3, filterUpstreamStatsServers(ctx, stats)["total_tools"])
}

func TestResidualDetectionEventIdentity(t *testing.T) {
	ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{Type: auth.AuthTypeAgent, AllowedServers: []string{"alpha"}})
	for _, name := range []string{"alpha", "beta", ""} {
		event := rt.Event{Type: rt.EventTypeSensitiveDataDetected, Payload: map[string]any{"server_name": name, "activity_id": "activity"}}
		assert.Equal(t, name == "alpha", eventVisibleToCaller(ctx, event), name)
		assert.True(t, eventVisibleToCaller(context.Background(), event))
	}
}
