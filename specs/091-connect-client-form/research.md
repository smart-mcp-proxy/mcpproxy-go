# Research — Native Connect Client Form (Spec 091, rev 2)

Rev 1 was reviewed by an internal adversarial 3-lens panel (Codex quota-locked,
Gemini tier sunset); its P1/P2 findings reshaped D1/D2/D6/D8 and added D9/D10.

## D1. Precondition token: keyed HMAC over resolved state + pending entry

- **Decision**: `internal/connect/token.go` computes HMAC-SHA256 with a
  per-core-instance random in-memory key over a canonical, **length-prefixed**
  encoding of `(clientID, configPath, requestedServerName, fileExists,
  resolvedEntryName, rawResolvedEntry, pendingEntrySerialized)`. "Resolved"
  means: the same equivalent-entry adoption lookup the write performs
  (`findEquivalentJSONServerName` for OpenCode-style adoption) picks the entry
  the write would actually replace — under whatever key it lives; that lookup
  is deterministic (exact name first, then sorted order) and resolved ONCE per
  operation, so the token and the write cover the same entry. The requested
  name and client are bound too: `resolvedEntryName` is empty for every absent
  target, so without them a token minted for one entry name validated a write
  under any other, creating a key the user never previewed. The pending entry (unmasked, as it would be
  written) is included so proxy-side drift (API-key rotation,
  `require_mcp_auth` toggle, listen-address change) also invalidates the token.
  Validation recomputes at write time; mismatch → discriminated 409, no write.
  Absent token → legacy behavior.
- **Rationale (panel findings closed)**: plain sha256 was an offline
  confirmation oracle for weak user secrets (preimage fully known but the
  masked value); pipe-joined fields were ambiguous; the unresolved
  `serversMap[serverName]` read missed the adopted-entry drift class; file-only
  state missed pending-entry drift (credential embedded without the FR-004
  notice ever shown).
- **Alternatives**: mtime/size (misses same-size edits); server-side revision
  store (state + expiry for no benefit); unkeyed hash (oracle).

## D2. Existing-entry disclosure: structural summary by construction (NOT masking)

- **Decision**: there is **no reusable masking routine** — the pending entry is
  masked at *construction* (`entryParams(true)` substitutes the mask constant),
  which cannot sanitize arbitrary user-authored content. So the preview never
  renders existing-entry content at all. Instead `internal/connect/summary.go`
  (new) builds `existing_entry_summary` from parsed non-secret projections
  only: resolved entry name, type, endpoint with query **and userinfo**
  stripped, command, header NAMES, env NAMES. Values other than the stripped
  endpoint/command never leave the core. Tests feed rotated keys, bearer
  headers, env secrets, `?apikey=` and `user:pass@` URLs and assert none
  appear in the serialized response.
- **Rationale**: whitelist-by-construction is provable; masking heuristics
  over arbitrary content are not. The user's real need in the stale-entry case
  is "which endpoint/name is being replaced", which the summary answers.

## D3. Aggregate list stays stat-only; detail resolves on selection

- Unchanged from rev 1: list binds to `GET /api/v1/connect` (config-presence
  only, no content reads, no TCC prompts); selection fires
  `GET /api/v1/connect/{client}` + `/preview`. One user-initiated content read
  per selection.

## D4. Swift shape: extracted @MainActor view model, thin SwiftUI view

- Unchanged from rev 1: `ConnectClientModel` reducer owns list/detail/preview/
  action/undo state; the Connect control is derived (exists only with a
  resolved, input-matching, refusal-free preview) — SC-002's structural
  guarantee; `AddServerView`'s inline-state pattern deliberately not repeated.

## D5. Menu + window plumbing

- Unchanged from rev 1: "Connect Client…" beside "Add Server…" in
  `MCPProxyApp.swift`; direct sheet presentation (no asyncAfter chains); the
  legacy preview-less dashboard connect sheet (`DashboardView.swift` ~1275)
  routes into the same presentation path (FR-012).

## D6. Off-socket behavior: explicit transport seam + strict-socket writes

- **Decision**: `APIClient` gains an explicit transport identity
  (`transportKind: .unixSocket | .tcp`) derived from its configured endpoint,
  AND the three mutating calls (connect/undo/disconnect) send in a
  strict-socket mode that FAILS (surfaced as an actionable error) instead of
  falling back to TCP when the socket disappears mid-session —
  `SocketTransport`'s per-request fallback ("otherwise let it go out over
  TCP") is exactly the hole the panel found. The model disables mutating
  controls when `transportKind != .unixSocket`.
- **Rationale**: rev 1 assumed a transport-identity API that does not exist;
  and without strict mode, a mid-session socket loss silently sent admin
  writes over TCP, violating the spec's non-socket edge case.

## D7. Reachability polling

- Unchanged: 2 s cancellable poll while unreachable (FR-013).

## D8. Non-create-capable absent case: refusal comes from the PREVIEW

- **Decision**: the preview handler runs the same guard the write uses
  (`connect.go`'s absent-config refusal) and populates `connect_refusal`
  (verbatim reason). The model maps a refusal-bearing preview to a
  non-connectable state: Connect control absent, refusal shown. No client-side
  capability table; capability knowledge stays core-side.
- **Rationale (panel P1 closed)**: rev 1's click-to-discover contradicted the
  spec's "non-connectable" requirement and rendered a false
  "file will be created… Undo removes it" statement for OpenCode; manual check
  9d ("Connect unavailable") was unsatisfiable without this third field.

## D9. Conflict discrimination and force semantics (new)

- **Decision**: the write's 409 body carries `action`:
  `"precondition_failed"` (token drift → model auto-re-previews) vs
  `"already_exists"` (legacy). Replace-classified flows send `force=true`
  together with the token; the token is the overwrite safety. `force=true` +
  stale token still refuses.
- **Rationale (panel P1 closed)**: without the discriminator and the force
  rule, a replace flow either looped forever on the legacy 409 (force=false)
  or could not distinguish "re-preview" from "retry with force".

## D10. SC-006 test home (new)

- **Decision**: `internal/server/connect_socket_e2e_test.go` (new) starts the
  server on a real Unix socket and drives `POST /api/v1/connect/{client}`
  end-to-end, asserting the socket caller reaches the gated write route with
  admin context (and that an agent-token TCP caller is still rejected).
  Quickstart's test commands gain `./internal/server -run ConnectSocket`.
- **Rationale (panel P1 closed)**: rev 1 planned no home for SC-006; httptest
  cannot prove the listener→tagging→auth-conversion→gate path.
