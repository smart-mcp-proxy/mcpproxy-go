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
// serialized payload, plus the Spec 080 v7 fields so their boolean /
// non-negative-integer / fixed-enum contracts are enforced on the wire form.
type anonymityScanEnvelope struct {
	EnvMarkers *json.RawMessage `json:"env_markers"`

	// Spec 080 (schema v7) structural checks. RawMessage so each field is
	// validated individually with a precise violation pattern.
	WizardShown       *json.RawMessage `json:"wizard_shown"`
	WizardConnectStep *json.RawMessage `json:"wizard_connect_step"`
	WebUIOpened       *json.RawMessage `json:"web_ui_opened"`
	DaysSinceInstall  *json.RawMessage `json:"days_since_install"`
	ActiveDays30d     *json.RawMessage `json:"active_days_30d"`
	PreviousShutdown  *json.RawMessage `json:"previous_shutdown"`
	LastErrorCode     *json.RawMessage `json:"last_error_code"`

	// Schema v8 structural check: the security-scanner sub-object must be
	// counts-and-fixed-enum-keys only. Deliberately NOT a pointer: JSON null
	// sets a *RawMessage pointer to nil, which is indistinguishable from an
	// absent field — as a plain RawMessage, absent stays empty while null
	// arrives as the literal bytes "null" and fails the object-shape check.
	TPAScanner json.RawMessage `json:"tpa_scanner"`

	// Spec 095 structural check: the diagnostics counter sub-object, whose
	// error_code_counts_24h map must be cataloged codes → non-negative counts.
	// Same not-a-pointer reasoning as TPAScanner.
	Diagnostics json.RawMessage `json:"diagnostics"`

	// Issue #969 structural check: the preflight baseline counter sub-object,
	// whose availability_block_reasons_24h map must be closed-enum reason keys
	// → non-negative counts. Same not-a-pointer reasoning as TPAScanner.
	Preflight json.RawMessage `json:"preflight"`

	// Schema v9 structural check: the trust-tier histogram must be keyed
	// exclusively by the fixed auto|scan|manual enum with non-negative integer
	// counts — a producer-side regression that let a server name in as a map
	// key must not reach the wire. Same not-a-pointer reasoning as TPAScanner.
	TrustModeDistribution json.RawMessage `json:"trust_mode_distribution"`
}

// v7FieldViolation builds the violation for a Spec 080 field that broke its
// documented shape (boolean, non-negative integer, or fixed enum).
func v7FieldViolation(field, reason string) *AnonymityViolation {
	return &AnonymityViolation{
		Rule:    "v7_field_invalid",
		Pattern: field,
		Reason:  fmt.Sprintf("v7 field %s %s", field, reason),
	}
}

// scanV7Bool asserts raw (if present) is a JSON boolean.
func scanV7Bool(raw *json.RawMessage, field string) *AnonymityViolation {
	if raw == nil {
		return nil
	}
	var b bool
	if err := json.Unmarshal(*raw, &b); err != nil {
		return v7FieldViolation(field, "must be a boolean")
	}
	return nil
}

// scanV7NonNegativeInt asserts raw (if present) is a non-negative JSON
// integer — no fractions, no strings, no null.
func scanV7NonNegativeInt(raw *json.RawMessage, field string) *AnonymityViolation {
	return scanNonNegativeInt(raw, field, v7FieldViolation)
}

// scanNonNegativeInt is the shared non-negative-integer assertion. mkViolation
// tags the failure with the schema-version rule of the calling scan pass.
func scanNonNegativeInt(raw *json.RawMessage, field string, mkViolation func(field, reason string) *AnonymityViolation) *AnonymityViolation {
	if raw == nil {
		return nil
	}
	// json.Number accepts quoted number strings ("3"); require a bare JSON
	// number token so strings never masquerade as counters.
	trimmed := bytes.TrimSpace(*raw)
	if len(trimmed) == 0 || trimmed[0] == '"' {
		return mkViolation(field, "must be a number")
	}
	var n json.Number
	if err := json.Unmarshal(trimmed, &n); err != nil {
		return mkViolation(field, "must be a number")
	}
	i, err := n.Int64()
	if err != nil {
		return mkViolation(field, "must be a whole integer")
	}
	if i < 0 {
		return mkViolation(field, "must be non-negative")
	}
	return nil
}

// scanV7Enum asserts raw (if present) is a JSON string drawn from allowed.
func scanV7Enum(raw *json.RawMessage, field string, allowed ...string) *AnonymityViolation {
	if raw == nil {
		return nil
	}
	var s string
	if err := json.Unmarshal(*raw, &s); err != nil {
		return v7FieldViolation(field, "must be a string enum")
	}
	for _, a := range allowed {
		if s == a {
			return nil
		}
	}
	return v7FieldViolation(field, "carries a value outside its documented fixed enum")
}

// scanV7Fields runs the Spec 080 structural checks (FR-016): every v7 field
// present in the serialized payload must be a boolean, a non-negative
// integer, or a member of its documented fixed enum. last_error_code is the
// tightest gate — only diagnostics-catalog MCPX_* codes — so free text,
// messages, or paths can never ride that field even if a producer-side
// check regresses.
func scanV7Fields(env *anonymityScanEnvelope) *AnonymityViolation {
	if v := scanV7Bool(env.WizardShown, "wizard_shown"); v != nil {
		return v
	}
	if v := scanV7NonNegativeInt(env.WebUIOpened, "web_ui_opened"); v != nil {
		return v
	}
	if v := scanV7NonNegativeInt(env.DaysSinceInstall, "days_since_install"); v != nil {
		return v
	}
	if v := scanV7NonNegativeInt(env.ActiveDays30d, "active_days_30d"); v != nil {
		return v
	}
	// The widened Spec 080 connect-step enum ("" only appears in synthetic
	// payloads; omitempty drops it in production).
	if v := scanV7Enum(env.WizardConnectStep, "wizard_connect_step",
		"", "completed", "completed_external", "skipped"); v != nil {
		return v
	}
	// "unknown" is spec-allowed on the wire (FR-010) even though the current
	// producer omits it (PreviousShutdownUnknown == "").
	if v := scanV7Enum(env.PreviousShutdown, "previous_shutdown",
		PreviousShutdownClean, PreviousShutdownCrash, "unknown"); v != nil {
		return v
	}
	if env.LastErrorCode != nil {
		var code string
		if err := json.Unmarshal(*env.LastErrorCode, &code); err != nil {
			return v7FieldViolation("last_error_code", "must be a string enum")
		}
		// FR-012: same fixed code set as diagnostics.error_code_counts_24h —
		// the shape check alone would admit any MCPX_-looking string.
		if !isValidMCPXCode(code) {
			return v7FieldViolation("last_error_code", "must be a cataloged MCPX_* diagnostic code")
		}
	}
	return nil
}

// v8FieldViolation builds the violation for a schema-v8 field that broke its
// documented shape (whitelisted keys, non-negative counts, fixed severity
// enum).
func v8FieldViolation(field, reason string) *AnonymityViolation {
	return &AnonymityViolation{
		Rule:    "v8_field_invalid",
		Pattern: field,
		Reason:  fmt.Sprintf("v8 field %s %s", field, reason),
	}
}

// tpaScannerScalarKeys is the fixed set of non-negative-integer keys allowed
// in the tpa_scanner sub-object. The last two are the schema-v9 funnel
// counters; adding a key here is the deliberate act that widens the whitelist.
var tpaScannerScalarKeys = []string{
	"scans_completed", "scans_failed", "scans_with_findings",
	"tool_change_gate_scans", "prompt_scans",
}

// scanV8TPAScanner asserts the schema-v8 tpa_scanner sub-object (if present)
// carries counts and fixed enum keys ONLY: an object whose keys are
// whitelisted, whose scalars are non-negative integers, and whose findings map
// is keyed exclusively by the severity enum with non-negative integer values.
// This is the wire-form backstop for the producer-side filtering in
// CounterRegistry.RecordTPAScanCompleted — a regression there (e.g. a server
// name or rule id leaking in as a map key) is caught before transmit.
func scanV8TPAScanner(raw json.RawMessage) *AnonymityViolation {
	if len(raw) == 0 {
		return nil
	}
	var obj map[string]json.RawMessage
	// json.Unmarshal accepts `null` into a nil map, so nil-ness must be
	// rejected explicitly — the field, when present, is required to be a
	// real object.
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return v8FieldViolation("tpa_scanner", "must be an object")
	}

	allowed := make(map[string]struct{}, len(tpaScannerScalarKeys)+1)
	for _, k := range tpaScannerScalarKeys {
		allowed[k] = struct{}{}
	}
	allowed["findings"] = struct{}{}
	for k := range obj {
		if _, ok := allowed[k]; !ok {
			// The violation is logged on send failure — echoing the
			// rejected key there would itself be the leak this rule
			// exists to stop, so the pattern stays constant.
			return v8FieldViolation("tpa_scanner", "carries a key outside the whitelist")
		}
	}

	for _, k := range tpaScannerScalarKeys {
		v, ok := obj[k]
		if !ok {
			continue
		}
		msg := json.RawMessage(v)
		if viol := scanNonNegativeInt(&msg, "tpa_scanner."+k, v8FieldViolation); viol != nil {
			return viol
		}
	}

	rawFindings, ok := obj["findings"]
	if !ok {
		return nil
	}
	var findings map[string]json.RawMessage
	// Same nil-map guard as the parent object: `findings: null` is not an
	// object either.
	if err := json.Unmarshal(rawFindings, &findings); err != nil || findings == nil {
		return v8FieldViolation("tpa_scanner.findings", "must be an object")
	}
	for sev, v := range findings {
		if !IsTPASeverity(sev) {
			return v8FieldViolation("tpa_scanner.findings",
				"carries a key outside the fixed severity enum")
		}
		msg := json.RawMessage(v)
		if viol := scanNonNegativeInt(&msg, "tpa_scanner.findings."+sev, v8FieldViolation); viol != nil {
			return viol
		}
	}
	return nil
}

// trustModeFieldViolation builds the violation for a schema-v9
// trust_mode_distribution field that broke its documented shape (fixed enum
// keys, non-negative integer counts).
func trustModeFieldViolation(field, reason string) *AnonymityViolation {
	return &AnonymityViolation{
		Rule:    "trust_mode_field_invalid",
		Pattern: field,
		Reason:  fmt.Sprintf("trust mode field %s %s", field, reason),
	}
}

// scanTrustModeDistribution asserts the schema-v9 trust_mode_distribution
// sub-object (if present) is an object keyed EXCLUSIVELY by the fixed
// auto|scan|manual enum with non-negative integer counts. This is the wire-form
// backstop for buildTrustModeDistribution: the histogram is derived from
// per-server config, so a regression there is exactly the kind that would leak
// a server name as a map key.
func scanTrustModeDistribution(raw json.RawMessage) *AnonymityViolation {
	if len(raw) == 0 {
		return nil
	}
	var obj map[string]json.RawMessage
	// Same nil-map guard as tpa_scanner: `null` unmarshals into a nil map, and
	// the field — when present — is required to be a real object.
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return trustModeFieldViolation("trust_mode_distribution", "must be an object")
	}
	for key, v := range obj {
		if !IsTrustModeKey(key) {
			// The rejected key is deliberately NOT echoed into the violation —
			// it is the very thing this rule exists to keep out of the logs.
			return trustModeFieldViolation("trust_mode_distribution",
				"carries a key outside the fixed trust-tier enum")
		}
		msg := json.RawMessage(v)
		if viol := scanNonNegativeInt(&msg, "trust_mode_distribution."+key, trustModeFieldViolation); viol != nil {
			return viol
		}
	}
	return nil
}

// diagFieldViolation builds the violation for a diagnostics counter field that
// broke its documented shape (cataloged code keys, non-negative counts).
func diagFieldViolation(field, reason string) *AnonymityViolation {
	return &AnonymityViolation{
		Rule:    "diagnostics_field_invalid",
		Pattern: field,
		Reason:  fmt.Sprintf("diagnostics field %s %s", field, reason),
	}
}

// scanDiagnosticsCounters asserts the diagnostics sub-object (if present)
// carries an error_code_counts_24h map keyed EXCLUSIVELY by catalog-registered
// MCPX_ codes with non-negative integer values (spec 095 FR-014). Until now
// the scanner never inspected that map, and its producer-side guard
// (RecordErrorCode) filters on the MCPX_ prefix alone — so a regression that
// let a server name, URL, or an uncataloged code through would have reached
// the wire unchecked. Keys are where identifying strings would leak, so the
// violation deliberately never echoes the offending key.
func scanDiagnosticsCounters(raw json.RawMessage) *AnonymityViolation {
	if len(raw) == 0 {
		return nil
	}
	var obj map[string]json.RawMessage
	// json.Unmarshal accepts `null` into a nil map; the field, when present,
	// must be a real object.
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return diagFieldViolation("diagnostics", "must be an object")
	}

	rawCounts, ok := obj["error_code_counts_24h"]
	if !ok {
		return nil
	}
	var counts map[string]json.RawMessage
	if err := json.Unmarshal(rawCounts, &counts); err != nil || counts == nil {
		return diagFieldViolation("diagnostics.error_code_counts_24h", "must be an object")
	}
	for code, v := range counts {
		if !isValidMCPXCode(code) {
			return diagFieldViolation("diagnostics.error_code_counts_24h",
				"carries a key that is not a cataloged MCPX_* diagnostic code")
		}
		msg := json.RawMessage(v)
		if viol := scanNonNegativeInt(&msg, "diagnostics.error_code_counts_24h", diagFieldViolation); viol != nil {
			return viol
		}
	}
	return nil
}

// preflightFieldViolation builds the violation for a preflight counter field
// that broke its documented shape (closed-enum reason keys, non-negative
// counts).
func preflightFieldViolation(field, reason string) *AnonymityViolation {
	return &AnonymityViolation{
		Rule:    "preflight_field_invalid",
		Pattern: field,
		Reason:  fmt.Sprintf("preflight field %s %s", field, reason),
	}
}

// scanPreflightCounters asserts the preflight sub-object (if present) is a
// CLOSED object of non-negative integer counts whose
// availability_block_reasons_24h map is keyed EXCLUSIVELY by the closed
// availability-block reason enum (issue #969).
// The producer folds unknown reasons into "other" and MarshalJSON filters again;
// this is the wire-form backstop, so a regression that let a reason STRING
// (which embeds server and tool names) become a key is caught before transmit.
// Keys are where identifying strings would leak, so the violation deliberately
// never echoes the offending key.
func scanPreflightCounters(raw json.RawMessage) *AnonymityViolation {
	if len(raw) == 0 {
		return nil
	}
	var obj map[string]json.RawMessage
	// json.Unmarshal accepts `null` into a nil map; the field, when present,
	// must be a real object.
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return preflightFieldViolation("preflight", "must be an object")
	}

	// The sub-object is CLOSED: only the documented count keys plus the reason
	// map may appear. Validating the known scalars alone would let a future
	// field that carries free text (a server name, a query, an error message)
	// ride along unchecked — exactly the leak this rule exists to stop. A new
	// counter must be added to preflightAllowedKeys deliberately, which is the
	// point at which its shape gets reviewed.
	for key := range obj {
		if !isPreflightAllowedKey(key) {
			return preflightFieldViolation("preflight",
				"carries a key outside the fixed preflight counter set")
		}
	}

	// Every scalar the sub-object carries is a non-negative count.
	for _, key := range preflightScalarKeys {
		v, ok := obj[key]
		if !ok {
			continue
		}
		msg := json.RawMessage(v)
		if viol := scanNonNegativeInt(&msg, "preflight."+key, preflightFieldViolation); viol != nil {
			return viol
		}
	}

	rawCounts, ok := obj[preflightReasonsKey]
	if !ok {
		return nil
	}
	var counts map[string]json.RawMessage
	if err := json.Unmarshal(rawCounts, &counts); err != nil || counts == nil {
		return preflightFieldViolation("preflight.availability_block_reasons_24h", "must be an object")
	}
	for reason, v := range counts {
		if !IsAvailabilityBlockReason(reason) {
			return preflightFieldViolation("preflight.availability_block_reasons_24h",
				"carries a key outside the fixed availability-block reason enum")
		}
		msg := json.RawMessage(v)
		if viol := scanNonNegativeInt(&msg, "preflight.availability_block_reasons_24h", preflightFieldViolation); viol != nil {
			return viol
		}
	}
	return nil
}

// preflightScalarKeys is the fixed set of non-negative-integer keys allowed in
// the preflight sub-object.
var preflightScalarKeys = []string{
	"filter_diag_emitted_24h",
	"filter_diag_missing_annotation_24h",
	"filter_diag_explicit_24h",
	"filter_diag_followed_24h",
	"availability_block_24h",
	"discovery_omission_24h",
}

// preflightReasonsKey is the one non-scalar key the preflight sub-object may
// carry (a closed-enum map, validated separately).
const preflightReasonsKey = "availability_block_reasons_24h"

// preflightAllowedKeys is the CLOSED key set of the preflight sub-object:
// preflightScalarKeys plus the reason map. Anything else is a violation.
var preflightAllowedKeys = func() map[string]struct{} {
	m := make(map[string]struct{}, len(preflightScalarKeys)+1)
	for _, k := range preflightScalarKeys {
		m[k] = struct{}{}
	}
	m[preflightReasonsKey] = struct{}{}
	return m
}()

func isPreflightAllowedKey(key string) bool {
	_, ok := preflightAllowedKeys[key]
	return ok
}

// ScanForPII scans a serialized telemetry payload (v3+) for PII leaks and
// structural violations. Returns nil when the payload is clean; otherwise
// returns an *AnonymityViolation. The returned error satisfies
// errors.Is(err, ErrAnonymityViolation).
//
// Rules (first-match wins):
//  1. Any substring from AnonymityBlockedPrefixes appears in the payload.
//  2. Any substring from BlockedValues appears in the payload.
//  3. env_markers, if present, fails to unmarshal into a strict all-bool
//     struct — meaning a field widened to a string/number/null.
//  4. Any Spec 080 v7 field present breaks its documented shape: booleans
//     (wizard_shown), non-negative integers (web_ui_opened,
//     days_since_install, active_days_30d), or fixed enums
//     (wizard_connect_step, previous_shutdown, last_error_code = MCPX_*).
//  5. tpa_scanner (schema v8), if present, is not an object of whitelisted
//     keys holding non-negative integer counts, with a findings map keyed
//     exclusively by the fixed severity enum.
//  6. diagnostics.error_code_counts_24h, if present, is not a map of
//     catalog-registered MCPX_* codes to non-negative integer counts.
//  7. preflight (issue #969), if present, is not a CLOSED object of
//     non-negative integer counts (keys drawn from preflightAllowedKeys) whose
//     availability_block_reasons_24h map is keyed exclusively by the closed
//     availability-block reason enum.
//  8. trust_mode_distribution (schema v9), if present, is not an object keyed
//     exclusively by the fixed auto|scan|manual trust-tier enum with
//     non-negative integer counts.
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

	// Rule 4: Spec 080 v7 fields must keep their boolean / non-negative
	// integer / fixed-enum shapes.
	if v := scanV7Fields(&env); v != nil {
		return v
	}

	// Rule 5: schema-v8 tpa_scanner must be counts + fixed severity keys only.
	if v := scanV8TPAScanner(env.TPAScanner); v != nil {
		return v
	}

	// Rule 6: diagnostics counters must be cataloged codes → non-negative ints.
	if v := scanDiagnosticsCounters(env.Diagnostics); v != nil {
		return v
	}

	// Rule 7: preflight counters must be closed-enum reason keys → non-negative
	// ints, and every scalar a non-negative count.
	if v := scanPreflightCounters(env.Preflight); v != nil {
		return v
	}

	// Rule 8: trust_mode_distribution (schema v9) must be fixed-enum trust-tier
	// keys → non-negative integer counts.
	if v := scanTrustModeDistribution(env.TrustModeDistribution); v != nil {
		return v
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
