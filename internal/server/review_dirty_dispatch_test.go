package server

import (
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolreview"
)

// Spec 117 FR-018 (#1610, T306d f/f2): while a server's latest tool capture
// is incomplete (review-dirty), dispatch of every tool of the server is
// refused with the ErrReviewNotRecorded text and the upstream sees zero
// calls, on every surface: call_tool_*, code_execution and the direct
// surface. Listing and description are unchanged: the direct entry stays
// listable and describable, and the call reaches the handler.
func TestReviewDirty_DispatchRefusedOnEverySurface(t *testing.T) {
	proxy, rt, up := seedEpochFixture(t)
	entry := &directCatalogEntry{
		ServerName:  "a",
		ToolName:    "erase",
		DisplayName: FormatDirectToolName("a", "erase"),
		ParamsJSON:  `{"type":"object"}`,
		Annotations: readSpec("erase").Annotations,
	}
	direct := func() string {
		req := mcp.CallToolRequest{}
		req.Params.Name = entry.DisplayName
		req.Params.Arguments = map[string]interface{}{}
		result, err := proxy.makeDirectModeHandler(entry)(adminCtx(), req)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotEmpty(t, result.Content)
		return result.Content[0].(mcp.TextContent).Text
	}
	want := toolreview.ErrReviewNotRecorded.Error()

	rt.MarkToolReviewDirtyForTest("a", 7)
	require.True(t, rt.ToolReviewDirty("a"))

	_, text := callToolReadResult(t, proxy, fullTierAgentOn("a"), "a:erase")
	assert.Contains(t, text, want, "call_tool_read is refused (got %s)", text)
	call := runSandboxCallTool(t, proxy, adminCtx(), "a", "erase")
	assert.False(t, call.OK)
	assert.Contains(t, call.Message, want, "code_execution is refused (got %q: %s)", call.Code, call.Message)
	assert.Contains(t, direct(), want, "the direct surface is refused")
	assert.Equal(t, int64(0), up.count.Load(), "no refused call reaches the upstream")

	// Listing and description are unchanged (f2): dirty is dispatch-only.
	decision := newDirectCallabilityEvaluator(proxy).evaluate("a", "erase")
	assert.True(t, decision.callable, "listing eligibility does not read reviewDirty")
	assert.True(t, decision.reviewDirty)
	assert.Empty(t, proxy.describeGateReason("a", "erase"), "describe_tool visibility is unchanged")
	gate := proxy.evaluateExactToolGate("a", "erase")
	assert.True(t, gate.class.Callable(), "the classification is unchanged")
	assert.False(t, gate.callable())

	// A complete newer capture clears it and the same calls dispatch.
	rt.ClearToolReviewDirtyForTest("a", 8)
	_, text = callToolReadResult(t, proxy, fullTierAgentOn("a"), "a:erase")
	assert.NotContains(t, text, want, "control: dispatches once clean (got %s)", text)
	assert.NotContains(t, direct(), want)
	assert.Equal(t, int64(2), up.count.Load())
}

// Spec 117 FR-018 (#1635 review): the dirty verdict read at the gate is a
// snapshot. A call already past the gate and parked in admission control must
// be re-checked immediately before the transport, so a capture that fails
// persistence while the call waits refuses it with zero upstream calls, on
// every dispatch surface.
func TestReviewDirty_MarkedWhileQueued_RefusedWithZeroUpstreamCalls(t *testing.T) {
	want := toolreview.ErrReviewNotRecorded.Error()
	directCall := func(t *testing.T, proxy *MCPProxyServer) string {
		entry := &directCatalogEntry{
			ServerName:  "a",
			ToolName:    "erase",
			DisplayName: FormatDirectToolName("a", "erase"),
			ParamsJSON:  `{"type":"object"}`,
			Annotations: readSpec("erase").Annotations,
		}
		req := mcp.CallToolRequest{}
		req.Params.Name = entry.DisplayName
		req.Params.Arguments = map[string]interface{}{}
		result, err := proxy.makeDirectModeHandler(entry)(adminCtx(), req)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotEmpty(t, result.Content)
		return result.Content[0].(mcp.TextContent).Text
	}
	surfaces := map[string]func(t *testing.T, proxy *MCPProxyServer) string{
		"call_tool_read": func(t *testing.T, proxy *MCPProxyServer) string {
			_, text := callToolReadResult(t, proxy, fullTierAgentOn("a"), "a:erase")
			return text
		},
		"code_execution": func(t *testing.T, proxy *MCPProxyServer) string {
			call := runSandboxCallTool(t, proxy, adminCtx(), "a", "erase")
			if call.OK {
				return "ok"
			}
			return call.Message
		},
		"direct": directCall,
	}
	for label, dispatch := range surfaces {
		t.Run(label, func(t *testing.T) {
			proxy, rt, up := seedEpochFixture(t)
			reg, release := holdAdmissionSlot(t, proxy, "a")

			done := make(chan string, 1)
			go func() { done <- dispatch(t, proxy) }()
			require.Eventually(t, func() bool {
				return reg.Server("a").Stats().Queued == 1
			}, 5*time.Second, 5*time.Millisecond, "the gated call must be parked in the admission queue")

			// A concurrent capture fails persistence while the call waits.
			rt.MarkToolReviewDirtyForTest("a", 7)
			release()

			var text string
			select {
			case text = <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("the queued call never returned")
			}
			assert.Contains(t, text, want, "the queued call is refused once the server is review-dirty (got %s)", text)
			assert.Equal(t, int64(0), up.count.Load(), "the refused call never reaches the upstream")

			// Control: a complete newer capture clears it and the call dispatches.
			rt.ClearToolReviewDirtyForTest("a", 8)
			text = dispatch(t, proxy)
			assert.NotContains(t, text, want, "control: dispatches once clean (got %s)", text)
			assert.Equal(t, int64(1), up.count.Load())
		})
	}
}
