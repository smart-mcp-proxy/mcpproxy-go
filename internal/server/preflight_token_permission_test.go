package server

// Issue #1548 follow-up: preflight (REST POST /api/v1/preflight, the CLI that
// drives it, and describe_tool check:true) must apply the caller's agent-token
// PERMISSION TIER — the read / write / destructive permissions a token was
// minted with — using the decision dispatch makes (handleCallToolVariant:
// tierForAnnotations over the target's effective annotations, then
// AuthContext.HasPermission). Before this, a read-only token was told a write
// tool was ready and was then refused at dispatch.
//
// Disclosure: discovery (retrieve_tools, routed describe_tool) still lists a
// tool above the token's tier and the access explainer reports it visible but
// blocked, so preflight answers the named, non-ready token_permission_denied at
// both tiers rather than the scope-silent not_found a profile-hidden tool
// gets. The direct surface hides tier-exceeding tools from its listing, and its
// check already answers not_found for them (TestDescribeDirectCheck_* tier
// cases); that stays unchanged.
//
// Everything runs on the Spec 108 enforcement-matrix fixture with counting
// upstreams, real stored tokens, the real REST router and the real dispatch.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
)

// tokenPermissionRefusal is the exact text dispatch's target-tier gate returns.
func tokenPermissionRefusal(perm, id string) string {
	return "Permission denied: token does not have '" + perm + "' permission required for tool '" + id + "'"
}

func newTokenPermissionFixture(t *testing.T) *restV3Fixture {
	t.Helper()
	return newPreflightPolicyFixture(t, func(cfg *config.Config) { cfg.RequireMCPAuth = true })
}

// The core repro: an unpinned read-only token and an unpinned read+write token,
// every tier of tool. REST answers HTTP 200 with a non-ready verdict for every
// tool above the token's tier, with the dispatch refusal text as detail.
func TestPreflightTokenPermission_RESTUnpinnedTokens(t *testing.T) {
	f := newTokenPermissionFixture(t)
	readOnly := f.mintWith("read-only", "", []string{"*"}, []string{auth.PermRead})
	readWrite := f.mintWith("read-write", "", []string{"*"}, []string{auth.PermRead, auth.PermWrite})

	ids := []string{"github:list_issues", "github:create_issue", "github:delete_repo", "notion:update_page", "filesystem:read_text_file"}

	code, out := f.restPreflight(t, readOnly, "", ids...)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, preflight.VerdictBlocked, out.Verdict)
	assert.Equal(t, preflight.StatusReady, out.byID(t, "github:list_issues").Status)
	assert.Equal(t, preflight.StatusReady, out.byID(t, "filesystem:read_text_file").Status)
	for id, perm := range map[string]string{"github:create_issue": auth.PermWrite, "notion:update_page": auth.PermWrite, "github:delete_repo": auth.PermDestructive} {
		res := out.byID(t, id)
		assert.Equal(t, preflight.StatusUnavailable, res.Status, id)
		assert.Equal(t, preflight.ReasonTokenPermissionDenied, res.Reason, id)
		assert.Equal(t, tokenPermissionRefusal(perm, id), res.Detail, id)
	}
	assertMatrixRow(t, "token_permission_denied", out.byID(t, "github:create_issue").Reason, out.Verdict)

	code, out = f.restPreflight(t, readWrite, "", ids...)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, preflight.VerdictBlocked, out.Verdict)
	for _, id := range []string{"github:list_issues", "github:create_issue", "notion:update_page", "filesystem:read_text_file"} {
		assert.Equal(t, preflight.StatusReady, out.byID(t, id).Status, id)
	}
	destructive := out.byID(t, "github:delete_repo")
	assert.Equal(t, preflight.ReasonTokenPermissionDenied, destructive.Reason)
	assert.Equal(t, tokenPermissionRefusal(auth.PermDestructive, "github:delete_repo"), destructive.Detail)

	// Operator API-key callers are not subject to token permissions: they hold
	// none, and a token's permissions apply only to that token's own calls.
	code, out = f.restPreflight(t, restV3AdminKey, "", ids...)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, preflight.VerdictReady, out.Verdict)

	assert.Zero(t, f.totalDispatched(), "preflight must never reach an upstream")
}

// Evaluator/dispatch parity for permission tiers: for every fixture tool and
// every permission set, the agent-tier REST preflight is ready exactly when
// the token's dispatch through the tool's own call_with variant executes; a
// token_permission_denied detail is byte-identical to the target-tier refusal
// dispatch returns; and a refused call never reaches an upstream.
func TestPreflightTokenPermission_DispatchParity(t *testing.T) {
	f := newTokenPermissionFixture(t)
	tools := []string{
		"github:list_issues", "github:create_issue", "github:delete_repo", "github:search_code",
		"github:get_secret_scanning_alert", "notion:update_page", "filesystem:read_text_file",
	}
	permSets := map[string][]string{
		"read":             {auth.PermRead},
		"read,write":       {auth.PermRead, auth.PermWrite},
		"read,destructive": {auth.PermRead, auth.PermDestructive},
		"all":              {auth.PermRead, auth.PermWrite, auth.PermDestructive},
	}

	blockedSeen, readySeen := 0, 0
	for name, perms := range permSets {
		key := f.mintWith("parity-"+strings.ReplaceAll(name, ",", "-"), "", []string{"*"}, perms)
		code, out := f.restPreflight(t, key, "", tools...)
		require.Equal(t, http.StatusOK, code)

		for _, id := range tools {
			res := out.byID(t, id)
			server, tool, _ := strings.Cut(id, ":")
			annotations, found := f.proxy.EffectiveAnnotations(server, tool)
			variant := contracts.DeriveCallWith(annotations)
			ctx := agentCtx([]string{"*"}, perms, "")

			before := f.totalDispatched()
			result, err := f.proxy.handleCallToolVariant(ctx, auditCallToolRequest(id, nil), variant)
			require.NoError(t, err)
			text := ""
			if result.IsError {
				text = resultText(t, result)
			}
			refused := strings.HasPrefix(text, "Permission denied:") || strings.HasPrefix(text, "Insufficient permissions:")

			switch res.Status {
			case preflight.StatusReady:
				readySeen++
				assert.False(t, refused, "%s with %s: preflight ready but dispatch refused: %q", id, name, text)
				assert.False(t, result.IsError, "%s with %s: preflight ready but dispatch failed: %q", id, name, text)
				assert.Equal(t, before+1, f.totalDispatched(), "%s with %s: a ready tool dispatches", id, name)
			default:
				blockedSeen++
				require.Equal(t, preflight.ReasonTokenPermissionDenied, res.Reason, "%s with %s", id, name)
				assert.True(t, refused, "%s with %s: preflight blocked, dispatch said %q", id, name, text)
				assert.Equal(t, before, f.totalDispatched(), "%s with %s: a refused call must not dispatch", id, name)

				// The target-tier refusal text is variant-independent; the
				// read door reaches it for any read-capable token.
				readResult, err := f.proxy.handleCallToolVariant(ctx, auditCallToolRequest(id, nil), contracts.ToolVariantRead)
				require.NoError(t, err)
				require.True(t, readResult.IsError)
				assert.Equal(t, res.Detail, resultText(t, readResult), "%s with %s: the same refusal bytes", id, name)
				assert.Equal(t, tokenPermissionRefusal(tierForAnnotations(annotations, found), id), res.Detail)
				assert.Equal(t, before, f.totalDispatched(), "%s with %s: a refused call must not dispatch", id, name)
			}
		}
	}
	require.NotZero(t, blockedSeen, "the parity table must exercise blocked rows")
	require.NotZero(t, readySeen, "the parity table must exercise ready rows")
}

// Composition with the profile policy: both must admit a tool. A profile that
// admits a write does not lift a read-only token, a token with every
// permission does not lift a profile's cap, a deny rule still hides, and the
// profile's scope-silent not_found keeps precedence (dispatch runs the profile
// gate first).
func TestPreflightTokenPermission_ComposesWithProfilePolicy(t *testing.T) {
	f := newTokenPermissionFixture(t)

	fullRead := f.mintWith("full-read", "work-full", []string{"*"}, []string{auth.PermRead})
	_, out := f.restPreflight(t, fullRead, "", "github:list_issues", "github:create_issue", "github:delete_repo", "github:no_such_tool")
	assert.Equal(t, preflight.StatusReady, out.byID(t, "github:list_issues").Status)
	assert.Equal(t, preflight.ReasonTokenPermissionDenied, out.byID(t, "github:create_issue").Reason, "work-full admits the write; the token does not")
	assert.Equal(t, preflight.ReasonTokenPermissionDenied, out.byID(t, "github:delete_repo").Reason)
	assert.Equal(t, preflight.ReasonNotFound, out.byID(t, "github:no_such_tool").Reason)

	fullRW := f.mintWith("full-rw", "work-full", []string{"*"}, []string{auth.PermRead, auth.PermWrite})
	_, out = f.restPreflight(t, fullRW, "", "github:create_issue", "github:delete_repo")
	assert.Equal(t, preflight.StatusReady, out.byID(t, "github:create_issue").Status)
	assert.Equal(t, preflight.ReasonTokenPermissionDenied, out.byID(t, "github:delete_repo").Reason, "read+write token, destructive tool")

	// A request profile narrows on top of the token's permissions.
	_, out = f.restPreflight(t, fullRW, restV3ReadonlyID, "github:create_issue", "github:list_issues")
	assert.Equal(t, preflight.ReasonNotFound, out.byID(t, "github:create_issue").Reason)
	assert.Equal(t, preflight.StatusReady, out.byID(t, "github:list_issues").Status)

	// Pinned to the read-capped profile: the profile hides the write and the
	// denied read whatever the token's permissions; the permitted read is ready.
	roRW := f.mintWith("ro-rw", restV3ReadonlyID, []string{"*"}, []string{auth.PermRead, auth.PermWrite})
	_, out = f.restPreflight(t, roRW, "", "github:list_issues", "github:create_issue", "github:get_secret_scanning_alert", "notion:update_page")
	assert.Equal(t, preflight.StatusReady, out.byID(t, "github:list_issues").Status)
	assert.Equal(t, preflight.ReasonNotFound, out.byID(t, "github:create_issue").Reason, "profile tier cap: hidden, not a permission verdict")
	assert.Equal(t, preflight.ReasonNotFound, out.byID(t, "github:get_secret_scanning_alert").Reason, "deny rule: hidden")
	assert.Equal(t, preflight.StatusReady, out.byID(t, "notion:update_page").Status, "an allowed exception within the token's write permission")

	roRead := f.mintWith("ro-read", restV3ReadonlyID, []string{"*"}, []string{auth.PermRead})
	_, out = f.restPreflight(t, roRead, "", "notion:update_page")
	assert.Equal(t, preflight.ReasonTokenPermissionDenied, out.byID(t, "notion:update_page").Reason,
		"the profile's allowed write exception does not lift a read-only token")

	// Dispatch agrees on the composed cases, with the same refusal bytes.
	for _, c := range []struct{ pin, id, perm string }{
		{"work-full", "github:create_issue", auth.PermWrite},
		{"work-full", "github:delete_repo", auth.PermDestructive},
		{restV3ReadonlyID, "notion:update_page", auth.PermWrite},
	} {
		result, err := f.proxy.handleCallToolVariant(agentCtx([]string{"*"}, []string{auth.PermRead}, c.pin), auditCallToolRequest(c.id, nil), contracts.ToolVariantRead)
		require.NoError(t, err)
		require.True(t, result.IsError, c.id)
		assert.Equal(t, tokenPermissionRefusal(c.perm, c.id), resultText(t, result), c.id)
	}
	rec := f.callTool(fullRead, "call_tool_write", map[string]interface{}{"name": "github:create_issue", "args_json": "{}"}, "")
	require.NotEqual(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Zero(t, f.totalDispatched(), "neither preflight nor a refused call may dispatch")

	rec = f.callTool(fullRead, "call_tool_read", map[string]interface{}{"name": "github:list_issues", "args_json": "{}"}, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 1, f.totalDispatched(), "only the permitted read reaches an upstream")
}

// describe_tool check:true in a session authenticated by a read-only token: the
// routed surface lists the write tool (discovery does not hide it), so the
// check names the permission refusal instead of answering ready.
func TestPreflightTokenPermission_InBandCheck(t *testing.T) {
	f := newTokenPermissionFixture(t)

	for name, pin := range map[string]string{"unpinned": "", "pinned work-full": "work-full"} {
		t.Run(name, func(t *testing.T) {
			ctx := agentCtx([]string{"*"}, []string{auth.PermRead}, pin)
			payload := inBandCheck(t, f.proxy, ctx, "github:list_issues", "github:create_issue", "github:delete_repo", "github:no_such_tool")
			assert.Equal(t, preflight.StatusReady, checkResultByID(t, payload, "github:list_issues").Status)
			for id, perm := range map[string]string{"github:create_issue": auth.PermWrite, "github:delete_repo": auth.PermDestructive} {
				res := checkResultByID(t, payload, id)
				assert.Equal(t, preflight.ReasonTokenPermissionDenied, res.Reason, id)
				assert.Equal(t, tokenPermissionRefusal(perm, id), res.Detail, id)
			}
			assert.Equal(t, preflight.ReasonNotFound, checkResultByID(t, payload, "github:no_such_tool").Reason)
			assertMatrixRow(t, "mcp_check_token_permission_denied", checkResultByID(t, payload, "github:create_issue").Reason, payload.Verdict)
			assert.NotEqual(t, preflight.VerdictReady, payload.Verdict)
		})
	}

	// Read+write token: only the destructive tool is refused.
	payload := inBandCheck(t, f.proxy, agentCtx([]string{"*"}, []string{auth.PermRead, auth.PermWrite}, ""), "github:create_issue", "github:delete_repo")
	assert.Equal(t, preflight.StatusReady, checkResultByID(t, payload, "github:create_issue").Status)
	assert.Equal(t, preflight.ReasonTokenPermissionDenied, checkResultByID(t, payload, "github:delete_repo").Reason)

	// Pinned to the read-capped profile, a read-only token's write check stays
	// the profile's scope-silent not_found.
	payload = inBandCheck(t, f.proxy, agentCtx([]string{"*"}, []string{auth.PermRead}, restV3ReadonlyID), "github:create_issue")
	assert.Equal(t, preflight.ReasonNotFound, checkResultByID(t, payload, "github:create_issue").Reason)

	// Administrator and unauthenticated sessions hold no token permissions.
	payload = inBandCheck(t, f.proxy, adminCtx(), "github:create_issue", "github:delete_repo")
	assert.Equal(t, preflight.VerdictReady, payload.Verdict)
	assert.Zero(t, f.totalDispatched())
}

// A pinned worker token created through the `credentials` MCP tool (#1557)
// carries every permission and is confined only by its profile, so preflight
// follows the profile policy exactly and the permission gate never narrows it.
func TestPreflightTokenPermission_CredentialsToolToken(t *testing.T) {
	cf := newCredentialsToolFixture(t, nil)
	f := cf.restV3Fixture
	for name := range f.upstreams {
		f.rt.Supervisor().StateView().UpdateServer(name, func(s *stateview.ServerStatus) { s.State = "ready" })
	}

	mint := func(name, profileName string) string {
		t.Helper()
		out := cf.cok(map[string]any{"operation": "create_token", "name": name, "profile": profileName, "expires_in": "1h"})
		secret, _ := out["credential"].(string)
		require.NotEmpty(t, secret, "%v", out)
		return secret
	}

	full := mint("worker-full", "work-full")
	_, out := f.restPreflight(t, full, "", "github:list_issues", "github:create_issue", "github:delete_repo")
	assert.Equal(t, preflight.VerdictReady, out.Verdict, "every permission, a profile that admits every tier")

	ro := mint("worker-ro", restV3ReadonlyID)
	_, out = f.restPreflight(t, ro, "", "github:list_issues", "github:create_issue", "notion:update_page")
	assert.Equal(t, preflight.StatusReady, out.byID(t, "github:list_issues").Status)
	assert.Equal(t, preflight.ReasonNotFound, out.byID(t, "github:create_issue").Reason, "the profile cap, not a permission verdict")
	assert.Equal(t, preflight.StatusReady, out.byID(t, "notion:update_page").Status, "allowed exception; the token holds write")

	rec := f.callTool(ro, "call_tool_write", map[string]interface{}{"name": "notion:update_page", "args_json": "{}"}, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 1, f.totalDispatched(), "only the executed call dispatched; checks made none")
}
