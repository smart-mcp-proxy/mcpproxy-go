package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// When output_sanitisation blocks a response the upstream call DID run, so
// the dispatch must still close its started record with a tool_call
// completion: without one the Activity Log has no row for a call that may
// have had side effects, and SSE consumers see a start that never ends. The
// completion carries the block message, never the response that was blocked.

// blockedSecretFixture is a direct-routing fixture whose "github:get_secret"
// answers with a critical secret, block mode on, and the activity service
// persisting records.
func blockedSecretFixture(t *testing.T) *directRoutingFixture {
	t.Helper()
	f := newDirectRoutingFixture(t, directServerSpec{server: "github", tools: []toolSpec{readSpec("get_secret")}})
	f.ups["github"].mcpSrv.AddTool(mcp.Tool{Name: "get_secret", InputSchema: mcp.ToolInputSchema{Type: "object"}},
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			f.ups["github"].record(req.Params.Name)
			return mcp.NewToolResultText("leaked " + awsKeyFixture), nil
		})
	f.proxy.config.OutputSanitisation = sanCfg("block", false, false)
	f.proxy.sanitisationDetector = security.NewDetector(config.DefaultSensitiveDataDetectionConfig())

	go f.rt.ActivityService().Start(f.rt.AppContext(), f.rt)
	deadline := time.Now().Add(5 * time.Second)
	for !f.rt.ActivityService().Started() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	require.True(t, f.rt.ActivityService().Started(), "activity service must subscribe before the call")
	return f
}

// assertBlockedCallRecorded checks the persisted activity and tool-call
// history for the blocked call: one tool_call row in error state with the
// block message, the policy_decision beside it, and the secret nowhere.
func assertBlockedCallRecorded(t *testing.T, f *directRoutingFixture) {
	t.Helper()
	rec := awaitToolCallActivity(t, f.rt.StorageManager(), "github", "get_secret")
	assert.Equal(t, storage.ActivityStatusError, rec.Status)
	assert.Contains(t, rec.ErrorMessage, "blocked by sanitisation policy")
	assert.Equal(t, "proxy_policy", rec.ErrorClass, "an output-policy block is mcpproxy's refusal, not an upstream failure")
	raw, err := json.Marshal(rec)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), awsKeyFixture, "the tool_call record must not carry the blocked response")

	all, _, err := f.rt.StorageManager().ListActivities(storage.ActivityFilter{Limit: 100})
	require.NoError(t, err)
	toolCalls, blocks := 0, 0
	for _, r := range all {
		if r.ServerName != "github" || r.ToolName != "get_secret" {
			continue
		}
		switch r.Type {
		case storage.ActivityTypeToolCall:
			toolCalls++
		case storage.ActivityTypePolicyDecision:
			if r.Status == storage.ActivityStatusBlocked {
				blocks++
			}
		}
		b, mErr := json.Marshal(r)
		require.NoError(t, mErr)
		assert.NotContains(t, string(b), awsKeyFixture, "no activity record may carry the blocked response")
	}
	assert.Equal(t, 1, toolCalls, "exactly one tool_call completion")
	assert.Equal(t, 1, blocks, "the block decision is still recorded")

}

func TestCallToolVariant_SanitisationBlockRecordsToolCallCompletion(t *testing.T) {
	f := blockedSecretFixture(t)
	req := mcp.CallToolRequest{}
	req.Params.Name = contracts.ToolVariantRead
	req.Params.Arguments = map[string]interface{}{"name": "github:get_secret", "args": map[string]interface{}{}}
	res, err := f.proxy.handleCallToolVariant(fullTierAgentOn("github"), req, contracts.ToolVariantRead)
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Equal(t, int64(1), f.ups["github"].count.Load(), "the upstream ran")

	assertBlockedCallRecorded(t, f)
}

func TestLegacyCallTool_SanitisationBlockRecordsToolCallCompletion(t *testing.T) {
	f := blockedSecretFixture(t)
	req := mcp.CallToolRequest{}
	req.Params.Name = "call_tool"
	req.Params.Arguments = map[string]interface{}{"name": "github:get_secret", "args": map[string]interface{}{}}
	res, err := f.proxy.handleCallTool(adminCtx(), req)
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Equal(t, int64(1), f.ups["github"].count.Load(), "the upstream ran")

	assertBlockedCallRecorded(t, f)
}

func TestDirectMode_SanitisationBlockRecordsToolCallCompletion(t *testing.T) {
	f := blockedSecretFixture(t)
	res := f.call(t, fullTierAgentOn("github"), "github", "get_secret")
	require.True(t, res.IsError)
	require.Equal(t, int64(1), f.ups["github"].count.Load(), "the upstream ran")

	assertBlockedCallRecorded(t, f)
}

// The tool-call history entry of a blocked call keeps the attempt but never
// the response: the block message takes its place. (The direct-routing
// fixture's incarnation guard drops every history write, so this is checked
// on the helper the dispatch paths share.)
func TestRecordOutputBlocked_HistoryCarriesBlockMessageNotResponse(t *testing.T) {
	proxy, _ := createTestProxyWithRuntime(t, nil)
	rec := &storage.ToolCallRecord{
		ID: "call-1", ServerID: "srv-id", ServerName: "github", ToolName: "get_secret",
		Response:  mcp.NewToolResultText("leaked " + awsKeyFixture),
		Timestamp: time.Now(),
	}
	msg := proxy.recordOutputBlocked(context.Background(), rec, 0,
		mcp.NewToolResultError("tool output blocked by sanitisation policy: critical sensitive data detected in tool output"))
	assert.Contains(t, msg, "blocked by sanitisation policy")

	history, err := proxy.storage.GetServerToolCalls("srv-id", 10)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, msg, history[0].Error)
	assert.Nil(t, history[0].Response)
	raw, err := json.Marshal(history[0])
	require.NoError(t, err)
	assert.NotContains(t, string(raw), awsKeyFixture)
}
