//go:build server

package server

// Spec 107 T072 (US1, FR-004/FR-009/FR-010, contracts/entitlement-predicate.md
// §1, spec.md Definitions "empty/absent access = deny-all for a
// non-administrator"): the group term of the entitlement predicate at the MCP
// doors.
//
// entitledServerNames(userID, isAdmin) (internal/serveredition/api/user_handlers.go:808)
// is wired into agent-token authentication TODAY via
// storage.Manager.SetAgentTokenScopeResolver (internal/serveredition/setup.go:113),
// which every /mcp* request re-derives the caller's AuthContext.AllowedServers
// through (mcpAuthMiddleware -> storage.ValidateAgentToken). But that
// predicate (until T075) never reads User.Groups or a `server_edition.access`
// grant at all: a non-administrator's wildcard token is narrowed to EVERY
// Shared server in the live configuration, regardless of which group (if any)
// the user belongs to. Bob, who belongs to no group, is the fixture that
// proves it: with an empty access.default_servers (the security invariant in
// the dispatch prompt — "empty/absent access map = deny-all for
// non-administrators"), his wildcard token must resolve to NOTHING, and every
// MCP door downstream of it (retrieve_tools, call_tool_*, describe_tool,
// upstream_servers tail_log, prompts, set_profile) must show him nothing and
// refuse non-disclosingly. Today it shows him everything.
//
// This file drives the REAL production wiring — wireServerEditionOAuth
// against the real storage.Manager (the exact objects mcpAuthMiddleware uses
// on /mcp) — rather than hand-building an already-narrow AuthContext, because
// the defect lives in how AllowedServers gets computed at authentication time,
// not in whether the MCP doors respect it once computed (Spec 105 already
// covers that half).
//
// Two-fixture oracle (T067, fixture_oracle_test.go, contracts's FR-010):
// fixture A carries shared servers a, b, a__b; fixture B carries only a. Every
// other input (users, groups, tokens) is identical. A caller's view of a
// server it IS entitled to must never depend on whether a server it is NOT
// entitled to happens to exist.
//
// Behaviour-red on HEAD: TestMCPGroupScope_WildcardToken_NoGroupMatch_DeniesAll,
// _RetrieveTools_, _CallToolDispatch_, _DescribeTool_, _TailLog_, _Prompts_,
// _SetProfile_ and _HotReload_ all fail today because the resolver installed
// by setup.go:113 ignores User.Groups entirely. No production code changes
// here.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/serveredition/users"
)

// Per-server high-entropy sentinels, embedded in each fixture server's tool
// description and log file — their accidental presence in a response body
// can only mean that server's data leaked to a caller not entitled to it.
// Mirrors internal/serveredition/fixture_oracle_test.go's sentinel discipline
// (T067), reproduced locally because that file's identifiers are unexported
// in package serveredition and unreachable from package server.
const (
	groupScopeCanaryA  = "canary-a-4f7c1e9b2d6a"
	groupScopeCanaryB  = "canary-b-8a2d6f317c90"
	groupScopeCanaryAB = "canary-ab-2c6f8a1e4b9d"
)

var groupScopeCanaries = map[string]string{
	"a":    groupScopeCanaryA,
	"b":    groupScopeCanaryB,
	"a__b": groupScopeCanaryAB,
}

// groupScopeFixture is one side of the two-fixture oracle: a full
// server-edition Server, wired with the REAL production entitlement resolver
// (wireServerEditionOAuth against the same storage.Manager mcpAuthMiddleware
// authenticates every /mcp* request through), plus Alice (group "eng"), Bob
// (no group) and Dana (administrator, via AdminEmails).
type groupScopeFixture struct {
	t       *testing.T
	srv     *Server
	proxy   *MCPProxyServer
	hmacKey []byte
	users   *users.UserStore
	alice   *users.User
	bob     *users.User
	dana    *users.User
}

// newGroupScopeFixture builds one fixture with the given shared servers, each
// carrying one indexed tool and one tail_log fixture file stamped with that
// server's sentinel.
func newGroupScopeFixture(t *testing.T, serverNames []string) *groupScopeFixture {
	t.Helper()

	logDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.APIKey = "test-api-key-t072"
	cfg.Logging.LogDir = logDir
	cfg.ServerEdition = &config.ServerEditionConfig{
		Enabled:     true,
		AdminEmails: []string{"dana@example.com"},
		// `oidc` is the one provider that yields groups; a non-empty
		// access.group_servers is refused for a legacy provider (FR-007).
		OAuth: &config.ServerEditionOAuthConfig{
			Provider:     "oidc",
			ClientID:     "test-client-id",
			ClientSecret: "test-client-secret",
			IssuerURL:    "https://idp.example.com",
		},
		// The two-fixture blueprint's group map (fixture_oracle_test.go):
		// eng -> [a], ops -> [a, b], no default grant — read live by the
		// entitlement predicate (T074/T075).
		Access: &config.ServerEditionAccessConfig{
			GroupServers:   map[string][]string{"eng": {"a"}, "ops": {"a", "b"}},
			DefaultServers: []string{},
		},
	}
	// Live config carries the shared servers DISABLED: an enabled entry makes
	// the runtime connect in a background goroutine (see
	// mcp_tail_log_scope_test.go's newTailLogScopeProxy, the established
	// pattern this mirrors). entitledServerNames reads this live config's
	// Shared flag only — connectivity is irrelevant to the predicate.
	for _, name := range serverNames {
		cfg.Servers = append(cfg.Servers, &config.ServerConfig{
			Name: name, Protocol: "http", Shared: true, Enabled: false,
		})
	}

	srv, err := NewServer(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown() })

	// Background initialization (StartBackgroundInitialization, triggered by
	// NewServer above) runs LoadConfiguredServers asynchronously: it re-saves
	// every cfg.Servers entry to storage (config is the source of truth) —
	// including the DISABLED shared-server placeholders registered above —
	// and only flips the runtime to PhaseReady once that sync completes. The
	// direct writes below (SaveUpstreamServer/AddServerConfig with
	// Enabled:true) race that sync: if LoadConfiguredServers's save for a
	// server lands AFTER this fixture's own save, it silently reverts the
	// server back to Enabled:false in storage, which makes isExactToolCallable
	// (mcp.go) drop its indexed tool from every later retrieve_tools call —
	// exactly the flake this wait closes. Same pattern as
	// newLogsTestServer (server_logs_missing_file_test.go).
	require.Eventually(t, func() bool {
		return srv.runtime.CurrentPhase() == runtime.PhaseReady
	}, 10*time.Second, 10*time.Millisecond, "fixture: runtime never reached PhaseReady")

	// Wire the REAL production multi-user OAuth setup (normally run inside
	// startCustomHTTPServer, server.go:2911) against the same StorageManager
	// mcpAuthMiddleware validates agent tokens through. This installs TODAY's
	// ungrouped SetAgentTokenScopeResolver (setup.go:113) — the thing this
	// file proves is missing the group term.
	httpAPIServer := httpapi.NewServer(srv, zap.NewNop().Sugar(), nil)
	wireServerEditionOAuth(srv, httpAPIServer)

	sm := srv.runtime.StorageManager()
	require.NotNil(t, sm, "fixture: the runtime must publish a storage manager")

	hmacKey, err := auth.GetOrCreateHMACKey(cfg.DataDir)
	require.NoError(t, err)

	us := users.NewUserStore(sm.GetDB())

	fx := &groupScopeFixture{t: t, srv: srv, proxy: srv.mcpProxy, hmacKey: hmacKey, users: us}

	fx.alice = users.NewUser("alice@example.com", "alice@example.com", "google", "sub-alice")
	require.NoError(t, us.CreateUser(fx.alice))
	fx.alice.Groups = []string{"eng"}
	require.NoError(t, us.UpdateUser(fx.alice))

	fx.bob = users.NewUser("bob@example.com", "bob@example.com", "google", "sub-bob")
	require.NoError(t, us.CreateUser(fx.bob))
	// Bob's Groups stay nil/empty: with an empty access.default_servers (the
	// shape T074 will add), he is the deny-all tenant the security invariant
	// requires — "empty/absent access map = deny-all for non-administrators".

	fx.dana = users.NewUser("dana@example.com", "dana@example.com", "google", "sub-dana")
	require.NoError(t, us.CreateUser(fx.dana))

	// Register each server on the MCP proxy's own storage/upstream/index —
	// the objects handleRetrieveTools/handleCallToolVariant/handleDescribeTool/
	// handleUpstreamServers/filterAggregatedPromptsForAuth/handleSetProfile
	// actually dispatch through. srv.mcpProxy.storage IS srv.runtime.StorageManager()
	// (server.go:304-323, NewMCPProxyServer(rt.StorageManager(), ...)), so this
	// shares the same BBolt handle as the user/token store above — one fixture,
	// one source of truth.
	for _, name := range serverNames {
		sentinel := groupScopeCanaries[name]
		sc := &config.ServerConfig{Name: name, Protocol: "http", URL: "http://127.0.0.1:1/mcp", Enabled: true}
		require.NoError(t, fx.proxy.storage.SaveUpstreamServer(sc))
		require.NoError(t, fx.proxy.upstreamManager.AddServerConfig(name, sc))
		require.NoError(t, fx.proxy.index.IndexTool(&config.ToolMetadata{
			Name:        name + ":tool1",
			ServerName:  name,
			Description: "Manage " + name + " resources for testing. " + sentinel,
			ParamsJSON:  `{"type":"object","properties":{}}`,
			Hash:        "hash-" + name,
		}))
		require.NoError(t, os.WriteFile(filepath.Join(logDir, "server-"+name+".log"),
			[]byte("CANARY upstream log line "+sentinel+"\n"), 0o600))
	}

	return fx
}

// mintToken creates a real agent token directly against storage (bypassing
// the mint HTTP door, which does not yet narrow against a group grant either
// — that is T075/T076's job) with the given REQUESTED AllowedServers, and
// returns the raw secret.
func (fx *groupScopeFixture) mintToken(owner *users.User, name string, requested []string) string {
	fx.t.Helper()
	return fx.mintTokenWithPermissions(owner, name, requested, []string{auth.PermRead})
}

// mintTokenWithPermissions is mintToken with an explicit permission set, for
// scenarios that must isolate the SCOPE gate from the separate permission-tier
// gate (contracts.ToolVariant*).
func (fx *groupScopeFixture) mintTokenWithPermissions(owner *users.User, name string, requested, permissions []string) string {
	fx.t.Helper()
	raw, err := auth.GenerateToken()
	require.NoError(fx.t, err)
	require.NoError(fx.t, fx.srv.runtime.StorageManager().CreateAgentToken(auth.AgentToken{
		Name:           name,
		UserID:         owner.ID,
		AllowedServers: requested,
		Permissions:    permissions,
	}, raw, fx.hmacKey))
	return raw
}

// resolve runs raw through the REAL production authentication path
// (storage.Manager.ValidateAgentToken, which invokes whatever resolver
// setup.go installed) and returns the resulting AuthContext — i.e. what
// mcpAuthMiddleware would hand every /mcp handler for this token, right now.
func (fx *groupScopeFixture) resolve(raw string) *auth.AuthContext {
	fx.t.Helper()
	tok, err := fx.srv.runtime.StorageManager().ValidateAgentToken(raw, fx.hmacKey)
	require.NoError(fx.t, err)
	require.NotNil(fx.t, tok)
	return tok.AuthContext()
}

// sessionCtx binds ac to a fresh fake MCP client session — the shape
// handleSetProfile requires (sessionIDFromContext) and TestReadCache's
// pattern already establishes (mcp_read_cache_authz_test.go).
func (fx *groupScopeFixture) sessionCtx(ac *auth.AuthContext) context.Context {
	fx.t.Helper()
	helper := mcpserver.NewMCPServer("test", "1.0.0")
	session := helper.WithContext(context.Background(), &fakeClientSession{id: "t072-" + ac.AgentName})
	return auth.WithAuthContext(session, ac)
}

func groupScopeToolNames(resp retrieveToolsResponse) []string {
	names := make([]string, 0, len(resp.Tools))
	for _, tool := range resp.Tools {
		if n, ok := tool["name"].(string); ok {
			names = append(names, n)
		}
	}
	return names
}

// TestMCPGroupScope_WildcardToken_NoGroupMatch_DeniesAll is the core defect:
// Bob belongs to no group, and an empty access.default_servers means deny-all
// for a non-administrator (security invariant #11 / spec.md Definitions).
// Proven across BOTH fixtures so the result cannot be an artifact of which
// shared servers happen to exist.
func TestMCPGroupScope_WildcardToken_NoGroupMatch_DeniesAll(t *testing.T) {
	for _, tc := range []struct {
		name    string
		servers []string
	}{
		{"fixture A (a, b, a__b)", []string{"a", "b", "a__b"}},
		{"fixture B (a only)", []string{"a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newGroupScopeFixture(t, tc.servers)
			raw := fx.mintToken(fx.bob, "bob-star", []string{"*"})
			ac := fx.resolve(raw)
			assert.Empty(t, ac.AllowedServers,
				"Bob is in no group and access.default_servers is empty: today's ungrouped "+
					"SetAgentTokenScopeResolver (setup.go:113 -> entitledServerNames, "+
					"user_handlers.go:808) materialises the wildcard into every Shared server instead of nothing")
		})
	}
}

// TestMCPGroupScope_RetrieveTools_UnentitledCallerSeesNothing: retrieve_tools
// metadata listing for Bob's resolved (today: over-granted) context.
func TestMCPGroupScope_RetrieveTools_UnentitledCallerSeesNothing(t *testing.T) {
	fx := newGroupScopeFixture(t, []string{"a", "b", "a__b"})
	raw := fx.mintToken(fx.bob, "bob-star", []string{"*"})
	ctx := auth.WithAuthContext(context.Background(), fx.resolve(raw))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"query": "manage resources testing", "limit": float64(10)}
	result, err := fx.proxy.handleRetrieveTools(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	body := resultText(t, result)

	var resp retrieveToolsResponse
	require.NoError(t, json.Unmarshal([]byte(body), &resp))
	assert.Empty(t, groupScopeToolNames(resp),
		"Bob is entitled to no server; retrieve_tools must return nothing, not every shared server's tools")
	for _, sentinel := range groupScopeCanaries {
		assert.NotContains(t, body, sentinel, "no shared server's data may reach an unentitled caller")
	}
}

// TestMCPGroupScope_CallToolDispatch_UnentitledCallerDeniedNonDisclosing:
// call_tool_read dispatch to an existing but out-of-scope server.
func TestMCPGroupScope_CallToolDispatch_UnentitledCallerDeniedNonDisclosing(t *testing.T) {
	fx := newGroupScopeFixture(t, []string{"a", "b", "a__b"})
	// Full permissions: this test isolates the SCOPE gate. A read-only token
	// would be refused for "insufficient permissions" first (the tool carries
	// no annotations, so the dispatcher conservatively requires the
	// destructive tier), which would mask the scope-refusal text this test
	// asserts on.
	raw := fx.mintTokenWithPermissions(fx.bob, "bob-star", []string{"*"},
		[]string{auth.PermRead, auth.PermWrite, auth.PermDestructive})
	ac := fx.resolve(raw)

	assert.False(t, ac.CanAccessServer("a"),
		"Bob's resolved AllowedServers must not include 'a': he is in no group and access.default_servers is empty")

	// Drive the real dispatcher too: handleCallToolVariant's scope gate
	// (server.go:2345, "is not in scope for this agent token") is the FIRST
	// check it runs against AllowedServers, before any upstream-connectivity
	// concern. With the CanAccessServer premise above true (target state), the
	// call below is refused for "not in scope", not for a downstream
	// connection error — proving the scope gate itself, not merely the
	// predicate it should consult, denies Bob.
	ctx := auth.WithAuthContext(context.Background(), ac)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"name": "a:tool1"}
	result, err := fx.proxy.handleCallToolVariant(ctx, req, contracts.ToolVariantRead)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.IsError, "dispatch to an out-of-scope server must be refused")
	assert.Contains(t, resultText(t, result), "not in scope",
		"today Bob's over-broad AllowedServers lets him past the scope gate entirely, so the dispatcher fails "+
			"later for an unrelated (and disclosing) connectivity reason instead")
}

// TestMCPGroupScope_DescribeTool_UnentitledCallerNonDisclosingParity: the
// indexed describe_tool surface (Spec 085) must refuse an out-of-scope
// EXISTING tool exactly like one that never existed — memory/project_entitlement_test_oracle.md's
// status/body-parity oracle, not "body must not contain the name".
func TestMCPGroupScope_DescribeTool_UnentitledCallerNonDisclosingParity(t *testing.T) {
	fx := newGroupScopeFixture(t, []string{"a", "b", "a__b"})
	raw := fx.mintToken(fx.bob, "bob-star", []string{"*"})
	ctx := auth.WithAuthContext(context.Background(), fx.resolve(raw))

	describeAs := func(id string) map[string]interface{} {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]interface{}{"tool_ids": []interface{}{id}}
		result, err := fx.proxy.handleDescribeTool(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, result)
		var resp describeToolResponse
		require.NoError(t, json.Unmarshal([]byte(resultText(t, result)), &resp))
		return describeErrorsByID(resp)[id]
	}

	hidden := describeAs("a:tool1")        // exists, out of Bob's scope
	absent := describeAs("zz:nonexistent") // never existed anywhere

	require.NotNil(t, absent, "fixture: a genuinely nonexistent id must refuse")
	require.NotNil(t, hidden, "describe_tool must refuse an existing tool Bob is not entitled to, "+
		"not disclose its definition")
	assert.Equal(t, absent["remediation"], hidden["remediation"],
		"an out-of-scope existing tool must refuse identically to one that never existed (non-disclosing)")
}

// TestMCPGroupScope_TailLog_UnentitledCallerDenied: upstream_servers
// tail_log for an existing but out-of-scope server must not disclose the log.
func TestMCPGroupScope_TailLog_UnentitledCallerDenied(t *testing.T) {
	fx := newGroupScopeFixture(t, []string{"a", "b", "a__b"})
	raw := fx.mintToken(fx.bob, "bob-star", []string{"*"})
	ctx := auth.WithAuthContext(context.Background(), fx.resolve(raw))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"operation": "tail_log", "name": "a"}
	result, err := fx.proxy.handleUpstreamServers(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)

	body := toolResultText(t, result)
	assert.True(t, result.IsError, "tail_log on an out-of-scope existing server must be refused")
	assert.NotContains(t, body, groupScopeCanaryA, "the upstream log content must never leak to an unentitled caller")
}

// TestMCPGroupScope_Prompts_UnentitledCallerSeesOnlyBuiltins: the aggregated
// prompt list (PR #973 finding F1 filter) applied to Bob's resolved context.
func TestMCPGroupScope_Prompts_UnentitledCallerSeesOnlyBuiltins(t *testing.T) {
	fx := newGroupScopeFixture(t, []string{"a", "b"})
	raw := fx.mintToken(fx.bob, "bob-star", []string{"*"})
	ctx := auth.WithAuthContext(context.Background(), fx.resolve(raw))

	const (
		builtinSetup = "setup-new-mcp-server"
		builtinTrbl  = "troubleshoot-mcp-server"
	)
	base := []mcp.Prompt{
		{Name: builtinSetup},
		{Name: builtinTrbl},
		aggregatedPromptForTest("a", "review"),
		aggregatedPromptForTest("b", "review"),
	}
	got := fx.proxy.filterAggregatedPromptsForAuth(ctx, base)
	assert.ElementsMatch(t, []string{builtinSetup, builtinTrbl}, promptNamesForTest(got),
		"Bob is entitled to no server; only built-in prompts must survive")
}

// TestMCPGroupScope_SetProfile_UnentitledCallerGetsEmptyServers: clearing the
// session profile must report Bob's REAL entitlement (nothing), not every
// shared server — the task prompt's "empty set_profile" scenario.
func TestMCPGroupScope_SetProfile_UnentitledCallerGetsEmptyServers(t *testing.T) {
	fx := newGroupScopeFixture(t, []string{"a", "b"})
	raw := fx.mintToken(fx.bob, "bob-star", []string{"*"})
	ctx := fx.sessionCtx(fx.resolve(raw))

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"profile": ""}
	result, err := fx.proxy.handleSetProfile(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.IsError, resultText(t, result))

	var resp struct {
		ActiveProfile string   `json:"active_profile"`
		Servers       []string `json:"servers"`
	}
	require.NoError(t, json.Unmarshal([]byte(resultText(t, result)), &resp))
	assert.Empty(t, resp.Servers,
		"clearing the profile must report Bob's real (empty) entitlement, not every shared server")
}

// TestMCPGroupScope_HotReload_GroupRemovalNarrowsExistingToken: the NEXT
// authentication on Alice's existing wildcard token, after she loses her only
// group, must narrow to nothing. This is the hot-reload half of the task
// scenario: no server restart, no new token, just the next request.
func TestMCPGroupScope_HotReload_GroupRemovalNarrowsExistingToken(t *testing.T) {
	fx := newGroupScopeFixture(t, []string{"a", "b"})
	raw := fx.mintToken(fx.alice, "alice-star", []string{"*"})

	before := fx.resolve(raw)
	require.NotEmpty(t, before.AllowedServers, "fixture: Alice must be entitled to something before the group change")

	fx.alice.Groups = nil
	require.NoError(t, fx.users.UpdateUser(fx.alice))

	after := fx.resolve(raw)
	assert.Empty(t, after.AllowedServers,
		"Alice lost her only group (eng); the next authentication on her existing wildcard token must narrow to "+
			"nothing (no group matches, access.default_servers is empty) — today's resolver never reads "+
			"User.Groups at all, so removing them changes nothing")
}

// TestMCPGroupScope_TwoFixtureParity_GroupScopedToken: Alice's view of her OWN
// entitlement (a narrow, already-scoped token request) must be identical
// whether or not server 'b' exists to be hidden from her (FR-010).
func TestMCPGroupScope_TwoFixtureParity_GroupScopedToken(t *testing.T) {
	fxA := newGroupScopeFixture(t, []string{"a", "b", "a__b"})
	fxB := newGroupScopeFixture(t, []string{"a"})

	rawA := fxA.mintToken(fxA.alice, "alice-a", []string{"a"})
	rawB := fxB.mintToken(fxB.alice, "alice-a", []string{"a"})

	acA := fxA.resolve(rawA)
	acB := fxB.resolve(rawB)
	assert.Equal(t, acA.AllowedServers, acB.AllowedServers,
		"Alice's resolved entitlement for her own requested scope must not depend on whether 'b' exists")

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"query": "manage resources testing", "limit": float64(10)}

	resultA, err := fxA.proxy.handleRetrieveTools(auth.WithAuthContext(context.Background(), acA), req)
	require.NoError(t, err)
	bodyA := resultText(t, resultA)
	assert.NotContains(t, bodyA, groupScopeCanaryB, "fixture A: Alice must never see evidence of server 'b'")
	assert.NotContains(t, bodyA, groupScopeCanaryAB, "fixture A: Alice must never see evidence of server 'a__b'")

	resultB, err := fxB.proxy.handleRetrieveTools(auth.WithAuthContext(context.Background(), acB), req)
	require.NoError(t, err)

	var respA, respB retrieveToolsResponse
	require.NoError(t, json.Unmarshal([]byte(bodyA), &respA))
	require.NoError(t, json.Unmarshal([]byte(resultText(t, resultB)), &respB))
	assert.Equal(t, groupScopeToolNames(respA), groupScopeToolNames(respB),
		"the set of tools Alice sees for her own entitlement must be identical whether or not 'b' exists")
}
