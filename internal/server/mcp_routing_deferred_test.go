package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolsig"
)

// Spec 102 US1 (T028–T033): deferred rendering of the direct enumeration
// surface.
//
// EVERY schema/annotation assertion in this file runs against the MARSHALLED
// JSON, never the Go struct. mcp.Tool has a custom MarshalJSON and
// mcp.ToolInputSchema normalizes on the way out — `mcp.NewTool` produces
// `{"properties":{},"required":[],"type":"object"}` from what looks in Go like
// an empty schema — so a struct-level assertion would pass on bytes that
// violate FR-004 (R11/D9).

// newDeferredRenderProxy builds the minimum proxy renderDirectTools needs: a
// logger, a live signature cache, and a config carrying the serialization mode.
// mainServer is nil, so currentConfig() falls back to this config — which is
// what makes the mode injectable without a Runtime.
func newDeferredRenderProxy(mode string) *MCPProxyServer {
	return &MCPProxyServer{
		logger:   zap.NewNop(),
		sigCache: toolsig.NewCache(),
		config: &config.Config{
			RoutingMode:            config.RoutingModeDirect,
			DirectToolResponseMode: mode,
		},
	}
}

// warmFixtureSignatures warms the shared cache for every fixture tool, the way
// the indexing path does at startup (FR-005). Rendering never compiles.
func warmFixtureSignatures(p *MCPProxyServer, tools []*config.ToolMetadata) {
	for _, t := range tools {
		p.sigCache.Warm(t.Hash, t.ParamsJSON, t.Description)
	}
}

// renderedByName marshals a rendered tool set into name -> raw JSON.
func renderedByName(t *testing.T, tools []mcpserver.ServerTool) map[string]json.RawMessage {
	t.Helper()
	out := make(map[string]json.RawMessage, len(tools))
	for _, st := range tools {
		raw, err := json.Marshal(st.Tool)
		require.NoErrorf(t, err, "marshal rendered tool %q", st.Tool.Name)
		out[st.Tool.Name] = raw
	}
	return out
}

// fieldOf extracts one top-level field from a marshalled tool entry.
func fieldOf(t *testing.T, raw json.RawMessage, field string) json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &m))
	return m[field]
}

func renderFixture(t *testing.T, mode string) map[string]json.RawMessage {
	t.Helper()
	p := newDeferredRenderProxy(mode)
	tools := fixtureTools()
	warmFixtureSignatures(p, tools)
	return renderedByName(t, p.renderDirectTools(buildDirectCatalog(tools, nil)))
}

// T028: the deferred wire shape.
func TestDeferredDirectEntry_MarshalledShape(t *testing.T) {
	entries := renderFixture(t, config.DirectToolResponseModeDeferred)
	require.Len(t, entries, 2)

	raw, ok := entries["github__create_issue"]
	require.True(t, ok, "deferral must not change which tools are listed")

	// FR-004: byte-exactly {"type":"object"} — never literal {}, never absent,
	// never carrying upstream properties/required. Asserted on the BYTES:
	// mcp.NewTool would marshal {"properties":{},"required":[],"type":"object"}
	// here and pass a struct-level check.
	assert.Equal(t, `{"type":"object"}`, string(fieldOf(t, raw, "inputSchema")))

	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &m))
	assert.NotContains(t, string(m["inputSchema"]), "properties")
	assert.NotContains(t, string(m["inputSchema"]), "required")
	assert.NotContains(t, string(m["inputSchema"]), "title")

	// FR-006 / research.md R2: deferred entries strip outputSchema, even though
	// this fixture tool declares one (full mode emits it — asserted in T032's
	// sibling below).
	_, hasOutput := m["outputSchema"]
	assert.False(t, hasOutput, "a deferred entry must not carry outputSchema")

	// Description = the existing "[server] …" text, then a newline, then the
	// BARE tool name and the Spec-085 compact signature. toolsig.Signature.Sig
	// is the parenthesized parameter list only and carries no name, so the name
	// prefix is this renderer's job.
	var desc string
	require.NoError(t, json.Unmarshal(m["description"], &desc))
	sig, ok := p085Signature(t, fixtureTools()[0])
	require.True(t, ok)
	assert.Equal(t, "[github] Create an issue\ncreate_issue"+sig.Sig, desc)
	assert.Contains(t, desc, "title*", "a required param is never elided (FR-004)")
}

// p085Signature renders a fixture tool's signature through the same grammar the
// renderer reads from the cache, so the expectation is not a hand-copied string.
func p085Signature(t *testing.T, tool *config.ToolMetadata) (toolsig.Signature, bool) {
	t.Helper()
	sig, err := toolsig.Render(tool.ParamsJSON, tool.Description)
	require.NoError(t, err)
	return sig, true
}

// T029: annotations parity. The raw-schema constructor leaves every hint nil
// where NewTool seeds readOnly=false/destructive=true/idempotent=false/
// openWorld=true, and mcp.Tool.MarshalJSON emits "annotations" unconditionally
// (no omitempty) — so without the D9 seeding the same tool marshals a DIFFERENT
// annotations object in the two modes.
func TestDeferredDirectEntry_AnnotationsByteIdenticalToFullMode(t *testing.T) {
	fixtures := map[string]*config.ToolAnnotations{
		"nil_annotations": nil,
		"one_hint":        {ReadOnlyHint: boolPtr(true)},
		"all_five": {
			Title:           "All Five",
			ReadOnlyHint:    boolPtr(false),
			DestructiveHint: boolPtr(true),
			IdempotentHint:  boolPtr(true),
			OpenWorldHint:   boolPtr(false),
		},
	}

	for name, ann := range fixtures {
		t.Run(name, func(t *testing.T) {
			tools := []*config.ToolMetadata{{
				ServerName:  "srv",
				Name:        name,
				Description: "A tool.",
				ParamsJSON:  `{"type":"object","properties":{"a":{"type":"string"}}}`,
				Hash:        "hash-" + name,
				Annotations: ann,
			}}

			render := func(mode string) json.RawMessage {
				p := newDeferredRenderProxy(mode)
				warmFixtureSignatures(p, tools)
				out := renderedByName(t, p.renderDirectTools(buildDirectCatalog(tools, nil)))
				raw, ok := out[FormatDirectToolName("srv", name)]
				require.True(t, ok)
				return raw
			}

			full := render(config.DirectToolResponseModeFull)
			deferred := render(config.DirectToolResponseModeDeferred)

			assert.JSONEq(t,
				string(fieldOf(t, full, "annotations")),
				string(fieldOf(t, deferred, "annotations")),
				"annotations must be identical across serialization modes (FR-004/D9)")
		})
	}
}

// T029 (second half): a deferred entry marshals cleanly. mcp.Tool.MarshalJSON
// returns errToolSchemaConflict when RawInputSchema and a typed InputSchema are
// both set — the exact failure mode of "reuse NewTool and then attach a raw
// schema".
func TestDeferredDirectEntry_MarshalsWithoutSchemaConflict(t *testing.T) {
	p := newDeferredRenderProxy(config.DirectToolResponseModeDeferred)
	tools := fixtureTools()
	warmFixtureSignatures(p, tools)

	for _, st := range p.renderDirectTools(buildDirectCatalog(tools, nil)) {
		_, err := json.Marshal(st.Tool)
		require.NoErrorf(t, err, "deferred tool %q must marshal without a schema conflict", st.Tool.Name)
	}
}

// T030: a signature-cache miss lists the entry WITHOUT a suffix — never
// dropped, never delayed, and never compiled on the request path (FR-005).
func TestDeferredDirectEntry_SignatureCacheMiss_ListedWithoutSuffix(t *testing.T) {
	p := newDeferredRenderProxy(config.DirectToolResponseModeDeferred)
	tools := fixtureTools()
	// Deliberately NOT warmed.

	before := p.sigCache.CompileCount()
	entries := renderedByName(t, p.renderDirectTools(buildDirectCatalog(tools, nil)))

	require.Len(t, entries, 2, "a cache miss must not drop the entry")

	raw := entries["github__create_issue"]
	var desc string
	require.NoError(t, json.Unmarshal(fieldOf(t, raw, "description"), &desc))
	assert.Equal(t, "[github] Create an issue", desc,
		"on a miss the whole suffix is absent and the entry is otherwise unchanged")

	// The placeholder schema is still the deferred one: deferral is not
	// conditional on the cache.
	assert.Equal(t, `{"type":"object"}`, string(fieldOf(t, raw, "inputSchema")))

	assert.Equal(t, before, p.sigCache.CompileCount(),
		"rendering must never compile a signature (FR-005)")
	assert.Zero(t, p.sigCache.Len(), "a miss must not memoize")
}

// A nil signature cache must not panic the renderer. Reachable today via the
// bare-struct construction path used by initRoutingModeServers tests.
func TestDeferredDirectEntry_NilSignatureCache_DoesNotPanic(t *testing.T) {
	p := &MCPProxyServer{
		logger: zap.NewNop(),
		config: &config.Config{DirectToolResponseMode: config.DirectToolResponseModeDeferred},
	}
	entries := renderedByName(t, p.renderDirectTools(buildDirectCatalog(fixtureTools(), nil)))
	require.Len(t, entries, 2)
	assert.Equal(t, `{"type":"object"}`, string(fieldOf(t, entries["github__read_file"], "inputSchema")))
}

// T032: set identity across modes (FR-008) — names, count, annotations and
// ordering source, before any filter and under the two direct-surface filters
// (FR-016/SC-005).
func TestDirectModes_SetIdentity(t *testing.T) {
	tools := fixtureTools()

	renderOrdered := func(mode string) ([]string, []mcpserver.ServerTool) {
		p := newDeferredRenderProxy(mode)
		warmFixtureSignatures(p, tools)
		rendered := p.renderDirectTools(buildDirectCatalog(tools, nil))
		names := make([]string, 0, len(rendered))
		for _, st := range rendered {
			names = append(names, st.Tool.Name)
		}
		return names, rendered
	}

	fullNames, fullTools := renderOrdered(config.DirectToolResponseModeFull)
	deferredNames, deferredTools := renderOrdered(config.DirectToolResponseModeDeferred)

	// Ordering source is the catalog's sorted display names, so equality here
	// is order-sensitive on purpose.
	assert.Equal(t, fullNames, deferredNames, "identity, count and ordering must match (FR-008)")

	fullEntries := renderedByName(t, fullTools)
	deferredEntries := renderedByName(t, deferredTools)
	for name, fullRaw := range fullEntries {
		assert.JSONEq(t,
			string(fieldOf(t, fullRaw, "annotations")),
			string(fieldOf(t, deferredEntries[name], "annotations")),
			"annotations for %q must match across modes", name)
	}

	// Full mode DOES carry the upstream schema and outputSchema — the control
	// that proves the deferred assertions above are not vacuous.
	assert.Contains(t, string(fieldOf(t, fullEntries["github__create_issue"], "inputSchema")), `"title"`)
	var fullMap map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(fullEntries["github__create_issue"], &fullMap))
	assert.Contains(t, fullMap, "outputSchema")

	// Under both direct-surface filters, for a scoped agent token and for a
	// profile-pinned one. The filters resolve through the published catalog, so
	// each mode gets its own proxy with the same catalog published.
	catalog := buildDirectCatalog(tools, nil)
	agentCtx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "reader",
		AllowedServers: []string{"github"},
		Permissions:    []string{auth.PermRead},
	})
	pinnedCtx := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "pinned",
		AllowedServers: []string{"github"},
		Permissions:    []string{auth.PermRead, auth.PermWrite},
		ProfilePin:     "gh",
	})

	filteredNames := func(t *testing.T, mode string, ctx context.Context) []string {
		t.Helper()
		// A REAL proxy here, not the bare render harness: the callability filter
		// reads storage, and a nil-storage proxy fails it closed — which would
		// make both modes trivially empty and the comparison vacuous.
		p := createTestMCPProxyServer(t)
		p.config.DirectToolResponseMode = mode
		p.config.Profiles = []config.ProfileConfig{{Name: "gh", Servers: []string{"github"}}}
		p.config.Servers = []*config.ServerConfig{{Name: "github", Enabled: true}}
		require.NoError(t, p.storage.SaveUpstreamServer(&config.ServerConfig{Name: "github", Enabled: true}))
		for _, tool := range tools {
			require.NoError(t, p.storage.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: tool.ServerName,
				ToolName:   tool.Name,
				Status:     storage.ToolApprovalStatusApproved,
			}))
		}
		warmFixtureSignatures(p, tools)
		p.publishDirectCatalog(catalog)

		rendered := p.renderDirectTools(catalog)
		mcpTools := make([]mcp.Tool, 0, len(rendered))
		for _, st := range rendered {
			mcpTools = append(mcpTools, st.Tool)
		}
		out := p.filterDirectToolsForAgentCallability(ctx, p.filterDirectModeToolsForAuth(ctx, mcpTools))
		names := make([]string, 0, len(out))
		for _, tool := range out {
			names = append(names, tool.Name)
		}
		return names
	}

	for label, ctx := range map[string]context.Context{"scoped_agent": agentCtx, "profile_pinned": pinnedCtx} {
		t.Run(label, func(t *testing.T) {
			full := filteredNames(t, config.DirectToolResponseModeFull, ctx)
			deferred := filteredNames(t, config.DirectToolResponseModeDeferred, ctx)
			assert.Equal(t, full, deferred, "serialization must not change which tools a session lists (FR-016)")
			assert.NotEmpty(t, full, "the filter fixture must leave something listed, or this asserts nothing")
		})
	}
}

// Deferral is opt-in and default-off (FR-001/FR-015): an unset mode renders
// exactly like an explicit "full".
func TestDirectRenderer_DefaultsToFull(t *testing.T) {
	unset := renderFixture(t, "")
	full := renderFixture(t, config.DirectToolResponseModeFull)
	assert.Equal(t, full, unset, "an unset direct_tool_response_mode must render full")
}
