package telemetry

import (
	"errors"
	"fmt"
)

// AnonymityBlockedPrefixes is the fixed list of byte-prefix substrings that
// MUST NOT appear anywhere in a serialized telemetry payload. These patterns
// catch accidental leaks of user home directories, macOS temp folders, and
// Windows user profile paths.
//
// Additional runtime-detected values (current hostname, home-dir basename,
// env-var values from a blocked set) are appended by the telemetry service
// at startup via BlockedValues — see T025 / spec 044.
var AnonymityBlockedPrefixes = []string{
	"/Users/",
	"/home/",
	`C:\Users\`,
	"/var/folders/",
}

// AnonymityViolation identifies which rule the payload tripped. The Pattern
// field is the offending substring (never the full payload: logging the whole
// payload would defeat the purpose of the scan).
type AnonymityViolation struct {
	// Rule is a short stable identifier ("blocked_prefix", "blocked_value",
	// "env_markers_non_bool") suitable for metrics labels.
	Rule string
	// Pattern is the literal substring or env-var name that matched.
	Pattern string
	// Reason is a human-readable summary (no payload contents).
	Reason string
}

// Error implements the error interface.
func (v *AnonymityViolation) Error() string {
	return fmt.Sprintf("telemetry anonymity violation: %s (rule=%s pattern=%q)", v.Reason, v.Rule, v.Pattern)
}

// ErrAnonymityViolation is a sentinel returned (wrapped) by ScanForPII when a
// violation is found. Tests use errors.As against *AnonymityViolation for
// richer detail; errors.Is against ErrAnonymityViolation remains valid for
// callers that only care about the category.
var ErrAnonymityViolation = errors.New("telemetry anonymity violation")

// Is allows AnonymityViolation to match ErrAnonymityViolation via errors.Is.
func (v *AnonymityViolation) Is(target error) bool {
	return target == ErrAnonymityViolation
}

// ScanForPII scans a serialized v3 telemetry payload for PII leaks and
// structural violations. Returns nil when the payload is clean; otherwise
// returns an *AnonymityViolation wrapped against ErrAnonymityViolation.
//
// Implementation lands in T010; this stub returns a "not implemented" error so
// that T009 tests fail until T010 fills it in.
func ScanForPII(payloadJSON []byte) error {
	return errors.New("ScanForPII: not implemented (T010 pending)")
}
