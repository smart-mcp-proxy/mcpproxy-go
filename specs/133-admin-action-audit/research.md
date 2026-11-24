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

**Mapping** (activity `actor_kind` → audit `caller.kind`; the request context, not this string, is the input — see the contract's §Caller): `api_key` → `api_key`; `socket` → `socket`; `agent_token` → `agent_token` (+ `token_name`, `token_prefix`, `profile_pin`); an `mcp_oat_` browser login → `agent_token` (it is `AuthTypeAgent`, `internal/auth/agent_token.go:394-420`, and is already recorded that way on `authz` lines); `cookie` / `bearer_jwt` administrator → `session_admin` (+ `user_id`, `user_email`, `role`, `provider`); `anonymous` → `anonymous`; `cli_offline` → `cli_offline` (new); no actor → `none` or `internal` (new / existing) + `trigger`. Native stdio has no activity spelling (activity falls back to `api_key`); the audit line says `stdio`, as `authz` lines do.

## D3 — Never refuse the change on a failed write (spec A3)

**Decision**: the existing sink failure policy applies unchanged (`audit-log.md` §Sink failure policy).

**Alternative — fail closed**: an administrator on a full disk could not revoke a leaked token or quarantine a server. A record of the safety action is worth less than the safety action. A strict mode can be a later opt-in; the doc says there is none today and this spec does not add one.

**Ordering**: the line is written after the effect persists, from the committed snapshot (the #1842 rule: values from the committed entry, not re-read live state), and before the response is returned, so a 2xx means the line was attempted. The write is synchronous and may delay the response; it never undoes the change. `ts` is the commit time; file order is not commit order (the commit lock is released before post-commit work, `internal/runtime/commit.go:483-505`). A crash between commit and write loses that one line — documented like the `authz` crash window.

## D4 — Committed effects only (spec A4)

**Decision**: one line per committed effect, observed where it persisted; nothing for a refused or no-op request; for a partial failure, lines for what persisted (round-1 finding: config-to-secret stores the keyring entry before the config patch, `internal/httpapi/server.go:3297-3309,3338-3353`).

**Why**: the count invariant stays exact and testable (`#admin_action == #committed changes`), and the reviewer's question is "who changed it". "Who tried" is a different event with its own invariant (one per refused administrative request) and its own disclosure questions (a scoped token probing admin doors); it is named as a follow-up, not folded in.

## D5 — Values: typed literals or withheld, never a hash (spec A5)

**Decision**: field names always; verbatim values only for fields whose type is a boolean, closed enum, bounded integer, timestamp, display prefix, public content-hash prefix, or a list of server/profile names; every other value is `withheld` (`"secret"` for secret-class paths, `"value"` for the rest) with no value and no hash.

**Why**: the audit doc's redaction guarantee is structural ("no key is ever populated from an argument"). A free-text value — a URL, a command, an args list — can carry a credential with no recognisable shape (a positional password), and a digest of it is a guessing oracle. The first draft proposed fingerprints for free text; the round-1 cross-review showed that contradicts "no hash of a secret" (FR-016), so fingerprints were dropped. The activity row (administrator-only) keeps the redacted detail for anyone who needs to see *what* changed.

**Why tool/skill hash prefixes are literal**: the approval hash is computed over the definition the upstream publishes to every connected agent (`internal/runtime/tool_quarantine.go:47-66`); it fingerprints public content, not operator configuration.

**Alternatives**: names only (the issue's first proposal) — cannot answer "enabled went from true to false"; fingerprints of free text — rejected above; the activity row's redacted before/after values — `redactActivityConfigValues` is a best-effort filter over arbitrary JSON, not a closed key set.

## D6 — Automatic approvals are recorded, per server and pass (spec A6)

**Decision**: `tool.approve` with `caller.kind: internal`, `trigger: policy`, `policy: trusted_baseline | scan_clean | auto_approve_tool_changes | hash_restored`, one line per server per pass, names capped at 64.

**Why**: "who approved this tool" includes "nobody — the proxy, under this policy". One line per tool would write thousands of lines when a large fleet (3,000 tools is a tested size) baselines; one line per pass keeps the answer and bounds the volume.

## D7 — Where the line is written

**Decision**: at the runtime / management funnel that commits the change — the same place that stamps the activity row with `stampActor` (`internal/runtime/activity_actor.go:57`) — through a small `AdminAuditor` seam the server installs from the sink it already owns (`internal/server/server.go:263`, `WithAuditSink`). Doors (REST, MCP tools, CLI via REST) do not write lines themselves.

**Why**: the funnel sees the committed snapshot and runs once per change whatever the door; writing at the door would duplicate the line for the CLI and the Web UI paths and miss doors added later.

**Alternative — consume the activity rows**: that is the bus, which drops (FR-018).

## D8 — Offline CLI writes

**Decision**: offline commands that change the rules while the core is stopped (they refuse with exit 3 when it runs) write with `caller.kind: cli_offline` through `audit.NewFileSink`, only when `audit_log` is effectively enabled and has a `path` (the resolver accepts a path with `enabled: false`, `internal/config/audit_log.go:155-189`, so the path alone is not enough). The stdout sink is never used: it would mix with the user's terminal output. An offline open or write failure prints one stderr warning and leaves the exit status alone; the daemon's doctor counters cannot see a short-lived process.

**Alternative — leave them out**: they are rare, but `secrets set` and offline `upstream add` change the rules, and an offline credential write already records a `cli_offline` activity actor (`cmd/mcpproxy/connect_credential.go:277,306`), so the vocabulary exists.

## D9 — The door registry test

**Decision**: a checked-in registry maps every (door, operation branch) to an `action` or an exemption with a reason. The test enumerates doors from the **constructed** objects, not from source text:

- HTTP: build the personal router and, under `-tags server`, the server-edition router as production does (including the sibling group mounted in `internal/serveredition/setup.go:343-350`), and `chi.Walk` them. Nested `Route`, `With` chains and prefix helpers (`RegisterRoutesWithPrefix`) are resolved by chi itself; a source-level AST walk would miss them (round-1 finding). Every method is enumerated; `GET`/`HEAD` routes are exempt by default **except** the registry's named list of state-changing `GET`s (the broker callback, reconcile-on-read), and a guard in the door table test fails when a `GET` it drives writes to storage or config.
- MCP: start the MCP server in a test, list its built-in admin tools, and read each tool's accepted operation values (the `operation` enum in the tool schema, plus the registry's alias list for values the handler accepts outside the enum, such as `quarantine`). A tool without an operation switch (`set_profile`) is one door.
- CLI: walk the constructed Cobra tree (`rootCmd.Commands()` recursively); every leaf command is classified as `daemon` (goes through REST, covered by the HTTP rows), `offline write` (mapped), or `read-only`. Commands whose branch depends on flags (`upstream add` keyring flags, `add-json`, config mode) list each branch.

Entries start as `pending: "PR-n"`. The set of pending entries is frozen in PR-1: a later PR may only remove entries from it, so a new door cannot bypass the guard by declaring itself pending. PR-5 makes `pending` itself a failure.

**Why**: the issue's lesson is that write doors were added without records (token regenerate had none). The repo already pins similar invariants with structural tests (the `go/types` OAuth codec guard of Spec 123, the outcome AST test of Spec 126); enumeration from the built router is the variant that cannot be fooled by registration style.
