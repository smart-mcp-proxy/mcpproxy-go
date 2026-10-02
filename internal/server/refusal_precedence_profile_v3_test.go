package server

// Spec 108 PR 108-bd-tests, T049: refusal precedence in the explainer order
// (contracts/refusals.md). Each row makes TWO gates fail at once and asserts
// that the EARLIER gate's exact text is what the caller sees, with zero
// upstream calls and the earlier gate's audit reason key.
//
// Chain: profile > server_in_scope > tool_rule > tier_cap > token_permission >
// global_gate > server_state > tool_approval, but each pair is asserted only
// where the call path on main actually implements both gates (D8):
//   - call_tool_* has no global_gate on main (read_only_mode and
//     disable_management gate the management operations only), so no
//     "X > global_gate" row exists for it; the code_execution and
//     upstream_servers global gates are P7 and P8.
//   - upstream_servers mutating operations check global_gate (read_only_mode,
//     disable_management) BEFORE token_permission (AuthorizeServerOp) on main.
//     That order predates Spec 108 and is unchanged here; 108-f's explainer
//     must model it (follow-up), so no token_permission > global_gate row.
//   - tier_cap > token_permission is TestCallTool_ProfileRefusalPrecedesTokenPermissionRefusal.

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

const (
	precedenceOutOfScopeText = "Server 'github' is not in scope for this agent token"
)

// precedenceFixture is the enforcement-matrix fixture with an audit sink and a
// running activity service, so a refusal's audit reason key and persisted
// block_reason can both be read back.
type precedenceFixture struct {
	proxy *MCPProxyServer
	sink  *recordingAuditSink
	ups   map[string]*countingUpstream
	f     *restV3Fixture
}

func newPrecedenceFixture(t *testing.T, configure func(*config.Config)) *precedenceFixture {
	t.Helper()
	f := newProfilesV3RESTFixture(t, configure)
	sink := &recordingAuditSink{}
	f.proxy.auditSink = sink
	return &precedenceFixture{proxy: f.proxy, sink: sink, ups: f.upstreams, f: f}
}

// call runs one call_tool_<variant> and returns the refusal text.
func (p *precedenceFixture) call(t *testing.T, ctx context.Context, variant, tool string) string {
	t.Helper()
	result, err := p.proxy.handleCallToolVariant(ctx, auditCallToolRequest(tool, nil), variant)
	require.NoError(t, err)
	require.True(t, result.IsError, "the call must be refused: %s", resultText(t, result))
	return resultText(t, result)
}

// denyReasons returns every `authz deny` audit reason key written so far.
func (p *precedenceFixture) denyReasons(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, line := range p.sink.decoded(t) {
		if line["event"] == "authz" && line["decision"] == "deny" {
			out = append(out, line["reason"].(string))
		}
	}
	return out
}

func (p *precedenceFixture) requireNoUpstreamCalls(t *testing.T) {
	t.Helper()
	for name, up := range p.ups {
		require.Empty(t, up.dispatched(), "%s must see zero upstream calls", name)
	}
}

// requireBlockReason waits for the persisted blocked row's profile block_reason.
func (p *precedenceFixture) requireBlockReason(t *testing.T, server, tool string, want profile.BlockReason) {
	t.Helper()
	p.f.waitBlocked(server, tool, want)
}

// setGithubServerState makes server github quarantined or disabled in BOTH
// the persisted record and the live StateView, the two reads dispatch uses.
func (p *precedenceFixture) setGithubServerState(t *testing.T, quarantined, enabled bool) {
	t.Helper()
	require.NoError(t, p.proxy.storage.SaveUpstreamServer(&config.ServerConfig{
		Name: "github", URL: p.ups["github"].URL, Protocol: "streamable-http", Enabled: enabled, Quarantined: quarantined,
	}))
	p.f.rt.Supervisor().StateView().UpdateServer("github", func(s *stateview.ServerStatus) {
		s.Quarantined = quarantined
		s.Enabled = enabled
	})
}

func TestRefusalPrecedence_ProfileV3ExplainerOrder(t *testing.T) {
	allPerms := []string{auth.PermRead, auth.PermWrite, auth.PermDestructive}

	// P1 profile (dangling base) > server_in_scope.
	t.Run("P1 dangling pin beats server scope", func(t *testing.T) {
		p := newPrecedenceFixture(t, nil)
		dangling := p.call(t, agentCtx([]string{"*"}, allPerms, "gone"), contracts.ToolVariantRead, "github:list_issues")
		unpinnedOutOfScope := p.call(t, agentCtx([]string{"notion"}, allPerms, ""), contracts.ToolVariantRead, "github:list_issues")
		require.Equal(t, precedenceOutOfScopeText, dangling)
		require.Equal(t, unpinnedOutOfScope, dangling, "a dangling pin answers with the bytes of an out-of-scope token")
		p.requireNoUpstreamCalls(t)
	})

	// P2 server_in_scope > tier_cap.
	t.Run("P2 token server scope beats the tier cap", func(t *testing.T) {
		p := newPrecedenceFixture(t, nil)
		text := p.call(t, agentCtx([]string{"notion"}, allPerms, "work-readonly"), contracts.ToolVariantWrite, "github:create_issue")
		require.Equal(t, precedenceOutOfScopeText, text)
		require.NotContains(t, text, "blocked by profile")
		require.Equal(t, []string{"token_scope"}, p.denyReasons(t))
		p.requireNoUpstreamCalls(t)
	})

	// P3 server_in_scope (server_not_in_profile) > allow rule (FR-007).
	t.Run("P3 profile server scope beats an allow rule", func(t *testing.T) {
		p := newPrecedenceFixture(t, nil)
		// work-readonly has allow:[filesystem:read_text_file] but not the
		// filesystem server; the allow rule cannot widen the server scope.
		text := p.call(t, pinnedProfileCtx("work-readonly"), contracts.ToolVariantRead, "filesystem:read_text_file")
		require.Equal(t, "Server 'filesystem' is not in scope for this agent token", text)
		require.Equal(t, []string{"token_scope"}, p.denyReasons(t))
		p.requireNoUpstreamCalls(t)
	})

	// P4 tool_rule > tier_cap.
	t.Run("P4 deny rule beats the tier cap", func(t *testing.T) {
		p := newPrecedenceFixture(t, func(cfg *config.Config) {
			both := enforcementMatrixProfiles()[0]
			both.Name = "rule-and-cap"
			both.Tools = &config.ProfileToolRules{Deny: []string{"github:create_issue"}}
			cfg.Profiles = append(cfg.Profiles, both)
		})
		text := p.call(t, pinnedProfileCtx("rule-and-cap"), contracts.ToolVariantWrite, "github:create_issue")
		require.Equal(t, v3Disclosed(t, "rule", "github", "create_issue", profile.TierWrite, "read", "rule-and-cap"), text)
		p.requireBlockReason(t, "github", "create_issue", profile.BlockReasonRule)
		p.requireNoUpstreamCalls(t)
	})

	// P5 tool_rule > token_permission.
	t.Run("P5 deny rule beats token permission", func(t *testing.T) {
		p := newPrecedenceFixture(t, nil)
		ctx := agentCtx([]string{"*"}, []string{auth.PermRead}, "work-readonly")
		text := p.call(t, ctx, contracts.ToolVariantDestructive, "github:get_secret_scanning_alert")
		require.Equal(t, v3Disclosed(t, "rule", "github", "get_secret_scanning_alert", profile.TierRead, "read", "work-readonly"), text)
		require.NotContains(t, text, "Insufficient permissions")
		p.requireBlockReason(t, "github", "get_secret_scanning_alert", profile.BlockReasonRule)
		p.requireNoUpstreamCalls(t)
	})

	// P6 tier_cap > token_permission is TestCallTool_ProfileRefusalPrecedesTokenPermissionRefusal
	// (call_tool_profile_v3_test.go); it is not duplicated here.

	// P7 profile > global_gate (code execution).
	t.Run("P7 profile beats the global code-execution gate", func(t *testing.T) {
		p := newPrecedenceFixture(t, func(cfg *config.Config) { cfg.EnableCodeExecution = false })
		result, err := p.proxy.handleCodeExecution(pinnedProfileCtx("work-readonly"), mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: "code_execution", Arguments: map[string]interface{}{"code": "1"}},
		})
		require.NoError(t, err)
		require.True(t, result.IsError)
		require.Equal(t, "unknown tool: code_execution", resultText(t, result))
		require.NotEqual(t, config.CodeExecutionDisabledMessage, resultText(t, result))
		p.requireBlockReason(t, "", "code_execution", profile.BlockReasonCodeExecution)
		p.requireNoUpstreamCalls(t)
	})

	// P8 profile > global_gate (management), REST dispatch path.
	t.Run("P8 profile beats the read-only-mode management gate", func(t *testing.T) {
		p := newPrecedenceFixture(t, func(cfg *config.Config) { cfg.ReadOnlyMode = true })
		_, err := p.proxy.CallToolDirect(urlProfileCtx(p.proxy, "work-readonly"), mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: "upstream_servers", Arguments: map[string]interface{}{"operation": "patch", "name": "github"}},
		})
		require.Error(t, err)
		require.Equal(t, "unknown tool: upstream_servers", err.Error())
		require.NotContains(t, err.Error(), "read-only mode")
		p.requireBlockReason(t, "", "upstream_servers", profile.BlockReasonManagement)
		p.requireNoUpstreamCalls(t)
	})

	// P9 token_permission > server_state (quarantined).
	t.Run("P9 token permission beats server quarantine", func(t *testing.T) {
		p := newPrecedenceFixture(t, nil)
		p.setGithubServerState(t, true, true)
		text := p.call(t, agentCtx([]string{"*"}, []string{auth.PermRead}, ""), contracts.ToolVariantWrite, "github:create_issue")
		require.Contains(t, text, "Insufficient permissions")
		require.NotContains(t, text, "quarantin")
		require.Equal(t, []string{"token_permission"}, p.denyReasons(t))
		p.requireNoUpstreamCalls(t)
	})

	// P10/P11 profile > server_state. The live tool's annotations mark
	// create_issue a write tool (readOnlyHint:false), so the tier word the
	// profile gate prints is "write" (it would be "destructive" for a tool
	// with no annotations, IntrinsicTier(nil,false)); the text must be the
	// profile tier text and never name the server state.
	for _, state := range []struct {
		name                 string
		quarantined, enabled bool
		mustNotContain       string
	}{
		{"P10 profile beats server quarantine", true, true, "quarantin"},
		{"P11 profile beats a disabled server", false, false, "disabled"},
	} {
		t.Run(state.name, func(t *testing.T) {
			p := newPrecedenceFixture(t, nil)
			p.setGithubServerState(t, state.quarantined, state.enabled)
			// Control: a profile that admits the call meets the server-state gate.
			// (A quarantined server answers with a structured, non-error result.)
			controlResult, controlErr := p.proxy.handleCallToolVariant(pinnedProfileCtx("work-full"),
				auditCallToolRequest("github:create_issue", nil), contracts.ToolVariantWrite)
			require.NoError(t, controlErr)
			require.Contains(t, resultText(t, controlResult), state.mustNotContain, "the server-state gate must fire when the profile admits the call")
			text := p.call(t, pinnedProfileCtx("work-readonly"), contracts.ToolVariantWrite, "github:create_issue")
			require.Equal(t, v3TierRefusal(t), text)
			require.NotContains(t, text, state.mustNotContain)
			p.requireBlockReason(t, "github", "create_issue", profile.BlockReasonTier)
			p.requireNoUpstreamCalls(t)
		})
	}

	// P12 profile > tool_approval.
	t.Run("P12 profile beats a pending tool approval", func(t *testing.T) {
		p := newPrecedenceFixture(t, nil)
		require.NoError(t, p.proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "github", ToolName: "create_issue", Status: storage.ToolApprovalStatusPending,
		}))
		text := p.call(t, pinnedProfileCtx("work-readonly"), contracts.ToolVariantWrite, "github:create_issue")
		require.Equal(t, v3TierRefusal(t), text)
		require.NotContains(t, text, "pending")
		p.requireBlockReason(t, "github", "create_issue", profile.BlockReasonTier)
		p.requireNoUpstreamCalls(t)
	})

}
