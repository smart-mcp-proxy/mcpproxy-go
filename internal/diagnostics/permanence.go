package diagnostics

// IsPermanent reports whether a failure carrying this code is deterministic and
// unrecoverable — the same attempt will fail identically until a human changes
// the configuration, the image, or the machine.
//
// It is the single permanence signal for the retry policy in
// internal/upstream/types (GH #1145). Declaring a NEW code permanent is a
// one-line `Retry: RetryPermanent,` in that code's existing register(...) call —
// there is deliberately no second list to keep in sync and no if-branch to add.
//
// Unregistered and unknown codes return false. That is the safe default and the
// reason MCPX_UNKNOWN_UNCLASSIFIED (which carries the zero RetryClass) stays
// retryable: failing to recognise an error is not evidence that retrying it is
// futile.
func IsPermanent(c Code) bool {
	e, ok := registry[c]
	return ok && e.Retry == RetryPermanent
}

// PermanentFailureReason renders the user-facing cause of a permanent park: the
// catalog message written to tell a user how to fix exactly this code. It falls
// back to the raw error, and then to a generic sentence, so a parked server is
// never surfaced without a reason (GH #1145).
func PermanentFailureReason(c Code, rawError string) string {
	if entry, ok := registry[c]; ok && entry.UserMessage != "" {
		return entry.UserMessage
	}
	if rawError != "" {
		return rawError
	}
	return "This server cannot start with its current configuration."
}
