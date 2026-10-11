package server

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

// One telemetry contract for every path that dispatches to an upstream
// (call_tool_* wrapper, direct routing, and each code_execution JS sub-call):
//
//   - upstream_tool_call_count_bucket counts ATTEMPTS, once per request the
//     managed client actually handed to the transport (noteUpstreamDispatched).
//     Calls refused before dispatch (validation, quarantine, policy,
//     disconnected server), calls the limiter shed or retired, calls cancelled
//     in the admission queue and calls the client refused to send never
//     reached an upstream and are not counted. A session-recovery repeat is a
//     second request and counts again. The code_execution invocation itself is
//     a built-in call, not an upstream dispatch: its sub-calls count.
//   - activation.first_real_tool_call_ever is stamped only for a usable result
//     (noteUpstreamSuccess): the upstream answered, did not answer isError:true,
//     and no output policy withheld it. Callers invoke it after their output
//     policy checks, never before.
//
// Schema v14 marks this definition; see docs/features/telemetry.md.

// noteUpstreamDispatched counts the upstream attempts one logical call made,
// from the managed client's own tally of requests handed to the transport
// (managed.DispatchTally): zero for a call cancelled in the admission queue,
// retired by the limiter, refused for a moved connection generation or sent to
// a disconnected client, and two when session recovery repeated the request.
// Call it once per logical call, after the upstream call returns. nil-safe.
func (p *MCPProxyServer) noteUpstreamDispatched(tally *managed.DispatchTally) {
	if p == nil {
		return
	}
	for n := tally.Count(); n > 0; n-- {
		p.recordUpstreamTool()
	}
}

// noteUpstreamSuccess stamps lifetime activation when the dispatch produced a
// usable result. nil-safe.
func (p *MCPProxyServer) noteUpstreamSuccess(result interface{}, err error) {
	if p == nil || err != nil || result == nil || upstreamAnsweredWithError(result) {
		return
	}
	p.recordRealToolCallSuccess()
}
