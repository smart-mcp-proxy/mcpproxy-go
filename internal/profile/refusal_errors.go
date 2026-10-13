package profile

import "errors"

// ToolBlockedError identifies an upstream tool call refused by a compiled
// profile policy. Its message is the stable MCP refusal rendered from the
// decision, while Reason carries the typed audit/HTTP classification.
type ToolBlockedError struct {
	Reason  BlockReason
	Message string
}

func (e *ToolBlockedError) Error() string { return e.Message }

// ErrCodeExecutionBlocked identifies a code_execution refusal enforced by
// the active profile. REST uses it to return the profile-specific 403 while
// MCP and the generic tool-call route retain the unknown-tool response shape.
var ErrCodeExecutionBlocked = errors.New("blocked by profile: code execution is disabled for this profile")

// ErrToolOutsideProfile is intentionally non-descriptive so REST replay can
// answer the same not-found shape for a hidden server and an unknown record.
var ErrToolOutsideProfile = errors.New("tool call not found")
