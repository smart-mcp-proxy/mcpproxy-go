package server

// Issue #1548: preflight (REST POST /api/v1/preflight, the CLI that drives it,
// and describe_tool check:true) must evaluate the caller's EFFECTIVE profile
// tool policy — tier cap, deny rules, unannotated handling, classification —
// with the decision dispatch makes, so a tool dispatch refuses under a profile
// never reads as ready.
//
// Everything here runs against the Spec 108 enforcement-matrix fixture: real
// in-process upstreams (every dispatch is counted), real stored agent tokens,
// the real REST router and auth middleware, and the real dispatch handler. No
// fake prefilters the policy cases away.
//
// work-readonly (Title "Work · Read-only"): servers github+notion, max_tier
// read, allow [notion:update_page, github:get_secret_scanning_alert,
// filesystem:read_text_file], deny [github:*secret*].

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
)

// newPreflightPolicyFixture is the enforcement-matrix REST fixture with each
// upstream's published connection state set to "ready". The shared fixture
// stamps Connected/ToolsDiscovered but leaves State empty, which preflight
// (correctly) answers as server_initializing; the live daemon publishes it.
func newPreflightPolicyFixture(t *testing.T, configure func(*config.Config)) *restV3Fixture {
	t.Helper()
	f := newProfilesV3RESTFixture(t, configure)
	for name := range f.upstreams {
		f.rt.Supervisor().StateView().UpdateServer(name, func(s *stateview.ServerStatus) { s.State = "ready" })
	}
	return f
}

// assertMatrixRow ties an observed (reason, verdict) to its committed sabotage
// matrix row, so the FR-016 coverage gate's rows are exercised, not just listed.
func assertMatrixRow(t *testing.T, scenario, reason, verdict string) {
	t.Helper()
	row, ok := loadSabotageMatrix(t)[scenario]
	require.True(t, ok, "scenario %q missing from %s", scenario, preflightMatrixPath)
	assert.Equal(t, row.Expect.Reason, reason, scenario)
	if row.Expect.Reason == preflight.ReasonToolBlockedByProfile {
		assert.Equal(t, row.Expect.Verdict, verdict, scenario)
	}
	assert.Equal(t, row.Expect.Verdict, preflight.ReasonVerdict(reason), scenario)
}

type preflightWireResult struct {
	ID         string   `json:"id"`
	Status     string   `json:"status"`
	Reason     string   `json:"reason"`
	Detail     string   `json:"detail"`
	DidYouMean []string `json:"did_you_mean"`
}

type preflightWire struct {
	Verdict string                `json:"verdict"`
	Tools   []preflightWireResult `json:"tools"`
}

// restPreflight POSTs one preflight with key and returns the HTTP status and
// the decoded data.
func (f *restV3Fixture) restPreflight(t *testing.T, key, profileName string, ids ...string) (int, preflightWire) {
	t.Helper()
	tools := make([]map[string]string, 0, len(ids))
	for _, id := range ids {
		tools = append(tools, map[string]string{"id": id})
	}
	body := map[string]interface{}{"tools": tools}
	if profileName != "" {
		body["profile"] = profileName
	}
	rec := f.do(http.MethodPost, "/api/v1/preflight", key, body, "")
	var env struct {
		Success bool          `json:"success"`
		Data    preflightWire `json:"data"`
	}
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env), rec.Body.String())
		require.True(t, env.Success, rec.Body.String())
	}
	return rec.Code, env.Data
}

func (w preflightWire) byID(t *testing.T, id string) preflightWireResult {
	t.Helper()
	for _, r := range w.Tools {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no result for %q in %+v", id, w.Tools)
	return preflightWireResult{}
}

// inBandCheck runs describe_tool check:true under ctx.
func inBandCheck(t *testing.T, proxy *MCPProxyServer, ctx context.Context, ids ...string) describeCheckPayload {
	t.Helper()
	raw := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		raw = append(raw, id)
	}
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"tool_ids": raw, "check": true}
	result, err := proxy.handleDescribeTool(ctx, req)
	require.NoError(t, err)
	require.False(t, result.IsError, "check returned an error result: %v", resultText(t, result))
	var payload describeCheckPayload
	require.NoError(t, json.Unmarshal([]byte(resultText(t, result)), &payload))
	return payload
}

// The issue's core repro, operator tier: the profile named in the body blocks
// above-cap tools and denied tools; permitted reads and allowed exceptions stay
// ready; HTTP stays 200 with a non-ready semantic verdict.
func TestPreflightProfilePolicy_RESTOperatorTier(t *testing.T) {
	f := newPreflightPolicyFixture(t, nil)

	code, out := f.restPreflight(t, restV3AdminKey, restV3ReadonlyID,
		"github:list_issues",               // read, permitted
		"notion:update_page",               // write, allowed exact exception
		"github:create_issue",              // write above the read cap
		"github:delete_repo",               // destructive above the read cap
		"github:search_code",               // unannotated
		"github:get_secret_scanning_alert", // read, allowed AND denied: deny wins
	)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, preflight.VerdictBlocked, out.Verdict)

	assert.Equal(t, preflight.StatusReady, out.byID(t, "github:list_issues").Status)
	assert.Equal(t, preflight.StatusReady, out.byID(t, "notion:update_page").Status, "an allowed exact exception stays ready")

	for _, id := range []string{"github:create_issue", "github:delete_repo", "github:search_code", "github:get_secret_scanning_alert"} {
		res := out.byID(t, id)
		assert.Equal(t, preflight.StatusUnavailable, res.Status, id)
		assert.Equal(t, preflight.ReasonToolBlockedByProfile, res.Reason, id)
		assert.True(t, strings.HasPrefix(res.Detail, "blocked by profile: "+id), "%s detail: %s", id, res.Detail)
	}
	assert.Contains(t, out.byID(t, "github:create_issue").Detail, "is a write tool")
	assertMatrixRow(t, "tool_blocked_by_profile", out.byID(t, "github:create_issue").Reason, out.Verdict)
	assert.Contains(t, out.byID(t, "github:get_secret_scanning_alert").Detail, "is denied by a rule")

	// Control: the same operator WITHOUT a profile has no tool policy in effect.
	code, out = f.restPreflight(t, restV3AdminKey, "", "github:create_issue", "github:get_secret_scanning_alert")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, preflight.VerdictReady, out.Verdict)

	assert.Zero(t, f.totalDispatched(), "preflight must never reach an upstream")
}

// A pinned agent token is evaluated under its pin with or without a profile in
// the body; the agent-token tier gets the scope-silent not_found, the same
// answer describe_tool and retrieve_tools give for a profile-hidden tool.
func TestPreflightProfilePolicy_RESTPinnedToken(t *testing.T) {
	f := newPreflightPolicyFixture(t, func(cfg *config.Config) { cfg.RequireMCPAuth = true })
	key := f.mint("ro-agent", restV3ReadonlyID)

	for _, body := range []string{"", restV3ReadonlyID} {
		t.Run("profile="+body, func(t *testing.T) {
			code, out := f.restPreflight(t, key, body, "github:list_issues", "github:create_issue", "github:get_secret_scanning_alert", "github:no_such_tool")
			require.Equal(t, http.StatusOK, code)
			assert.Equal(t, preflight.StatusReady, out.byID(t, "github:list_issues").Status)
			absent := out.byID(t, "github:no_such_tool")
			require.Equal(t, preflight.ReasonNotFound, absent.Reason)
			for _, id := range []string{"github:create_issue", "github:get_secret_scanning_alert"} {
				res := out.byID(t, id)
				assert.Equal(t, preflight.ReasonNotFound, res.Reason, id)
				assert.Equal(t, absent.Detail, res.Detail, "byte-identical to an absent tool")
				assert.NotContains(t, res.Detail, "profile")
			}
			assert.NotEqual(t, preflight.VerdictReady, out.Verdict)
		})
	}

	// Typos never suggest a profile-hidden tool.
	_, out := f.restPreflight(t, key, "", "github:create_issu")
	assert.NotContains(t, out.byID(t, "github:create_issu").DidYouMean, "github:create_issue")

	// Execution parity: dispatch refuses the same tools and never reaches the
	// upstream; the permitted read executes.
	rec := f.callTool(key, "call_tool_write", map[string]interface{}{"name": "github:create_issue", "args_json": "{}"}, "")
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	assert.Contains(t, restV3Error(t, rec), "blocked by profile: github:create_issue is a write tool")
	rec = f.callTool(key, "call_tool_read", map[string]interface{}{"name": "github:get_secret_scanning_alert", "args_json": "{}"}, "")
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	assert.Zero(t, f.totalDispatched(), "neither preflight nor a blocked call may dispatch")

	rec = f.callTool(key, "call_tool_read", map[string]interface{}{"name": "github:list_issues", "args_json": "{}"}, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Len(t, f.upstreams["github"].dispatched(), 1, "only the permitted read reaches the upstream")
}

// An explicit request profile can only NARROW the credential's pin.
func TestPreflightProfilePolicy_RequestProfileOnlyNarrows(t *testing.T) {
	f := newPreflightPolicyFixture(t, func(cfg *config.Config) { cfg.RequireMCPAuth = true })
	full := f.mint("full-agent", "work-full")
	ro := f.mint("ro-agent", restV3ReadonlyID)

	// Pinned wide, asking narrow: narrowed.
	_, out := f.restPreflight(t, full, "", "github:create_issue")
	require.Equal(t, preflight.StatusReady, out.byID(t, "github:create_issue").Status, "control: work-full admits the write")
	_, out = f.restPreflight(t, full, restV3ReadonlyID, "github:create_issue")
	assert.Equal(t, preflight.ReasonNotFound, out.byID(t, "github:create_issue").Reason)

	// Pinned narrow, asking wide: never widened.
	_, out = f.restPreflight(t, ro, "work-full", "github:create_issue", "github:list_issues")
	assert.Equal(t, preflight.ReasonNotFound, out.byID(t, "github:create_issue").Reason)
	assert.Equal(t, preflight.StatusReady, out.byID(t, "github:list_issues").Status)
	assert.Zero(t, f.totalDispatched())
}

// Token permission tiers compose with the profile: an all-tiers token does not
// lift the profile's cap, and a read-only token keeps its permitted read.
func TestPreflightProfilePolicy_ComposesWithTokenPermissions(t *testing.T) {
	f := newPreflightPolicyFixture(t, func(cfg *config.Config) { cfg.RequireMCPAuth = true })
	allTiers := f.mint("all-tiers", restV3ReadonlyID)
	readOnly := f.mintWith("read-only", restV3ReadonlyID, []string{"*"}, []string{auth.PermRead})

	for name, key := range map[string]string{"all tiers": allTiers, "read only": readOnly} {
		t.Run(name, func(t *testing.T) {
			_, out := f.restPreflight(t, key, "", "github:list_issues", "github:delete_repo")
			assert.Equal(t, preflight.StatusReady, out.byID(t, "github:list_issues").Status)
			assert.Equal(t, preflight.ReasonNotFound, out.byID(t, "github:delete_repo").Reason)
		})
	}
	assert.Zero(t, f.totalDispatched())
}

// A pin whose profile no longer exists stays deny-all; it never falls back to
// an unprofiled (wider) view.
func TestPreflightProfilePolicy_DeletedPinDeniesAll(t *testing.T) {
	f := newPreflightPolicyFixture(t, func(cfg *config.Config) { cfg.RequireMCPAuth = true })
	key := f.mint("orphan", "gone")
	_, out := f.restPreflight(t, key, "", "github:list_issues", "github:create_issue")
	assert.Equal(t, preflight.ReasonNotFound, out.byID(t, "github:list_issues").Reason)
	assert.Equal(t, preflight.ReasonNotFound, out.byID(t, "github:create_issue").Reason)
	assert.Equal(t, preflight.VerdictUnknownIDs, out.Verdict)
}

// A policy edit between two requests applies to the very next preflight:
// neither a tightened nor a loosened policy is answered from a stale snapshot.
func TestPreflightProfilePolicy_PolicyChangeBetweenRequests(t *testing.T) {
	f := newPreflightPolicyFixture(t, func(cfg *config.Config) { cfg.RequireMCPAuth = true })
	f.wireProfiles()
	key := f.mint("full-agent", "work-full")

	_, out := f.restPreflight(t, key, "", "github:create_issue")
	require.Equal(t, preflight.StatusReady, out.byID(t, "github:create_issue").Status)
	_, out = f.restPreflight(t, restV3AdminKey, "work-full", "github:create_issue")
	require.Equal(t, preflight.StatusReady, out.byID(t, "github:create_issue").Status)

	status, resp := f.json(http.MethodPut, "/api/v1/profiles/work-full", map[string]interface{}{
		"name": "work-full", "servers": []string{"github", "notion", "filesystem"},
		"max_tier": "destructive", "unannotated": "as_write",
		"tools": map[string]interface{}{"deny": []string{"github:create_issue"}},
	})
	require.Equal(t, http.StatusOK, status, resp)

	_, out = f.restPreflight(t, key, "", "github:create_issue")
	assert.Equal(t, preflight.ReasonNotFound, out.byID(t, "github:create_issue").Reason, "a tightened policy applies to the next request")
	_, out = f.restPreflight(t, restV3AdminKey, "work-full", "github:create_issue")
	assert.Equal(t, preflight.ReasonToolBlockedByProfile, out.byID(t, "github:create_issue").Reason)

	// And dispatch agrees.
	rec := f.callTool(key, "call_tool_write", map[string]interface{}{"name": "github:create_issue", "args_json": "{}"}, "")
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	assert.Zero(t, f.totalDispatched())
}

// describe_tool check:true in a pinned session, in a URL-scoped admin session,
// and as an unprofiled admin (control).
func TestPreflightProfilePolicy_InBandCheck(t *testing.T) {
	f := newPreflightPolicyFixture(t, nil)
	allPerms := []string{auth.PermRead, auth.PermWrite, auth.PermDestructive}

	pinned := agentCtx([]string{"*"}, allPerms, restV3ReadonlyID)
	urlScoped := profile.WithProfileScope(adminCtx(), profileScopeForSlugIn(f.cfg, restV3ReadonlyID))

	for name, ctx := range map[string]context.Context{"pinned token": pinned, "url-scoped admin": urlScoped} {
		t.Run(name, func(t *testing.T) {
			payload := inBandCheck(t, f.proxy, ctx,
				"github:list_issues", "notion:update_page", "github:create_issue",
				"github:get_secret_scanning_alert", "github:search_code", "github:no_such_tool")
			absent := checkResultByID(t, payload, "github:no_such_tool")
			require.Equal(t, preflight.ReasonNotFound, absent.Reason)
			hidden := checkResultByID(t, payload, "github:create_issue")
			assertMatrixRow(t, "mcp_check_profile_hidden", hidden.Reason, preflight.ReasonVerdict(hidden.Reason))
			assert.Equal(t, preflight.StatusReady, checkResultByID(t, payload, "github:list_issues").Status)
			assert.Equal(t, preflight.StatusReady, checkResultByID(t, payload, "notion:update_page").Status)
			for _, id := range []string{"github:create_issue", "github:get_secret_scanning_alert", "github:search_code"} {
				res := checkResultByID(t, payload, id)
				assert.Equal(t, preflight.ReasonNotFound, res.Reason, id)
				assert.Equal(t, absent.Detail, res.Detail, id)
			}
			assert.NotEqual(t, preflight.VerdictReady, payload.Verdict)
		})
	}

	payload := inBandCheck(t, f.proxy, adminCtx(), "github:create_issue", "github:get_secret_scanning_alert")
	assert.Equal(t, preflight.VerdictReady, payload.Verdict, "an unprofiled admin session has no tool policy")
	assert.Zero(t, f.totalDispatched())
}

// Evaluator/dispatch parity across every fixture tool under every fixture
// profile: the operator-tier preflight reports tool_blocked_by_profile exactly
// when dispatch under a token pinned to that profile refuses with a profile
// refusal, and the refusal text is the same bytes. Blocked dispatches never
// reach an upstream.
func TestPreflightProfilePolicy_DispatchParity(t *testing.T) {
	f := newPreflightPolicyFixture(t, nil)
	allPerms := []string{auth.PermRead, auth.PermWrite, auth.PermDestructive}
	tools := map[string]string{
		"github:list_issues":               contracts.ToolVariantRead,
		"github:create_issue":              contracts.ToolVariantWrite,
		"github:delete_repo":               contracts.ToolVariantDestructive,
		"github:search_code":               contracts.ToolVariantDestructive,
		"github:get_secret_scanning_alert": contracts.ToolVariantRead,
		"notion:update_page":               contracts.ToolVariantWrite,
	}
	ids := make([]string, 0, len(tools))
	for id := range tools {
		ids = append(ids, id)
	}

	blockedSeen, readySeen := 0, 0
	for _, profileName := range []string{restV3ReadonlyID, "work-full", "legacy"} {
		code, out := f.restPreflight(t, restV3AdminKey, profileName, ids...)
		require.Equal(t, http.StatusOK, code)

		for id, variant := range tools {
			res := out.byID(t, id)
			if res.Reason == preflight.ReasonServerNotInScope {
				continue // outside the profile's servers; the scope gate owns it
			}
			before := f.totalDispatched()
			result, err := f.proxy.handleCallToolVariant(agentCtx([]string{"*"}, allPerms, profileName), auditCallToolRequest(id, nil), variant)
			require.NoError(t, err)
			text := ""
			if result.IsError {
				text = resultText(t, result)
			}
			profileRefused := strings.HasPrefix(text, "blocked by profile: ")
			if res.Reason == preflight.ReasonToolBlockedByProfile {
				blockedSeen++
				assert.True(t, profileRefused, "%s under %s: preflight blocked, dispatch said %q", id, profileName, text)
				assert.Equal(t, res.Detail, text, "%s under %s: the same refusal bytes", id, profileName)
				assert.Equal(t, before, f.totalDispatched(), "%s under %s: a blocked call must not dispatch", id, profileName)
			} else {
				readySeen++
				assert.Equal(t, preflight.StatusReady, res.Status, "%s under %s", id, profileName)
				assert.False(t, profileRefused, "%s under %s: preflight ready but dispatch refused by profile: %q", id, profileName, text)
			}
		}
	}
	require.NotZero(t, blockedSeen, "the parity table must exercise blocked rows")
	require.NotZero(t, readySeen, "the parity table must exercise ready rows")
}

// The v3 resolution's own scope composes into the in-band check too: an
// anonymous caller under the FR-008a binding guard (a named client binding
// exists and no anonymous_profile confines anonymous access) is deny-all for
// dispatch, so the check must not answer ready — nor suggest any tool.
func TestPreflightProfilePolicy_InBandAnonymousBindingGuard(t *testing.T) {
	f := newPreflightPolicyFixture(t, func(cfg *config.Config) { cfg.RequireMCPAuth = false })

	payload := inBandCheck(t, f.proxy, anonCtx(), "github:list_issues", "github:list_issue")
	require.Equal(t, preflight.StatusReady, checkResultByID(t, payload, "github:list_issues").Status,
		"control: with no named binding, anonymous access is legacy-unrestricted")

	f.mintClient("cursor", restV3ReadonlyID, auth.ProfileModeLocked)
	payload = inBandCheck(t, f.proxy, anonCtx(), "github:list_issues", "github:list_issue")
	assert.Equal(t, preflight.ReasonNotFound, checkResultByID(t, payload, "github:list_issues").Reason)
	assert.Empty(t, checkResultByID(t, payload, "github:list_issue").DidYouMean, "a deny-all scope suggests nothing")

	result, err := f.proxy.handleCallToolVariant(anonCtx(), auditCallToolRequest("github:list_issues", nil), contracts.ToolVariantRead)
	require.NoError(t, err)
	assert.True(t, result.IsError, "dispatch agrees: the guarded anonymous caller is refused")
	assert.Zero(t, f.totalDispatched())
}

// Direct-surface check mode composes the v3 resolution too: a switchable
// client bound to a permissive profile that selected a restrictive one through
// /mcp/p/<slug> is decided by the SELECTED profile at dispatch, so the check
// must hide what it excludes — in the verdict and in did_you_mean, including
// when every id in the batch is gated before evaluation.
func TestPreflightProfilePolicy_DirectCheckUsesEffectiveSelection(t *testing.T) {
	f := newDirectCheckFixture(t)
	switchTo := []string{"restricted"}
	f.proxy.config.Servers = []*config.ServerConfig{{Name: "github", Enabled: true}, {Name: "we__ird", Enabled: true}}
	f.proxy.config.Profiles = []config.ProfileConfig{
		{Name: "permissive", Servers: []string{"github"}, MaxTier: "destructive", SwitchableTo: &switchTo},
		{Name: "restricted", Servers: []string{"github"}, MaxTier: "destructive", Tools: &config.ProfileToolRules{Deny: []string{"github:read_file"}}},
	}
	ctx := clientCtx("laptop", "permissive", auth.ProfileModeSwitchable)
	ctx = profile.WithProfileScope(ctx, profileScopeForSlugIn(f.proxy.config, "restricted"))

	// Control: under the binding alone the tool is visible and ready.
	control := f.check(t, clientCtx("laptop", "permissive", auth.ProfileModeSwitchable), []interface{}{"github:read_file"})
	require.Equal(t, preflight.StatusReady, control.Results[0].Status, "%+v", control.Results[0])

	// All ids gated: the early return must not suggest the hidden tool.
	payload := f.check(t, ctx, []interface{}{"github:read_fil"})
	assert.NotContains(t, payload.Results[0].DidYouMean, "github:read_file")
	assert.NotContains(t, payload.Results[0].DidYouMean, "github__read_file")

	payload = f.check(t, ctx, []interface{}{"github:read_file", "github__read_file", "we__ird__do_thing"})
	byID := checkResultsByID(payload)
	assert.Equal(t, preflight.ReasonNotFound, byID["github:read_file"].Reason)
	assert.Equal(t, preflight.ReasonNotFound, byID["github__read_file"].Reason)
	assert.Equal(t, preflight.ReasonNotFound, byID["we__ird__do_thing"].Reason, "outside the selected profile's servers")
}

// On the direct surface a selection-hidden exact id must answer what an absent
// id answers in every server state — the planner's not_found — never the
// evaluator's connection verdict, in both id grammars.
func TestPreflightProfilePolicy_DirectCheckHiddenEqualsAbsentInEveryState(t *testing.T) {
	f := newDirectCheckFixture(t)
	switchTo := []string{"restricted"}
	f.proxy.config.Servers = []*config.ServerConfig{{Name: "github", Enabled: true}}
	f.proxy.config.Profiles = []config.ProfileConfig{
		{Name: "permissive", Servers: []string{"github"}, MaxTier: "destructive", SwitchableTo: &switchTo},
		{Name: "restricted", Servers: []string{"github"}, MaxTier: "destructive", Tools: &config.ProfileToolRules{Deny: []string{"github:read_file"}}},
	}
	ctx := clientCtx("laptop", "permissive", auth.ProfileModeSwitchable)
	ctx = profile.WithProfileScope(ctx, profileScopeForSlugIn(f.proxy.config, "restricted"))

	for _, state := range []preflight.ServerRuntimeState{preflight.RuntimeStateReady, preflight.RuntimeStateError, preflight.RuntimeStateConnecting, preflight.RuntimeStatePendingAuth} {
		t.Run(string(state), func(t *testing.T) {
			f.proxy.preflightStateSource = func() (preflight.StateReader, func(serverName, toolName string) *config.ToolAnnotations, error) {
				return stubState{state: state}, func(string, string) *config.ToolAnnotations { return nil }, nil
			}
			t.Cleanup(func() { f.proxy.preflightStateSource = nil })

			payload := f.check(t, ctx, []interface{}{"github:read_file", "github__read_file", "github:no_such_tool", "github__no_such_tool"})
			byID := checkResultsByID(payload)
			norm := func(r describeCheckResult) describeCheckResult { r.ID = ""; r.DidYouMean = nil; return r }
			assert.Equal(t, norm(byID["github:no_such_tool"]), norm(byID["github:read_file"]), "canonical grammar")
			assert.Equal(t, norm(byID["github__no_such_tool"]), norm(byID["github__read_file"]), "display grammar")
			assert.Equal(t, preflight.ReasonNotFound, byID["github:read_file"].Reason)
		})
	}
}

// A tool the effective selection hides must not take part in direct-id
// resolution at all: in a display/canonical collision it can neither win the
// display lookup nor shadow the permitted canonical owner, so the answer is the
// same whether or not the hidden tool exists.
func TestPreflightProfilePolicy_DirectCheckHiddenToolNeverShadows(t *testing.T) {
	check := func(t *testing.T, includeHidden bool) describeCheckResult {
		t.Helper()
		p := directCanonicalOverlapFixture(t, includeHidden)
		p.preflightRecorder = func(_ internalRuntime.PreflightActivity) error { return nil }
		switchTo := []string{"restricted"}
		p.config.Servers = []*config.ServerConfig{{Name: "x", Enabled: true}, {Name: "x__y", Enabled: true}}
		p.config.Profiles = []config.ProfileConfig{
			{Name: "permissive", Servers: []string{"x", "x__y"}, MaxTier: "destructive", SwitchableTo: &switchTo},
			{Name: "restricted", Servers: []string{"x", "x__y"}, MaxTier: "destructive", Tools: &config.ProfileToolRules{Deny: []string{"x:y:z"}}},
		}
		ctx := clientCtx("laptop", "permissive", auth.ProfileModeSwitchable)
		ctx = profile.WithProfileScope(ctx, profileScopeForSlugIn(p.config, "restricted"))
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]interface{}{"tool_ids": []interface{}{"x__y:z"}, "check": true}
		result, err := p.describeToolHandler(describeSurfaceDirect)(ctx, req)
		require.NoError(t, err)
		require.False(t, result.IsError, "%v", result.Content)
		var payload describeCheckPayload
		require.NoError(t, json.Unmarshal([]byte(resultText(t, result)), &payload))
		require.Len(t, payload.Results, 1)
		return payload.Results[0]
	}
	without := check(t, false)
	with := check(t, true)
	require.Equal(t, preflight.StatusReady, without.Status, "control: the permitted canonical owner is ready")
	assert.Equal(t, without, with, "the selection-hidden display owner must not change the answer")
}

// Sol r1 finding 1, end to end: when the snapshot holds AUTHORITATIVE
// identity data for the server (connected, discovery stamped, the tool
// listed) but the published runtime state is not ready, an absent id exits
// at the registration identity gate as not_found while a profile-hidden id
// the snapshot lists used to fall through to the connection verdict
// (server_unhealthy, server_initializing, oauth_required) — confirming the
// hidden tool exists. Both surfaces a pinned agent reads (REST and the
// indexed describe_tool check) must answer the two identically, and must
// never suggest a hidden tool.
func TestPreflightProfilePolicy_HiddenEqualsAbsentWithAuthoritativeIdentity(t *testing.T) {
	f := newPreflightPolicyFixture(t, func(cfg *config.Config) { cfg.RequireMCPAuth = true })
	key := f.mint("ro-agent", restV3ReadonlyID)
	allPerms := []string{auth.PermRead, auth.PermWrite, auth.PermDestructive}
	pinned := agentCtx([]string{"*"}, allPerms, restV3ReadonlyID)
	hidden := []string{"github:create_issue", "github:get_secret_scanning_alert", "github:search_code"}
	const absentID = "github:no_such_tool"

	norm := func(r describeCheckResult) describeCheckResult { r.ID = ""; r.DidYouMean = nil; return r }
	assertNoHiddenSuggested := func(t *testing.T, r describeCheckResult) {
		t.Helper()
		for _, h := range hidden {
			assert.NotContains(t, r.DidYouMean, h, "a hidden tool must never be suggested")
		}
	}

	for _, state := range []string{"error", "connecting", "disconnected", "pending auth", "ready"} {
		t.Run(state, func(t *testing.T) {
			f.rt.Supervisor().StateView().UpdateServer("github", func(s *stateview.ServerStatus) {
				s.State = state
				s.Connected = true
				s.ToolsDiscovered = true
			})
			t.Cleanup(func() {
				f.rt.Supervisor().StateView().UpdateServer("github", func(s *stateview.ServerStatus) { s.State = "ready" })
			})
			// The precondition the finding needs: the snapshot lists every
			// hidden tool and does not list the absent one.
			for _, id := range hidden {
				server, tool, _ := strings.Cut(id, ":")
				require.True(t, f.proxy.resolveExactToolIdentity(server, tool).Found, id)
			}
			require.False(t, f.proxy.resolveExactToolIdentity("github", "no_such_tool").Found)

			ids := append([]string{absentID}, hidden...)
			body := map[string]interface{}{"tools": func() []map[string]string {
				out := make([]map[string]string, 0, len(ids))
				for _, id := range ids {
					out = append(out, map[string]string{"id": id})
				}
				return out
			}()}
			rec := f.do(http.MethodPost, "/api/v1/preflight", key, body, "")
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var env struct {
				Data struct {
					Tools []describeCheckResult `json:"tools"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
			rest := map[string]describeCheckResult{}
			for _, r := range env.Data.Tools {
				rest[r.ID] = r
			}
			mcpRes := checkResultsByID(inBandCheck(t, f.proxy, pinned, ids...))

			for surface, got := range map[string]map[string]describeCheckResult{"rest": rest, "mcp check": mcpRes} {
				want := got[absentID]
				require.NotEmpty(t, want.Status, "%s: no result for the absent id", surface)
				assertNoHiddenSuggested(t, want)
				for _, id := range hidden {
					assert.Equal(t, norm(want), norm(got[id]), "%s: %s must answer exactly what an absent id answers", surface, id)
					assertNoHiddenSuggested(t, got[id])
				}
			}
			assert.Zero(t, f.totalDispatched(), "preflight never dispatches")
		})
	}
}

// Sol r1 finding 2 (rejected as a defect; this test pins the evidence): a
// stored set_profile selection that is no longer admissible — its profile was
// deleted, or a policy edit removed it from the base's switchable_to — is
// cleared by the v3 resolver's per-request re-validation (FR-022). mcp-go runs
// the server's WithToolFilter chain (filterProfileV3Tools, registered on the
// indexed AND the direct servers) at tools/call BEFORE the describe_tool
// handler, and that filter resolves through the RECORDING resolver, so by the
// time a check runs the same request has already cleared the selection and
// recorded the effective resolution. The check therefore changes nothing a
// request does not already change: selection and recorded resolution are the
// same after the handler as after the filter, and the verdict is the
// fail-closed base view with zero upstream dispatch.
func TestPreflightProfilePolicy_CheckAddsNoSessionMutationBeyondTheRequest(t *testing.T) {
	f := newPreflightPolicyFixture(t, nil)
	allPerms := []string{auth.PermRead, auth.PermWrite, auth.PermDestructive}
	surfaces := map[string]func(ctx context.Context, ids ...string) map[string]describeCheckResult{
		"indexed": func(ctx context.Context, ids ...string) map[string]describeCheckResult {
			return checkResultsByID(inBandCheck(t, f.proxy, ctx, ids...))
		},
		"direct": func(ctx context.Context, ids ...string) map[string]describeCheckResult {
			raw := make([]interface{}, 0, len(ids))
			for _, id := range ids {
				raw = append(raw, id)
			}
			req := mcp.CallToolRequest{}
			req.Params.Arguments = map[string]interface{}{"tool_ids": raw, "check": true}
			result, err := f.proxy.describeToolHandler(describeSurfaceDirect)(ctx, req)
			require.NoError(t, err)
			require.False(t, result.IsError, "%v", result.Content)
			var payload describeCheckPayload
			require.NoError(t, json.Unmarshal([]byte(resultText(t, result)), &payload))
			return checkResultsByID(payload)
		},
	}
	selections := map[string]string{
		"deleted profile":                 "no-such-profile",
		"not in the base's switchable_to": "legacy",
	}
	for surface, run := range surfaces {
		for name, sel := range selections {
			t.Run(surface+"/"+name, func(t *testing.T) {
				sid := "sess-" + surface + "-" + strings.ReplaceAll(name, " ", "-")
				f.proxy.sessionStore.SetSession(sid, "laptop", "1", false, false, nil)
				f.proxy.sessionStore.SetActiveProfile(sid, sel)
				ctx := sessionCtx(clientCtx("laptop", restV3ReadonlyID, auth.ProfileModeSwitchable), sid)
				ctx = auth.WithAuthContext(ctx, func() *auth.AuthContext {
					ac := auth.AuthContextFromContext(ctx)
					ac.Permissions = allPerms
					return ac
				}())

				// What mcp-go does at tools/call, ahead of the handler.
				f.proxy.filterProfileV3Tools(ctx, []mcp.Tool{{Name: "describe_tool"}})
				afterFilter := f.proxy.sessionStore.GetActiveProfile(sid)
				info := f.proxy.sessionStore.GetSession(sid)
				require.NotNil(t, info)
				recProfile, recSource := info.Profile, info.ProfileSource
				assert.Empty(t, afterFilter, "the request's own tool filter already clears an inadmissible selection")
				assert.Equal(t, restV3ReadonlyID, recProfile, "and records the base resolution")

				before := f.totalDispatched()
				got := run(ctx, "github:list_issues", "github:create_issue", "github:no_such_tool")

				assert.Equal(t, afterFilter, f.proxy.sessionStore.GetActiveProfile(sid), "the check adds no selection change")
				info = f.proxy.sessionStore.GetSession(sid)
				assert.Equal(t, recProfile, info.Profile, "the check adds no recorded-resolution change")
				assert.Equal(t, recSource, info.ProfileSource)

				// Fail-closed effective view: the base, never the stale selection.
				if surface == "indexed" {
					// (This fixture builds no direct catalog, so the direct
					// surface answers every id not_found; the hidden-vs-absent
					// equality below is what it pins.)
					assert.Equal(t, preflight.StatusReady, got["github:list_issues"].Status, "%+v", got["github:list_issues"])
				}
				assert.Equal(t, preflight.ReasonNotFound, got["github:create_issue"].Reason, "%+v", got["github:create_issue"])
				assert.Equal(t, got["github:no_such_tool"].Detail, got["github:create_issue"].Detail)
				assert.Equal(t, before, f.totalDispatched(), "a check never dispatches")
			})
		}
	}
}
