package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 108 FR-032 (T059/T061a): the access chain's `callable` equals the
// outcome of a real call. Every enforcement-matrix tool is evaluated for every
// kind of subject and then actually dispatched through call_tool_*; the two
// must agree, with zero upstream calls when the verdict is not callable.

type accessTool struct {
	server, tool, variant string
}

func enforcementMatrixAccessTools() []accessTool {
	return []accessTool{
		{"github", "list_issues", contracts.ToolVariantRead},
		{"github", "create_issue", contracts.ToolVariantWrite},
		{"github", "delete_repo", contracts.ToolVariantDestructive},
		{"github", "search_code", contracts.ToolVariantDestructive}, // unannotated: the most permissive variant
		{"github", "get_secret_scanning_alert", contracts.ToolVariantRead},
		{"notion", "update_page", contracts.ToolVariantWrite},
		{"filesystem", "read_text_file", contracts.ToolVariantRead},
	}
}

func (f *restV3Fixture) clientDispatchCtx(t *testing.T, clientID string) context.Context {
	t.Helper()
	rec, err := f.proxy.clientCredentialRecord(clientID)
	require.NoError(t, err)
	require.NotNil(t, rec, "client %s must hold a credential record", clientID)
	return auth.WithAuthContext(context.Background(), rec.AuthContext())
}

func TestEvaluateAccess_CallableEqualsDispatchOutcome(t *testing.T) {
	type subjectCase struct {
		name    string
		subject profile.AccessSubject
		ctx     func(f *restV3Fixture) context.Context
	}
	cases := []subjectCase{
		{
			name:    "locked client on work-readonly",
			subject: profile.AccessSubject{Kind: profile.AccessSubjectClient, ClientID: "cursor"},
			ctx:     func(f *restV3Fixture) context.Context { return f.clientDispatchCtx(t, "cursor") },
		},
		{
			name:    "switchable client on work-full",
			subject: profile.AccessSubject{Kind: profile.AccessSubjectClient, ClientID: "laptop"},
			ctx:     func(f *restV3Fixture) context.Context { return f.clientDispatchCtx(t, "laptop") },
		},
		{
			name:    "switchable client on work-readonly",
			subject: profile.AccessSubject{Kind: profile.AccessSubjectClient, ClientID: "roswitch"},
			ctx:     func(f *restV3Fixture) context.Context { return f.clientDispatchCtx(t, "roswitch") },
		},
		{
			name:    "profile subject work-readonly",
			subject: profile.AccessSubject{Kind: profile.AccessSubjectProfile, Profile: "work-readonly"},
			ctx:     func(f *restV3Fixture) context.Context { return urlProfileCtx(f.proxy, "work-readonly") },
		},
		{
			name:    "profile subject legacy",
			subject: profile.AccessSubject{Kind: profile.AccessSubjectProfile, Profile: "legacy"},
			ctx:     func(f *restV3Fixture) context.Context { return urlProfileCtx(f.proxy, "legacy") },
		},
		{
			name:    "client holding the admin key",
			subject: profile.AccessSubject{Kind: profile.AccessSubjectClient, ClientID: "windsurf", CredentialState: profile.CredentialStateAdminKey},
			ctx:     func(f *restV3Fixture) context.Context { return adminCtx() },
		},
		{
			name:    "keyless client, require_mcp_auth off, no anonymous profile",
			subject: profile.AccessSubject{Kind: profile.AccessSubjectClient, ClientID: "vscode", CredentialState: profile.CredentialStateNone},
			ctx:     func(f *restV3Fixture) context.Context { return anonCtx() },
		},
	}

	f := newProfilesV3RESTFixture(t, func(cfg *config.Config) { cfg.RequireMCPAuth = false })
	f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)
	f.mintClient("laptop", "work-full", auth.ProfileModeSwitchable)
	f.mintClient("roswitch", "work-readonly", auth.ProfileModeSwitchable)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, tool := range enforcementMatrixAccessTools() {
				verdict := f.proxy.EvaluateAccess(tc.subject, tool.server, tool.tool)
				before := f.totalDispatched()
				result, err := f.proxy.handleCallToolVariant(tc.ctx(f), auditCallToolRequest(tool.server+":"+tool.tool, nil), tool.variant)
				require.NoError(t, err)
				dispatched := f.totalDispatched() - before

				outcome := !result.IsError && dispatched == 1
				assert.Equal(t, outcome, verdict.Callable, "%s:%s reason=%q ", tool.server, tool.tool, verdict.Reason)
				if !verdict.Callable {
					assert.Zero(t, dispatched, "%s:%s: a non-callable row must cause zero upstream calls", tool.server, tool.tool)
					assert.True(t, result.IsError)
					assert.NotEqual(t, profile.AccessReasonNone, verdict.Reason)
				} else {
					assert.Equal(t, profile.AccessReasonNone, verdict.Reason)
				}
				// A visible row satisfies every discovery step.
				if verdict.Visible {
					for _, s := range verdict.Steps {
						switch s.Step {
						case profile.StepCredential, profile.StepProfile, profile.StepServerInScope, profile.StepToolRule, profile.StepTierCap:
							assert.NotEqual(t, profile.AccessStepFail, s.Status)
						}
					}
				}
				require.Len(t, verdict.Steps, len(profile.StepOrder()), "Steps is complete, in StepOrder")
				for i, s := range verdict.Steps {
					assert.Equal(t, profile.StepOrder()[i], s.Step)
				}
			}
		})
	}
}

func TestEvaluateAccess_ReasonIsFirstFailingStepInCanonicalOrder(t *testing.T) {
	f := newProfilesV3RESTFixture(t, nil)
	f.mintClient("cursor", "work-readonly", auth.ProfileModeLocked)
	f.mintClient("gone", "no-such-profile", auth.ProfileModeLocked)
	f.mintClient("old", "work-readonly", auth.ProfileModeLocked)
	_, err := f.rt.StorageManager().ForgetClientCredential("old")
	require.NoError(t, err)

	cursor := profile.AccessSubject{Kind: profile.AccessSubjectClient, ClientID: "cursor"}
	verdictOf := func(subject profile.AccessSubject, server, tool string) profile.AccessVerdict {
		return f.proxy.EvaluateAccess(subject, server, tool)
	}

	t.Run("FR-010 decision reasons under a locked work-readonly client", func(t *testing.T) {
		v := verdictOf(cursor, "github", "create_issue")
		assert.False(t, v.Visible)
		assert.False(t, v.Callable)
		assert.Equal(t, profile.AccessReasonAboveTierCap, v.Reason)
		assert.Equal(t, profile.TierWrite, v.ProfileTier)

		v = verdictOf(cursor, "github", "search_code")
		assert.Equal(t, profile.AccessReasonUnannotatedHidden, v.Reason)

		v = verdictOf(cursor, "github", "get_secret_scanning_alert")
		assert.Equal(t, profile.AccessReasonDeniedByRule, v.Reason, "deny beats the allow rule")

		v = verdictOf(cursor, "filesystem", "read_text_file")
		assert.Equal(t, profile.AccessReasonServerNotInProfile, v.Reason, "server_not_in_profile beats the allow rule")

		v = verdictOf(cursor, "github", "list_issues")
		assert.True(t, v.Visible)
		assert.True(t, v.Callable)
		assert.Equal(t, profile.AccessReasonNone, v.Reason)

		v = verdictOf(cursor, "notion", "update_page")
		assert.True(t, v.Callable, "an explicit allow rule admits the write tool")
	})

	t.Run("a revoked client fails the credential step and skips the rest", func(t *testing.T) {
		v := verdictOf(profile.AccessSubject{Kind: profile.AccessSubjectClient, ClientID: "old"}, "github", "list_issues")
		assert.False(t, v.Visible)
		assert.False(t, v.Callable)
		assert.Equal(t, profile.AccessReasonCredential, v.Reason)
		assert.Equal(t, profile.StepCredential, v.FirstFailure())
		for _, s := range v.Steps[1:] {
			assert.Equal(t, profile.AccessStepSkip, s.Status, s.Step)
		}
	})

	t.Run("a dangling binding is the profile step", func(t *testing.T) {
		v := verdictOf(profile.AccessSubject{Kind: profile.AccessSubjectClient, ClientID: "gone"}, "github", "list_issues")
		assert.False(t, v.Visible)
		assert.Equal(t, profile.AccessReasonProfile, v.Reason)
	})

	t.Run("no credential under require_mcp_auth is refused", func(t *testing.T) {
		strict := newProfilesV3RESTFixture(t, func(cfg *config.Config) { cfg.RequireMCPAuth = true })
		v := strict.proxy.EvaluateAccess(profile.AccessSubject{Kind: profile.AccessSubjectClient, ClientID: "vscode", CredentialState: profile.CredentialStateNone}, "github", "list_issues")
		assert.Equal(t, profile.AccessReasonCredential, v.Reason)
	})

	t.Run("a read tool pending approval is visible but not callable", func(t *testing.T) {
		require.NoError(t, f.proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "github", ToolName: "list_issues", Status: storage.ToolApprovalStatusPending,
		}))
		t.Cleanup(func() {
			require.NoError(t, f.proxy.storage.SaveToolApproval(&storage.ToolApprovalRecord{
				ServerName: "github", ToolName: "list_issues", Status: storage.ToolApprovalStatusApproved,
			}))
		})
		v := verdictOf(cursor, "github", "list_issues")
		assert.True(t, v.Visible)
		assert.False(t, v.Callable)
		assert.Equal(t, profile.AccessReasonToolApproval, v.Reason)
		assert.Equal(t, profile.StepToolApproval, v.FirstFailure())
	})

	t.Run("a disabled server is server_state", func(t *testing.T) {
		require.NoError(t, f.proxy.storage.SaveUpstreamServer(&config.ServerConfig{Name: "notion", Enabled: false}))
		v := verdictOf(cursor, "notion", "update_page")
		assert.True(t, v.Visible)
		assert.False(t, v.Callable)
		assert.Equal(t, profile.AccessReasonServerState, v.Reason)
	})

	t.Run("an unknown client cannot be evaluated", func(t *testing.T) {
		_, err := f.proxy.NewAccessEvaluator(profile.AccessSubject{Kind: profile.AccessSubjectClient, ClientID: "no-such-client"})
		require.ErrorIs(t, err, profile.ErrUnknownClient)
		// A registry client with no credential record is known: it evaluates as keyless.
		_, err = f.proxy.NewAccessEvaluator(profile.AccessSubject{Kind: profile.AccessSubjectClient, ClientID: "vscode"})
		require.NoError(t, err)
	})

	t.Run("an unknown profile subject cannot be evaluated", func(t *testing.T) {
		_, err := f.proxy.NewAccessEvaluator(profile.AccessSubject{Kind: profile.AccessSubjectProfile, Profile: "nope"})
		require.ErrorIs(t, err, profile.ErrUnknownProfile)
	})
}

// E13: read_only_mode gates the management operations only, never an upstream
// tool, so the global_gate step passes for every upstream row and callable
// still equals the real dispatch outcome with the switch on.
func TestEvaluateAccess_ReadOnlyModeDoesNotGateUpstreamTools(t *testing.T) {
	f := newProfilesV3RESTFixture(t, func(cfg *config.Config) { cfg.ReadOnlyMode = true })
	f.mintClient("laptop", "work-full", auth.ProfileModeSwitchable)
	subject := profile.AccessSubject{Kind: profile.AccessSubjectClient, ClientID: "laptop"}

	for _, tool := range enforcementMatrixAccessTools() {
		verdict := f.proxy.EvaluateAccess(subject, tool.server, tool.tool)
		before := f.totalDispatched()
		result, err := f.proxy.handleCallToolVariant(f.clientDispatchCtx(t, "laptop"), auditCallToolRequest(tool.server+":"+tool.tool, nil), tool.variant)
		require.NoError(t, err)
		outcome := !result.IsError && f.totalDispatched()-before == 1
		assert.Equal(t, outcome, verdict.Callable, "%s:%s", tool.server, tool.tool)
		for _, s := range verdict.Steps {
			if s.Step == profile.StepGlobalGate {
				assert.Equal(t, profile.AccessStepPass, s.Status)
			}
		}
	}
}

func TestAccessReason_EnumEqualsDataModel(t *testing.T) {
	want := []string{
		"server_not_in_profile", "denied_by_rule", "unannotated_hidden", "above_tier_cap",
		"credential", "profile", "server_in_scope", "token_permission", "global_gate", "server_state", "tool_approval",
	}
	var got []string
	for _, r := range profile.AccessReasons() {
		got = append(got, string(r))
	}
	assert.Equal(t, want, got)
	// The adapters cover the FR-010 reasons and every step.
	assert.Equal(t, profile.AccessReasonAboveTierCap, profile.AccessReasonFromDecision(profile.ReasonAboveTierCap))
	assert.Equal(t, profile.AccessReasonNone, profile.AccessReasonFromDecision(profile.ReasonNone))
	for _, step := range profile.StepOrder() {
		if step == profile.StepToolRule || step == profile.StepTierCap {
			assert.Equal(t, profile.AccessReasonNone, profile.AccessReasonFromStep(step), "%s reports a decision reason instead", step)
			continue
		}
		assert.Equal(t, string(step), string(profile.AccessReasonFromStep(step)))
	}
}
