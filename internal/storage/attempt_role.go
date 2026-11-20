package storage

import "strings"

// ErrorClassProxyPolicy is the error class of a call MCPProxy itself refused.
// It mirrors callerr.ClassProxyPolicy; storage cannot import callerr.
const ErrorClassProxyPolicy = "proxy_policy"

// AttemptRole says what one activity row contributes to the counts of its
// work session (Spec 124 D2).
//
// Every tool-call attempt has exactly ONE counted row, so counting needs no
// state besides the row itself:
//
//   - a tool_call row is the counted row of its attempt;
//   - an internal_tool_call named call_tool_* mirrors a tool_call and is never
//     counted; any other internal_tool_call (a builtin) is counted;
//   - a blocking policy_decision is counted only when it is the sole row of its
//     attempt. One emitted next to a tool_call row carries Companion and is not
//     counted again;
//   - everything else (warnings, prompts, system rows) is not an attempt.
//
// The emit sites uphold "one counted row per attempt"; the handler-path test in
// internal/server pins it.
type AttemptRole struct {
	// Counted: the row is the counted row of an attempt.
	Counted bool
	// Failed: the attempt's final outcome is not success.
	Failed bool
	// Block: the failure is a refusal by MCPProxy itself (FR-016). Blocks are
	// failures, counted apart.
	Block bool
}

// AttemptRoleOf classifies a row. It is pure: the same row always gets the
// same role.
func AttemptRoleOf(r *ActivityRecord) AttemptRole {
	if r == nil {
		return AttemptRole{}
	}
	switch r.Type {
	case ActivityTypeToolCall:
		return countedRole(r)
	case ActivityTypeInternalToolCall:
		if strings.HasPrefix(r.ToolName, InternalCallToolPrefix) {
			return AttemptRole{}
		}
		return countedRole(r)
	case ActivityTypePolicyDecision:
		if r.Companion || !isBlockingDecision(r.Status) {
			return AttemptRole{}
		}
		return AttemptRole{Counted: true, Failed: true, Block: true}
	default:
		return AttemptRole{}
	}
}

func countedRole(r *ActivityRecord) AttemptRole {
	role := AttemptRole{Counted: true, Failed: r.Status != ActivityStatusSuccess}
	if role.Failed {
		role.Block = r.Status == ActivityStatusBlocked ||
			r.Status == ActivityStatusRejected ||
			r.ErrorClass == ErrorClassProxyPolicy
	}
	return role
}

// isBlockingDecision reports whether a policy_decision status is a refusal.
// Both spellings exist in stored rows.
func isBlockingDecision(status string) bool {
	return status == ActivityStatusBlocked || status == "block"
}
