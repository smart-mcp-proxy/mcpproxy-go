package storage

import "strings"

// The activity log holds several populations of record, and only one of them is
// "a call the user made". Everything the product prints as a count of CALLS —
// the Activity Log header, the Usage tab's tiles, the histogram under them, the
// tray glance, the CLI — has to count that one population, or the same instance
// reports different totals for the same window on two screens sitting side by
// side (audit finding F1, #1046: 51 vs 25 vs 19).
//
// CountsAsCall is that definition, and it lives here — in the package both the
// aggregate (internal/runtime) and the REST handlers (internal/httpapi) already
// depend on — so there is exactly one copy of it.

// glanceDiscoveryBuiltins are the built-ins whose SUCCESSFUL calls are rows the
// user sees (tray GlanceSelection.swift rule 3, mirrored by the usage
// timeline): retrieve_tools and describe_tool. code_execution is deliberately
// absent — it is an internal primitive, and the upstream calls its scripts make
// are tool_call records that count on their own, so counting the wrapper too
// would double a script under a name no upstream owns. A FAILED code_execution
// still counts, through the failure branch below: it dispatched nothing, so its
// own record is the only trace of the attempt.
var glanceDiscoveryBuiltins = map[string]bool{
	"retrieve_tools": true,
	"describe_tool":  true,
}

// glanceManagementBuiltins never appear in the glance at all (rule 1), whatever
// their status: they are mcpproxy's own housekeeping, not work the user asked
// an upstream for. Keep this set in lockstep with the Swift one.
var glanceManagementBuiltins = map[string]bool{
	"upstream_servers":    true,
	"quarantine_security": true,
}

// CountsAsCall reports whether a record is one of the calls the user made, and
// whether that call is a failure.
//
// Admission, by record type:
//
//   - tool_call — an upstream dispatch. Counts, except when it was shed by a
//     concurrency limiter (spec 093 "rejected"): a shed call never executed and
//     never reached an upstream, so it is backpressure, not traffic. A "blocked"
//     dispatch (a policy refused a code_execution sub-call) counts as a failed
//     call: the user made it and did not get it.
//   - internal_tool_call — one of mcpproxy's own built-ins. The call_tool_*
//     variants are excluded because the dispatch they wrap ALREADY emitted its
//     own tool_call record with the same request id, so counting both would
//     double every direct call. (The tray glance instead collapses the pair by
//     request id, which lets it keep a wrapper that failed BEFORE dispatch and
//     has no upstream partner. A counter sees one record at a time and cannot
//     tell the two apart, so it drops both: undercounting pre-dispatch failures
//     is the safer error of the two.) Management built-ins never count. The rest
//     count when they failed, and on success only for the discovery built-ins
//     the user actually sees.
//   - policy_decision — counts only when a policy blocked or shed the attempt,
//     as a failed call. An allow decision is bookkeeping about a call that is
//     already counted through its own tool_call record.
//
// Every other type — system_start, security_scan, quarantine and config
// changes, prompt fetches — is an EVENT. Those belong in the Activity Log, and
// the log's row count is a real number; it just is not a count of calls.
func CountsAsCall(rec *ActivityRecord) (counted, isError bool) {
	if rec == nil || rec.ToolName == "" {
		return false, false
	}

	switch rec.Type {
	case ActivityTypeToolCall:
		switch rec.Status {
		case ActivityStatusRejected:
			return false, false
		case ActivityStatusBlocked:
			return true, true
		default:
			return true, rec.Status == ActivityStatusError
		}

	case ActivityTypeInternalToolCall:
		if strings.HasPrefix(rec.ToolName, InternalCallToolPrefix) {
			return false, false
		}
		if glanceManagementBuiltins[rec.ToolName] {
			return false, false
		}
		if rec.Status == ActivityStatusSuccess && !glanceDiscoveryBuiltins[rec.ToolName] {
			return false, false
		}
		// Anything a built-in did not complete is a failure, whether it errored
		// or a policy refused it.
		return true, rec.Status != ActivityStatusSuccess

	case ActivityTypePolicyDecision:
		if rec.Status == ActivityStatusBlocked || rec.Status == ActivityStatusRejected {
			return true, true
		}
		return false, false

	default:
		return false, false
	}
}
