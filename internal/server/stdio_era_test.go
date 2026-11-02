package server

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 058 T036a — the stdio era gap. FR-028 pins the client-facing surface to
// the legacy protocol era, but mcp-go offers no stdio equivalent of
// WithStreamableHTTPProtocolVersions: the era is chosen per request by the
// shared handler, so a stdio client could opt into 2026-07-28 today while the
// HTTP surface refuses it. The pin must mean the same thing on every surface,
// so a modern request on stdio is rejected before it is dispatched — exactly
// what the Streamable HTTP transport does for a version it does not serve.
//
// Lifting the pin (T041) must lift this guard with it; the guard reads the
// same constant set as clientFacingStreamableOptions.
//
// Test names avoid the substring "MCP" (release-qa-gate.yml skips on it).

func TestStdioEra_ModernRequestIsRejectedUnderThePin(t *testing.T) {
	proxy := profileEraRuntimeProxy(t)
	probe := newStdioProbe(t, proxy.GetMCPServer())

	resp := probe.request(string(mcp.MethodToolsList), map[string]any{"_meta": modernMeta()})
	errObj, ok := resp["error"].(map[string]any)
	require.True(t, ok, "a %s request on stdio must be refused as a JSON-RPC error while the pin holds, got: %v", mcp.LATEST_PROTOCOL_VERSION, resp)
	assert.EqualValues(t, mcp.INVALID_REQUEST, errObj["code"])
	msg, _ := errObj["message"].(string)
	assert.Contains(t, msg, mcp.LATEST_PROTOCOL_VERSION, "the refusal names the version it will not serve: %s", msg)
	assert.Contains(t, msg, mcp.LATEST_LEGACY_PROTOCOL_VERSION, "and the one it does: %s", msg)
}

// The guard is era-scoped, not a stdio shutdown: a legacy stdio client still
// completes the handshake and lists tools.
func TestStdioEra_LegacyRequestsStillServed(t *testing.T) {
	proxy := profileEraRuntimeProxy(t)
	probe := newStdioProbe(t, proxy.GetMCPServer())

	init := probe.request(string(mcp.MethodInitialize), map[string]any{
		"protocolVersion": mcp.LATEST_LEGACY_PROTOCOL_VERSION,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "era-probe", "version": "1.0.0"},
	})
	require.Nil(t, init["error"], "legacy initialize must succeed: %v", init)

	list := probe.request(string(mcp.MethodToolsList), nil)
	require.Nil(t, list["error"], "legacy tools/list must succeed: %v", list)
	result, ok := list["result"].(map[string]any)
	require.True(t, ok)
	tools, _ := result["tools"].([]any)
	assert.NotEmpty(t, tools, "the legacy stdio client lists the built-in tools")
}

// The guard is stdio-scoped: a modern request on the HTTP surface is governed
// by the transport's own version pin, not by this hook, so the era harness's
// unpinned endpoint keeps serving modern requests.
func TestStdioEra_GuardDoesNotReachTheHTTPSurface(t *testing.T) {
	env := newProfileTestEnv(t)
	url := unpinnedProxyEndpoint(t, env.proxyServer, env.proxyServer.mcpProxy.GetMCPServer())

	c := newRawEraClient(t, url, modernEra())
	list := c.result(string(mcp.MethodToolsList), nil)
	c.requireEra(list)
}
