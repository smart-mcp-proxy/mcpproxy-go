package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// The script bridge is a fourth upstream dispatch path (issue #935), so
// output_sanitisation must hold there too: a secret that call_tool_* would
// redact or block must not reach JavaScript raw, where a script can read it,
// pass it to another upstream, or return it. Before this seam existed the
// bridge returned client.CallTool's result untouched.

func TestSubCallSanitisation_RedactMasksSecretBeforeScriptSeesIt(t *testing.T) {
	cfg := config.DefaultOutputSanitisationConfig()
	cfg.ResponseAction = "redact"
	u := &upstreamToolCaller{logger: zap.NewNop(), proxy: newSanProxy(t, cfg, true)}

	res, err := u.sanitiseSubCallResult(context.Background(), "srv", "tool", "req-1",
		textResult(mcp.TextContent{Type: "text", Text: "token " + awsKeyFixture}))
	require.NoError(t, err)
	ctr, ok := res.(*mcp.CallToolResult)
	require.True(t, ok)
	assert.NotContains(t, firstText(ctr), awsKeyFixture, "the script must not see the raw secret")
	assert.Contains(t, firstText(ctr), "token ", "non-secret text survives")
}

func TestSubCallSanitisation_BlockRefusesTheSubCall(t *testing.T) {
	cfg := config.DefaultOutputSanitisationConfig()
	cfg.ResponseAction = "block"
	u := &upstreamToolCaller{logger: zap.NewNop(), proxy: newSanProxy(t, cfg, true)}

	res, err := u.sanitiseSubCallResult(context.Background(), "srv", "tool", "req-1",
		textResult(mcp.TextContent{Type: "text", Text: awsKeyFixture}))
	require.Error(t, err, "a blocked response must surface to the script as a failed call")
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "blocked by sanitisation policy")
}

func TestSubCallSanitisation_DefaultOptOutIsANoOp(t *testing.T) {
	u := &upstreamToolCaller{logger: zap.NewNop(), proxy: newSanProxy(t, config.DefaultOutputSanitisationConfig(), true)}
	in := textResult(mcp.TextContent{Type: "text", Text: awsKeyFixture})
	res, err := u.sanitiseSubCallResult(context.Background(), "srv", "tool", "req-1", in)
	require.NoError(t, err)
	assert.Same(t, in, res, "the opt-out default forwards the result untouched, as call_tool_* does")
}

func TestSubCallSanitisation_NoProxyIsANoOp(t *testing.T) {
	// Unit fixtures drive the caller without a proxy; the seam must not panic
	// or change behaviour there.
	u := &upstreamToolCaller{logger: zap.NewNop()}
	in := textResult(mcp.TextContent{Type: "text", Text: awsKeyFixture})
	res, err := u.sanitiseSubCallResult(context.Background(), "srv", "tool", "req-1", in)
	require.NoError(t, err)
	assert.Same(t, in, res)
}

// A tool that publishes no annotations is open-world by the MCP spec default,
// hence untrusted — so the untrusted-only strip policy must reach it from a
// script exactly as it does from call_tool_* (which derives trust the same way).
func TestSubCallSanitisation_NoAnnotationsIsUntrusted_StripApplies(t *testing.T) {
	cfg := config.DefaultOutputSanitisationConfig()
	cfg.StripControlChars = true
	u := &upstreamToolCaller{logger: zap.NewNop(), proxy: newSanProxy(t, cfg, false)}

	in := textResult(mcp.TextContent{Type: "text", Text: "plain\x1b[31mred\x1b[0m"})
	res, err := u.sanitiseSubCallResult(context.Background(), "srv", "unknown-tool", "req-1", in)
	require.NoError(t, err)
	ctr := res.(*mcp.CallToolResult)
	assert.NotContains(t, firstText(ctr), "\x1b[", "control sequences must be stripped from an unannotated (open-world) tool's output")
}
