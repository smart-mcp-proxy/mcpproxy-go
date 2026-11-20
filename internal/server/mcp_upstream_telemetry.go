package server

import (
	"context"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

// One telemetry contract for every path that dispatches to an upstream
// (call_tool_* wrapper, direct routing, and each code_execution JS sub-call):
//
//   - upstream_tool_call_count_bucket counts ATTEMPTS, once per request the
//     core client actually handed to the transport (noteUpstreamDispatched).
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
// from the call recorder the dispatch context carries (callerr.BeginDispatch):
// the core client marks it at the exact point a tools/call is handed to the
// transport, so it reads zero for a call cancelled in the admission queue,
// retired by the limiter, refused for a moved connection generation or sent to
// a disconnected client, and two when session recovery repeated the request.
// Pass the context that was handed to the upstream call, after it returns.
// nil-safe.
func (p *MCPProxyServer) noteUpstreamDispatched(dispatchCtx context.Context) {
	if p == nil {
		return
	}
	for n := transport.CallRecorderFrom(dispatchCtx).Snapshot().Dispatches; n > 0; n-- {
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

// upstreamOutcome is the schema-17 completion outcome of ONE logical upstream
// call (Spec 126 FR-035): one per call that dispatched at least one request,
// whichever exit the call takes. The dispatch paths begin it right after
// noteUpstreamDispatched and defer record() at once, so every later return —
// upstream errors, an isError answer, the output-sanitisation and
// output-validation blocks, the moved-generation and review-dirty refusals —
// records exactly one outcome. Only the success point calls markUsable, after
// the output policy checks, at the same point first_real_tool_call_ever is
// stamped. Dispatch counts requests (a session-recovery repeat counts twice);
// this counts the logical call once.
type upstreamOutcome struct {
	p           *MCPProxyServer
	server      string
	incarnation int64
	dispatched  bool
	usable      bool
	recorded    bool
}

// beginUpstreamOutcome reads whether the logical call dispatched anything from
// the call recorder on dispatchCtx (the context handed to the upstream call).
// Zero dispatches — refused by the managed client, shed by the limiter,
// cancelled in the admission queue — means no outcome at all.
func (p *MCPProxyServer) beginUpstreamOutcome(dispatchCtx context.Context, serverName string, incarnation int64) *upstreamOutcome {
	return &upstreamOutcome{
		p:           p,
		server:      serverName,
		incarnation: incarnation,
		dispatched:  transport.CallRecorderFrom(dispatchCtx).Snapshot().Dispatches > 0,
	}
}

// markUsable marks the outcome usable when the result is: no error, a result,
// and no isError:true answer. Callers invoke it after their output policy
// checks, exactly where noteUpstreamSuccess / recordRealToolCallSuccess run.
func (o *upstreamOutcome) markUsable(result interface{}, err error) {
	if o == nil || err != nil || result == nil || upstreamAnsweredWithError(result) {
		return
	}
	o.usable = true
}

// record reports the outcome once; later calls are no-ops. Deferred by the
// dispatch paths.
func (o *upstreamOutcome) record() {
	if o == nil || o.recorded || !o.dispatched || o.p == nil {
		return
	}
	o.recorded = true
	o.p.noteUpstreamOutcome(o.server, o.incarnation, o.usable)
}

// noteUpstreamOutcome hands one outcome to the runtime, which applies the
// starter classifier (FR-037), counts it in the reset-on-accept window and
// advances the persisted success_scope_ever (FR-038). Unit tests that observe
// a bare registry (telemetryRegOverride) get the count without a classifier.
func (p *MCPProxyServer) noteUpstreamOutcome(serverName string, incarnation int64, usable bool) {
	if p == nil {
		return
	}
	if p.telemetryRegOverride == nil && p.mainServer != nil && p.mainServer.runtime != nil {
		p.mainServer.runtime.RecordUpstreamOutcome(serverName, incarnation, usable)
		return
	}
	telemetry.RecordUpstreamOutcomeOn(p.telemetryRegistry(), usable, false)
}
