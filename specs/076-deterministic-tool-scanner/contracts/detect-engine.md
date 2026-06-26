# Contract: `internal/security/detect` engine

This feature has no new external HTTP/MCP contract — existing scan entry points (CLI `security scan`, REST `/api/v1/servers/{name}/scan`, MCP `quarantine_security`) keep their request/response shapes. The only externally-visible change is **additive** fields on each finding (`confidence`, `signals`). The meaningful contracts here are the internal Go interfaces, which gate each check's testability.

## Engine API

```go
package detect

// Engine runs all registered checks over a registry snapshot and aggregates
// per-tool signals into findings. Pure aside from the recover() isolation.
type Engine struct { /* registered checks, thresholds */ }

func NewEngine(opts Options) *Engine

// Scan inspects every tool in the snapshot. A check that panics or errors is
// isolated (recovered), counted in Coverage, and never aborts the scan.
func (e *Engine) Scan(reg RegistryView) Result

type Result struct {
    Findings []Finding // self-contained; converts 1:1 to scanner.ScanFinding
    Coverage Coverage
}

type Coverage struct {
    ChecksRun     int
    ChecksFailed  int
    FailedCheckIDs []string
}
```

> **Import-cycle note (T1 design decision).** `Result.Findings` is `[]detect.Finding`,
> not `[]scanner.ScanFinding`. The scanner wiring (T012) imports `detect` to delegate
> `tpa-descriptions`, so `detect` must NOT back-import `scanner` (that would form a
> cycle). `detect` is therefore self-contained: `Finding` carries the same fields and
> the same vocabulary string values (severity / threat level / threat type) as
> `scanner.ScanFinding`, and the scanner layer converts `detect.Finding` →
> `scanner.ScanFinding` (whose additive `Confidence` + `Signals` fields already exist).
> The no-back-import invariant is enforced by the offline import-guard test.

### Guarantees (contract tests)

1. **Determinism**: `Scan(reg)` returns byte-identical findings (including order) for identical `reg`.
2. **Totality**: a check that panics is recovered; `Scan` still returns; `Coverage.ChecksFailed` reflects it.
3. **Offline**: the package imports no `net`, `os` (filesystem), `os/exec`, or HTTP/Docker client — enforced by an import-guard test.
4. **Tier semantics**: a finding containing any hard signal has `ThreatLevel=dangerous`; a soft-only finding has `ThreatLevel=warning` and severity = distinct soft-signal count mapping.
5. **Consensus**: two independent signals on one tool yield higher combined confidence (and risk contribution) than either alone.

## Check API

```go
type Check interface {
    ID() string                              // stable, e.g. "unicode.hidden"
    Inspect(tool ToolView, reg RegistryView) []Signal
}
```

### Per-check contract tests (each check ships with these)

| Check | MUST flag | MUST NOT flag |
|-------|-----------|---------------|
| `unicode.hidden` | zero-width / bidi / tag-block / PUA in raw text; escalate ≥3 classes or decoded tag-message | ASCII-only descriptions; ordinary accented Unicode (é, ü) |
| `shadowing.cross_server` | description naming another server's tool; same tool name across two servers | a tool referencing *its own* name; common verbs |
| `payload.decoded` | base64/hex that **decodes** to `curl … \| sh`, `chmod`, `rm -rf`, raw IP:port | base64 that decodes to benign data (icon, JSON); short tokens |
| `directive.imperative` | `<IMPORTANT>`, "before using this tool", "do not tell the user", "ignore previous instructions", variants | the same phrase in example-position ("detects prompts such as 'ignore previous instructions'") |
| `capability.mismatch` | a math/string tool that reads `~/.ssh`/has an unexplained data-sink param ("sidenote") | a file tool that legitimately reads files; a network tool that fetches |
| `secret.embedded` | a live API key / Luhn-valid card in the description (high confidence) | a documented placeholder / `AKIA…EXAMPLE` (low/none) |

## Eval gate contract (`cmd/scan-eval`)

```
scan-eval --corpus <dir> --gate --min-recall 0.90 --max-fp 0.05
```
- Exit `0` iff recall(malicious) ≥ 0.90 AND FP(hard-negative) ≤ 0.05.
- Exit non-zero with a per-metric breakdown otherwise.
- Emits the metrics JSON (recall, precision, FP rate, F1, per-category) for the CI log.
