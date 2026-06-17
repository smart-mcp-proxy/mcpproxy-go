# Research: macOS TCC-safe Connect wizard & App-Data denial diagnostics

## R1 — What triggers the "wants to access data from other apps" dialog

- **Decision**: Treat the prompt as macOS TCC **App Data** (`kTCCServiceSystemPolicyAppData`, Sonoma 14+). It gates **content reads** (`open`+`read` / `file-read-data`), **not** metadata (`stat`/`lstat`/`access`).
- **Rationale**: Confirmed by multiple sources (eclecticlight.co container articles; mjtsai "TCC doesn't prevent protected folders from being listed" — *"denies file-read-data … does not block anything else such as metadata, which is what lets us stat these files"*). Therefore `os.Stat` never prompts; `os.ReadFile`/`os.Open` of another app's data does.
- **Alternatives considered**: Full Disk Access (heavy, user-granted, rejected as default), new entitlement (none exists for reading another app's data; rejected), App Group entitlement (only for legitimate shared group containers; N/A).

## R2 — Which mcpproxy code actually triggers it

- **Decision**: The Connect wizard is the sole trigger. `Service.GetAllStatus()` (`internal/connect/connect.go:135`) calls `findEntry` → `os.ReadFile` for **every** installed+supported client to compute `Connected`, on every status request. The Docker probe is metadata-only (`os.Stat`) and is NOT a trigger.
- **Rationale**: Code audit found `os.ReadFile`/`os.Open` on other apps' configs only in `internal/connect` (connect.go:294/444/534/559/644/729, backup.go:25). Docker resolution (`shellwrap.probeWellKnownDocker`, `secureenv.discoverUnixPaths`) is `os.Stat` only.
- **Alternatives considered**: login-shell hydration sourcing rc files (indirect, low likelihood); ruled out as the primary cause.

## R3 — Detecting a permission denial robustly in Go

- **Decision**: Classify via `errors.Is(err, fs.ErrPermission)` (covers `EPERM` and `EACCES`, which both map to `fs.ErrPermission` in the stdlib). Optionally tighten to `errors.Is(err, syscall.EPERM)` for the TCC-specific case, but `fs.ErrPermission` is the portable, idiomatic gate.
- **Rationale**: Constitution + FR-011 forbid string-matching error text. `os.Open`/`os.ReadFile` return `*fs.PathError` wrapping the syscall errno; `errors.Is(err, fs.ErrPermission)` is the documented way to test it. TCC content denial surfaces as `EPERM` ("operation not permitted").
- **Alternatives considered**: string match on "operation not permitted" (rejected — brittle, FR-011); shelling out to read TCC.db (SIP-protected, rejected).

## R4 — `Connected` tri-state without breaking the REST contract

- **Decision**: Keep `Connected bool` (defaults false) for backward compatibility and ADD an `AccessState string` enum field (`""`=unknown/not-checked, `accessible`, `absent`, `denied`, `malformed`) plus `Remediation string`. Overall `GetAllStatus` sets `Exists` (stat) and leaves `AccessState` empty/`unknown` (no content read). Per-client `GetStatus(id)` fills `Connected` + `AccessState` via the on-demand read.
- **Rationale**: FR-006 forbids removing/repurposing existing fields. Additive enum is safe; old clients ignore unknown fields. `Connected=false` with `AccessState=unknown` reads as "not yet checked," not "disconnected."
- **Alternatives considered**: changing `Connected` to a string (breaking, rejected); a separate endpoint only (still need the field to carry denied state).

## R5 — Where the doctor check lives and how it detects a persisted denial

- **Decision**: Add a macOS-only diagnostics check (build-tagged `_darwin.go` + `_other.go` no-op) in the existing diagnostics registry. Detection is **best-effort**: attempt a representative content read of a known-present client config that exists (via stat) but, if a read returns `fs.ErrPermission`, report a warning with the remediation command. Never warn when reads succeed or when no client config exists.
- **Rationale**: There is no SIP-free API to read the TCC database; inferring from an actual `EPERM` on a real protected path is the only reliable, false-positive-free signal. Build tags keep it a true no-op off darwin (FR-008).
- **Alternatives considered**: parsing `~/Library/Application Support/com.apple.TCC/TCC.db` (SIP-protected, would itself need FDA; rejected); calling `tccutil` (no read/query verb; rejected).

## R6 — Remediation message content & bundle identifiers

- **Decision**: Message = cause + two remediations: (a) System Settings → Privacy & Security → App Data → enable mcpproxy; (b) `tccutil reset SystemPolicyAppData <bundle-id>`. Bundle IDs: release `com.smartmcpproxy.mcpproxy`, dev `com.smartmcpproxy.mcpproxy.dev` (from `native/macos/MCPProxy/MCPProxy/Info.plist`). Emit the release id by default and mention the dev suffix.
- **Rationale**: `tccutil reset <service> [bundle-id]` is the documented reset; `SystemPolicyAppData` is the service token (kTCCService prefix dropped).
- **Alternatives considered**: omitting bundle id (resets all apps — broader than needed; keep as fallback note).

## R7 — Testing the denied path without a real OS denial

- **Decision**: Introduce a seam: a package-level `readConfigFile func(string) ([]byte, error)` (default `os.ReadFile`) or a small `configReader` the Service holds, so tests inject a func returning `&fs.PathError{Op:"open", Path:p, Err: syscall.EPERM}`. Classification + surfacing assert on the injected error.
- **Rationale**: FR-012 requires the denied path be testable without a real denial; matches the existing `homeDir` override and `wellKnownDockerPathsFn` seam patterns in the codebase.
- **Alternatives considered**: chmod 000 a temp file (root can still read; flaky in CI; rejected in favor of the injected error).
