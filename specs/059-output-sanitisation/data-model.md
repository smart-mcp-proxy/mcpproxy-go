# Phase 1 Data Model: Output Sanitisation Enforcement (Track B)

## OutputSanitisationConfig (config, `internal/config/config.go`)

Mirrors `OutputValidationConfig`. Wired into root `Config` as `OutputSanitisation *OutputSanitisationConfig json:"output_sanitisation,omitempty" mapstructure:"output-sanitisation"`, defaulted in `DefaultConfig()`.

| Field | JSON | Type | Default | Meaning |
|-------|------|------|---------|---------|
| SpotlightUntrusted | `spotlight_untrusted` | bool | `true` | FR-B1: wrap untrusted text in delimiters |
| ResponseAction | `response_action` | string | `"spotlight"` | `spotlight` (no mutation) \| `redact` \| `block` |
| StripControlChars | `strip_control_chars` | bool | `false` | FR-B4 master toggle for strip classes |
| StripClasses | `strip_classes` | []string | `["ansi","c0c1","bidi","zero_width"]` | which classes when strip enabled |
| MaxRedactions | `max_redactions` | int | `100` | cap on redactions per response |

**Helpers** (mirroring Track A):
- `IsEnabled() bool` — false only if the whole block is nil/zero-value-disabled; otherwise true (default config is "enabled" but non-mutating).
- `IsSpotlightEnabled() bool` — `SpotlightUntrusted` (default true).
- `IsRedact() bool` — `ResponseAction == "redact"`.
- `IsBlock() bool` — `ResponseAction == "block"`.
- `IsStripEnabled() bool` — `StripControlChars`.
- `EnabledStripClasses() map[string]bool` — normalised set from `StripClasses` when strip enabled.
- `WouldMutate(trust string) bool` — true if redact/block, or (untrusted && (strip || spotlight)). Used for the fast path.

`DefaultOutputSanitisationConfig()` returns the table's defaults → **non-mutating beyond spotlight wrap on untrusted** (FR-B6, FR-X1).

## SanitisationDecision (server, `internal/server/output_sanitisation.go`)

Pure verdict returned by `evaluateOutputSanitisation(...)`, consumed by the hook to emit a `policy_decision` activity record.

| Field | Type | Meaning |
|-------|------|---------|
| Action | string | `"none"` \| `"spotlight"` \| `"redact"` \| `"strip"` \| `"block"` (highest applied) |
| Blocked | bool | true → payload replaced with remediation error |
| RedactedCount | int | secrets masked |
| RedactedCategories | []string | distinct categories masked |
| StrippedClasses | []string | control classes neutralised |
| Spotlighted | bool | untrusted wrapper applied |
| Reason | string | human-readable summary for the audit record |

Emit rule: a `policy_decision` activity record is written when `Action != "none"` (decision = `Action`, or `"blocked"` when `Blocked`), reusing `emitActivityPolicyDecision(serverName, toolName, sessionID, decision, reason)`. Pure spotlight on untrusted is recorded as `decision="spotlight"` (informational) — but to avoid activity-log spam, spotlight-only is logged at debug and does NOT emit a policy_decision (only mutating/blocking actions do). Redact/strip/block always emit.

## Pure sanitiser functions (`internal/security/sanitizer.go`)

- `SpotlightUntrusted(text, server, tool string) string` — escape sentinels, wrap (FR-B1/B2).
- `StripControlSequences(text string, classes map[string]bool) (out string, stripped []string)` — per-class (FR-B4).
- `(*Detector) Redact(content string) (redacted string, dets []Detection)` — pattern-based masking → `[REDACTED:<category>]` (FR-B3).

## Trust-gated decision table

| trust | action | strip | result |
|-------|--------|-------|--------|
| trusted | spotlight | off | unchanged (byte-identical) |
| untrusted | spotlight | off | wrap only |
| any | redact | off | mask secrets (+wrap if untrusted) |
| any | block + critical detection | — | replace payload w/ remediation error, audit |
| untrusted | spotlight | on | strip classes + wrap |
