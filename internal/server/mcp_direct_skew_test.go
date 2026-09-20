package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 102 US4 (T070–T072) — the publication-skew window.
//
// FR-017 asks for the catalog and mcp-go's tool registry to be "rebuilt
// atomically, so no window exposes one without the other". They are two
// publications and cannot be made one — mcp-go owns its registry read — so
// T001 accepted the requirement as a SAFETY PROPERTY delivered by ordering
// (D13): SetTools lands the registry first, the catalog is published
// immediately after, and the guarantee is directional.
//
// This file is where that claim stops being an assertion. Each case stages the
// real window — real render, real SetTools, real publish, paused in between —
// and observes what a concurrent session actually sees. What the window exposes
// is recorded here, including the cases it does NOT close.
//
// DiscoverTools is the one thing replaced by a fixture: upstream.Manager is
// concrete and only returns tools from CONNECTED clients (research.md R12), so
// every test in this feature stages its projection. Everything downstream of it
// — renderDirectTools, withDirectBuiltins, SetTools, publishDirectCatalog — is
// the production path.

type skewFixture struct {
	proxy *MCPProxyServer
}

func newSkewFixture(t *testing.T, tools []*config.ToolMetadata) *skewFixture {
	return newSkewFixtureInMode(t, tools, config.DirectToolResponseModeFull)
}

// newSkewFixtureInMode is the deferred-capable constructor. The residual cases
// MUST use deferred: in full mode a schema change is visible in the listing, so
// "silently stale" would be proven by a listing that is not silent at all.
func newSkewFixtureInMode(t *testing.T, tools []*config.ToolMetadata, mode string) *skewFixture {
	t.Helper()
	p := createTestMCPProxyServer(t)
	p.config.RoutingMode = config.RoutingModeDirect
	p.config.DirectToolResponseMode = mode

	servers := map[string]struct{}{}
	for _, tool := range tools {
		servers[tool.ServerName] = struct{}{}
	}
	for name := range servers {
		require.NoError(t, p.storage.SaveUpstreamServer(&config.ServerConfig{Name: name, Enabled: true}))
	}
	for _, tool := range tools {
		require.NoError(t, p.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: tool.ServerName, ToolName: tool.Name,
			Status: storage.ToolApprovalStatusApproved,
		}))
	}

	f := &skewFixture{proxy: p}
	f.publish(t, tools)
	return f
}

// publish runs one COMPLETE rebuild: render, SetTools, publish.
func (f *skewFixture) publish(t *testing.T, tools []*config.ToolMetadata) {
	t.Helper()
	f.rebuildPaused(t, tools, func() {})
}

// rebuildPaused stages the window: the registry is updated, `during` runs while
// the catalog is still the PREVIOUS generation, and only then is the new
// catalog published. This is RefreshDirectModeTools' own ordering, with the
// pause where a concurrent request would land.
func (f *skewFixture) rebuildPaused(t *testing.T, tools []*config.ToolMetadata, during func()) {
	t.Helper()
	p := f.proxy
	for _, tool := range tools {
		p.sigCache.Warm(tool.Hash, tool.ParamsJSON, tool.Description)
	}
	cat := directCatalogFor(p, tools)
	rendered := p.withDirectBuiltins(p.renderDirectTools(cat))

	p.directServer.SetTools(rendered...)
	during()
	p.publishDirectCatalog(cat)
}

// wireEntry is the marshalled registry entry for one display name — the exact
// bytes a client receives.
func (f *skewFixture) wireEntry(t *testing.T, display string) string {
	t.Helper()
	st, ok := f.proxy.directServer.ListTools()[display]
	require.Truef(t, ok, "%q is not registered", display)
	raw, err := json.Marshal(st.Tool)
	require.NoError(t, err)
	return string(raw)
}

// registeredHandler returns the handler mcp-go would actually dispatch to,
// rather than one the test built for itself.
func (f *skewFixture) registeredHandler(t *testing.T, display string) mcpserver.ToolHandlerFunc {
	t.Helper()
	st, ok := f.proxy.directServer.ListTools()[display]
	require.Truef(t, ok, "%q is not registered", display)
	return st.Handler
}

// listed is what this session's tools/list actually serves right now: the live
// registry through the live filter chain.
func (f *skewFixture) listed(ctx context.Context) map[string]struct{} {
	p := f.proxy
	registered := p.directServer.ListTools()
	tools := make([]mcp.Tool, 0, len(registered))
	for _, st := range registered {
		tools = append(tools, st.Tool)
	}
	out := map[string]struct{}{}
	for _, tool := range p.filterDirectToolsForAgentCallability(ctx, p.filterDirectModeToolsForAuth(ctx, tools)) {
		out[tool.Name] = struct{}{}
	}
	return out
}

func (f *skewFixture) describable(ctx context.Context, id string) bool {
	_, ok := f.proxy.resolveDirectDescribeID(ctx, id)
	return ok
}

func skewTool(server, name, desc, params string, ann *config.ToolAnnotations) *config.ToolMetadata {
	return &config.ToolMetadata{
		ServerName: server, Name: name, Description: desc, ParamsJSON: params,
		// The hash covers description + schemas, exactly as the indexer's does,
		// so a definition change moves it and a cosmetic one does not.
		Hash:        "h-" + server + "-" + name + "-" + hashish(desc+params+annKey(ann)),
		Annotations: ann,
	}
}

func annKey(a *config.ToolAnnotations) string {
	if a == nil {
		return "nil"
	}
	raw, _ := json.Marshal(a)
	return string(raw)
}

func hashish(s string) string {
	var h uint64 = 1469598103934665603
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return string([]byte{byte('a' + h%26), byte('a' + (h/26)%26), byte('a' + (h/676)%26)})
}

var skewBase = func() []*config.ToolMetadata {
	return []*config.ToolMetadata{
		skewTool("fs", "read", "Read a file", `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`,
			&config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
		skewTool("fs", "write", "Write a file", `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`,
			&config.ToolAnnotations{ReadOnlyHint: boolPtr(false)}),
	}
}

// ---------------------------------------------------------------------------
// Group 1 — closed by design (the ordering)
// ---------------------------------------------------------------------------

// An ADDED name is in the registry before its catalog entry lands.
//
// Spec 105 PR F (FR-008, FR008-G1) closes the window this test used to
// document as a residual. Before this fix, both filters resolved a rendered
// tool's identity by looking up its NAME in whatever catalog happened to be
// live — which, in this window, is still the PREVIOUS generation, one that
// does not admit "fs__stat" at all — so a scoped session was denied and an
// unscoped one fell through to the "serve the raw registry" fallback, seeing
// the name with nothing to check it against.
//
// Now renderDirectTools stamps each tool with the identity of the SAME
// catalog build that produced its handler (Spec 105 FR-008), and the filters
// read that stamp first. Since SetTools lands the NEW registry (with its
// stamps) before this pause even starts, every session — scoped or not — sees
// "fs__stat" immediately, correctly attributed to "fs", with no residual left
// to document. describe_tool is untouched by this PR and still answers from
// the published catalog, so it remains one generation behind for the width of
// the window — the SAFE "listed-but-undescribable" direction SC-007 (Spec
// 102) has always permitted, never the reverse.
func TestSkew_AddedNameBeforeItsCatalogEntry(t *testing.T) {
	f := newSkewFixture(t, skewBase())
	unscoped := context.Background()
	scoped := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "reader",
		AllowedServers: []string{"fs"},
		Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
	})

	added := append(skewBase(), skewTool("fs", "stat", "Stat a path", `{"type":"object"}`,
		&config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}))
	// The added tool is approved under its own name: on the direct surface a
	// catalog tool with NO approval record is pending while the quarantine
	// gate is active (Spec 105 FR-009), which would hide it from the scoped
	// session for a reason unrelated to the skew ordering under test.
	require.NoError(t, f.proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "fs", ToolName: "stat", Status: storage.ToolApprovalStatusApproved,
	}))

	f.rebuildPaused(t, added, func() {
		assert.Contains(t, f.listed(scoped), "fs__stat",
			"the stamp on the newly-registered tool already says owner \"fs\", in scope, so a scoped session sees it immediately")
		assert.False(t, f.describable(scoped, "fs__stat"),
			"describe still answers from the previous, unpublished catalog — listed-but-undescribable, the safe direction")

		assert.Contains(t, f.listed(unscoped), "fs__stat")
		assert.False(t, f.describable(unscoped, "fs__stat"))
	})

	// After the publish both sessions agree, in both directions.
	for name, ctx := range map[string]context.Context{"unscoped": unscoped, "scoped": scoped} {
		assert.Containsf(t, f.listed(ctx), "fs__stat", "%s: listed after the publish", name)
		assert.Truef(t, f.describable(ctx, "fs__stat"), "%s: describable after the publish", name)
	}
}

// A REMOVED name leaves the registry first, so during the window the catalog
// still admits it. This is the one direction the ordering does NOT close.
func TestSkew_RemovedNameIsDescribableWhileUnlisted(t *testing.T) {
	f := newSkewFixture(t, skewBase())
	ctx := context.Background()

	remaining := []*config.ToolMetadata{skewBase()[0]}

	var describableWhileUnlisted bool
	f.rebuildPaused(t, remaining, func() {
		_, listed := f.listed(ctx)["fs__write"]
		describableWhileUnlisted = !listed && f.describable(ctx, "fs__write")
	})

	// Recorded, not asserted away: for the width of the window a removed tool
	// can still be described from the previous snapshot. It is stale, not a
	// disclosure — the same session could have described it one request earlier,
	// and the definition it gets is the one it was already served. The window
	// closes at the publish.
	assert.True(t, describableWhileUnlisted,
		"documenting the residual: removals expose describable-but-unlisted for the width of the window")
	assert.False(t, f.describable(ctx, "fs__write"), "and it is closed once the catalog is published")
}

// A DESCRIPTION change is visible in the registry before the catalog carries
// it. Both halves stay self-consistent: the listing shows the new text, and
// describe answers from the old snapshot until the publish.
func TestSkew_DescriptionChangeIsSelfConsistentOnBothSides(t *testing.T) {
	f := newSkewFixture(t, skewBase())
	ctx := context.Background()

	changed := skewBase()
	changed[0] = skewTool("fs", "read", "Read a file, now with feeling",
		changed[0].ParamsJSON, changed[0].Annotations)

	f.rebuildPaused(t, changed, func() {
		assert.Contains(t, f.listed(ctx), "fs__read", "the tool is listed throughout")
		require.True(t, f.describable(ctx, "fs__read"), "and describable throughout")

		entry, _ := f.proxy.resolveDirectDescribeID(ctx, "fs__read")
		assert.Equal(t, "Read a file", entry.Description,
			"describe answers from the published snapshot, which is still the old one")
	})

	entry, ok := f.proxy.resolveDirectDescribeID(ctx, "fs__read")
	require.True(t, ok)
	assert.Equal(t, "Read a file, now with feeling", entry.Description)
}

// An ORIGIN FLIP: the SAME display name, owned by a different upstream in the
// next generation. Only the "__" ambiguity makes this expressible —
// "a__b__c" is (server "a", tool "b__c") or (server "a__b", tool "c") — and it
// is the sharpest form of the skew question.
//
// Spec 105 PR F (FR-008, FR008-G1/G3/G5) closes it. Before this fix, the
// filters scope-checked "a__b__c" by looking its NAME up in whatever catalog
// happened to be live — during this window, still the OLD one, which says the
// owner is "a" — while the registry already held the NEW origin's handler, so
// an old-scoped token saw the tool listed under the wrong owner and was only
// refused once it actually dispatched, by a message that NAMED the new owner
// (D12's disclosure).
//
// Now renderDirectTools stamps each registered tool with the identity of the
// SAME build that produced its handler, and the filters read that stamp
// first — never a fresh catalog lookup. Since SetTools lands the NEW registry
// (new stamp: owner "a__b") before this pause starts, an old-only token is
// excluded from the listing immediately, and if a call is still attempted
// against the stale/registered handler directly (bypassing mcp-go's own
// call-time re-evaluation, which would otherwise answer the SAME
// unregistered-name envelope first — Spec 105 T086a), the refusal it gets
// names neither origin.
//
// An earlier version of this test flipped alpha__run to beta__run, which are
// different display names and therefore not an origin flip at all; it asserted
// nothing. (Found by cross-model review.)
func TestSkew_OriginFlipNeverSplitsScopeFromDispatch(t *testing.T) {
	const display = "a__b__c"

	oldOrigin := []*config.ToolMetadata{
		skewTool("a", "b__c", "Owned by a", `{"type":"object"}`, &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
	}
	newOrigin := []*config.ToolMetadata{
		skewTool("a__b", "c", "Owned by a__b", `{"type":"object"}`, &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
	}
	require.Equal(t, display, FormatDirectToolName("a", "b__c"))
	require.Equal(t, display, FormatDirectToolName("a__b", "c"),
		"both origins must flatten to ONE display name, or this is not an origin flip")

	f := newSkewFixture(t, oldOrigin)
	require.NoError(t, f.proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "a__b", Enabled: true}))
	require.NoError(t, f.proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "a__b", ToolName: "c", Status: storage.ToolApprovalStatusApproved,
	}))

	// A token that may reach the OLD origin but not the new one.
	oldOnly := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "a-only",
		AllowedServers: []string{"a"},
		Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
	})

	f.rebuildPaused(t, newOrigin, func() {
		// The stale CATALOG still says this name belongs to "a" — describe_tool,
		// unchanged by this PR, still resolves through it — but the LISTING no
		// longer does: the registered tool's own stamp already says "a__b",
		// out of scope for this token, so it is absent immediately.
		entry, ok := f.proxy.resolveDirectDescribeID(oldOnly, display)
		require.True(t, ok, "the stale catalog still resolves the name for describe")
		require.Equal(t, "a", entry.ServerName, "…to the OLD origin — a residual describe_tool is out of this PR's scope")

		assert.NotContains(t, f.listed(oldOnly), display,
			"the listing reads the NEW stamp on the registered tool, not the stale catalog, so it is excluded immediately")

		// The registry already holds the NEW origin's handler, invoked here
		// directly (bypassing mcp-go's own call-time filter re-evaluation,
		// which — now that the filters are stamp-based — would already refuse
		// this with the SAME envelope a wholly unregistered name gets, before
		// the handler ever ran; see TestDirectFullMode_InSeamScopeRefusalMatchesUnregisteredName).
		// This is the handler's OWN defense-in-depth check, and D12 forbids it
		// from naming either origin.
		result, err := f.registeredHandler(t, display)(oldOnly, mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: display},
		})
		require.NoError(t, err)
		require.True(t, result.IsError,
			"a token scoped to the old origin must not reach the new one through a stale listing")
		text := result.Content[0].(mcp.TextContent).Text
		// "a__b__c" (the caller-supplied display name, which D12 permits
		// echoing) happens to contain "a__b" as a raw substring, so the
		// disclosure check is against the OLD message's own distinguishing
		// phrasing — naming the origin AS a server, in a sentence that
		// confirms a scope check fired — not against that coincidental
		// substring.
		assert.Equal(t, "tool 'a__b__c' not found", text,
			"the refusal must be worded exactly like an unregistered name's, never naming the origin actually dispatched to (D12)")
		assert.NotContains(t, text, "does not have access", "nor disclose that a scope check is what fired")
		assert.NotContains(t, text, "Owned by", "nor leak the entry's own description")
	})

	// After the publish the listing agrees with the registry again: the name
	// now belongs to an origin this token cannot see, so it is gone.
	assert.NotContains(t, f.listed(oldOnly), display)
	assert.False(t, f.describable(oldOnly, display))
}

// ---------------------------------------------------------------------------
// Group 2 — closed by withholding
// ---------------------------------------------------------------------------

// A collision that appears WITHIN the new generation is withheld by the
// builder, so it is never registered and never resolvable — in the window or
// after it.
func TestSkew_WithinGenerationCollisionIsNeverExposed(t *testing.T) {
	f := newSkewFixture(t, skewBase())
	ctx := context.Background()

	colliding := append(skewBase(),
		skewTool("clash", "a__b", "First origin", `{"type":"object"}`, nil),
		skewTool("clash__a", "b", "Second origin", `{"type":"object"}`, nil),
	)

	f.rebuildPaused(t, colliding, func() {
		assert.NotContains(t, f.listed(ctx), "clash__a__b")
		assert.False(t, f.describable(ctx, "clash__a__b"))
	})

	assert.NotContains(t, f.listed(ctx), "clash__a__b",
		"a withheld collision is absent after the publish too — it was never rendered")
	assert.False(t, f.describable(ctx, "clash__a__b"))
}

// ---------------------------------------------------------------------------
// Group 3 — the three documented residuals
// ---------------------------------------------------------------------------

// Residual 1 (T002): an INPUT-SCHEMA-only change can be described one
// generation stale, invisibly — the listing does not change, because the 085
// grammar collapses the edited nested property to "~" and the rendered
// signature is byte-identical.
//
// The edit must be semantically different but signature-identical, and the new
// hash must be warmed before the rebuild renders, or Peek misses and the
// description visibly loses its suffix instead — which would make this a
// visible change and prove nothing about an invisible one.
func TestSkew_InputSchemaOnlyChangeIsSilentlyStale(t *testing.T) {
	nested := func(inner string) string {
		return `{"type":"object","properties":{"opts":{"type":"object","properties":{"mode":{"type":"` + inner + `"}}}},"required":[]}`
	}
	before := []*config.ToolMetadata{
		skewTool("fs", "read", "Read a file", nested("string"), &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
	}
	after := []*config.ToolMetadata{
		skewTool("fs", "read", "Read a file", nested("integer"), &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
	}
	require.NotEqual(t, before[0].Hash, after[0].Hash, "the edit must move the hash")

	f := newSkewFixtureInMode(t, before, config.DirectToolResponseModeDeferred)
	ctx := context.Background()

	// Both signatures must render identically, or the description would change
	// visibly and this would be testing the wrong thing.
	f.proxy.sigCache.Warm(after[0].Hash, after[0].ParamsJSON, after[0].Description)
	sigBefore, okBefore := f.proxy.sigCache.Peek(before[0].Hash)
	sigAfter, okAfter := f.proxy.sigCache.Peek(after[0].Hash)
	require.True(t, okBefore && okAfter)
	require.Equal(t, sigBefore.Sig, sigAfter.Sig,
		"the fixture edit must be signature-identical, or the staleness would be visible")

	wireBefore := f.wireEntry(t, "fs__read")

	f.rebuildPaused(t, after, func() {
		entry, ok := f.proxy.resolveDirectDescribeID(ctx, "fs__read")
		require.True(t, ok)
		assert.Equal(t, nested("string"), entry.ParamsJSON,
			"documenting residual 1: the schema is one generation stale")
	})

	// …and NOTHING in the listing said so. In deferred mode the entry carries
	// the placeholder and a signature that collapsed the edited nested property
	// to "~", so the wire bytes are unchanged across a semantic schema change.
	// That silence is the residual; in full mode the schema would have moved
	// visibly and there would be nothing to document.
	assert.Equal(t, wireBefore, f.wireEntry(t, "fs__read"),
		"the listing must be byte-identical across the change, or the staleness is not silent")

	entry, _ := f.proxy.resolveDirectDescribeID(ctx, "fs__read")
	assert.Equal(t, nested("integer"), entry.ParamsJSON, "the window closes at the publish")
}

// Residual 2 (T002): an OUTPUT-SCHEMA-only change is stale in the same window.
// Advisory only — nothing dispatches on it — and it does not self-heal, which
// is why it was accepted rather than closed.
func TestSkew_OutputSchemaOnlyChangeIsSilentlyStale(t *testing.T) {
	base := skewBase()
	base[0].OutputSchemaJSON = `{"type":"object","properties":{"text":{"type":"string"}}}`
	f := newSkewFixtureInMode(t, base, config.DirectToolResponseModeDeferred)
	ctx := context.Background()

	after := skewBase()
	after[0].OutputSchemaJSON = `{"type":"object","properties":{"bytes":{"type":"integer"}}}`
	after[0].Hash = base[0].Hash + "-out"

	wireBefore := f.wireEntry(t, "fs__read")

	f.rebuildPaused(t, after, func() {
		entry, ok := f.proxy.resolveDirectDescribeID(ctx, "fs__read")
		require.True(t, ok)
		assert.Contains(t, entry.OutputSchemaJSON, "text",
			"documenting residual 2: the output schema is one generation stale")
	})

	// Silent by construction: deferred entries strip outputSchema entirely
	// (FR-006/R2), so the listing cannot show the change even in principle.
	assert.Equal(t, wireBefore, f.wireEntry(t, "fs__read"))
	assert.NotContains(t, wireBefore, "outputSchema")

	entry, _ := f.proxy.resolveDirectDescribeID(ctx, "fs__read")
	assert.Contains(t, entry.OutputSchemaJSON, "bytes")
}

// An ANNOTATIONS-only change — read becoming destructive — used to be listed
// one generation stale (Spec 102's "residual 3"), because the listing filter
// re-derived the tier from a fresh catalog lookup, and the catalog is still
// the OLD (read-tier) generation for the width of this window.
//
// Spec 105 PR F closes it: renderDirectTools now stamps each registered tool
// with the tier of the SAME build that produced its handler, so as soon as
// SetTools lands the NEW (destructive-tier) registration — before this pause
// even starts — a read-scoped token's listing reflects it immediately. No
// earlier revocation is required (US3 scenario 3): the withholding tracks the
// handler actually registered at each seam, exactly like the call-time
// authorization already did.
func TestSkew_AnnotationsOnlyChangeIsStaleButNeverAdmitsTheCall(t *testing.T) {
	before := []*config.ToolMetadata{
		skewTool("fs", "purge", "Purge", `{"type":"object"}`, &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)}),
	}
	after := []*config.ToolMetadata{
		skewTool("fs", "purge", "Purge", `{"type":"object"}`, &config.ToolAnnotations{DestructiveHint: boolPtr(true)}),
	}

	f := newSkewFixture(t, before)
	readOnly := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "reader",
		AllowedServers: []string{"fs"},
		Permissions:    []string{auth.PermRead},
	})

	f.rebuildPaused(t, after, func() {
		assert.NotContains(t, f.listed(readOnly), "fs__purge",
			"the newly-registered tool's own stamp already says tier destructive, so a read-scoped token is withheld immediately, not one generation stale")

		// …and the call is refused, exactly as before this PR: call-time
		// authorization never reads the catalog OR the stamp — the handler
		// re-derives the tier from the annotations it was registered with.
		// Dispatched through the handler mcp-go actually holds — building one
		// here would prove only that a handler constructed by the test
		// refuses, not that the REGISTERED one does.
		result, err := f.registeredHandler(t, "fs__purge")(readOnly, mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: "fs__purge"},
		})
		require.NoError(t, err)
		require.True(t, result.IsError)
		assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "Permission denied",
			"call-time authorization never reads the catalog, so the stale listing cannot admit the call")
	})
}

// ---------------------------------------------------------------------------
// T071 — the two NO-REBUILD cache cases
// ---------------------------------------------------------------------------

// The signature cache mutates independently of rebuilds. A miss that later warms
// must not make a listed tool vanish: the discriminator reads the STORED
// renderedDescription, it does not re-render.
func TestSkew_SignatureCacheWarmAfterRegistrationChangesNothing(t *testing.T) {
	tools := skewBase()
	p := createTestMCPProxyServer(t)
	p.config.RoutingMode = config.RoutingModeDirect
	p.config.DirectToolResponseMode = config.DirectToolResponseModeDeferred
	require.NoError(t, p.storage.SaveUpstreamServer(&config.ServerConfig{Name: "fs", Enabled: true}))
	for _, tool := range tools {
		require.NoError(t, p.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "fs", ToolName: tool.Name, Status: storage.ToolApprovalStatusApproved,
		}))
	}

	// Rendered with a COLD cache: no signature suffixes.
	cat := directCatalogFor(p, tools)
	p.directServer.SetTools(p.withDirectBuiltins(p.renderDirectTools(cat))...)
	p.publishDirectCatalog(cat)

	f := &skewFixture{proxy: p}
	ctx := context.Background()
	before := f.listed(ctx)
	require.Contains(t, before, "fs__read")

	// The index warms the cache afterwards. No rebuild.
	for _, tool := range tools {
		p.sigCache.Warm(tool.Hash, tool.ParamsJSON, tool.Description)
	}

	assert.Equal(t, before, f.listed(ctx), "a cache warm must not change what is listed")
	assert.True(t, f.describable(ctx, "fs__read"), "nor what is describable")
}

// And the reverse: an eviction between registration and a later call.
func TestSkew_SignatureCacheEvictionAfterRegistrationChangesNothing(t *testing.T) {
	tools := skewBase()
	f := newSkewFixture(t, tools)
	f.proxy.config.DirectToolResponseMode = config.DirectToolResponseModeDeferred
	f.publish(t, tools) // rendered WARM, with suffixes

	ctx := context.Background()
	before := f.listed(ctx)
	require.Contains(t, before, "fs__read")

	require.Positive(t, f.proxy.sigCache.RetainHashes(nil), "the fixture must actually evict something")

	assert.Equal(t, before, f.listed(ctx), "an eviction must not change what is listed")
	assert.True(t, f.describable(ctx, "fs__read"), "nor what is describable")
}

// ---------------------------------------------------------------------------
// T072 — cross-cutting invariants over the whole skew set
// ---------------------------------------------------------------------------

// generation increments exactly once per rebuild, and not at all on a guarded
// no-op reload.
func TestSkew_GenerationIncrementsOncePerRebuildAndNotOnANoOpReload(t *testing.T) {
	f := newSkewFixture(t, skewBase())
	start := f.proxy.loadDirectCatalog().Generation()

	f.rebuildPaused(t, skewBase(), func() {
		assert.Equal(t, start, f.proxy.loadDirectCatalog().Generation(),
			"the generation must not move until the catalog is actually published")
	})
	assert.Equal(t, start+1, f.proxy.loadDirectCatalog().Generation())

	// A reload with no serialization change must publish nothing at all.
	f.proxy.RefreshDirectModeToolsOnSerializationChange()
	assert.Equal(t, start+1, f.proxy.loadDirectCatalog().Generation(),
		"a guarded no-op reload must not publish a new generation")
}

// Across every case above: a read-scoped token never has a DESTRUCTIVE tool's
// call admitted, whichever side of the window it lands on.
func TestSkew_ReadScopedTokenNeverHasADestructiveCallAdmitted(t *testing.T) {
	destructive := []*config.ToolMetadata{
		skewTool("fs", "purge", "Purge", `{"type":"object"}`, &config.ToolAnnotations{DestructiveHint: boolPtr(true)}),
	}
	f := newSkewFixture(t, destructive)
	readOnly := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type: auth.AuthTypeAgent, AgentName: "reader",
		AllowedServers: []string{"fs"},
		Permissions:    []string{auth.PermRead},
	})

	entry := directCatalogFor(f.proxy, destructive).byDisplayName["fs__purge"]
	require.NotNil(t, entry)

	check := func(when string) {
		assert.NotContainsf(t, f.listed(readOnly), "fs__purge", "%s: must not be listed", when)
		assert.Falsef(t, f.describable(readOnly, "fs__purge"), "%s: must not be describable", when)

		result, err := f.proxy.makeDirectModeHandler(entry)(readOnly, mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: "fs__purge"},
		})
		require.NoError(t, err)
		require.Truef(t, result.IsError, "%s: the call must be refused", when)
		assert.Containsf(t, result.Content[0].(mcp.TextContent).Text, "Permission denied", "%s", when)
	}

	check("before the rebuild")
	f.rebuildPaused(t, destructive, func() { check("inside the window") })
	check("after the publish")
}
