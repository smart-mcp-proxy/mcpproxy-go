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

// Spec 098 — regression guard for the index-shadowing gap found by the T025
// live no-skew E2E (and originally pinned here as a characterization test).
//
// The runtime REMOVES a tool from the shared Bleve index the moment it becomes
// user-blocked, pending, or changed (internal/runtime/lifecycle.go tool-index
// maintenance). Index absence alone therefore cannot prove nonexistence: a
// spec-032 approval record is authoritative evidence the tool exists upstream.
// The evaluator's existence source is index ∨ approval record, so the three
// approval-derived reasons below survive de-indexing instead of being shadowed
// by a misleading not_found (whose did_you_mean could even point at a
// DIFFERENT server's tool on a rug-pull — the worst possible hint).
//
// These tests reproduce the live ordering — server indexed, the sabotaged tool
// NOT indexed, approval record present — and assert the true reason reaches
// the caller while every dispatch path still refuses (FR-002 both directions).
func TestPreflightReasonSurvivesToolDeindexing(t *testing.T) {
	const server = "gh"

	cases := []struct {
		name string
		// approval is the record the runtime leaves behind for the sabotage.
		approval *storage.ToolApprovalRecord
		// want is the reason FR-004 names for this state; it must NOT be
		// shadowed by not_found even though the tool is absent from the index.
		want preflight.Reason
	}{
		{
			name: "tool_blocked_by_user",
			approval: &storage.ToolApprovalRecord{
				ServerName: server, ToolName: "create_issue",
				Status: storage.ToolApprovalStatusApproved, Disabled: true,
			},
			want: preflight.ReasonToolBlockedByUser,
		},
		{
			name: "tool_pending_approval",
			approval: &storage.ToolApprovalRecord{
				ServerName: server, ToolName: "create_issue",
				Status: storage.ToolApprovalStatusPending,
			},
			want: preflight.ReasonToolPendingApproval,
		},
		{
			name: "tool_changed",
			approval: &storage.ToolApprovalRecord{
				ServerName: server, ToolName: "create_issue",
				Status: storage.ToolApprovalStatusChanged,
			},
			want: preflight.ReasonToolChanged,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newPreflightFixture(t, nil)
			fixture.addServer(t, &config.ServerConfig{Name: server, Enabled: true, Protocol: "http"})
			// The server is in the index; the sabotaged tool is not — exactly
			// what a live instance looks like after the block/pending/changed
			// transition de-indexes it.
			fixture.indexTool(t, server, "list_issues")
			require.NoError(t, fixture.storage.SaveToolApproval(tc.approval))

			// Every dispatch path still refuses: the FR-002 callability
			// guarantee holds regardless of the index.
			gate := fixture.proxy.evaluateToolGate(server, "create_issue")
			assert.False(t, gate.callable(), "dispatch gate must refuse")
			assert.NotNil(t,
				fixture.proxy.directToolCallabilityBlock(context.Background(), server, "create_issue", map[string]interface{}{}),
				"direct-mode dispatch must refuse")
			assert.Error(t,
				(&upstreamToolCaller{proxy: fixture.proxy}).policyRefusal(server, "create_issue"),
				"code_execution / stored-script bridge must refuse")

			out, err := fixture.proxy.RunPreflight(context.Background(), preflight.Params{
				Tools: []preflight.ToolRef{{ID: server + ":create_issue"}},
			})
			require.NoError(t, err)
			require.Len(t, out.Results, 1)

			// Agreement on the DECISION...
			assert.Equal(t, preflight.StatusUnavailable, out.Results[0].Status)
			// ...AND on the reason: the approval record proves existence, so
			// the taxonomy code that names this state reaches the caller.
			assert.Equal(t, tc.want, out.Results[0].Reason,
				"approval-derived reason must survive de-indexing (not_found would be misleading)")
			assert.Empty(t, out.Results[0].DidYouMean,
				"a known tool must never carry a did_you_mean suggestion")
		})
	}
}

// A truly unknown id (no index entry AND no approval record) still reports
// not_found on a Ready server — the fix must not widen existence beyond the
// two authoritative sources.
func TestPreflightUnknownToolStillNotFound(t *testing.T) {
	fixture := newPreflightFixture(t, nil)
	fixture.addServer(t, &config.ServerConfig{Name: "gh", Enabled: true, Protocol: "http"})
	fixture.indexTool(t, "gh", "list_issues")

	out, err := fixture.proxy.RunPreflight(context.Background(), preflight.Params{
		Tools: []preflight.ToolRef{{ID: "gh:create_issue"}},
	})
	require.NoError(t, err)
	require.Len(t, out.Results, 1)
	assert.Equal(t, preflight.ReasonNotFound, out.Results[0].Reason)
}
