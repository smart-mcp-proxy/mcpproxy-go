package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
