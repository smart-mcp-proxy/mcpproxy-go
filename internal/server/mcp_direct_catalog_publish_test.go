package server

import (
	"context"
	"sort"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 102 T015/T016 + T021: publication and deny-on-miss.
//
// D13 rule 2 draws a distinction the old directToolPermissions map could not:
//
//	nil catalog   = "not built yet"    -> do NOT deny (the proxy is still coming up)
//	empty catalog = "built, has nothing" -> DO deny every display name
//
// Collapsing those two is how a discovery filter turns into an allow-all at
// exactly the moment upstream discovery is failing, so both directions are
// pinned here rather than left to the filters' own tests.

func TestPublishDirectCatalog_SwapIsVisibleAndGenerationAdvances(t *testing.T) {
	p := &MCPProxyServer{}

	assert.Nil(t, p.loadDirectCatalog(), "no catalog before the first publish")

	first := buildDirectCatalog(fixtureTools(), nil)
	p.publishDirectCatalog(first)

	got := p.loadDirectCatalog()
	require.NotNil(t, got)
	assert.Equal(t, 2, got.Len())
	assert.Equal(t, uint64(1), got.Generation(), "the first publish is generation 1")

	second := buildDirectCatalog(nil, nil)
	p.publishDirectCatalog(second)

	got = p.loadDirectCatalog()
	require.NotNil(t, got)
	assert.Equal(t, 0, got.Len(), "the swap must be wholesale, not a merge")
	assert.Equal(t, uint64(2), got.Generation(),
		"generation must advance once per publish so the skew tests can tell a rebuild "+
			"from a signature-cache warm/evict")
}

// TestPublishDirectCatalog_EmptyIsNotNil is the distinction itself.
func TestPublishDirectCatalog_EmptyIsNotNil(t *testing.T) {
	p := &MCPProxyServer{}
	p.publishDirectCatalog(buildDirectCatalog(nil, nil))

	cat := p.loadDirectCatalog()
	require.NotNil(t, cat, "an empty catalog is a REAL catalog — the filters must deny against it")
	assert.Equal(t, 0, cat.Len())
}

// TestResolveDirectTool_DenyOnMissButNotOnNilCatalog pins the three-way outcome
// the filters share, so both of them cannot drift apart from it.
func TestResolveDirectTool_DenyOnMissButNotOnNilCatalog(t *testing.T) {
	p := &MCPProxyServer{}

	t.Run("nil catalog resolves by parsing and does not deny", func(t *testing.T) {
		entry, decision := p.resolveDirectTool("github__create_issue")
		assert.Nil(t, entry, "there is no entry to hand back without a catalog")
		assert.Equal(t, directResolveNoCatalog, decision,
			"a proxy that has not built its catalog yet must not start hiding tools")
	})

	p.publishDirectCatalog(buildDirectCatalog(fixtureTools(), nil))

	t.Run("known name resolves to its entry", func(t *testing.T) {
		entry, decision := p.resolveDirectTool("github__create_issue")
		require.NotNil(t, entry)
		assert.Equal(t, directResolveFound, decision)
		assert.Equal(t, "github", entry.ServerName)
		assert.Equal(t, "create_issue", entry.ToolName)
	})

	t.Run("unknown name is denied", func(t *testing.T) {
		entry, decision := p.resolveDirectTool("ghost__tool")
		assert.Nil(t, entry)
		assert.Equal(t, directResolveDenied, decision,
			"a name the catalog does not admit must be denied, not waved through by re-parsing it")
	})

	t.Run("a separator-less name is a built-in, not a denial", func(t *testing.T) {
		// Every upstream tool is named through FormatDirectToolName, which always
		// inserts "__". A name without one therefore cannot be an upstream
		// projection — it is a tool this proxy registered itself, and denying it
		// would delete built-ins off their own surface.
		for _, name := range []string{"describe_tool", "retrieve_tools"} {
			entry, decision := p.resolveDirectTool(name)
			assert.Nil(t, entry, "a built-in has no upstream catalog entry")
			assert.Equal(t, directResolveBuiltin, decision, "%s must be kept", name)
		}
	})

	t.Run("a withheld collision is denied in both id forms", func(t *testing.T) {
		p2 := &MCPProxyServer{}
		p2.publishDirectCatalog(buildDirectCatalog([]*config.ToolMetadata{
			{ServerName: "a", Name: "b__c", Hash: "h1"},
			{ServerName: "a__b", Name: "c", Hash: "h2"},
		}, nil))

		entry, decision := p2.resolveDirectTool("a__b__c")
		assert.Nil(t, entry)
		assert.Equal(t, directResolveDenied, decision,
			"a withheld collision must be denied — re-parsing it would pick an origin "+
				"the catalog deliberately refused to choose between")
	})
}

// publishPermsCatalog publishes a catalog carrying exactly the given
// displayName -> requiredPermission pairs.
//
// It exists so tests written against the retired directToolPermissions map keep
// their original intent. Those tests seed a permission tier directly; going
// through buildDirectCatalog would mean synthesizing the annotations that happen
// to DERIVE each tier, which would test the derivation rather than the filter
// the test is actually about.
func publishPermsCatalog(p *MCPProxyServer, perms map[string]string) {
	cat := &directCatalog{
		byDisplayName: make(map[string]*directCatalogEntry, len(perms)),
		displayNames:  make([]string, 0, len(perms)),
	}
	for name, perm := range perms {
		serverName, toolName, _ := ParseDirectToolName(name)
		cat.byDisplayName[name] = &directCatalogEntry{
			DisplayName:        name,
			ServerName:         serverName,
			ToolName:           toolName,
			RequiredPermission: perm,
		}
		cat.displayNames = append(cat.displayNames, name)
	}
	sort.Strings(cat.displayNames)
	p.publishDirectCatalog(cat)
}

// TestFilterDirectModeToolsForAuth_NoCatalogPreservesPreChangeBehaviour pins the
// startup window.
//
// Between process start and the first publish there is no catalog. Denying there
// would serve an empty listing to everyone during startup; allowing everything
// would hand a scoped token tools it must not see. The retired
// directToolPermissions map resolved this by accident — a nil map missed, and a
// miss dropped the tool for a scoped agent — so the behaviour is preserved
// deliberately here rather than left to be rediscovered.
func TestFilterDirectModeToolsForAuth_NoCatalogPreservesPreChangeBehaviour(t *testing.T) {
	proxy := &MCPProxyServer{}
	require.Nil(t, proxy.loadDirectCatalog(), "precondition: no catalog published")

	tools := []mcp.Tool{
		{Name: FormatDirectToolName("github", "get_issue")},
		{Name: "retrieve_tools"},
	}

	t.Run("unauthenticated caller keeps everything", func(t *testing.T) {
		got := proxy.filterDirectModeToolsForAuth(context.Background(), tools)
		assert.Len(t, got, 2, "startup must not blank the listing for an ordinary caller")
	})

	t.Run("scoped agent drops upstream tools but keeps built-ins", func(t *testing.T) {
		ctx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
			Type:           auth.AuthTypeAgent,
			AllowedServers: []string{"github"},
			Permissions:    []string{auth.PermRead},
		})
		got := proxy.filterDirectModeToolsForAuth(ctx, tools)

		names := make([]string, 0, len(got))
		for _, tl := range got {
			names = append(names, tl.Name)
		}
		assert.Equal(t, []string{"retrieve_tools"}, names,
			"with no catalog the tier is unknown, so an upstream tool fails closed for a scoped "+
				"agent — but a built-in, which has no tier to begin with, must survive")
	})
}
