package server

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
)

// Issue #1683: direct routing mode (/mcp/all, server__tool names) is the fifth
// upstream dispatch path and must honour output_sanitisation like call_tool_*
// and code_execution do. Each test drives the handler mcp-go actually
// registered (directRoutingFixture) against a real in-process upstream that
// returns a secret.

func directSanitisationFixture(t *testing.T, cfg *config.OutputSanitisationConfig, body string) *directRoutingFixture {
	t.Helper()
	f := newDirectRoutingFixture(t, directServerSpec{server: "github", tools: []toolSpec{readSpec("get_secret")}})
	f.proxy.config.OutputSanitisation = cfg
	f.proxy.sanitisationDetector = security.NewDetector(config.DefaultSensitiveDataDetectionConfig())
	f.ups["github"].okBody.Store(&body)
	return f
}

func directText(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, r.Content)
	return r.Content[0].(mcp.TextContent).Text
}

func TestDirectModeSanitisation_RedactMasksSecret(t *testing.T) {
	f := directSanitisationFixture(t, sanCfg("redact", false, false), "token "+awsKeyFixture+" end")
	res := f.call(t, context.Background(), "github", "get_secret")
	require.False(t, res.IsError, directText(t, res))
	assert.NotContains(t, directText(t, res), awsKeyFixture, "the agent must not receive the raw secret")
	assert.Contains(t, directText(t, res), "token ", "non-secret text survives")
}

func TestDirectModeSanitisation_BlockRefusesCriticalResponse(t *testing.T) {
	f := directSanitisationFixture(t, sanCfg("block", false, false), awsKeyFixture)
	res := f.call(t, context.Background(), "github", "get_secret")
	assert.True(t, res.IsError, "a critical detection in block mode must block")
	assert.Contains(t, directText(t, res), "blocked by sanitisation policy")
	assert.NotContains(t, directText(t, res), awsKeyFixture)
}

func TestDirectModeSanitisation_SpotlightWrapsUntrustedText(t *testing.T) {
	f := directSanitisationFixture(t, sanCfg("spotlight", true, false), "hello")
	res := f.call(t, context.Background(), "github", "get_secret")
	require.False(t, res.IsError, directText(t, res))
	text := directText(t, res)
	assert.Contains(t, text, "untrusted:github/get_secret")
	assert.Contains(t, text, "hello")
}

func TestDirectModeSanitisation_StripRemovesControlSequences(t *testing.T) {
	f := directSanitisationFixture(t, sanCfg("spotlight", false, true), "plain\x1b[31mred\x1b[0m")
	res := f.call(t, context.Background(), "github", "get_secret")
	require.False(t, res.IsError, directText(t, res))
	assert.NotContains(t, directText(t, res), "\x1b[")
}

func TestDirectModeSanitisation_DefaultOptOutForwardsUnchanged(t *testing.T) {
	f := directSanitisationFixture(t, config.DefaultOutputSanitisationConfig(), "token "+awsKeyFixture)
	res := f.call(t, context.Background(), "github", "get_secret")
	require.False(t, res.IsError, directText(t, res))
	assert.Equal(t, "token "+awsKeyFixture, directText(t, res),
		"with no output_sanitisation configured the response is forwarded byte for byte")
}
