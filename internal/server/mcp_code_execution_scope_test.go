package server

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/jsruntime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
)

// Spec 105 FR-009 on the nested (code_execution) dispatch path. The sandbox is
// a dispatch path like call_tool_* and direct mode: every upstream call a
// script makes must resolve the target's registration identity from the
// canonical server / raw tool pair it will dispatch, and refuse with zero
// upstream calls when that identity cannot be resolved.

// sandboxCall is the parsed {ok, code, message} envelope of one call_tool()
// issued from inside a script.
type sandboxCall struct {
	OK      bool
	Code    string
	Message string
}

// runSandboxCallTool executes a script that issues exactly one
// call_tool(server, tool, {}) through the real code_execution handler under
// the given caller and returns the inner envelope the script observed.
func runSandboxCallTool(t *testing.T, proxy *MCPProxyServer, ctx context.Context, server, tool string) sandboxCall {
	t.Helper()
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "code_execution",
		Arguments: map[string]interface{}{
			"code": fmt.Sprintf(
				`var r = call_tool(%q, %q, {}); ({ ok: r.ok, code: r.error ? r.error.code : null, message: r.error ? r.error.message : null })`,
				server, tool),
			"input":   map[string]interface{}{},
			"options": map[string]interface{}{"timeout_ms": 10000, "max_tool_calls": 0},
		},
	}}
	result, err := proxy.handleCodeExecution(ctx, request)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.Content)
	text := result.Content[0].(mcp.TextContent).Text
	require.False(t, result.IsError, "the script itself must run: %s", text)

	var outer map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &outer), "code_execution body must be JSON: %s", text)
	inner, ok := outer["value"].(map[string]interface{})
	require.True(t, ok, "code_execution wraps the script's return value under \"value\": %s", text)

	call := sandboxCall{}
	call.OK, _ = inner["ok"].(bool)
	call.Code, _ = inner["code"].(string)
	call.Message, _ = inner["message"].(string)
	return call
}

// Spec 105 FR-009 G4 (task T007), nested-dispatch cells. Inside a script,
// call_tool('a', 'ghost') names a tool the discovery snapshot of the KNOWN
// server "a" does not contain. Today lookupToolPermission answers the sandbox
// with the destructive tier for an unresolvable name, so a token holding that
// tier — and an administrator, whose AuthInfo passes every permission check —
// is dispatched to the upstream with an unverified name. Research D4: a failed
// identity resolution on a known server is refused with the insufficient-
// permission envelope (PERMISSION_DENIED) and zero upstream calls for EVERY
// caller, administrators included; the separate unknown-server branch
// (policyRefusal's fail-open for a server with no stored record) is unchanged.
//
// Oracle note: the counting stub has no handler for "ghost", so the count
// cannot witness a reach by itself — the merge base reaches the upstream and
// the script sees its "tool 'ghost' not found" answer under a non-permission
// code, which is why the envelope code is asserted alongside the count.
func TestCodeExecution_UnresolvedIdentityOnKnownServer_RefusedForEveryCaller(t *testing.T) {
	for label, ctx := range map[string]context.Context{
		"full-tier a-only token": fullTierAgentOn("a"),
		"api-key admin":          adminCtx(),
	} {
		t.Run(label, func(t *testing.T) {
			proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
			up := startCountingUpstream(t, proxy, rt, "a", readSpec("erase"))
			require.Nil(t, proxy.lookupToolAnnotations("a", "ghost"), "fixture: the snapshot must not contain ghost")

			call := runSandboxCallTool(t, proxy, ctx, "a", "ghost")
			assert.False(t, call.OK, "an unresolvable identity must not succeed")
			assert.Equal(t, string(jsruntime.ErrorCodePermissionDenied), call.Code,
				"a failed identity resolution on a known server must be the insufficient-permission envelope (got %q: %s)", call.Code, call.Message)
			assert.NotContains(t, call.Message, "not found", "the upstream's own answer must never be relayed to the script")
			assert.Equal(t, int64(0), up.count.Load(), "the call must never reach the upstream")

			// Positive control on the same fixture and caller: the discovered
			// tool dispatches, under its exact raw name.
			ctl := runSandboxCallTool(t, proxy, ctx, "a", "erase")
			assert.True(t, ctl.OK, "control: erase is discovered and approved and must dispatch (got %q: %s)", ctl.Code, ctl.Message)
			assert.Equal(t, int64(1), up.count.Load())
			assert.Equal(t, []string{"erase"}, up.dispatched())
		})
	}

	t.Run("control: unknown server keeps the server-existence answer", func(t *testing.T) {
		proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{{Name: "a", Enabled: true}})
		up := startCountingUpstream(t, proxy, rt, "a", readSpec("erase"))

		// An administrator names a server with no stored record: that is the
		// unknown-server branch, which stays fail-open to the existing
		// "server not found" answer rather than becoming PERMISSION_DENIED.
		call := runSandboxCallTool(t, proxy, adminCtx(), "zzz", "ghost")
		assert.False(t, call.OK)
		assert.NotEqual(t, string(jsruntime.ErrorCodePermissionDenied), call.Code,
			"an UNKNOWN server is server-existence handling, not identity resolution (got %q: %s)", call.Code, call.Message)
		assert.Contains(t, call.Message, "server not found: zzz")
		assert.Equal(t, int64(0), up.count.Load())
	})
}

// Nested-path twin of TestCallToolRead_EmptySnapshot_KeepsServerLevelVerdicts:
// a KNOWN server whose StateView snapshot is EMPTY because of its own state
// (quarantined, never discovered) must not be answered with the unresolved-
// identity envelope inside a script either. The sandbox's annotation lookup
// keeps its tier fallback for an un-hydrated snapshot, so checkDispatchGates
// passes the call on to policyRefusal, which answers with the pre-105
// quarantine verdict — for administrators and full-tier agents alike.
func TestCodeExecution_EmptySnapshot_KeepsServerLevelVerdicts(t *testing.T) {
	for label, ctx := range map[string]context.Context{
		"full-tier a-only token": fullTierAgentOn("a"),
		"api-key admin":          adminCtx(),
	} {
		t.Run(label, func(t *testing.T) {
			serverCfg := &config.ServerConfig{Name: "a", Enabled: true, Quarantined: true}
			proxy, rt := createTestProxyWithRuntime(t, []*config.ServerConfig{serverCfg})
			require.NoError(t, proxy.storage.SaveUpstreamServer(serverCfg))
			rt.Supervisor().StateView().UpdateServer("a", func(s *stateview.ServerStatus) {
				s.Name, s.Enabled, s.Quarantined, s.Connected, s.Tools = "a", true, true, false, nil
			})
			require.NotEqual(t, jsruntime.PermissionTierUnresolved, proxy.lookupToolPermission("a", "erase"),
				"an empty snapshot is the server's state, not an unresolved identity")

			call := runSandboxCallTool(t, proxy, ctx, "a", "erase")
			assert.False(t, call.OK)
			assert.NotEqual(t, string(jsruntime.ErrorCodePermissionDenied), call.Code,
				"the quarantine verdict must answer, not the identity gate (got %q: %s)", call.Code, call.Message)
			assert.Contains(t, call.Message, "quarantined for security review", "pre-105 body from policyRefusal")
			assert.NotContains(t, call.Message, "cannot be resolved")
		})
	}
}
