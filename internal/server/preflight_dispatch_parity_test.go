package server

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// FR-002 (no-skew). Two guarantees, tested separately because they are NOT the
// same strength:
//
//   - SHARED POLICY GATES (quarantine, approval, user/config disablement,
//     server disablement) are two-way: dispatch refuses ⇔ preflight says
//     unavailable, with the reason the taxonomy names for that state.
//   - EXISTENCE is one-way only: an unindexed tool preflights as `not_found`
//     while dispatch stays deliberately fail-open. A non-ready preflight must
//     therefore never be read as "dispatch would refuse".
type parityCase struct {
	name string
	// setup installs the sabotage for this cell.
	setup func(t *testing.T, f *preflightFixture)
	// dispatchRefuses is the expected dispatch decision for the shared gates.
	dispatchRefuses bool
	// reason is the expected preflight reason ("" ⇒ ready).
	reason preflight.Reason
}

func parityCases() []parityCase {
	const server, tool = "gh", "create_issue"

	return []parityCase{
		{
			name: "ready",
			setup: func(t *testing.T, f *preflightFixture) {
				f.addServer(t, &config.ServerConfig{Name: server, Enabled: true, Protocol: "http"})
			},
			dispatchRefuses: false,
		},
		{
			name: "server_disabled",
			setup: func(t *testing.T, f *preflightFixture) {
				f.addServer(t, &config.ServerConfig{Name: server, Enabled: false, Protocol: "http"})
			},
			dispatchRefuses: true,
			reason:          preflight.ReasonServerDisabled,
		},
		{
			name: "server_quarantined",
			setup: func(t *testing.T, f *preflightFixture) {
				f.addServer(t, &config.ServerConfig{Name: server, Enabled: true, Quarantined: true, Protocol: "http"})
			},
			dispatchRefuses: true,
			reason:          preflight.ReasonServerQuarantined,
		},
		{
			name: "tool_denied_by_config",
			setup: func(t *testing.T, f *preflightFixture) {
				f.addServer(t, &config.ServerConfig{
					Name: server, Enabled: true, Protocol: "http",
					DisabledTools: []string{tool},
				})
			},
			dispatchRefuses: true,
			reason:          preflight.ReasonToolDeniedByConfig,
		},
		{
			name: "tool_blocked_by_user",
			setup: func(t *testing.T, f *preflightFixture) {
				f.addServer(t, &config.ServerConfig{Name: server, Enabled: true, Protocol: "http"})
				require.NoError(t, f.storage.SaveToolApproval(&storage.ToolApprovalRecord{
					ServerName: server, ToolName: tool,
					Status: storage.ToolApprovalStatusApproved, Disabled: true,
				}))
			},
			dispatchRefuses: true,
			reason:          preflight.ReasonToolBlockedByUser,
		},
		{
			name: "tool_pending_approval",
			setup: func(t *testing.T, f *preflightFixture) {
				f.addServer(t, &config.ServerConfig{Name: server, Enabled: true, Protocol: "http"})
				require.NoError(t, f.storage.SaveToolApproval(&storage.ToolApprovalRecord{
					ServerName: server, ToolName: tool,
					Status: storage.ToolApprovalStatusPending,
				}))
			},
			dispatchRefuses: true,
			reason:          preflight.ReasonToolPendingApproval,
		},
		{
			// The rug-pull guard is a class of its own — the pre-098 classifier
			// collapsed it into pending_approval (research D2).
			name: "tool_changed",
			setup: func(t *testing.T, f *preflightFixture) {
				f.addServer(t, &config.ServerConfig{Name: server, Enabled: true, Protocol: "http"})
				require.NoError(t, f.storage.SaveToolApproval(&storage.ToolApprovalRecord{
					ServerName: server, ToolName: tool,
					Status: storage.ToolApprovalStatusChanged,
				}))
			},
			dispatchRefuses: true,
			reason:          preflight.ReasonToolChanged,
		},
		{
			// auto_approve_tool_changes (trust_mode auto) opts the server out of
			// the tool-level quarantine gate: dispatch calls the tool, so the
			// preflight must report it ready.
			name: "auto_approve_tool_changes_makes_changed_ready",
			setup: func(t *testing.T, f *preflightFixture) {
				autoApprove := true
				f.addServer(t, &config.ServerConfig{
					Name: server, Enabled: true, Protocol: "http",
					AutoApproveToolChanges: &autoApprove,
				})
				require.NoError(t, f.storage.SaveToolApproval(&storage.ToolApprovalRecord{
					ServerName: server, ToolName: tool,
					Status: storage.ToolApprovalStatusChanged,
				}))
			},
			dispatchRefuses: false,
		},
		{
			// A user block is a user decision, not a quarantine gate: it applies
			// even on an auto-approving server.
			name: "auto_approve_still_honors_user_block",
			setup: func(t *testing.T, f *preflightFixture) {
				autoApprove := true
				f.addServer(t, &config.ServerConfig{
					Name: server, Enabled: true, Protocol: "http",
					AutoApproveToolChanges: &autoApprove,
				})
				require.NoError(t, f.storage.SaveToolApproval(&storage.ToolApprovalRecord{
					ServerName: server, ToolName: tool,
					Status: storage.ToolApprovalStatusApproved, Disabled: true,
				}))
			},
			dispatchRefuses: true,
			reason:          preflight.ReasonToolBlockedByUser,
		},
		{
			// Global quarantine off ⇒ pending records do not gate anything.
			name: "quarantine_globally_disabled_makes_pending_ready",
			setup: func(t *testing.T, f *preflightFixture) {
				off := false
				f.cfg.QuarantineEnabled = &off
				f.addServer(t, &config.ServerConfig{Name: server, Enabled: true, Protocol: "http"})
				require.NoError(t, f.storage.SaveToolApproval(&storage.ToolApprovalRecord{
					ServerName: server, ToolName: tool,
					Status: storage.ToolApprovalStatusPending,
				}))
			},
			dispatchRefuses: false,
		},
	}
}

func TestPreflightAndDispatchAgreeOnSharedPolicyGates(t *testing.T) {
	const server, tool = "gh", "create_issue"

	for _, tc := range parityCases() {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newPreflightFixture(t, nil)
			tc.setup(t, fixture)
			// Existence is a separate, one-way concern: index the tool so this
			// case isolates the policy gates.
			fixture.indexTool(t, server, tool)

			gate := fixture.proxy.evaluateToolGate(server, tool)
			assert.Equal(t, !tc.dispatchRefuses, gate.callable(), "dispatch gate")

			// Direct mode is a separate dispatch path over the same primitive.
			block := fixture.proxy.directToolCallabilityBlock(context.Background(), server, tool, map[string]interface{}{})
			assert.Equal(t, tc.dispatchRefuses, block != nil, "direct-mode dispatch")

			// The sandbox (code_execution + stored scripts) shares one bridge.
			caller := &upstreamToolCaller{proxy: fixture.proxy}
			assert.Equal(t, tc.dispatchRefuses, caller.policyRefusal(server, tool) != nil, "code_execution dispatch")

			out, err := fixture.proxy.RunPreflight(context.Background(), preflight.Params{
				Tools: []preflight.ToolRef{{ID: server + ":" + tool}},
			})
			require.NoError(t, err)
			require.Len(t, out.Results, 1)

			if tc.dispatchRefuses {
				assert.Equal(t, preflight.StatusUnavailable, out.Results[0].Status,
					"a state dispatch refuses must never preflight as ready")
				assert.Equal(t, tc.reason, out.Results[0].Reason)
			} else {
				assert.Equal(t, preflight.StatusReady, out.Results[0].Status,
					"a callable tool must preflight as ready")
			}
		})
	}
}

// One-way carve-out: dispatch is deliberately fail-open on existence, so an
// unindexed tool is `not_found` to a preflight and still callable to dispatch.
// Asserting equivalence here would be wrong — it is the documented asymmetry.
func TestPreflightExistenceGateIsOneWay(t *testing.T) {
	fixture := newPreflightFixture(t, nil)
	fixture.addServer(t, &config.ServerConfig{Name: "gh", Enabled: true, Protocol: "http"})
	// deliberately NOT indexed

	gate := fixture.proxy.evaluateToolGate("gh", "unindexed_tool")
	assert.True(t, gate.callable(), "dispatch does not gate on index presence")
	assert.Nil(t, (&upstreamToolCaller{proxy: fixture.proxy}).policyRefusal("gh", "unindexed_tool"))

	out, err := fixture.proxy.RunPreflight(context.Background(), preflight.Params{
		Tools: []preflight.ToolRef{{ID: "gh:unindexed_tool"}},
	})
	require.NoError(t, err)
	assert.Equal(t, preflight.ReasonNotFound, out.Results[0].Reason)
}

// The sandbox must stay fail-open for a server that has no stored record at all
// (in-process fixtures register upstreams without one), otherwise the
// consolidation would break script surfaces it was only meant to align.
func TestCodeExecutionGateFailsOpenForUnknownServer(t *testing.T) {
	fixture := newPreflightFixture(t, nil)
	caller := &upstreamToolCaller{proxy: fixture.proxy}
	assert.NoError(t, caller.policyRefusal("never-configured", "some_tool"))

	// ... and with no proxy wired at all (bare unit-test callers).
	assert.NoError(t, (&upstreamToolCaller{}).policyRefusal("gh", "create_issue"))
}

// Fail-open is licensed for a server that is genuinely UNKNOWN. It is not
// licensed for one whose record could not be READ: treating a storage failure
// as "unknown server" would let a script through a quarantine gate by breaking
// the database.
func TestCodeExecutionGateRefusesWhenTheServerRecordCannotBeRead(t *testing.T) {
	fixture := newPreflightFixture(t, nil)
	fixture.addServer(t, &config.ServerConfig{Name: "gh", Enabled: true, Protocol: "http"})
	caller := &upstreamToolCaller{proxy: fixture.proxy}

	require.NoError(t, caller.policyRefusal("gh", "create_issue"))

	require.NoError(t, fixture.storage.Close())

	err := caller.policyRefusal("gh", "create_issue")
	require.Error(t, err, "an unreadable record must not be mistaken for an unknown server")
	assert.Contains(t, err.Error(), "cannot verify policy")

	// The same read failure must also refuse on a server the config never
	// mentioned — the point is that nothing can be verified at all.
	require.Error(t, caller.policyRefusal("never-configured", "some_tool"))
}

// Direct mode reads the LIVE config for the global quarantine switch, exactly
// as evaluateToolGate does. Before this, it took the construction-time config
// and defaulted a missing one to "quarantine off", so the two dispatch paths
// disagreed about the same pending tool — the skew FR-002 exists to prevent.
func TestDirectCallabilityReadsTheSameQuarantineSwitchAsTheSharedGate(t *testing.T) {
	fixture := newPreflightFixture(t, nil)
	fixture.addServer(t, &config.ServerConfig{Name: "gh", Enabled: true, Protocol: "http"})
	require.NoError(t, fixture.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "gh", ToolName: "create_issue", Status: storage.ToolApprovalStatusPending,
	}))

	assertAgree := func(t *testing.T, wantCallable bool) {
		t.Helper()
		gate := fixture.proxy.evaluateToolGate("gh", "create_issue")
		assert.Equal(t, wantCallable, gate.callable(), "shared gate")
		block := fixture.proxy.directToolCallabilityBlock(context.Background(), "gh", "create_issue", map[string]interface{}{})
		assert.Equal(t, wantCallable, block == nil, "direct mode")
	}

	assertAgree(t, false)

	off := false
	fixture.cfg.QuarantineEnabled = &off
	assertAgree(t, true)

	// No resolvable config at all: both paths must fail CLOSED on the switch
	// rather than one of them reading "quarantine off".
	saved := fixture.proxy.config
	fixture.proxy.config = nil
	t.Cleanup(func() { fixture.proxy.config = saved })
	assertAgree(t, false)
}

// A disabled server answers "server disabled", not "tool pending approval":
// the server-level gates own the response when they fire (pre-098 ordering,
// git show bfd43e7ce). Spec 098 moved only the callability DECISION into the
// shared classifier, not the wording preference.
func TestDirectCallabilityDisabledServerKeepsTheServerLevelRefusal(t *testing.T) {
	fixture := newPreflightFixture(t, nil)
	fixture.addServer(t, &config.ServerConfig{Name: "gh", Enabled: false, Protocol: "http"})
	require.NoError(t, fixture.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "gh", ToolName: "create_issue", Status: storage.ToolApprovalStatusPending,
	}))

	block := fixture.proxy.directToolCallabilityBlock(context.Background(), "gh", "create_issue", map[string]interface{}{})
	require.NotNil(t, block, "a disabled server is never callable")

	rendered := renderToolResultText(t, block)
	assert.Contains(t, rendered, "TOOL_BLOCKED")
	assert.NotContains(t, rendered, "TOOL_QUARANTINED",
		"a disabled server must not answer with the approval-lock response")

	// Enabling the server hands the response back to the approval lock.
	fixture.addServer(t, &config.ServerConfig{Name: "gh", Enabled: true, Protocol: "http"})
	enabled := fixture.proxy.directToolCallabilityBlock(context.Background(), "gh", "create_issue", map[string]interface{}{})
	require.NotNil(t, enabled)
	assert.Contains(t, renderToolResultText(t, enabled), "TOOL_QUARANTINED")
}

// renderToolResultText flattens a CallToolResult's text content for assertions.
func renderToolResultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	var b strings.Builder
	for _, content := range result.Content {
		if text, ok := content.(mcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	require.NotEmpty(t, b.String(), "expected text content in %+v", result)
	return b.String()
}

// The consolidated classifier resolves the two research-D2 divergences in
// dispatch's favor.
func TestClassifyServerToolStatusHonorsQuarantineFlags(t *testing.T) {
	t.Run("auto approving server is not reported pending", func(t *testing.T) {
		fixture := newPreflightFixture(t, nil)
		autoApprove := true
		fixture.addServer(t, &config.ServerConfig{
			Name: "gh", Enabled: true, Protocol: "http", AutoApproveToolChanges: &autoApprove,
		})
		require.NoError(t, fixture.storage.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "gh", ToolName: "create_issue", Status: storage.ToolApprovalStatusPending,
		}))
		assert.Equal(t, "", fixture.proxy.classifyServerToolStatus("gh", "create_issue"))
	})

	t.Run("quarantined server reports server_quarantined", func(t *testing.T) {
		fixture := newPreflightFixture(t, nil)
		fixture.addServer(t, &config.ServerConfig{Name: "gh", Enabled: true, Quarantined: true, Protocol: "http"})
		assert.Equal(t, "server_quarantined", fixture.proxy.classifyServerToolStatus("gh", "create_issue"))
	})
}

// describe_tool's gate keeps its own reason vocabulary and its own ordering
// while reading the shared evaluation.
func TestDescribeGateReasonDelegatesToSharedGate(t *testing.T) {
	fixture := newPreflightFixture(t, nil)
	fixture.addServer(t, &config.ServerConfig{Name: "gh", Enabled: true, Protocol: "http"})
	require.NoError(t, fixture.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "gh", ToolName: "changed_tool", Status: storage.ToolApprovalStatusChanged,
	}))
	require.NoError(t, fixture.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "gh", ToolName: "pending_tool", Status: storage.ToolApprovalStatusPending,
	}))

	assert.Equal(t, visReasonToolChangedApproval, fixture.proxy.describeGateReason("gh", "changed_tool"))
	assert.Equal(t, visReasonToolPendingApproval, fixture.proxy.describeGateReason("gh", "pending_tool"))
	assert.Equal(t, "", fixture.proxy.describeGateReason("gh", "plain_tool"))

	fixture.addServer(t, &config.ServerConfig{Name: "locked", Enabled: true, Quarantined: true, Protocol: "http"})
	assert.Equal(t, visReasonServerQuarantined, fixture.proxy.describeGateReason("locked", "anything"))
}

// A user-disabled tool that is ALSO approval-locked still answers with its lock
// message on the dispatch paths (pre-098 ordering), even though the spec-098
// precedence classifies it as blocked_by_user. The callability decision is
// shared; only the wording preference is path-local.
func TestDispatchKeepsLockMessagePreferenceForDisabledPendingTool(t *testing.T) {
	fixture := newPreflightFixture(t, nil)
	fixture.addServer(t, &config.ServerConfig{Name: "gh", Enabled: true, Protocol: "http"})
	require.NoError(t, fixture.storage.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "gh", ToolName: "create_issue",
		Status: storage.ToolApprovalStatusPending, Disabled: true,
	}))

	gate := fixture.proxy.evaluateToolGate("gh", "create_issue")
	assert.False(t, gate.callable())
	assert.Equal(t, storage.ToolApprovalStatusPending, gate.lockStatus)
	assert.Equal(t, preflight.ToolClassBlockedByUser, gate.class)
}

// Scope parity: the session (MCP) path and the preflight path must answer a
// STALE agent-token profile_pin the same way — deny-all, not "fall back to the
// token's own scope". Preflight has intersected to deny-all since spec 098
// (resolvePreflightScope); this asserts the live session path agrees, so an
// agent cannot reach through /mcp what a preflight tells it it cannot have.
func TestStaleTokenPinDeniesOnBothSessionAndPreflightPaths(t *testing.T) {
	fixture := newPreflightFixture(t, func(cfg *config.Config) {
		cfg.Profiles = []config.ProfileConfig{{Name: "ops", Servers: []string{"gh"}}}
	})
	fixture.addServer(t, &config.ServerConfig{Name: "gh", Enabled: true, Protocol: "http"})
	fixture.addServer(t, &config.ServerConfig{Name: "secret", Enabled: true, Protocol: "http"})
	fixture.indexTool(t, "gh", "create_issue")
	fixture.indexTool(t, "secret", "exfiltrate")

	// A token scoped to BOTH servers but pinned to "ops" (gh only): the pin is
	// the narrowing under test.
	authCtx := &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "agent-1",
		ProfilePin:     "ops",
		AllowedServers: []string{"gh", "secret"},
	}
	ctx := auth.WithAuthContext(context.Background(), authCtx)
	params := preflight.Params{
		Tools:           []preflight.ToolRef{{ID: "gh:create_issue"}, {ID: "secret:exfiltrate"}},
		Tier:            preflight.TierAgentToken,
		TokenProfilePin: "ops",
		TokenServers:    []string{"gh", "secret"},
	}

	// Baseline: while "ops" exists both paths allow gh and deny secret.
	visible, _ := fixture.proxy.toolVisibleToSession(ctx, "gh", "create_issue")
	assert.True(t, visible, "session path: the pinned profile's server is visible")
	visible, reason := fixture.proxy.toolVisibleToSession(ctx, "secret", "exfiltrate")
	assert.False(t, visible)
	assert.Equal(t, visReasonServerNotInScope, reason)

	out, err := fixture.proxy.RunPreflight(ctx, params)
	require.NoError(t, err)
	assert.Equal(t, preflight.StatusReady, resultByID(t, out, "gh:create_issue").Status)
	assert.Equal(t, preflight.ReasonNotFound, resultByID(t, out, "secret:exfiltrate").Reason)

	// The operator deletes the pinned profile. Neither path may widen.
	fixture.cfg.Profiles = nil

	for _, id := range []struct{ server, tool string }{{"gh", "create_issue"}, {"secret", "exfiltrate"}} {
		visible, reason := fixture.proxy.toolVisibleToSession(ctx, id.server, id.tool)
		assert.False(t, visible, "session path: %s:%s must be denied under a stale pin", id.server, id.tool)
		assert.Equal(t, visReasonServerNotInScope, reason)

		_, scope := fixture.proxy.resolveActiveProfile(ctx)
		require.NotNil(t, scope)
		vis, _ := fixture.proxy.indexedToolVisible(authCtx, scope, id.server, id.tool)
		assert.False(t, vis, "search path: %s:%s must be denied under a stale pin", id.server, id.tool)
	}

	out, err = fixture.proxy.RunPreflight(ctx, params)
	require.NoError(t, err)
	assert.Equal(t, preflight.ReasonNotFound, resultByID(t, out, "gh:create_issue").Reason)
	assert.Equal(t, preflight.ReasonNotFound, resultByID(t, out, "secret:exfiltrate").Reason)
}
