package server

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 133 FR-022: every built-in MCP tool registered on any routing surface
// (with the optional surfaces switched on, and a lazily built single-server surface) is classified in the door
// registry, and every MCP row still names a registered tool. Per-surface
// comparison and the admin tools' operation descriptors land in PR-1b.
func TestAdminDoorRegistry_CoversEveryBuiltinTool(t *testing.T) {
	p, _ := createTestProxyWithRuntimeCfg(t, nil, func(c *config.Config) {
		c.EnableSkills = true
		c.EnablePrompts = true
	})
	names := map[string]bool{}
	for n := range p.server.ListTools() {
		names[n] = true
	}
	for _, mode := range []string{config.RoutingModeRetrieveTools, config.RoutingModeDirect, config.RoutingModeCodeExecution} {
		if s := p.GetMCPServerForMode(mode); s != nil {
			for n := range s.ListTools() {
				names[n] = true
			}
		}
	}
	// The single-server surface (/mcp/s/<name>) is built lazily; build one.
	sp, _, _ := newProfilesV3FixtureUpstreams(t, nil)
	single := sp.SingleServerMCP("github")
	require.NotNil(t, single, "single-server surface must be constructible in the fixture")
	for n := range single.ListTools() {
		names[n] = true
	}
	// With skills on, the single-server surface registers the skills built-ins.
	ssp, _, _ := newProfilesV3FixtureUpstreams(t, func(cfg *config.Config) { cfg.EnableSkills = true })
	ssp.RefreshSkillsAvailability()
	skilled := ssp.SingleServerMCP("github")
	require.NotNil(t, skilled, "skills-enabled single-server surface must be constructible")
	for n := range skilled.ListTools() {
		names[n] = true
	}
	var doors []string
	for n := range names {
		doors = append(doors, "MCP "+n)
	}
	sort.Strings(doors)
	require.GreaterOrEqual(t, len(doors), 10)
	missing, stale := audit.DiffDoors("MCP ", doors)
	require.Empty(t, missing, "built-in tools with no registry row (internal/audit/admindoors.go)")
	require.Empty(t, stale, "registry rows whose built-in tool no longer exists")
}
