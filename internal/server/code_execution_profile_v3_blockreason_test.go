package server

import (
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// waitNestedChild polls for the tool_call child of parentID on (server, tool).
// Activity is persisted by the bus subscriber, so a fixed sleep would be flaky.
func waitNestedChild(t *testing.T, rt *runtime.Runtime, parentID, server, tool string) *storage.ActivityRecord {
	t.Helper()
	var found *storage.ActivityRecord
	require.Eventually(t, func() bool {
		records, _, err := rt.StorageManager().ListActivities(storage.ActivityFilter{ParentID: parentID, Limit: 50})
		require.NoError(t, err)
		for _, record := range records {
			if record.Type == storage.ActivityTypeToolCall && record.ServerName == server && record.ToolName == tool {
				found = record
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "child record %s:%s of %s", server, tool, parentID)
	return found
}

// waitCodeExecutionParent returns the request id of the code_execution parent
// record, which is the parent_id its sandbox children carry.
func waitCodeExecutionParent(t *testing.T, rt *runtime.Runtime) string {
	t.Helper()
	var parentID string
	require.Eventually(t, func() bool {
		records, _, err := rt.StorageManager().ListActivities(storage.ActivityFilter{
			Types: []string{string(storage.ActivityTypeInternalToolCall)}, Tool: "code_execution", Limit: 50,
		})
		require.NoError(t, err)
		if len(records) == 0 {
			return false
		}
		parentID = records[0].RequestID
		return parentID != ""
	}, 5*time.Second, 10*time.Millisecond, "code_execution parent record")
	return parentID
}

// codeExecProfileFixture is the work-readonly fixture with code execution
// enabled for it, a github upstream that has a deny-rule match
// (list_secrets), an unannotated tool (search_code) and a write tool, and the
// activity service started.
func codeExecProfileFixture(t *testing.T) (*MCPProxyServer, *runtime.Runtime, *countingUpstream) {
	t.Helper()
	proxy, rt := newProfilesV3Fixture(t)
	updated := *rt.Config()
	updated.Profiles = append([]config.ProfileConfig(nil), updated.Profiles...)
	updated.Profiles[0].CodeExecution = boolPtr(true)
	rt.UpdateConfig(&updated, "")
	up := startCountingUpstream(t, proxy, rt, "github",
		writeSpec("create_issue"), readSpec("list_secrets"), toolSpec{Name: "search_code", Description: "Search code"})
	startProfileV3ActivityService(t, rt)
	return proxy, rt, up
}

// TestCodeExecution_ProfileV3NestedRefusalReasons pins the block_reason of the
// child record for every profile refusal cause, for both script APIs, and its
// absence for a refusal that is not a profile tool-policy one (data-model.md).
func TestCodeExecution_ProfileV3NestedRefusalReasons(t *testing.T) {
	cases := []struct {
		name       string
		code       string
		server     string
		tool       string
		wantReason string
	}{
		{"tier above cap", `call_tool("github", "create_issue", {})`, "github", "create_issue", "profile_tier"},
		{"deny rule", `call_tool("github", "list_secrets", {})`, "github", "list_secrets", "profile_rule"},
		{"unannotated", `call_tool("github", "search_code", {})`, "github", "search_code", "profile_unannotated"},
		{"call_tools batch element", `call_tools([{server: "github", tool: "create_issue", args: {}}])`, "github", "create_issue", "profile_tier"},
		{"server outside profile", `call_tool("filesystem", "read_text_file", {})`, "filesystem", "read_text_file", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proxy, rt, up := codeExecProfileFixture(t)

			result, err := proxy.handleCodeExecution(urlProfileCtx(proxy, "work-readonly"), mcp.CallToolRequest{
				Params: mcp.CallToolParams{Arguments: map[string]interface{}{"code": tc.code}},
			})
			require.NoError(t, err)
			require.NotNil(t, result)
			require.False(t, result.IsError, resultText(t, result))
			require.Empty(t, up.dispatched(), "a refused nested call must never reach the upstream")

			parentID := waitCodeExecutionParent(t, rt)
			child := waitNestedChild(t, rt, parentID, tc.server, tc.tool)
			assert.Equal(t, storage.ActivityStatusBlocked, child.Status)
			assert.Equal(t, parentID, child.ParentID)
			assert.Equal(t, "work-readonly", child.Profile)
			assert.Equal(t, "url", child.ProfileSource)
			assert.Equal(t, tc.wantReason, child.EffectiveBlockReason())
			if tc.wantReason == "" {
				assert.Empty(t, child.BlockReason)
				assert.NotContains(t, child.Metadata, storage.MetadataKeyBlockReason)
			}
		})
	}
}

// TestCodeExecution_ProfileV3NestedRefusalMatchesTopLevel proves the nested
// child and the top-level policy_decision for the same refused tool agree on
// block_reason, attribution and refusal text.
func TestCodeExecution_ProfileV3NestedRefusalMatchesTopLevel(t *testing.T) {
	proxy, rt, _ := codeExecProfileFixture(t)
	ctx := urlProfileCtx(proxy, "work-readonly")

	top, err := proxy.handleCallToolVariant(ctx, auditCallToolRequest("github:create_issue", nil), contracts.ToolVariantWrite)
	require.NoError(t, err)
	require.True(t, top.IsError)
	_, err = proxy.handleCodeExecution(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: map[string]interface{}{"code": `call_tool("github", "create_issue", {})`}},
	})
	require.NoError(t, err)

	child := waitNestedChild(t, rt, waitCodeExecutionParent(t, rt), "github", "create_issue")
	var decision *storage.ActivityRecord
	require.Eventually(t, func() bool {
		records, _, listErr := rt.StorageManager().ListActivities(storage.ActivityFilter{
			Types: []string{string(storage.ActivityTypePolicyDecision)}, Server: "github", Tool: "create_issue", Limit: 50,
		})
		require.NoError(t, listErr)
		if len(records) == 0 {
			return false
		}
		decision = records[0]
		return true
	}, 5*time.Second, 10*time.Millisecond, "top-level policy_decision")

	assert.Equal(t, decision.EffectiveBlockReason(), child.EffectiveBlockReason())
	assert.Equal(t, "profile_tier", child.EffectiveBlockReason())
	assert.Equal(t, decision.Profile, child.Profile)
	assert.Equal(t, decision.ProfileSource, child.ProfileSource)
	assert.Equal(t, decision.Metadata["reason"], child.ErrorMessage)
}
