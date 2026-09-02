package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
)

// #1166 follow-up (G4). Two consumers recompute the `quarantined_servers`
// scalar from a PER-ENTRY `quarantined` key — httpapi.filterUpstreamStatsServers
// on the scoped-caller path, and contracts.ConvertUpstreamStatsToServerStats —
// and NEITHER upstream_stats producer emitted it. Both therefore counted zero
// unconditionally, so a scoped caller's security surface read clean even when
// one of its own allowed servers was quarantined.
//
// The oracle is the CONSUMER's answer, not the presence of a map key: asserting
// `entry["quarantined"] != nil` would pass on a producer that emitted the key
// under a different name or type and left the count broken all the same.
func TestUpstreamStatsFromSnapshot_EntryCarriesQuarantined(t *testing.T) {
	view := stateview.New()
	view.UpdateServer("alpha", func(st *stateview.ServerStatus) {
		st.Name = "alpha"
		st.Config = &config.ServerConfig{Name: "alpha", Protocol: "stdio"}
		st.Enabled = true
		st.Connected = true
		st.State = "Ready"
		st.Quarantined = true
		st.ToolCount = 5
	})
	view.UpdateServer("beta", func(st *stateview.ServerStatus) {
		st.Name = "beta"
		st.Config = &config.ServerConfig{Name: "beta", Protocol: "stdio"}
		st.Enabled = true
		st.Connected = true
		st.State = "Ready"
		st.ToolCount = 7
	})

	stats := upstreamStatsFromSnapshot(view.Snapshot(), false)

	// Precondition: the producer really did describe both servers, so a zero
	// count below would mean the key is missing rather than the fixture.
	servers, ok := stats["servers"].(map[string]interface{})
	require.True(t, ok, "no servers map in %#v", stats)
	require.Len(t, servers, 2)
	require.Equal(t, 1, stats["quarantined_servers"],
		"precondition: the producer's own scalar counts the quarantined server")

	derived := contracts.ConvertUpstreamStatsToServerStats(stats)
	assert.Equal(t, 1, derived.QuarantinedServers,
		"a consumer recomputing the scalar from the entries must reach the producer's own answer")
	assert.Equal(t, 2, derived.TotalServers)
}
