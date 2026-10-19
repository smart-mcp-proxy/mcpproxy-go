package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A quarantined server added through the MCP `upstream_servers add` door must
// get the same inspection exemption the REST door grants, otherwise the
// supervisor's quarantine policy refuses (or tears down) the connection and no
// tool definition is ever captured for review. Seen live with Roblox StudioMCP
// (2026-10-13): added via MCP, connected, then disconnected 5s later; review
// stayed "not_captured" until a manual fetch.
func TestHandleAddUpstream_QuarantinedServerGetsInspectionExemption(t *testing.T) {
	p := newAddFromRegistryTestServer(t)
	sup := p.mainServer.runtime.Supervisor()
	require.NotNil(t, sup)

	req := addRequest("roblox", "fixture.py")
	// enabled=false skips the connection wait; the exemption is granted on
	// admission, not on connect, exactly as the REST door does.
	req.Params.Arguments.(map[string]interface{})["enabled"] = false
	res, err := p.handleAddUpstream(context.Background(), req)
	require.NoError(t, err)
	require.False(t, res.IsError, res.Content[0].(mcp.TextContent).Text)

	got, err := p.mainServer.runtime.StorageManager().GetUpstreamServer("roblox")
	require.NoError(t, err)
	require.True(t, got.Quarantined, "a manual-trust add is quarantined")
	assert.True(t, sup.IsInspectionExempted("roblox"),
		"MCP add must grant the inspection exemption so the quarantined server can be connected for review")
}
