package server

import (
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 102 T024 (D15/R14): the direct surface must be serving before the first
// servers.changed event.
//
// Today initRoutingModeServers registers nothing on p.directServer — it leaves a
// comment saying direct tools are built lazily on servers.changed — so until the
// first upstream reconcile the direct surface lists ZERO tools, including its own
// built-ins. FR-009 requires describe_tool to be present on that surface, and
// "present once an upstream happens to connect" is not present.
//
// This also makes the empty catalog real at init, which is what T027's
// SubscribeEvents hoist then becomes load-bearing for: with a published empty
// catalog the filters deny on miss, so a DROPPED first servers.changed no longer
// self-heals the way a nil catalog accidentally did.

func TestInitRoutingModeServers_DirectSurfaceServesBuiltinsImmediately(t *testing.T) {
	p := newDirectInitTestProxy(t)

	require.NotNil(t, p.directServer, "precondition: the direct server is constructed")

	registered := p.directServer.ListTools()
	names := make([]string, 0, len(registered))
	for name := range registered {
		names = append(names, name)
	}

	assert.Contains(t, names, "describe_tool",
		"describe_tool must be on the direct surface before any servers.changed fires (FR-009/FR-018) — "+
			"a built-in that appears only once an upstream connects is not a built-in")
}

// TestInitRoutingModeServers_PublishesANonNilCatalog is the other half of D15.
// The initial rebuild must leave a REAL empty catalog behind, not nil: nil means
// "not built yet" and the filters decline to deny, so an install that never
// completed its first rebuild would silently run without direct-surface scope
// filtering.
func TestInitRoutingModeServers_PublishesANonNilCatalog(t *testing.T) {
	p := newDirectInitTestProxy(t)

	cat := p.loadDirectCatalog()
	require.NotNil(t, cat,
		"initRoutingModeServers must publish a catalog, so the filters are in deny-on-miss "+
			"from the first request rather than from the first upstream reconcile")
	assert.Equal(t, 0, cat.Len(), "with no upstreams connected the catalog is empty but present")
	assert.Equal(t, uint64(1), cat.Generation(), "the initial rebuild is generation 1")
}

// TestBuildDirectModeTools_KeepsDescribeToolOnTheDiscoverToolsErrorPath is
// FR-018's teeth.
//
// The error path returns early. If describe_tool were appended only on the happy
// path, the first upstream hiccup would call SetTools with a slice that omits it
// — and SetTools REPLACES the whole registry, so the built-in would vanish from
// the direct surface until an unrelated successful rebuild put it back.
func TestBuildDirectModeTools_KeepsDescribeToolOnTheDiscoverToolsErrorPath(t *testing.T) {
	p := newDirectInitTestProxy(t)

	// No upstream manager wired: DiscoverTools cannot succeed, which is the
	// shape of the error path.
	tools, cat := p.buildDirectModeTools()

	require.NotNil(t, cat, "the error path must still yield a non-nil catalog (D13 rule 2)")

	found := false
	for _, st := range tools {
		if st.Tool.Name == "describe_tool" {
			found = true
		}
	}
	assert.True(t, found,
		"describe_tool must survive a DiscoverTools failure: SetTools replaces the whole registry, "+
			"so dropping it here deletes the built-in from the live surface")
}

// newDirectInitTestProxy builds the minimum proxy initRoutingModeServers needs:
// a config (it reads RoutingMode and EnablePrompts) and a logger. No upstream
// manager — DiscoverTools therefore fails, which is deliberate for these tests.
func newDirectInitTestProxy(t *testing.T) *MCPProxyServer {
	t.Helper()

	p := &MCPProxyServer{
		config: &config.Config{RoutingMode: config.RoutingModeDirect},
		logger: zap.NewNop(),
	}
	p.initRoutingModeServers()
	return p
}
