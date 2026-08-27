package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 102 US2 (T042–T058): describe_tool on the DIRECT surface.
//
// The whole point of this file is that the direct surface resolves ids through
// the CATALOG — the same snapshot tools/list rendered from — and never by
// re-parsing a display name. Several fixtures below therefore use a server name
// that itself contains "__", where ParseDirectToolName's split-on-first-"__"
// gives the wrong answer: any test that passes with a re-parsing resolver is
// not testing what US2 promises.

// describeDirectFixture is the tool projection these tests publish. It carries
// one ordinary tool, one destructive tool, one tool on a "__"-containing server,
// one tool declaring an output schema, and the two halves of a display-name
// collision.
func describeDirectFixture() []*config.ToolMetadata {
	return []*config.ToolMetadata{
		{
			ServerName:  "github",
			Name:        "read_file",
			Description: "Read a file",
			ParamsJSON:  `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`,
			Hash:        "h-read-file",
			Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)},
		},
		{
			ServerName:  "github",
			Name:        "delete_repo",
			Description: "Delete a repository",
			ParamsJSON:  `{"type":"object","properties":{"repo":{"type":"string"}},"required":["repo"]}`,
			Hash:        "h-delete-repo",
			Annotations: &config.ToolAnnotations{DestructiveHint: boolPtr(true)},
		},
		{
			// A server name containing the direct separator. Splitting the
			// display name "we__ird__do_thing" on the FIRST "__" yields
			// ("we", "ird__do_thing") — a different tool entirely.
			ServerName:  "we__ird",
			Name:        "do_thing",
			Description: "Do a thing",
			ParamsJSON:  `{"type":"object","properties":{"x":{"type":"string"}}}`,
			Hash:        "h-do-thing",
			Annotations: &config.ToolAnnotations{ReadOnlyHint: boolPtr(true)},
		},
		{
			ServerName:       "github",
			Name:             "create_issue",
			Description:      "Create an issue",
			ParamsJSON:       `{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`,
			OutputSchemaJSON: `{"type":"object","properties":{"number":{"type":"integer"}}}`,
			Hash:             "h-create-issue",
			Annotations:      &config.ToolAnnotations{ReadOnlyHint: boolPtr(false)},
		},
		// Two DISTINCT origins that flatten to the same display name
		// "clash__a__b": the catalog withholds both (Phase 2 T014).
		{
			ServerName:  "clash",
			Name:        "a__b",
			Description: "First origin",
			ParamsJSON:  `{"type":"object"}`,
			Hash:        "h-clash-1",
		},
		{
			ServerName:  "clash__a",
			Name:        "b",
			Description: "Second origin",
			ParamsJSON:  `{"type":"object"}`,
			Hash:        "h-clash-2",
		},
	}
}

// newDirectDescribeProxy publishes the fixture catalog on a real proxy and
// approves every fixture tool, so callability is not the thing under test.
func newDirectDescribeProxy(t *testing.T) *MCPProxyServer {
	t.Helper()
	p := createTestMCPProxyServer(t)

	tools := describeDirectFixture()
	servers := map[string]struct{}{}
	for _, tool := range tools {
		servers[tool.ServerName] = struct{}{}
	}
	for name := range servers {
		require.NoError(t, p.storage.SaveUpstreamServer(&config.ServerConfig{Name: name, Enabled: true}))
	}
	for _, tool := range tools {
		require.NoError(t, p.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: tool.ServerName,
			ToolName:   tool.Name,
			Status:     storage.ToolApprovalStatusApproved,
		}))
	}

	p.publishDirectCatalog(buildDirectCatalog(tools, nil))
	return p
}

// callDescribeDirect invokes describe_tool through the DIRECT surface handler.
func callDescribeDirect(t *testing.T, p *MCPProxyServer, ctx context.Context, ids []interface{}) describeToolResponse {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"tool_ids": ids}
	result, err := p.describeToolHandler(describeSurfaceDirect)(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError, "describe_tool returned an error result: %v", result.Content)

	var resp describeToolResponse
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &resp))
	return resp
}

func describeErrorsByID(resp describeToolResponse) map[string]map[string]interface{} {
	byID := map[string]map[string]interface{}{}
	for _, e := range resp.Errors {
		byID[e["id"].(string)] = e
	}
	return byID
}

// T042: both accepted id forms resolve to the SAME definition, through the
// registration mapping.
func TestDescribeDirect_BothIDFormsResolveIdentically(t *testing.T) {
	p := newDirectDescribeProxy(t)
	ctx := context.Background()

	for _, tc := range []struct{ name, direct, canonical string }{
		{"plain server", "github__read_file", "github:read_file"},
		// The one that a re-parsing resolver gets wrong.
		{"server name contains the separator", "we__ird__do_thing", "we__ird:do_thing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viaDirect := callDescribeDirect(t, p, ctx, []interface{}{tc.direct})
			viaCanonical := callDescribeDirect(t, p, ctx, []interface{}{tc.canonical})

			require.Empty(t, viaDirect.Errors, "direct id %q must resolve", tc.direct)
			require.Empty(t, viaCanonical.Errors, "canonical id %q must resolve", tc.canonical)
			require.Len(t, viaDirect.Definitions, 1)
			require.Len(t, viaCanonical.Definitions, 1)

			assert.Equal(t, viaCanonical.Definitions[0], viaDirect.Definitions[0],
				"both id forms must resolve to one definition")
		})
	}

	// Proof the separator case is not accidentally right: a first-"__" split
	// would have named a server called "we".
	resp := callDescribeDirect(t, p, ctx, []interface{}{"we__ird__do_thing"})
	require.Len(t, resp.Definitions, 1)
	assert.Equal(t, "we__ird", resp.Definitions[0]["server"])
	assert.Equal(t, "we__ird:do_thing", resp.Definitions[0]["name"])
}

// T043: the permission-tier gate. A read-scoped token cannot LIST a destructive
// tool, so it must not be able to describe one either.
func TestDescribeDirect_PermissionTierGate(t *testing.T) {
	p := newDirectDescribeProxy(t)

	readOnly := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "reader",
		AllowedServers: []string{"github"},
		Permissions:    []string{auth.PermRead},
	})

	resp := callDescribeDirect(t, p, readOnly, []interface{}{"github__delete_repo", "github__read_file"})

	require.Len(t, resp.Definitions, 1, "only the read-tier tool may describe")
	assert.Equal(t, "github:read_file", resp.Definitions[0]["name"])

	byID := describeErrorsByID(resp)
	require.Contains(t, byID, "github__delete_repo")
	assert.Equal(t, describeErrNotFound, byID["github__delete_repo"]["error"],
		"a tool absent from this token's own listing must be indistinguishable from one that does not exist")
	assert.Equal(t, describeNotFoundRemediation, byID["github__delete_repo"]["remediation"],
		"the remediation must not confirm that the tool exists")
}

// A token scoped to another server sees nothing of this one.
func TestDescribeDirect_ServerScopeGate(t *testing.T) {
	p := newDirectDescribeProxy(t)

	elsewhere := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "other",
		AllowedServers: []string{"gitlab"},
		Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
	})

	resp := callDescribeDirect(t, p, elsewhere, []interface{}{"github__read_file"})
	assert.Empty(t, resp.Definitions)
	byID := describeErrorsByID(resp)
	require.Contains(t, byID, "github__read_file")
	assert.Equal(t, describeErrNotFound, byID["github__read_file"]["error"])
}

// T044: catalog divergence. A tool that is pending approval is still LISTED for
// a non-agent session, so it must still describe — from the catalog snapshot.
// An index-backed resolver answers not_found here, which would make deferral
// strictly worse than full mode.
func TestDescribeDirect_PendingToolStillDescribesFromSnapshot(t *testing.T) {
	p := newDirectDescribeProxy(t)
	require.NoError(t, p.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github",
		ToolName:   "read_file",
		Status:     storage.ToolApprovalStatusPending,
	}))

	// No auth context: the non-agent direct listing retains pending tools.
	resp := callDescribeDirect(t, p, context.Background(), []interface{}{"github__read_file"})

	require.Empty(t, resp.Errors, "a listed tool is never undescribable (SC-007)")
	require.Len(t, resp.Definitions, 1)
	assert.Equal(t, "github:read_file", resp.Definitions[0]["name"])
	assert.Equal(t, "Read a file", resp.Definitions[0]["description"],
		"the definition comes from the catalog snapshot, not the index")
}

// The definition is never index-backed: a tool absent from the search index
// still describes, because the catalog is the authority on this surface.
func TestDescribeDirect_DefinitionIsCatalogBackedNotIndexBacked(t *testing.T) {
	p := newDirectDescribeProxy(t)
	require.Nil(t, p.lookupIndexedTool("github", "read_file"),
		"the fixture must NOT be indexed, or this asserts nothing")

	resp := callDescribeDirect(t, p, context.Background(), []interface{}{"github__read_file"})
	require.Empty(t, resp.Errors)
	require.Len(t, resp.Definitions, 1)
}

// T046a: a withheld display-name collision resolves in NEITHER id form. This is
// the describe half of the Phase 2 T014 assertion, which could not go green
// before a resolver existed.
func TestDescribeDirect_WithheldCollisionResolvesInNeitherForm(t *testing.T) {
	p := newDirectDescribeProxy(t)

	ids := []interface{}{"clash__a__b", "clash:a__b", "clash__a:b"}
	resp := callDescribeDirect(t, p, context.Background(), ids)

	assert.Empty(t, resp.Definitions, "a withheld collision must resolve in no form at all")
	byID := describeErrorsByID(resp)
	for _, id := range ids {
		require.Containsf(t, byID, id.(string), "id %v must report an error", id)
		assert.Equal(t, describeErrNotFound, byID[id.(string)]["error"])
	}
}

// T046b: output_schema is present iff the tool declares one. Written BEFORE the
// implementation, so it fails first.
func TestDescribeDirect_OutputSchemaPresentOnlyWhenDeclared(t *testing.T) {
	p := newDirectDescribeProxy(t)

	resp := callDescribeDirect(t, p, context.Background(),
		[]interface{}{"github__create_issue", "github__read_file"})
	require.Empty(t, resp.Errors)
	require.Len(t, resp.Definitions, 2)

	byName := map[string]map[string]interface{}{}
	for _, def := range resp.Definitions {
		byName[def["name"].(string)] = def
	}

	withSchema := byName["github:create_issue"]
	require.Contains(t, withSchema, "output_schema", "a declaring tool must carry output_schema")
	raw, err := json.Marshal(withSchema["output_schema"])
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"object","properties":{"number":{"type":"integer"}}}`, string(raw))

	assert.NotContains(t, byName["github:read_file"], "output_schema",
		"a tool with no output schema must not carry an empty one")
}

// A malformed id, and an id for a server the catalog never had, are both plain
// not-founds — and neither fails the batch (T058's shape).
func TestDescribeDirect_UnknownAndMalformedIDsDoNotFailTheBatch(t *testing.T) {
	p := newDirectDescribeProxy(t)

	resp := callDescribeDirect(t, p, context.Background(),
		[]interface{}{"github__read_file", "not-an-id", "gone__vanished"})

	require.Len(t, resp.Definitions, 1, "the valid id must still resolve")
	byID := describeErrorsByID(resp)
	require.Contains(t, byID, "not-an-id")
	require.Contains(t, byID, "gone__vanished")
	assert.Equal(t, describeErrNotFound, byID["not-an-id"]["error"])
	assert.Equal(t, describeErrNotFound, byID["gone__vanished"]["error"])
}

// T049: a listed pending DESTRUCTIVE tool describes with its REAL annotations
// and call_with "destructive".
//
// The failure this guards is quiet and dangerous: buildFullToolEntry otherwise
// reads annotations from the StateView, which does not carry them for a tool in
// this state, and DeriveCallWith reads the resulting nil as the READ tier. The
// agent would be told a repo-deleting tool is safe to call read-only, at the one
// moment the answer matters (D10).
func TestDescribeDirect_PendingDestructiveKeepsItsRealAnnotations(t *testing.T) {
	p := newDirectDescribeProxy(t)
	require.NoError(t, p.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github",
		ToolName:   "delete_repo",
		Status:     storage.ToolApprovalStatusPending,
	}))
	require.Nil(t, p.lookupToolAnnotations("github", "delete_repo"),
		"the StateView must NOT carry these annotations, or the override is untested")

	resp := callDescribeDirect(t, p, context.Background(), []interface{}{"github__delete_repo"})
	require.Empty(t, resp.Errors)
	require.Len(t, resp.Definitions, 1)

	def := resp.Definitions[0]
	// contracts.DeriveCallWith answers the VARIANT name, which is what an agent
	// calls; the task text's "destructive" is the tier it names.
	assert.Equal(t, string(contracts.ToolVariantDestructive), def["call_with"],
		"the safety hint must come from the catalog snapshot, not from an absent StateView entry")

	annotations, ok := def["annotations"].(map[string]interface{})
	require.True(t, ok, "the definition must carry the upstream annotations")
	assert.Equal(t, true, annotations["destructiveHint"])
}

// The override must not leak onto the retrieve path: full-mode entries keep
// reading the StateView, which is what leaves the frozen goldens untouched.
func TestDescribeDirect_AnnotationsOverrideUnusedOnRetrievePath(t *testing.T) {
	p := newDirectDescribeProxy(t)
	entry := p.buildToolEntry(
		&config.SearchResult{Tool: describeDirectFixture()[1]},
		config.ToolResponseModeFull,
		toolEntryOpts{},
	)
	assert.Equal(t, string(contracts.ToolVariantRead), entry["call_with"],
		"with no override the builder must still read the StateView (nil here), unchanged")
	assert.NotContains(t, entry, "annotations")
}

// T055 (definition-mode half) / T056: a miscased id is corrected from the
// CATALOG, and only ever to a tool this session can list.
func TestDescribeDirect_CaseCorrectionIsCatalogBackedAndGated(t *testing.T) {
	p := newDirectDescribeProxy(t)

	// Case is never folded on a resolution path — server and tool names are
	// exact keys in the approval, quarantine, profile and scope stores — so a
	// miscased id must NOT resolve; it may only be corrected.
	resp := callDescribeDirect(t, p, context.Background(), []interface{}{"GITHUB__read_file"})
	require.Empty(t, resp.Definitions, "a miscased id must never resolve")
	byID := describeErrorsByID(resp)
	require.Contains(t, byID, "GITHUB__read_file")
	assert.Equal(t, describeErrNotFound, byID["GITHUB__read_file"]["error"])
	assert.Contains(t, byID["GITHUB__read_file"]["remediation"], "github__read_file",
		"the correction must name the canonical id, in the grammar the caller used")

	// Gated: a read-scoped token miscasing a DESTRUCTIVE tool gets no
	// correction, because the correction would confirm the tool exists.
	readOnly := auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "reader",
		AllowedServers: []string{"github"},
		Permissions:    []string{auth.PermRead},
	})
	scoped := callDescribeDirect(t, p, readOnly, []interface{}{"github__DELETE_repo"})
	scopedByID := describeErrorsByID(scoped)
	require.Contains(t, scopedByID, "github__DELETE_repo")
	assert.Equal(t, describeNotFoundRemediation, scopedByID["github__DELETE_repo"]["remediation"],
		"no correction may name a tool absent from this token's own listing")
}

// T057: listing↔describe parity, in BOTH directions, for four session shapes.
// No id may be describable-but-unlisted (a disclosure) or listed-but-
// undescribable (SC-007).
func TestDescribeDirect_ListingAndDescribeAgreeInBothDirections(t *testing.T) {
	p := newDirectDescribeProxy(t)
	p.config.Profiles = []config.ProfileConfig{{Name: "gh", Servers: []string{"github"}}}
	p.config.Servers = []*config.ServerConfig{
		{Name: "github", Enabled: true},
		{Name: "we__ird", Enabled: true},
		{Name: "clash", Enabled: true},
		{Name: "clash__a", Enabled: true},
	}

	sessions := map[string]context.Context{
		"admin": context.Background(),
		"read_scoped_token": auth.WithAuthContext(context.Background(), &auth.AuthContext{
			Type: auth.AuthTypeAgent, AgentName: "reader",
			AllowedServers: []string{"github", "we__ird"},
			Permissions:    []string{auth.PermRead},
		}),
		"write_scoped_token": auth.WithAuthContext(context.Background(), &auth.AuthContext{
			Type: auth.AuthTypeAgent, AgentName: "writer",
			AllowedServers: []string{"github", "we__ird"},
			Permissions:    []string{auth.PermRead, auth.PermWrite},
		}),
		"profile_pinned": auth.WithAuthContext(context.Background(), &auth.AuthContext{
			Type: auth.AuthTypeAgent, AgentName: "pinned",
			AllowedServers: []string{"github", "we__ird"},
			Permissions:    []string{auth.PermRead, auth.PermWrite, auth.PermDestructive},
			ProfilePin:     "gh",
		}),
	}

	catalog := p.loadDirectCatalog()
	require.NotNil(t, catalog)

	for name, ctx := range sessions {
		t.Run(name, func(t *testing.T) {
			// What this session's tools/list actually serves, built through the
			// real render + filter chain.
			rendered := p.renderDirectTools(catalog)
			mcpTools := make([]mcp.Tool, 0, len(rendered))
			for _, st := range rendered {
				mcpTools = append(mcpTools, st.Tool)
			}
			listed := map[string]struct{}{}
			for _, tool := range p.filterDirectToolsForAgentCallability(ctx, p.filterDirectModeToolsForAuth(ctx, mcpTools)) {
				listed[tool.Name] = struct{}{}
			}
			require.NotEmpty(t, listed, "every fixture session must list something, or this proves nothing")

			// Every catalog display name, whether listed or not, probed in both
			// grammars. A name that resolves but was not listed is a disclosure;
			// one that was listed but does not resolve breaks SC-007.
			for _, display := range catalog.DisplayNames() {
				entry, ok := catalog.Lookup(display)
				require.True(t, ok)
				canonical := entry.ServerName + ":" + entry.ToolName
				_, wasListed := listed[display]

				for _, id := range []string{display, canonical} {
					_, resolves := p.resolveDirectDescribeID(ctx, id)
					assert.Equalf(t, wasListed, resolves,
						"id %q: listed=%v but describable=%v", id, wasListed, resolves)
				}
			}

			// And the reverse direction over the withheld collision, which is in
			// no listing and must be in no describe either.
			for _, id := range []string{"clash__a__b", "clash:a__b", "clash__a:b"} {
				_, resolves := p.resolveDirectDescribeID(ctx, id)
				assert.Falsef(t, resolves, "withheld collision %q must never resolve", id)
			}
		})
	}
}

// T058: a server removed between tools/list and describe_tool. The stale id is
// a per-id not-found and the rest of the batch still answers.
func TestDescribeDirect_ServerRemovedBetweenListAndDescribe(t *testing.T) {
	p := newDirectDescribeProxy(t)

	// The agent listed both, then a rebuild dropped we__ird entirely.
	remaining := []*config.ToolMetadata{}
	for _, tool := range describeDirectFixture() {
		if tool.ServerName == "we__ird" {
			continue
		}
		remaining = append(remaining, tool)
	}
	p.publishDirectCatalog(buildDirectCatalog(remaining, nil))

	resp := callDescribeDirect(t, p, context.Background(),
		[]interface{}{"we__ird__do_thing", "github__read_file"})

	require.Len(t, resp.Definitions, 1, "the surviving id must still answer — the batch does not fail")
	assert.Equal(t, "github:read_file", resp.Definitions[0]["name"])

	byID := describeErrorsByID(resp)
	require.Contains(t, byID, "we__ird__do_thing")
	assert.Equal(t, describeErrNotFound, byID["we__ird__do_thing"]["error"])
}
