# Data Model — Native Connect Client Form (Spec 091, rev 2)

## Go (core) — additive

### ConnectPreview (internal/connect/preview.go)

Three new fields (contracts §1): `ExistingEntrySummary *EntrySummary` (only
when `EntryExists`), `PreconditionToken string` (always),
`ConnectRefusal string` (only when the write would refuse).

### EntrySummary (internal/connect/summary.go, new)

`EntryName, Type, Endpoint (query+userinfo stripped), Command, HeaderNames [],
EnvNames []` — built from parsed projections only; no arbitrary values.
Resolved via the same equivalent-entry adoption lookup the write performs.

### Token (internal/connect/token.go, new)

`DerivePreconditionToken(key []byte, configPath string, fileExists bool,
resolvedEntryName string, rawResolvedEntry json.RawMessage,
pendingEntry json.RawMessage) string` — canonical length-prefixed encoding →
HMAC-SHA256(per-instance in-memory random key) → hex. Deterministic per key;
distinct for: absent vs present file, resolved entry present vs absent, any
raw byte change of the resolved (possibly adopted) entry — including values
the summary hides — and any change in the pending entry (credential rotation,
auth toggle, address change).

### Connect request (internal/httpapi/connect.go)

Body gains `PreconditionToken string` (optional). Validation order: resolve
current raw state (same adoption lookup) + rebuild pending entry → if token
present and mismatch → **409 `{"action":"precondition_failed", reason}`**,
nothing written → else existing flow (backup when file exists, write). Legacy
`already_exists` 409 keeps its `action`. Absent token = legacy behavior.

## Swift — models and state machine

### ClientStatus (extended decode)

Adds: `icon`, `server_name`, `access_state`, `remediation` (all optional —
tolerate older cores). Unknown client ids decode generically (name fallback,
default icon).

### ConnectPreviewModel (new)

`configPath, entryText, entryExists, existingEntrySummary?, containsAPIKey,
accessState, preconditionToken, connectRefusal?`. Derived `changeKind`:
refusal present → `.refused(reason)` (no Connect control);
absent → `.create`; readable && !entryExists → `.add`; entryExists → `.replace`
(summary names the adopted entry when its key differs);
unreadable/denied → `.blockedByAccess` (no Connect control).
Derived safety-net statement: `.create` → "file will be created… Undo removes
it"; `.add`/`.replace` → "timestamped backup will be created alongside it".
`containsAPIKey` → credential notice.

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
  (selection, entryName) AND `changeKind` is not `.refused`/`.blockedByAccess`
  — SC-002's structural guarantee.
- Replace-classified connects send `force=true` + token; `.conflict` is set
  ONLY from a 409 with `action == "precondition_failed"` and triggers an
  automatic re-preview (a legacy `already_exists` 409 is a `.failed` — it
  cannot occur in this flow and must not loop).
- `undo` is set only from a `.succeeded` connect in this form instance.
- Off-socket transport (`APIClient.transportKind != .unixSocket`) ⇒
  Connect/Undo/Disconnect disabled with explanation; list/detail/preview
  unaffected. Mutating requests use strict-socket mode: mid-session socket
  loss fails the request rather than silently riding TCP.
- `coreUnreachable` polls every 2 s until loaded.
- Unsupported client rows are disabled with reason; unreadable/denied rows
  show the mapped label ("config unreadable" / "access not granted" +
  remediation); the Connect control is absent.

### ConnectResult

`success(backupIdentity?)` / failure(reason). Backup identity feeds `undo`.
