# Data Model — Native Connect Client Form (Spec 091)

## Go (core) — additive

### ConnectPreview (internal/connect/preview.go)

Two new fields (contracts §1): `ExistingEntryText string` (masked, only when
`EntryExists`), `PreconditionToken string` (always).

### Token (internal/connect/token.go, new)

`DerivePreconditionToken(configPath string, fileExists bool, entryName string,
rawEntry json.RawMessage) string` — canonical serialization → sha256 → hex.
Deterministic; distinct for: absent vs empty file, entry present vs absent,
any raw-entry byte change (incl. values masking hides).

### Connect request (internal/httpapi/connect.go)

Body gains `PreconditionToken string` (optional). Validation order: resolve
current raw state → if token present and mismatch → 409 + reason, nothing
written → else existing flow (backup when file exists, write).

## Swift — models and state machine

### ClientStatus (extended decode)

Adds: `icon`, `server_name`, `access_state`, `remediation` (all optional —
tolerate older cores). Unknown client ids decode generically (name fallback,
default icon).

### ConnectPreviewModel (new)

`configPath, entryText, entryExists, existingEntryText?, containsAPIKey,
accessState, preconditionToken`. Derived `changeKind`:
absent → `.create`; readable && !entryExists → `.add`; entryExists → `.replace`;
unreadable/denied → `.blockedByAccess` (no Connect control).
Derived safety-net statement: `.create` → "file will be created… Undo removes
it"; else "timestamped backup will be created alongside it". `containsAPIKey`
→ credential notice.

### ConnectClientModel (new, @MainActor)

States:

| Slice | Values |
|-------|--------|
| `list` | `.loading` / `.loaded([ClientRow])` / `.coreUnreachable(polling)` |
| `selection` | `nil` / clientID |
| `detail` | `.loading` / `.resolved(ClientDetail)` |
| `preview` | `.idle` / `.loading` / `.resolved(ConnectPreviewModel)` / `.failed(reason)` |
| `action` | `.idle` / `.inFlight` / `.failed(reason)` / `.conflict(reason)` / `.succeeded(ConnectResult)` |
| `undo` | `nil` / `.available(backupIdentity, clientID)` — cleared on use or form close |
| `entryName` | default "mcpproxy"; editing resets `preview` to `.idle` and refetches |

Invariants (unit-tested):

- The Connect control exists iff `preview == .resolved` for the CURRENT
  (selection, entryName) — SC-002's structural guarantee.
- `action == .conflict` triggers an automatic re-preview.
- `undo` is set only from a `.succeeded` connect in this form instance.
- Off-socket transport ⇒ Connect/Undo/Disconnect disabled with explanation;
  list/detail/preview unaffected.
- `coreUnreachable` polls every 2 s until loaded.
- Unsupported client rows are disabled with reason; unreadable/denied rows
  disable Connect with the mapped label ("config unreadable" / "access not
  granted" + remediation).

### ConnectResult

`success(backupIdentity?)` / failure(reason). Backup identity feeds `undo`.
