# Phase 1 Data Model — Diagnostics & Error Taxonomy

**Date**: 2026-04-24

## Types

### `Severity`

```go
type Severity string

const (
    SeverityInfo  Severity = "info"
    SeverityWarn  Severity = "warn"
    SeverityError Severity = "error"
)
```

Used to drive tray badge colour and UI styling. Stable enum — no additions without a spec update.

### `FixStepType`

```go
type FixStepType string

const (
    FixStepLink    FixStepType = "link"    // external URL; renders as clickable link
    FixStepCommand FixStepType = "command" // shell command; renders as copyable block
    FixStepButton  FixStepType = "button"  // triggers a fixer via the fix endpoint
)
```

### `FixStep`

```go
type FixStep struct {
    Type        FixStepType `json:"type"`
    Label       string      `json:"label"`         // human label shown in UI
    Command     string      `json:"command,omitempty"`
    URL         string      `json:"url,omitempty"`
    FixerKey    string      `json:"fixer_key,omitempty"`    // matches fixers registry key; only for Button
    Destructive bool        `json:"destructive,omitempty"`  // when true, UI/CLI default to dry-run
}
```

**Validation**:
- If `Type == FixStepLink`, `URL` MUST be non-empty.
- If `Type == FixStepCommand`, `Command` MUST be non-empty.
- If `Type == FixStepButton`, `FixerKey` MUST be non-empty and MUST correspond to a registered fixer.

### `CatalogEntry`

```go
type CatalogEntry struct {
    Code         Code      `json:"code"`
    Severity     Severity  `json:"severity"`
    UserMessage  string    `json:"user_message"`
    FixSteps     []FixStep `json:"fix_steps"`
    DocsURL      string    `json:"docs_url"`
    Deprecated   bool      `json:"deprecated,omitempty"`
    ReplacedBy   Code      `json:"replaced_by,omitempty"`
}
```

**Validation** (enforced by `catalog_test.go`):
- `Code` matches regex `^MCPX_(OAUTH|STDIO|HTTP|DOCKER|CONFIG|QUARANTINE|NETWORK|UNKNOWN)_[A-Z0-9_]+$`.
- `UserMessage` non-empty.
- `len(FixSteps) >= 1`.
- `DocsURL` matches `^docs/errors/<Code>\\.md$` (relative) OR an absolute https URL.
- If `Deprecated`, `ReplacedBy` MUST point to another registered non-deprecated code.

### `Code`

```go
type Code string
```

An alias for a stable, UPPER_SNAKE_CASE code string. Immutable once shipped (FR-004).

### `DiagnosticError`

Runtime per-server record carried on the stateview snapshot.

```go
type DiagnosticError struct {
    Code       Code      `json:"code"`
    Severity   Severity  `json:"severity"`
    Cause      string    `json:"cause,omitempty"`    // truncated raw error message for debug
    CauseType  string    `json:"cause_type,omitempty"`// Go type name of wrapped error (optional)
    ServerID   string    `json:"server_id"`
    DetectedAt time.Time `json:"detected_at"`
}
```

**Validation**:
- `Code` MUST match a registered CatalogEntry.
- `Cause` truncated to 256 chars to avoid log pollution and response bloat.
- `DetectedAt` monotonic within a snapshot — never rewritten if the same code persists.

### `FixAttempt` (activity log row)

Written to the existing ActivityBucket on every dry-run or execute attempt.

```go
type FixAttempt struct {
    ID          string        `json:"id"`              // uuid
    ServerID    string        `json:"server_id"`
    Code        Code          `json:"code"`
    Mode        string        `json:"mode"`            // "dry_run" | "execute"
    Outcome     string        `json:"outcome"`         // "success" | "failed" | "blocked"
    FailureMsg  string        `json:"failure_msg,omitempty"`
    RequestedBy string        `json:"requested_by,omitempty"` // user id (server edition) or "local"
    DurationMS  int64         `json:"duration_ms"`
    StartedAt   time.Time     `json:"started_at"`
    Preview     string        `json:"preview,omitempty"` // human preview for dry-run
}
```

**Validation**:
- `Mode` ∈ {"dry_run", "execute"}.
- `Outcome` ∈ {"success", "failed", "blocked"}.
- `blocked` means rate-limited or destructive-without-confirm.

## Registry

```go
// In internal/diagnostics/registry.go
var registry = map[Code]CatalogEntry{}

func init() {
    // Populated from codes.go constants; validated by catalog_test.go at test time.
}

func Get(c Code) (CatalogEntry, bool) { /* ... */ }
func All() []CatalogEntry              { /* stable-sorted snapshot */ }
```

## Fixers

```go
// In internal/diagnostics/fixers.go
type FixerFunc func(ctx context.Context, req FixRequest) (FixResult, error)

type FixRequest struct {
    ServerID string
    Mode     string   // "dry_run" or "execute"
}

type FixResult struct {
    Outcome    string // success|failed|blocked
    Preview    string // set for dry_run
    FailureMsg string
}

var fixers = map[string]FixerFunc{} // keyed by FixStep.FixerKey
```

**Rules**:
- A fixer's `Destructive` FixStep MUST respect `req.Mode` and MUST NOT mutate state when `mode == "dry_run"`.
- A non-destructive fixer may ignore `mode` (read-only probe).
- Fixers write their audit row via `activity_service.RecordFixAttempt(...)` regardless of mode.

## Relationships

```
Code ──1:1── CatalogEntry ──1:N── FixStep
                                     │
                                     ├─ Button ──→ FixerKey ──→ FixerFunc
                                     ├─ Link    ──→ URL
                                     └─ Command ──→ Command (copy-to-clipboard)

Server stateview snapshot ──0:1── DiagnosticError ──→ Code

Activity log ──1:N── FixAttempt ──→ (Code, ServerID)
```

## State transitions

A server's `DiagnosticError` moves through:

```
[no diagnostic] ── connection fails ──→ DiagnosticError{code, severity:error}
DiagnosticError ── server becomes healthy ──→ [no diagnostic]
DiagnosticError{code=A} ── different failure occurs ──→ DiagnosticError{code=B}
```

- A diagnostic is cleared atomically with the snapshot when the underlying cause resolves.
- If a server oscillates, we keep only the most-recent diagnostic (no history on snapshot; history lives in activity log).
