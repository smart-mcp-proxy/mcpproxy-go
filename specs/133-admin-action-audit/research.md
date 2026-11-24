# Research and decisions — Spec 133 `admin_action`

Each decision names the alternative it beat. A1–A6 in spec.md are the assumptions the reporter is invited to push back on (issue #1698).

## D1 — Additive within `schema_version: 1` (spec A1)

**Decision**: new `event` value `admin_action`, new keys, new `caller.kind` values `none` and `cli_offline`, all under `schema_version: 1` and the same schema `$id`.

**Why**: `docs/features/audit-log.md` §Versioning already says a bump happens "only for a removed or renamed key, or a narrowed vocabulary" and that "adding a key or an enum value is a minor change". Nothing is removed or renamed.

**Alternative — `schema_version: 2`**: every existing line would change version, and every shipper filter in the field (`select(.schema_version == 1)`, the doc's own recipe) would silently stop matching the three existing kinds. That is a larger break than the one it avoids.

**The cost, stated**: `event` and `caller.kind` are closed enums in the published schema (`docs/schemas/audit-line-v1.schema.json`, `properties.event.enum`, `properties.caller.properties.kind.enum`). A consumer validating against a copy taken before this change rejects `admin_action` lines. The doc's claim that "a strict consumer holding the v1 document keeps validating after a minor additive change" is true for new keys only (`additionalProperties: true`), not new enum values; FR-003 corrects the sentence and tells shippers to switch on `event` and ignore unknown values.

## D2 — Audit `caller`, #1842 vocabulary mapped onto it (spec A2)

**Decision**: the line's identity is the common `caller` object, built by the same derivation as `authz` lines (`auditCallerFromContext`, `internal/server/audit_funnel.go:231`). The #1842 activity keys map onto it; the caller-asserted surface becomes `client.surface`.

**Why**: a shipper indexes `caller.kind` across the file. Writing `actor_kind` on one event kind would split that index. The #1842 values were chosen to be liftable, and they are — but two of them are not trustworthy enough for a trusted key: `surface` comes from the `X-MCPProxy-Surface` header on REST (`internal/httpapi/surface.go`), and the audit doc's rule is that a header-derived value never feeds `source` or any trusted key (`audit-log.md` §Common keys, `source`). It sits under `client`, which is already "caller-asserted, untrusted".

**Alternative — copy the #1842 keys verbatim** (`actor_kind`, `actor_name`, `surface`): one vocabulary across activity and audit, but two caller vocabularies inside the audit file, and a header-derived trusted key.

**Mapping** (activity `actor_kind` → audit `caller.kind`): `api_key` → `api_key`; `socket` → `socket`; `agent_token` → `agent_token` (+ `token_name`, `token_prefix`, `profile_pin`); an `mcp_oat_` browser login → `agent_token` (it is `AuthTypeAgent`, `internal/auth/agent_token.go:394-420`, and is already recorded that way on `authz` lines); `cookie` / `bearer_jwt` administrator → `session_admin` (+ `user_id`, `user_email`, `role`, `provider`); `anonymous` → `anonymous`; `cli_offline` → `cli_offline` (new); no actor → `none` or `internal` (new / existing) + `trigger`.

## D3 — Never refuse the change on a failed write (spec A3)

**Decision**: the existing sink failure policy applies unchanged (`audit-log.md` §Sink failure policy).

**Alternative — fail closed**: an administrator on a full disk could not revoke a leaked token or quarantine a server. A record of the safety action is worth less than the safety action. A strict mode can be a later opt-in; the doc says there is none today and this spec does not add one.

**Ordering**: the line is written after the commit, from the commit's snapshot (the #1842 rule: values from the committed entry, not re-read live state), and before the response is returned, so a 2xx means the line was attempted. A crash between commit and write loses that one line — documented like the `authz` crash window.

## D4 — Committed changes only (spec A4)

**Decision**: one line per committed change; nothing for a refused, failed or no-op request.

**Why**: the count invariant stays exact and testable (`#admin_action == #committed changes`), and the reviewer's question is "who changed it". "Who tried" is a different event with its own invariant (one per refused administrative request) and its own disclosure questions (a scoped token probing admin doors); it is named as a follow-up, not folded in.

## D5 — Values: closed literal list, fingerprints, redaction (spec A5)

**Decision**: field names always; verbatim values only for a closed list of booleans, enums, bounded integers and display prefixes; `before_sha256`/`after_sha256` (RFC 8785, the existing `args_sha256` code in `internal/audit/canonical.go`) for other values; `redacted: true` and nothing else for secret-bearing fields.

**Why**: the audit doc's redaction guarantee is structural ("no key is ever populated from an argument"). A free-text value — a URL, a command, an args list — can carry a credential, so it gets a fingerprint, which still answers "did it change, and is it back to what it was". A secret gets no hash at all: a hash of a low-entropy secret is a guessing oracle.

**Alternatives**: names only (the issue's first proposal) — cannot answer "enabled went from true to false"; the activity row's redacted before/after values — `redactActivityConfigValues` is a best-effort filter over arbitrary JSON, not a closed key set, so it does not meet the audit doc's bar.

## D6 — Automatic approvals are recorded, per server and pass (spec A6)

**Decision**: `tool.approve` with `caller.kind: internal`, `trigger: policy`, `policy: trusted_baseline | scan_clean | auto_approve_tool_changes | hash_restored`, one line per server per pass, names capped at 64.

**Why**: "who approved this tool" includes "nobody — the proxy, under this policy". One line per tool would write thousands of lines when a large fleet (3,000 tools is a tested size) baselines; one line per pass keeps the answer and bounds the volume.

## D7 — Where the line is written

**Decision**: at the runtime / management funnel that commits the change — the same place that stamps the activity row with `stampActor` (`internal/runtime/activity_actor.go:57`) — through a small `AdminAuditor` seam the server installs from the sink it already owns (`internal/server/server.go:263`, `WithAuditSink`). Doors (REST, MCP tools, CLI via REST) do not write lines themselves.

**Why**: the funnel sees the committed snapshot and runs once per change whatever the door; writing at the door would duplicate the line for the CLI and the Web UI paths and miss doors added later.

**Alternative — consume the activity rows**: that is the bus, which drops (FR-018).

## D8 — Offline CLI writes

**Decision**: offline commands that change the rules while the core is stopped (they refuse with exit 3 when it runs) open `audit_log.path` through `audit.NewFileSink` and write with `caller.kind: cli_offline`. With no file path configured they write nothing (the stdout sink would mix with the user's terminal output).

**Alternative — leave them out**: they are rare, but `auth tokens decrypt` changes how secrets are stored, and an offline credential write already records a `cli_offline` activity actor (`cmd/mcpproxy/connect_credential.go:277,306`), so the vocabulary exists.

## D9 — The door inventory test

**Decision**: a checked-in registry (`internal/audit/admindoors` or a test-only table) maps every mutating door to an `action` or an exemption with a reason. A test enumerates doors from the source — chi route registrations with `Post|Put|Patch|Delete` under `/api/v1` in `internal/httpapi` (bare and `-tags server`), the operation switch of each built-in admin MCP tool in `internal/server`, and offline CLI commands that open storage for writing — and fails on any door missing from the registry, and on any registry entry that names a door that no longer exists. Entries start as `pending: "PR-n"`; the last slice makes `pending` a failure.

**Why**: the issue's lesson is that write doors were added without records (token regenerate had none). The repo already pins similar invariants with AST tests (the `go/types` OAuth codec guard of Spec 123, the outcome AST test of Spec 126).
