# Phase 1 Data Model: Deterministic Offline MCP Tool-Scanner v2

Detection is in-memory and stateless; the only persisted state is the existing tool-approval/quarantine store (unchanged). These are the core in-process types.

## Tier

Enum: `TierHard`, `TierSoft`.
- `TierHard` → contributes to auto-quarantine; near-zero FP by construction.
- `TierSoft` → review-raise only; never auto-quarantines alone.

## ThreatType

Reuses the existing report vocabulary: `tool_poisoning`, `prompt_injection`, `rug_pull`, `exfiltration`, `malicious_code`, `uncategorized`. (Maps onto `ScanFinding.ThreatType`.)

## ToolView (input)

Read-only projection of one tool, supplied to checks:
- `Server string` — owning server name
- `Name string` — tool name
- `Description string` — raw description (un-normalized)
- `InputSchema json.RawMessage`
- `OutputSchema json.RawMessage`
- `NormalizedText string` — precomputed normalized description+schema-text (lazily, once per tool)

**Validation**: empty description/schema is valid input → yields zero signals (no error).

## RegistryView (input, cross-tool context)

Read-only snapshot of all servers' current tools, enabling cross-server checks:
- `Tools []ToolView`
- `ToolsByName map[string][]ToolView` — for collision detection
- `ToolNames map[string]struct{}` — fast membership for "description references another tool"
- Built once per scan, passed to every check.

## Signal (check output)

Emitted by a `Check.Inspect`:
- `CheckID string` — stable identifier, e.g. `"unicode.hidden"`, `"shadowing.cross_server"`
- `Tier Tier`
- `ThreatType string`
- `Confidence float64` — 0.0–1.0; for soft signals this is pre-discount-then-discounted by the position classifier
- `Evidence string` — render-safe (truncated, control-char/zero-width escaped); for `payload.decoded` this is the *decoded* content
- `Detail string` — short human explanation

**Validation**: `Confidence` clamped to [0,1]; `Evidence` length-capped; CheckID must be from the registered check set.

## Check (interface)

```go
type Check interface {
    ID() string
    Inspect(tool ToolView, reg RegistryView) []Signal
}
```
- Pure and total: no I/O, no panics escape (engine wraps each `Inspect` in `recover()`).
- Deterministic: identical inputs → identical output ordering.

## Finding (aggregation output → existing ScanFinding)

Per-tool aggregation of signals:
- Any `TierHard` signal → finding `ThreatLevel=dangerous`, action = quarantine; severity from the hard signal (critical for escalated unicode/decoded, high otherwise).
- Else soft signals → severity by **count of distinct soft CheckIDs**: 1→`low`, 2→`medium`, 3+→`high`; `ThreatLevel=warning`, action = review.
- Confidence = combined (independent signals add, capped at 1.0) — agreement raises it.

**New fields added to `internal/security/scanner/types.go::ScanFinding`**:
- `Confidence float64`
- `Signals []string` — contributing CheckIDs

## Risk score (modified aggregation rule)

- Stop deduplicating by `(rule_id+location)` in a way that hides agreement. Independent signals on a tool **add** to the score (still bounded 0–100). Hard findings dominate; soft findings accumulate by distinct-signal count.

## Coverage / degradation record

Per-scan:
- `ChecksRun int`, `ChecksFailed int`, `FailedCheckIDs []string`
- A failed check (recovered panic/error) increments `ChecksFailed`; the report surfaces "degraded confidence" exactly as today's `scanners_failed` path does. The scan never aborts.

## Labeled corpus entry (eval data, existing shape extended)

In `specs/065-evaluation-foundation/datasets/`:
- `id string`, `description string` (and optionally `name`, `input_schema`, `server` for new checks)
- `label` ∈ {`malicious`, `benign`}
- `category` ∈ {`tool_poisoning`, `prompt_injection`, `shadowing`, `rug_pull`, `unicode_smuggling`, `decoded_payload`, `capability_mismatch`, `benign`, `hard_negative`}
- New entries add the `unicode_smuggling`, `decoded_payload`, `capability_mismatch` categories and more `hard_negative`s.

## State transitions

No new state machine. Hard findings feed the **existing** quarantine state machine (`pending`/`approved`/`changed`) via the existing quarantine integration — a hard finding marks the tool for quarantine through the current path; soft findings are report-only and do not transition approval state.
