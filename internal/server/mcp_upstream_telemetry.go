package server

import (
	"errors"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// One telemetry contract for every path that dispatches to an upstream
// (call_tool_* wrapper, direct routing, and each code_execution JS sub-call):
//
//   - upstream_tool_call_count_bucket counts ATTEMPTS, once per actual upstream
//     dispatch (noteUpstreamDispatched). Calls refused before dispatch
//     (validation, quarantine, policy, disconnected server) and calls the
//     concurrency limiter shed or the managed client refused to send never
//     reached an upstream and are not counted. The code_execution invocation
//     itself is a built-in call, not an upstream dispatch: its sub-calls count.
//   - activation.first_real_tool_call_ever is stamped only for a usable result
//     (noteUpstreamSuccess): the upstream answered, did not answer isError:true,
//     and no output policy withheld it. Callers invoke it after their output
//     policy checks, never before.
//
// Schema v14 marks this definition; see docs/features/telemetry.md.

// reachedUpstream reports whether a dispatch attempt actually left the proxy.
func reachedUpstream(err error) bool {
	if err == nil {
		return true
	}
	if _, shed := asShed(err); shed {
		return false
	}
	return !errors.Is(err, managed.ErrConnectionGenerationChanged)
}

// noteUpstreamDispatched counts one upstream attempt. Call it once per
// dispatch, right after the upstream call returns. nil-safe.
func (p *MCPProxyServer) noteUpstreamDispatched(err error) {
	if p == nil || !reachedUpstream(err) {
		return
	}
	p.recordUpstreamTool()
}

// noteUpstreamSuccess stamps lifetime activation when the dispatch produced a
// usable result. nil-safe.
func (p *MCPProxyServer) noteUpstreamSuccess(result interface{}, err error) {
	if p == nil || err != nil || result == nil || upstreamAnsweredWithError(result) {
		return
	}
	p.recordRealToolCallSuccess()
}
