//go:build !server

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 108-l (L7, SC-001, T126a): the six audit acceptance checks as tests that
// are NAMED after the check, so a reader (and specs/108-profiles-v3/
// acceptance-checks.json) finds each one directly. Check 6 (Web deep links) is
// the Playwright spec e2e/web-ui-sweep/profiles-scope.spec.ts; the CLI half of
// check 2 is TestProfilesV3Acceptance_Check2_CLIListsBlockedRecord in
// cmd/mcpproxy. None of the names contains E2E, Binary or MCPProtocol, so the CI
// -skip regex runs them. Each test asserts the exact spec text and carries a
// mutation sub-test that inverts its premise, so a vacuous pass is impossible.
//
// The fixture is 108-d's newProfilesV3Fixture (the contracts/enforcement-matrix.md
// profiles, three counting upstreams); the helpers are the existing ones.

func p108ToolNames(resp profileV3RetrieveResponse) []string {
	names := make([]string, 0, len(resp.Tools))
	for _, tool := range resp.Tools {
		if name, ok := tool["name"].(string); ok {
			names = append(names, name)
		}
	}
	return names
}

// Check 1 (US1-1): a session under a read-only profile cannot discover a write,
// destructive, denied or unclassified tool, nor a server outside the profile; the
// response counts what it hid without naming it, and a pinned credential never
// learns it is pinned.
func TestProfilesV3Acceptance_Check1_DiscoveryHidesNonReadTools(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	indexEnforcementMatrixFixtureTools(t, proxy)
	cursor := clientCtx("cursor", "work-readonly", auth.ProfileModeLocked)

	// Every tool the profile must not reveal, and the read tool it must. A hidden
	// tool of an in-scope server is counted; a server outside the profile is not
	// revealed even as a count (FR-011), so filesystem adds nothing to the total.
	for _, hidden := range []string{"create_issue", "delete_repo", "search_code", "get_secret_scanning_alert"} {
		resp := callRetrieveToolsV3(t, proxy, cursor, hidden, 10)
		require.Empty(t, p108ToolNames(resp), "%s must not be discovered under work-readonly", hidden)
		require.NotNil(t, resp.HiddenByProfile, "%s: hidden_by_profile is reported", hidden)
		require.GreaterOrEqual(t, *resp.HiddenByProfile, 1, "%s: the count says something was hidden", hidden)
		require.Nil(t, resp.Profile, "a locked credential (pin source) never learns the profile")
	}
	require.Empty(t, p108ToolNames(callRetrieveToolsV3(t, proxy, cursor, "read_text_file", 10)), "no filesystem tool is returned")
	visible := callRetrieveToolsV3(t, proxy, cursor, "list_issues", 10)
	require.Equal(t, []string{"github:list_issues"}, p108ToolNames(visible))

	t.Run("the response counts hidden tools without naming one", func(t *testing.T) {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]interface{}{"query": "create_issue", "limit": float64(10)}
		result, err := proxy.handleRetrieveTools(cursor, req)
		require.NoError(t, err)
		var body map[string]any
		require.NoError(t, json.Unmarshal([]byte(resultText(t, result)), &body))
		require.Contains(t, body, "hidden_by_profile")
		delete(body, "query") // the caller's own query is echoed; nothing else may carry the name
		rest, err := json.Marshal(body)
		require.NoError(t, err)
		require.NotContains(t, string(rest), "create_issue", "a hidden tool is never named")
	})

	t.Run("the same session through /mcp/p/work-readonly with the admin key reports the profile", func(t *testing.T) {
		resp := callRetrieveToolsV3(t, proxy, urlProfileCtx(proxy, "work-readonly"), "create_issue", 10)
		require.Empty(t, p108ToolNames(resp))
		require.NotNil(t, resp.Profile)
		require.Equal(t, "work-readonly", *resp.Profile)
	})

	t.Run("mutation: a profile that allows write tools sees create_issue", func(t *testing.T) {
		wide, _ := newProfilesV3FixtureWithConfig(t, func(cfg *config.Config) { cfg.Profiles[0].MaxTier = "write" })
		indexEnforcementMatrixFixtureTools(t, wide)
		resp := callRetrieveToolsV3(t, wide, clientCtx("cursor", "work-readonly", auth.ProfileModeLocked), "create_issue", 10)
		require.Equal(t, []string{"github:create_issue"}, p108ToolNames(resp), "the assertion above is not vacuous")
	})
}

// Check 2, first half (US1-2): the write call is refused before any upstream I/O
// with the exact text, the refusal is the same bytes whichever way the profile
// was resolved, and a blocked activity record carries the attribution (client,
// profile, source) and block_reason=profile_tier. The second half (the record is
// listed by client+status on REST, the CLI, the Web UI and macOS) is
// TestActivityScopeParams_ListFiltersAndAliases (REST),
// TestProfilesV3Acceptance_Check2_CLIListsBlockedRecord (CLI),
// activity-scope-params.spec.ts (Web) and ScopeFilterTests (macOS).
func TestProfilesV3Acceptance_Check2_WriteRefusedAndRecorded(t *testing.T) {
	const refusal = "blocked by profile: github:create_issue is a write tool; this profile allows read tools only"
	proxy, rt := newProfilesV3Fixture(t)
	up := startCountingUpstream(t, proxy, rt, "github", writeSpec("create_issue"))
	go rt.ActivityService().Start(rt.AppContext(), rt)
	require.Eventually(t, rt.ActivityService().Started, 5*time.Second, time.Millisecond,
		"the activity service must subscribe before the refusal is emitted")

	cursor := clientCtx("cursor", "work-readonly", auth.ProfileModeLocked)
	result, err := proxy.handleCallToolVariant(cursor, auditCallToolRequest("github:create_issue", nil), "call_tool_write")
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Equal(t, refusal, resultText(t, result))
	require.Empty(t, up.dispatched(), "refused before any upstream I/O")

	var rec *storage.ActivityRecord
	require.Eventually(t, func() bool {
		records, _, listErr := rt.StorageManager().ListActivities(storage.ActivityFilter{ClientID: "cursor", Status: "blocked", Limit: 50})
		require.NoError(t, listErr)
		for _, r := range records {
			if r.ServerName == "github" && r.ToolName == "create_issue" {
				rec = r
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "a blocked record is listed by client+status (the SC-006 filter)")
	require.Equal(t, "blocked", rec.Status)
	require.Equal(t, string(profile.BlockReasonTier), rec.Metadata[storage.MetadataKeyBlockReason])
	require.Equal(t, "cursor", rec.ClientID)
	require.Equal(t, "work-readonly", rec.EffectiveProfile())
	require.Equal(t, string(profile.SourcePin), rec.ProfileSource)

	t.Run("the refusal is the same bytes for every resolution source", func(t *testing.T) {
		for name, ctx := range map[string]context.Context{
			"url":        urlProfileCtx(proxy, "work-readonly"),
			"session":    sessionProfileCtx(t, proxy, "acceptance-check2", "work-readonly"),
			"pin":        pinnedProfileCtx("work-readonly"),
			"binding":    clientCtx("desktop", "work-readonly", auth.ProfileModeSwitchable),
			"cursor pin": cursor,
		} {
			res, callErr := proxy.handleCallToolVariant(ctx, auditCallToolRequest("github:create_issue", nil), "call_tool_write")
			require.NoError(t, callErr, name)
			require.Equal(t, refusal, resultText(t, res), name)
		}
	})

	t.Run("mutation: a profile that allows writes dispatches the call", func(t *testing.T) {
		full, fullRT := newProfilesV3Fixture(t)
		fullUp := startCountingUpstream(t, full, fullRT, "github", writeSpec("create_issue"))
		res, callErr := full.handleCallToolVariant(clientCtx("cursor", "work-full", auth.ProfileModeLocked), auditCallToolRequest("github:create_issue", nil), "call_tool_write")
		require.NoError(t, callErr)
		require.False(t, res.IsError, resultText(t, res))
		require.NotEmpty(t, fullUp.dispatched())
	})
}

// Check 3 (US1-3): an unannotated tool is hidden until an operator classifies it;
// describe_tool answers the uniform not-found; the call is refused with
// profile_unannotated; classifying it read makes it visible and callable
// within one request. The classification is made through the MCP `profiles` tool
// (the same operation as `profile classify`, the Web UI and macOS editor).
func TestProfilesV3Acceptance_Check3_UnannotatedHiddenUntilClassified(t *testing.T) {
	f := newProfilesToolFixture(t, nil)
	indexEnforcementMatrixFixtureTools(t, f.proxy)
	cursor := clientCtx("cursor", "work-readonly", auth.ProfileModeLocked)
	const tool = "github:search_code" // carries no annotations in the fixture

	// Before: absent from discovery, uniform not-found, refused with profile_unannotated.
	require.Empty(t, p108ToolNames(callRetrieveToolsV3(t, f.proxy, cursor, "search_code", 10)))
	missing := callDescribe(t, f.proxy, cursor, []interface{}{tool})
	nonexistent := callDescribe(t, f.proxy, cursor, []interface{}{"github:does_not_exist_at_all"})
	require.Empty(t, missing.Definitions)
	require.Len(t, missing.Errors, 1)
	missing.Errors[0]["id"], nonexistent.Errors[0]["id"] = "X", "X"
	require.Equal(t, nonexistent.Errors[0], missing.Errors[0], "describe_tool answers the uniform not-found")

	callBefore, err := f.proxy.handleCallToolVariant(cursor, auditCallToolRequest(tool, nil), "call_tool_read")
	require.NoError(t, err)
	require.True(t, callBefore.IsError)
	require.Equal(t, "blocked by profile: github:search_code has no tier annotation; an operator can classify it in the profile to allow it", resultText(t, callBefore))
	require.Empty(t, f.upstreams["github"].dispatched())

	// Classify it read from the MCP tool; no restart, no reload step.
	out := f.ok(map[string]any{"operation": "classify", "name": "work-readonly", "tool": tool, "tier": "read"})
	require.NotNil(t, out)

	after := callRetrieveToolsV3(t, f.proxy, cursor, "search_code", 10)
	require.Equal(t, []string{tool}, p108ToolNames(after), "visible within one request of the classification")
	callAfter, err := f.proxy.handleCallToolVariant(cursor, auditCallToolRequest(tool, nil), "call_tool_read")
	require.NoError(t, err)
	require.False(t, callAfter.IsError, resultText(t, callAfter))
	require.NotEmpty(t, f.upstreams["github"].dispatched(), "and callable")

	t.Run("mutation: clearing the classification hides it again", func(t *testing.T) {
		f.ok(map[string]any{"operation": "classify", "name": "work-readonly", "tool": tool, "tier": ""})
		require.Empty(t, p108ToolNames(callRetrieveToolsV3(t, f.proxy, cursor, "search_code", 10)))
	})
}

// Check 4 (US2-2): reassigning a client takes effect on its next request without
// touching its credential (so its config file, which holds the secret, stays
// byte-identical), and one profile_change record names the surface, client and
// both profiles. The live-session tools/list_changed notification is
// TestProfilesTool_AssignSendsListChangedLikeREST (MCP) and
// TestBindingChange_NotifiesEachSessionOfTheTokenOnce (REST) in this package.
func TestProfilesV3Acceptance_Check4_ReassignTakesEffectNextRequest(t *testing.T) {
	if !clientsEdition {
		t.Skip("client credentials exist in the personal edition only")
	}
	f := newProfilesToolFixture(t, nil)
	indexEnforcementMatrixFixtureTools(t, f.proxy)
	secret := f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)

	// asCursor authenticates the stored credential exactly as the MCP auth
	// middleware does, so the context carries whatever the record says NOW.
	asCursor := func() context.Context {
		tok, err := f.rt.StorageManager().ValidateAgentToken(secret, f.hmacKey)
		require.NoError(t, err)
		return auth.WithAuthContext(context.Background(), tok.AuthContext())
	}
	resolve := func() (string, string) {
		res := f.proxy.ResolveProfileV3(asCursor(), f.proxy.profileIndexFor(f.proxy.currentConfig()))
		return res.Name, res.Source
	}

	name, source := resolve()
	require.Equal(t, "work-readonly", name)
	require.Equal(t, string(profile.SourcePin), source)
	require.Empty(t, p108ToolNames(callRetrieveToolsV3(t, f.proxy, asCursor(), "create_issue", 10)))
	before, err := f.rt.StorageManager().ValidateAgentToken(secret, f.hmacKey)
	require.NoError(t, err)

	// The reassignment (the MCP form of `mcpproxy client set-profile cursor work-full`).
	f.ok(map[string]any{"operation": "assign", "client": "cursor", "profile": "work-full"})

	name, source = resolve()
	require.Equal(t, "work-full", name, "the next request resolves to the new profile")
	require.Equal(t, string(profile.SourcePin), source, "a locked credential keeps resolving at the pin tier")
	require.Equal(t, []string{"github:create_issue"}, p108ToolNames(callRetrieveToolsV3(t, f.proxy, asCursor(), "create_issue", 10)),
		"create_issue is discoverable under work-full on the very next request")

	after, err := f.rt.StorageManager().ValidateAgentToken(secret, f.hmacKey)
	require.NoError(t, err, "the same secret still authenticates: nothing was re-issued, so the client's config file stays valid and unchanged")
	require.Equal(t, before.TokenPrefix, after.TokenPrefix)
	require.Equal(t, auth.ProfileModeLocked, after.ProfileMode, "the lock is kept")

	require.Eventually(t, func() bool {
		recs, _, listErr := f.rt.StorageManager().ListActivities(storage.ActivityFilter{Types: []string{string(storage.ActivityTypeProfileChange)}, Limit: 50})
		require.NoError(t, listErr)
		changes := 0
		for _, r := range recs {
			if r.Metadata["change"] == string(profile.ChangeAssign) {
				changes++
				require.Equal(t, string(profile.SurfaceMCP), r.Metadata["surface"])
			}
		}
		return changes == 1
	}, 5*time.Second, 10*time.Millisecond, "exactly one profile_change record names the surface")

	t.Run("mutation: before the reassignment the same call was hidden", func(t *testing.T) {
		g := newProfilesToolFixture(t, nil)
		indexEnforcementMatrixFixtureTools(t, g.proxy)
		_ = g.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)
		require.Empty(t, p108ToolNames(callRetrieveToolsV3(t, g.proxy, clientCtx("cursor", "work-readonly", auth.ProfileModeLocked), "create_issue", 10)))
	})
}

// Check 5 (US2-4, US2-5): a locked client cannot switch (set_profile and the
// profile URL answer the uniform refusal, its own pin is admitted); a switchable
// one reaches only its declared targets; the management tool `profiles` exists
// for an administrator session only.
func TestProfilesV3Acceptance_Check5_LockedCannotSwitchAndManagementHidden(t *testing.T) {
	proxy, _ := newProfilesV3Fixture(t)
	idx := proxy.profileIndexFor(proxy.currentConfig())
	setProfile := func(ctx context.Context, target string) (*mcp.CallToolResult, error) {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]interface{}{"profile": target}
		return proxy.handleSetProfile(ctx, req)
	}

	t.Run("locked Cursor: set_profile work-full gets the uniform refusal and nothing is stored", func(t *testing.T) {
		ctx := sessionCtx(clientCtx("cursor", "work-readonly", auth.ProfileModeLocked), "acc5-locked")
		res, err := setProfile(ctx, "work-full")
		require.NoError(t, err)
		require.True(t, res.IsError)
		require.Equal(t, "unknown profile 'work-full'", resultText(t, res))
		require.Empty(t, proxy.sessionStore.GetActiveProfile("acc5-locked"))
		require.Equal(t, "work-readonly", proxy.ResolveProfileV3(ctx, idx).Name)
	})

	t.Run("locked Cursor: /mcp/p/work-full is refused like a missing profile, its own /mcp/p/work-readonly is admitted", func(t *testing.T) {
		srv := proxy.mainServer
		srv.logger = zap.NewNop()
		srv.mcpProxy = proxy
		handler := (profileGateFleet{srv: srv, cfg: proxy.currentConfig()}).handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		get := func(slug string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodPost, "/mcp/p/"+slug, http.NoBody).
				WithContext(clientCtx("cursor", "work-readonly", auth.ProfileModeLocked))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			return rec
		}
		require.Equal(t, http.StatusOK, get("work-readonly").Code, "naming its own pin is not a switch (FR-022)")
		other, missing := get("work-full"), get("no-such-profile")
		require.Equal(t, http.StatusNotFound, other.Code)
		require.Equal(t, strings.ReplaceAll(missing.Body.String(), "no-such-profile", "work-full"), other.Body.String())
	})

	t.Run("switchable Codex: set_profile reaches its declared target and nothing else", func(t *testing.T) {
		ctx := sessionCtx(clientCtx("codex", "work-readonly", auth.ProfileModeSwitchable), "acc5-switchable")
		ok, err := setProfile(ctx, "work-full")
		require.NoError(t, err)
		require.False(t, ok.IsError, resultText(t, ok))
		require.Equal(t, "work-full", proxy.sessionStore.GetActiveProfile("acc5-switchable"))

		refused, err := setProfile(sessionCtx(clientCtx("codex", "work-readonly", auth.ProfileModeSwitchable), "acc5-switchable-2"), "admin-all")
		require.NoError(t, err)
		require.True(t, refused.IsError)
		require.Equal(t, "unknown profile 'admin-all'", resultText(t, refused), "an undeclared target gets the uniform refusal")
	})

	t.Run("the profiles management tool is for an administrator session only", func(t *testing.T) {
		f := newProfilesToolFixture(t, nil)
		tools := []mcp.Tool{{Name: "profiles"}, {Name: "call_tool_read"}}
		for name, tc := range map[string]struct {
			ctx  context.Context
			want bool
		}{
			"admin API key": {apiKeyCtx(), true},
			"locked Cursor": {clientCtx("cursor", "work-readonly", auth.ProfileModeLocked), false},
			"anonymous":     {anonCtx(), false},
		} {
			names := profileV3ToolNames(f.proxy.filterProfileV3Tools(tc.ctx, tools))
			require.Equal(t, tc.want, containsName(names, "profiles"), "tools/list for %s", name)
			res, err := f.proxy.handleProfiles(tc.ctx, mcpCallRequest(map[string]any{"operation": "list"}))
			require.NoError(t, err)
			require.Equal(t, !tc.want, res.IsError, "a call from %s", name)
		}
	})
}
