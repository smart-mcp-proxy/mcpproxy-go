package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// A profile's tool policy refuses a tool in three ways (tier cap, deny rule,
// unannotated tool). The audit stream names which one, instead of "other".

func TestAuditReasonFromBlockKeyAndProfileReason(t *testing.T) {
	cases := []struct {
		name          string
		reasonKey     string
		profileReason string
		wantReason    string
		wantDisclosed bool
	}{
		{"tier", telemetry.BlockReasonOther, string(profile.BlockReasonTier), "profile_tier", true},
		{"rule", telemetry.BlockReasonOther, string(profile.BlockReasonRule), "profile_rule", true},
		{"unannotated", telemetry.BlockReasonOther, string(profile.BlockReasonUnannotated), "profile_unannotated", true},
		// The typed cause wins over a gate key a nested refusal may carry.
		{"tier over token_permission", telemetry.BlockReasonTokenPermission, string(profile.BlockReasonTier), "profile_tier", true},
		// Not audit reasons: built-in invocations never reach the audit stream.
		{"code_execution stays other", telemetry.BlockReasonOther, string(profile.BlockReasonCodeExecution), "other", true},
		{"management stays other", telemetry.BlockReasonOther, string(profile.BlockReasonManagement), "other", true},
		// Server scope keeps its own non-disclosing reason via the gate key.
		{"profile_scope key", telemetry.BlockReasonProfileScope, "", "profile_scope", false},
		{"server scope cause alone is not a tool-policy reason", telemetry.BlockReasonTokenScope, string(profile.BlockReasonServerScope), "token_scope", false},
		{"no profile cause", telemetry.BlockReasonToolNotCallable, "", "tool_not_callable", true},
		{"unknown stays other", "nonsense", "", "other", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, disclosed := auditReasonFromBlockKeyAndProfileReason(tc.reasonKey, tc.profileReason)
			assert.Equal(t, tc.wantReason, reason)
			assert.Equal(t, tc.wantDisclosed, disclosed)
		})
	}
	// The one-argument form is unchanged for callers with no profile cause.
	reason, disclosed := auditReasonFromBlockKey(telemetry.BlockReasonTokenScope)
	assert.Equal(t, "token_scope", reason)
	assert.False(t, disclosed)
}

// profileAuditFixture is one server with a read, a denied-by-rule read, a
// write and an unannotated tool behind a read-capped profile "ro".
func profileAuditFixture(t *testing.T) (*MCPProxyServer, *recordingAuditSink, *countingUpstream) {
	t.Helper()
	proxy, rt := createTestProxyWithRuntimeCfg(t, nil, func(cfg *config.Config) {
		cfg.Servers = []*config.ServerConfig{{Name: "fx", Enabled: true}}
		cfg.EnableCodeExecution = true
		cfg.Profiles = []config.ProfileConfig{{
			Name: "ro", Servers: []string{"fx"}, MaxTier: config.ProfileTierRead,
			Unannotated:   config.ProfileUnannotatedDeny,
			CodeExecution: boolPtr(true),
			Tools:         &config.ProfileToolRules{Deny: []string{"fx:secret_read"}},
		}}
	})
	sink := &recordingAuditSink{}
	proxy.auditSink = sink
	up := startCountingUpstream(t, proxy, rt, "fx",
		readSpec("note_get"), readSpec("secret_read"), writeSpec("note_set"), toolSpec{Name: "mystery", Description: "no annotations"})
	return proxy, sink, up
}

func TestAuditFunnel_ProfileToolPolicyRefusalsNameTheRule(t *testing.T) {
	cases := []struct {
		tool   string
		reason string
	}{
		{"note_set", "profile_tier"},
		{"secret_read", "profile_rule"},
		{"mystery", "profile_unannotated"},
	}
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			proxy, sink, up := profileAuditFixture(t)

			result, err := proxy.handleCallToolVariant(pinnedProfileCtx("ro"),
				auditCallToolRequest("fx:"+tc.tool, map[string]interface{}{}), contracts.ToolVariantRead)
			require.NoError(t, err)
			require.True(t, result.IsError)
			assert.Contains(t, resultText(t, result), "blocked by profile")
			assert.Equal(t, int64(0), up.count.Load(), "a refused call never reaches the upstream")

			lines := sink.decoded(t)
			require.Len(t, lines, 1, "a refusal is exactly one authz line")
			assert.Equal(t, "authz", lines[0]["event"])
			assert.Equal(t, "deny", lines[0]["decision"])
			assert.Equal(t, tc.reason, lines[0]["reason"])
			assert.Equal(t, true, lines[0]["disclosed"], "the caller's own error text already names the cause")
			assert.Equal(t, "ro", callerOf(t, lines[0])["profile_pin"])
		})
	}
}

func TestAuditFunnel_ProfileAllowedCallIsStillAllow(t *testing.T) {
	proxy, sink, up := profileAuditFixture(t)

	result, err := proxy.handleCallToolVariant(pinnedProfileCtx("ro"),
		auditCallToolRequest("fx:note_get", map[string]interface{}{}), contracts.ToolVariantRead)
	require.NoError(t, err)
	require.False(t, result.IsError, resultText(t, result))
	assert.Equal(t, int64(1), up.count.Load())

	lines := sink.decoded(t)
	require.Len(t, lines, 2)
	assert.Equal(t, "allow", lines[0]["decision"])
	assert.Equal(t, "none", lines[0]["reason"])
}

func TestAuditFunnel_NestedProfileToolPolicyRefusalNamesTheRule(t *testing.T) {
	cases := []struct {
		tool   string
		reason string
	}{
		{"note_set", "profile_tier"},
		{"secret_read", "profile_rule"},
		{"mystery", "profile_unannotated"},
	}
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			proxy, sink, up := profileAuditFixture(t)

			call := runSandboxCallTool(t, proxy, pinnedProfileCtx("ro"), "fx", tc.tool)
			require.False(t, call.OK)
			assert.Equal(t, int64(0), up.count.Load())

			lines := sink.decoded(t)
			require.Len(t, lines, 1, "the nested refusal writes exactly one authz line")
			assert.Equal(t, "deny", lines[0]["decision"])
			assert.Equal(t, tc.reason, lines[0]["reason"])
			assert.Equal(t, true, lines[0]["disclosed"],
				"the script reads the refusal in full, so the line must not claim it was concealed")
			assert.Equal(t, "code_execution", lines[0]["surface"])
			assert.NotEmpty(t, lines[0]["parent_id"])
		})
	}
}
