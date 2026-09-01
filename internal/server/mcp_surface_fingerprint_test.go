package server

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func st(name, desc string, opts ...mcp.ToolOption) mcpserver.ServerTool {
	all := append([]mcp.ToolOption{mcp.WithDescription(desc)}, opts...)
	return mcpserver.ServerTool{Tool: mcp.NewTool(name, all...)}
}

// The fingerprint has to cover exactly what a client sees in tools/list, because
// that is also precisely what invalidates a provider's prompt cache: Anthropic's
// rule is that "modifying tool definitions (names, descriptions, parameters)
// invalidates the entire cache".
func TestToolSetFingerprint_CoversWhatAClientCanSee(t *testing.T) {
	base := []mcpserver.ServerTool{st("a", "first"), st("b", "second")}
	require.Equal(t, toolSetFingerprint(base), toolSetFingerprint(base),
		"a fingerprint must be stable for identical input")

	reordered := []mcpserver.ServerTool{st("b", "second"), st("a", "first")}
	assert.Equal(t, toolSetFingerprint(base), toolSetFingerprint(reordered),
		"tools/list is sorted by name, so registration order is not observable")

	assert.NotEqual(t, toolSetFingerprint(base),
		toolSetFingerprint([]mcpserver.ServerTool{st("a", "CHANGED"), st("b", "second")}),
		"a description change invalidates the provider cache and must be detected")

	assert.NotEqual(t, toolSetFingerprint(base),
		toolSetFingerprint([]mcpserver.ServerTool{
			st("a", "first", mcp.WithString("q", mcp.Description("query"))), st("b", "second")}),
		"a parameter change invalidates the provider cache and must be detected")

	assert.NotEqual(t, toolSetFingerprint(base),
		toolSetFingerprint([]mcpserver.ServerTool{st("a", "first")}),
		"a removed tool must be detected")
}

// The direct surface's handlers close over catalog entries, so registry and
// catalog are published as a pair. Skipping a rebuild is only safe when ROUTING
// is unchanged too — and routing can move while the listing stays byte-identical,
// because a display-name collision resolves to one server or the other without
// changing what is listed.
func TestDirectCatalogRoutingFingerprint_CatchesRoutingMovesTheListingHides(t *testing.T) {
	catA := &directCatalog{
		mode:         config.DirectToolResponseModeFull,
		displayNames: []string{"x__foo"},
		byDisplayName: map[string]*directCatalogEntry{
			"x__foo": {DisplayName: "x__foo", ServerName: "alpha", ToolName: "foo", Hash: "h1"},
		},
	}
	same := &directCatalog{
		mode:         config.DirectToolResponseModeFull,
		displayNames: []string{"x__foo"},
		byDisplayName: map[string]*directCatalogEntry{
			"x__foo": {DisplayName: "x__foo", ServerName: "alpha", ToolName: "foo", Hash: "h1"},
		},
	}
	require.Equal(t, catA.routingFingerprint(), same.routingFingerprint())

	// Identical DisplayName, different upstream: the listing cannot show this.
	rerouted := &directCatalog{
		mode:         config.DirectToolResponseModeFull,
		displayNames: []string{"x__foo"},
		byDisplayName: map[string]*directCatalogEntry{
			"x__foo": {DisplayName: "x__foo", ServerName: "BETA", ToolName: "foo", Hash: "h1"},
		},
	}
	assert.NotEqual(t, catA.routingFingerprint(), rerouted.routingFingerprint(),
		"same name, different server — skipping this rebuild would leave stale routing")

	// Permission is derived from upstream annotations and gates scoped tokens.
	reperm := &directCatalog{
		mode:         config.DirectToolResponseModeFull,
		displayNames: []string{"x__foo"},
		byDisplayName: map[string]*directCatalogEntry{
			"x__foo": {DisplayName: "x__foo", ServerName: "alpha", ToolName: "foo", Hash: "h1",
				RequiredPermission: "write"},
		},
	}
	assert.NotEqual(t, catA.routingFingerprint(), reperm.routingFingerprint(),
		"a permission change must not be skipped")

	modeFlip := &directCatalog{
		mode:          config.DirectToolResponseModeDeferred,
		displayNames:  catA.displayNames,
		byDisplayName: catA.byDisplayName,
	}
	assert.NotEqual(t, catA.routingFingerprint(), modeFlip.routingFingerprint(),
		"serialization mode changes what is rendered")
}

// The guard's whole purpose: an unchanged surface must not re-register, because
// SetTools notifies every connected client unconditionally.
func TestRefreshDirectModeTools_UnchangedContentDoesNotRepublish(t *testing.T) {
	p := newReloadGuardProxy(t, config.DirectToolResponseModeFull)
	first := p.loadDirectCatalog().Generation()

	p.RefreshDirectModeTools()
	p.RefreshDirectModeTools()
	p.RefreshDirectModeTools()

	assert.Equal(t, first, p.loadDirectCatalog().Generation(),
		"nothing changed, so no rebuild was published and no client was notified")
}

// ...but a real change must still get through. Guarding must not become muting.
func TestRefreshDirectModeTools_RealChangeStillRepublishes(t *testing.T) {
	p := newReloadGuardProxy(t, config.DirectToolResponseModeFull)
	before := p.loadDirectCatalog().Generation()

	p.config.DirectToolResponseMode = config.DirectToolResponseModeDeferred
	p.RefreshDirectModeTools()

	assert.Equal(t, before+1, p.loadDirectCatalog().Generation())
}

// The code-execution surface is built from built-ins ONLY — buildCodeExecModeTools
// never iterates upstreams — yet it was rebuilt on every servers.changed. Measured
// on one ordinary 4-server startup: 9 refreshes, tool_count 7 every time, so 9 of 9
// notifications told clients about a change that could not have happened.
func TestRefreshCodeExecModeTools_UnchangedContentDoesNotRepublish(t *testing.T) {
	p := newReloadGuardProxy(t, config.DirectToolResponseModeFull)
	require.NotNil(t, p.codeExecServer)

	p.RefreshCodeExecModeTools()
	base := p.codeExecPublishes.Load()

	p.RefreshCodeExecModeTools()
	p.RefreshCodeExecModeTools()

	assert.Equal(t, base, p.codeExecPublishes.Load(),
		"the code-execution surface cannot change on server churn")
}

func TestRefreshCodeExecModeTools_RealChangeStillRepublishes(t *testing.T) {
	p := newReloadGuardProxy(t, config.DirectToolResponseModeFull)
	p.RefreshCodeExecModeTools()
	base := p.codeExecPublishes.Load()

	p.config.EnableCodeExecution = !p.config.EnableCodeExecution
	p.RefreshCodeExecModeTools()

	assert.Equal(t, base+1, p.codeExecPublishes.Load(),
		"flipping enable_code_execution swaps the live tool for the disabled stub")
}

// Both halves of the guard are load-bearing, and the routing half is the one no
// integration test can reach: the reload harness has no upstreams, so a
// routing-only change cannot be staged there. Pinned here instead.
func TestDirectSurfaceUnchanged_RequiresBothHalves(t *testing.T) {
	assert.True(t, directSurfaceUnchanged("t1", "r1", "t1", "r1"),
		"identical listing and identical routing is the skip case")

	assert.False(t, directSurfaceUnchanged("t1", "r1", "t2", "r1"),
		"the listing moved")
	assert.False(t, directSurfaceUnchanged("t1", "r1", "t1", "r2"),
		"the LISTING is identical but dispatch moved — skipping would leave stale routing")

	assert.False(t, directSurfaceUnchanged("", "", "t1", "r1"),
		"nothing published yet: the first rebuild must always proceed")
}
