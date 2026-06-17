# Data Model: macOS TCC-safe Connect wizard & App-Data denial diagnostics

No persistent storage changes. These are in-memory/DTO shapes.

## AccessOutcome (new enum)

Classification of an attempt to access (read or write) a client config file.

| Value | Meaning | Derivation |
|-------|---------|------------|
| `accessible` | File read/written successfully | no error |
| `absent` | File does not exist → client "not installed" | `errors.Is(err, fs.ErrNotExist)` |
| `denied` | Blocked by OS permission (macOS TCC App Data) | `errors.Is(err, fs.ErrPermission)` |
| `malformed` | Read OK but contents unparseable | parse error after a successful read |

Rules:
- Classification MUST come from the error class, never from substring matching (FR-011).
- `accessible` is the only outcome that yields a trustworthy `Connected` value.

## ClientStatus (extended — additive only)

Existing fields preserved exactly (FR-006). New fields:

| Field | JSON | Type | Notes |
|-------|------|------|-------|
| *(existing)* `Exists` | `exists` | bool | Set via `os.Stat` (metadata) — unchanged |
| *(existing)* `Connected` | `connected` | bool | Now only authoritative when `AccessState == "accessible"`; remains `false` when `unknown` |
| **AccessState** | `access_state` | string | `""`/`unknown` (overall status, not content-checked), `accessible`, `absent`, `denied`, `malformed` |
| **Remediation** | `remediation,omitempty` | string | Present only when `AccessState == denied`; the macOS App-Data fix text |

State transitions for `AccessState`:
- Overall `GetAllStatus`: every installed client → `unknown` (no content read). Absent clients → `Exists=false` (`AccessState` may be `absent` or left empty).
- `GetStatus(id)` / connect / disconnect: performs the content access → resolves to `accessible | absent | denied | malformed`.

## AccessError (new typed error)

Returned by connect/disconnect when the underlying file access is `denied`.

| Field | Type | Notes |
|-------|------|-------|
| Client | string | client id/name |
| Path | string | config path attempted |
| Outcome | AccessOutcome | `denied` (could also wrap `malformed`) |
| Remediation | string | actionable fix text |

- `Error()` returns a human-readable message including cause + remediation.
- `errors.Is`/`errors.As` friendly so the REST layer can map it to the right field.

## DoctorCheckResult (uses existing diagnostics result shape)

| Field | Type | Notes |
|-------|------|-------|
| Status | pass/warn | warn when a persisted App-Data denial is detected |
| Summary | string | "macOS blocked mcpproxy from reading other apps' data" |
| Remediation | string | `tccutil reset SystemPolicyAppData com.smartmcpproxy.mcpproxy` + Settings path |
| Platform gate | — | darwin-only; no-op (not emitted) on other platforms |

## Remediation text (canonical)

```
macOS blocked mcpproxy from reading <client>'s configuration (Privacy & Security ▸ App Data).
Fix: System Settings ▸ Privacy & Security ▸ App Data ▸ enable mcpproxy,
or run: tccutil reset SystemPolicyAppData com.smartmcpproxy.mcpproxy
(dev builds: com.smartmcpproxy.mcpproxy.dev)
```
