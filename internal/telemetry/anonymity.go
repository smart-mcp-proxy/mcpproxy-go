package telemetry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// AnonymityBlockedPrefixes is the fixed list of byte-prefix substrings that
// MUST NOT appear anywhere in a serialized telemetry payload. These patterns
// catch accidental leaks of user home directories, macOS temp folders, and
// Windows user profile paths.
//
// Additional runtime-detected values (current hostname, home-dir basename,
// env-var values from a blocked set) are appended by the telemetry service
// at startup via BlockedValues — see T025 / spec 044.
// NOTE on backslash form: telemetry payloads are JSON, and JSON encodes a
// single backslash as two bytes (`\\`). So the Windows user-profile prefix
// appears on the wire as `C:\\Users\\`. We match the JSON-escaped form here.
var AnonymityBlockedPrefixes = []string{
	"/Users/",
	"/home/",
	`C:\\Users\\`,
	"/var/folders/",
}

// BlockedValues is the runtime-populated list of literal substrings that MUST
// NOT appear in a payload. Populated at startup by the telemetry service from:
//   - os.Hostname() (if non-empty)
//   - last path component of os.UserHomeDir() (if non-empty)
//   - values of env vars in the blocked set (GITHUB_TOKEN, GITLAB_TOKEN,
//     OPENAI_API_KEY, ANTHROPIC_API_KEY, GOOGLE_API_KEY), when non-empty.
//
// The package-level var is intentionally mutable so tests can inject fake
// values. Production code should call SetBlockedValues (added in T025) once
// at startup; after that, treat the slice as read-only.
var BlockedValues []string

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

// anonymityScanEnvelope extracts env_markers into a strict bool-typed struct
// so we can catch any widening of EnvMarkers fields to non-bool types in the
// serialized payload.
type anonymityScanEnvelope struct {
	EnvMarkers *json.RawMessage `json:"env_markers"`
}

// ScanForPII scans a serialized v3 telemetry payload for PII leaks and
// structural violations. Returns nil when the payload is clean; otherwise
// returns an *AnonymityViolation. The returned error satisfies
// errors.Is(err, ErrAnonymityViolation).
//
// Rules (first-match wins):
//  1. Any substring from AnonymityBlockedPrefixes appears in the payload.
//  2. Any substring from BlockedValues appears in the payload.
//  3. env_markers, if present, fails to unmarshal into a strict all-bool
//     struct — meaning a field widened to a string/number/null.
//
// The implementation never logs the payload — it only reports which rule
// tripped and the offending pattern (a small literal). Callers should log at
// error level and skip the heartbeat.
func ScanForPII(payloadJSON []byte) error {
	// Rule 1: static blocked prefixes.
	for _, p := range AnonymityBlockedPrefixes {
		if bytes.Contains(payloadJSON, []byte(p)) {
			return &AnonymityViolation{
				Rule:    "blocked_prefix",
				Pattern: p,
				Reason:  fmt.Sprintf("payload contains blocked prefix %q", p),
			}
		}
	}

	// Rule 2: runtime-populated blocked values (hostnames, tokens, etc.).
	for _, v := range BlockedValues {
		if v == "" {
			continue
		}
		if bytes.Contains(payloadJSON, []byte(v)) {
			return &AnonymityViolation{
				Rule:    "blocked_value",
				Pattern: v,
				Reason:  "payload contains a runtime-blocked value (hostname, token, or home-dir basename)",
			}
		}
	}

	// Rule 3: env_markers must serialize with all-bool fields. We use a
	// strict decoder against EnvMarkers; any type mismatch is a violation.
	var env anonymityScanEnvelope
	if err := json.Unmarshal(payloadJSON, &env); err != nil {
		// A malformed envelope is itself a structural problem — report it.
		return &AnonymityViolation{
			Rule:    "malformed_payload",
			Pattern: "",
			Reason:  fmt.Sprintf("payload failed envelope decode: %v", err),
		}
	}
	if env.EnvMarkers != nil {
		dec := json.NewDecoder(bytes.NewReader(*env.EnvMarkers))
		dec.DisallowUnknownFields()
		var m EnvMarkers
		if err := dec.Decode(&m); err != nil {
			return &AnonymityViolation{
				Rule:    "env_markers_non_bool",
				Pattern: "env_markers",
				Reason:  fmt.Sprintf("env_markers has a non-bool or unknown field: %v", err),
			}
		}
	}

	return nil
}

// Compile-time guards so linters don't flag the unused imports if the file is
// trimmed later.
var _ = errors.New

// sensitiveEnvVarNames is the fixed set of env-var names whose VALUES (when
// non-empty) are appended to BlockedValues at startup. The names themselves
// never appear in the payload; only the values would, and those are exactly
// what we want the scanner to catch if leaked.
var sensitiveEnvVarNames = []string{
	"GITHUB_TOKEN",
	"GITLAB_TOKEN",
	"OPENAI_API_KEY",
	"ANTHROPIC_API_KEY",
	"GOOGLE_API_KEY",
}

// initBlockedValuesOnce guards PopulateBlockedValues so repeat calls are safe.
var initBlockedValuesOnce sync.Once

// PopulateBlockedValues (Spec 044 T025) scans the current process's OS-level
// identity (hostname, user home dir, sensitive env vars) and appends any
// non-empty literal to BlockedValues. Safe to call multiple times — only the
// first call has effect.
//
// Inputs are read through function pointers so tests can inject fakes without
// reshelling os.Hostname / os.Getenv.
func PopulateBlockedValues() {
	initBlockedValuesOnce.Do(func() {
		populateBlockedValuesFrom(os.Hostname, os.UserHomeDir, os.Getenv)
	})
}

// populateBlockedValuesFrom is the testable core of PopulateBlockedValues. It
// appends to BlockedValues:
//   - os.Hostname() result (if non-empty and distinguishable — we skip values
//     shorter than 3 bytes to avoid false positives on generic strings).
//   - The LAST path component of os.UserHomeDir() (i.e. the username), if
//     non-empty. We deliberately do NOT blocklist the full home-dir path:
//     that is already covered by AnonymityBlockedPrefixes (/Users/, /home/).
//   - The value of each env var in sensitiveEnvVarNames, if non-empty.
//
// Duplicate values are coalesced. Short values (<3 bytes) are dropped to
// avoid spurious matches against short tokens in normal payload JSON.
func populateBlockedValuesFrom(
	hostname func() (string, error),
	userHomeDir func() (string, error),
	getenv func(string) string,
) {
	seen := make(map[string]struct{}, len(BlockedValues)+8)
	out := make([]string, 0, len(BlockedValues)+8)
	// Preserve any pre-existing values (tests may inject fakes).
	for _, v := range BlockedValues {
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}

	add := func(v string) {
		if len(v) < 3 {
			return
		}
		if _, dup := seen[v]; dup {
			return
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}

	if h, err := hostname(); err == nil {
		add(h)
	}
	if home, err := userHomeDir(); err == nil && home != "" {
		add(filepath.Base(home))
	}
	for _, name := range sensitiveEnvVarNames {
		add(getenv(name))
	}

	BlockedValues = out
}
