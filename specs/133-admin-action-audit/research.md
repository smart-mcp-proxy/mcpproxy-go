# Research and decisions — Spec 133 `admin_action`

Each decision names the alternative it beat. A1–A6 in spec.md are the assumptions the reporter is invited to push back on (issue #1698).

## D1 — Additive within `schema_version: 1` (spec A1)

**Decision**: new `event` value `admin_action`, new keys, new `caller.kind` values `none` and `cli_offline`, all under `schema_version: 1` and the same schema `$id`.

**Why**: `docs/features/audit-log.md` §Versioning already says a bump happens "only for a removed or renamed key, or a narrowed vocabulary" and that "adding a key or an enum value is a minor change". Nothing is removed or renamed.

**Alternative — `schema_version: 2`**: every existing line would change version, and every shipper filter in the field (`select(.schema_version == 1)`, the doc's own recipe) would silently stop matching the three existing kinds. That is a larger break than the one it avoids.

**Published vs producer-strict** (round-2 finding): the published schema stays consumer-tolerant and does not forbid the new keys on the other kinds — forbidding a key the old schema tolerated would narrow it; exclusivity is enforced by the producer-strict test only. `outcome` stays the shared root property.

**The cost, stated**: `event` and `caller.kind` are closed enums in the published schema (`docs/schemas/audit-line-v1.schema.json`, `properties.event.enum`, `properties.caller.properties.kind.enum`). A consumer validating against a copy taken before this change rejects `admin_action` lines. The doc's claim that "a strict consumer holding the v1 document keeps validating after a minor additive change" is true for new keys only (`additionalProperties: true`), not new enum values; FR-003 corrects the sentence and tells shippers to switch on `event` and ignore unknown values.

## D2 — Audit `caller`, #1842 vocabulary mapped onto it (spec A2)

**Decision**: the line's identity is the common `caller` object, built by the same derivation as `authz` lines (`auditCallerFromContext`, `internal/server/audit_funnel.go:231`). The #1842 activity keys map onto it; the caller-asserted surface becomes `client.surface`.

**Why**: a shipper indexes `caller.kind` across the file. Writing `actor_kind` on one event kind would split that index. The #1842 values were chosen to be liftable, and they are — but two of them are not trustworthy enough for a trusted key: `surface` comes from the `X-MCPProxy-Surface` header on REST (`internal/httpapi/surface.go`), and the audit doc's rule is that a header-derived value never feeds `source` or any trusted key (`audit-log.md` §Common keys, `source`). It sits under `client`, which is already "caller-asserted, untrusted".

**Alternative — copy the #1842 keys verbatim** (`actor_kind`, `actor_name`, `surface`): one vocabulary across activity and audit, but two caller vocabularies inside the audit file, and a header-derived trusted key.

**Mapping** (activity `actor_kind` → audit `caller.kind`; the request context, not this string, is the input — see the contract's §Caller): `api_key` → `api_key`; `socket` → `socket`; `agent_token` → `agent_token` (+ `token_name`, `token_prefix`, `profile_pin`); an `mcp_oat_` browser login → `agent_token` (it is `AuthTypeAgent`, `internal/auth/agent_token.go:394-420`, and is already recorded that way on `authz` lines); `cookie` / `bearer_jwt` administrator → `session_admin` (+ `user_id`, `user_email`, `role`, `provider`); `anonymous` → `anonymous`; `cli_offline` → `cli_offline` (new); no actor → `none` or `internal` (new / existing) + `trigger`. Native stdio has no activity spelling (activity falls back to `api_key`); the audit line says `stdio`, as `authz` lines do.

## D3 — Never refuse the change on a failed write (spec A3)

**Decision**: the existing sink failure policy applies unchanged (`audit-log.md` §Sink failure policy).

**Alternative — fail closed**: an administrator on a full disk could not revoke a leaked token or quarantine a server. A record of the safety action is worth less than the safety action. A strict mode can be a later opt-in; the doc says there is none today and this spec does not add one.

**Ordering**: the line is written after the effect persists, from the committed snapshot (the #1842 rule: values from the committed entry, not re-read live state), and before the response is returned — except completions that arrive after the response (contract rule 1) and lines the builder rejects (counted). The write is synchronous and may delay the response; it never undoes the change. `ts` is the commit time; file order is not commit order (the commit lock is released before post-commit work, `internal/runtime/commit.go:483-505`). A crash between commit and write loses that one line — documented like the `authz` crash window.

## D4 — Committed effects only (spec A4)

**Decision**: one line per committed effect, observed where it persisted; nothing for a refused or no-op request; for a partial failure, lines for what persisted (round-1 finding: config-to-secret stores the keyring entry before the config patch, `internal/httpapi/server.go:3297-3309,3338-3353`).

**Why**: the obligation stays exact and testable (one record per eligible committed effect, in batch-key units — contract §Count invariant keeps it apart from attempts and parseable records), and the reviewer's question is "who changed it". "Who tried" is a different event with its own invariant (one per refused administrative request) and its own disclosure questions (a scoped token probing admin doors); it is named as a follow-up, not folded in.

## D5 — Values: typed literals or withheld, never a hash (spec A5)

**Decision**: field names always; verbatim values only for fields whose type is a boolean, closed enum, bounded integer, timestamp, display prefix, count, or a list of server/profile names (and only when the value converts to the field's closed set); every other value is `withheld` (`"secret"` for secret-class paths, `"value"` for the rest) with no value and no hash.

**Why**: the audit doc's redaction guarantee is structural ("no key is ever populated from an argument"). A free-text value — a URL, a command, an args list — can carry a credential with no recognisable shape (a positional password), and a digest of it is a guessing oracle. The first draft proposed fingerprints for free text; the round-1 cross-review showed that contradicts "no hash of a secret" (FR-016), so fingerprints were dropped. The activity row (administrator-only) keeps the redacted detail for anyone who needs to see *what* changed.

**Why tool and skill approval hashes are withheld too** (round-2 finding): the hash covers upstream-controlled descriptions, schemas and defaults (`internal/runtime/tool_quarantine.go:47-66`) and skill files (`internal/skills/hash.go:11-66`); nothing proves those secret-free, so a digest of them is the same oracle. The only digest on a line is the **embedded** TPA bundle's (it ships in the public binary); an external bundle's digest is withheld even when signed, because a signature authenticates bytes without making them public (round-4 finding).

**Alternatives**: names only (the issue's first proposal) — cannot answer "enabled went from true to false"; fingerprints of free text — rejected above; the activity row's redacted before/after values — `redactActivityConfigValues` is a best-effort filter over arbitrary JSON, not a closed key set.

## D6 — Automatic approvals are recorded, per server and pass (spec A6)

**Decision**: `tool.approve` with `caller.kind: internal`, `trigger: policy`, `policy: trusted_baseline | scan_clean | auto_approve_tool_changes | hash_restored`, one line per server per pass, names capped at 64.

**Why**: "who approved this tool" includes "nobody — the proxy, under this policy". One line per tool would write thousands of lines when a large fleet (3,000 tools is a tested size) baselines; one line per pass keeps the answer and bounds the volume.

## D7 — Where the line is written

**Decision**: at the runtime / management funnel that commits the change — the same place that stamps the activity row with `stampActor` (`internal/runtime/activity_actor.go:57`) — through a small `AdminAuditor` seam the server installs from the sink it already owns (`internal/server/server.go:263`, `WithAuditSink`). Doors (REST, MCP tools, CLI via REST) do not write lines themselves.

**Why**: the funnel sees the committed snapshot and runs once per change whatever the door; writing at the door would duplicate the line for the CLI and the Web UI paths and miss doors added later.

**Alternative — consume the activity rows**: that is the bus, which drops (FR-018).

## D8 — Offline CLI writes

**Decision**: two mechanisms (round-4 finding: one lock cannot answer both questions). *Who owns the resource?* — for the config file, a lock on its canonical path that `serve` takes before it touches the file and a CLI holds across its read-modify-write and audit attempt (round-5: a released probe races; round-6: `--config` and `--data-dir` are independent, so a data-directory lock would not protect a shared file); with a daemon holding it, a rule-changing CLI command goes through REST (or refuses if the core is unreachable). Keyring writes have no observer and are recorded by whichever process performs them. *Who may write the file?* — an exclusive advisory lock on `<audit_log.path>.lock`. A CLI with no daemon and the file lock writes with `caller.kind: cli_offline` through `audit.NewFileSink`, only when `audit_log` is effectively enabled and has a `path` (the resolver accepts a path with `enabled: false`, `internal/config/audit_log.go:155-189`, so the path alone is not enough). The stdout sink is never used: it would mix with the user's terminal output. An offline open or write failure prints one stderr warning and leaves the exit status alone; the daemon's doctor counters cannot see a short-lived process.

**Alternative — leave them out**: they are rare, but `secrets set` and offline `upstream add` change the rules, and an offline credential write already records a `cli_offline` activity actor (`cmd/mcpproxy/connect_credential.go:277,306`), so the vocabulary exists.

## D9 — The door registry test

**Decision**: a checked-in registry maps every (door, operation branch) to an `action` or an exemption with a reason. The test enumerates doors from the **constructed** objects, not from source text:

- HTTP: build the personal router and, under `-tags server`, the server-edition router as production does (including the sibling group mounted in `internal/serveredition/setup.go:343-350`), and `chi.Walk` them (the repo already checks missing and stale routes this way, `internal/httpapi/scope_route_table_guard_test.go:230-272`). The outer `http.ServeMux` has more direct registrations than the Spec 132 OAuth tables (MCP mounts and aliases, other handlers, `internal/server/server.go:3541-3588,3832-3876`): PR-1 routes every outer registration through one production registrar that records path, methods and delegation (e.g. "delegates to the chi API router"), and the test classifies each. Nested `Route`, `With` chains and prefix helpers (`RegisterRoutesWithPrefix`) are resolved by chi itself; a source-level AST walk would miss them (round-1 finding). Every route and method is enumerated and must be classified by name — `GET` included, with no default exemption (round-2 finding: a default lets a newly mutating read merge unclassified). Each `GET` is classified by effect branch — no write, a named bookkeeping write (e.g. the skill-content integrity mark, `internal/httpapi/skills.go:343`), or a mapped administrative effect — and the door table harness asserts zero admin lines plus only the permitted storage delta for the first two.
- REST built-in dispatch: the `/tools/call` path that reaches `upstream_servers` and `quarantine_security` is a `switch toolName` today (`internal/server/mcp.go:7841-7894`), not a table; PR-1 puts it behind production descriptors the test reads (with a negative fixture for a REST-only addition).
- MCP (per surface: call-tool, code, direct and single-server registrations are compared separately): PR-1 moves the four admin tools (`upstream_servers`, `quarantine_security`, `profiles`, `credentials`) onto dispatch descriptors — one table per tool mapping each accepted operation value and alias to its handler and its effect branches (e.g. `credentials revoke` → token | client | client+grant). The handlers dispatch from that table and the test reads the same table, so an alias the handler accepts cannot be missing from the guard (the `quarantine` alias, `mcp.go:5001-5004`, is the existing case of schema and dispatch disagreeing). A tool without an operation switch (`set_profile`) is one door. The test also lists **every** registered built-in tool, so a new built-in cannot appear without a classification.
- CLI: walk the constructed Cobra tree (`rootCmd.Commands()` recursively), built by one shared constructor for both editions and run in the tagged CI job too; every runnable node (`Runnable()`, not only leaves) is classified as `daemon` (goes through REST, covered by the HTTP rows), `offline write` (mapped), or `read-only`. Branches inside a command (`upstream add` keyring flags, the `add-json` daemon/file split, config mode) cannot be discovered by walking the tree; the registry lists them per command, and the exact-effect fixtures pin them. The guard's claim is therefore "every door is classified", not "every branch is discovered".

Entries start as `pending: "PR-n"`. The set of pending entries is frozen in PR-1: a later PR may only remove entries from it, so a new door cannot bypass the guard by declaring itself pending. PR-5 makes `pending` itself a failure.

**Why**: the issue's lesson is that write doors were added without records (token regenerate had none). The repo already pins similar invariants with structural tests (the `go/types` OAuth codec guard of Spec 123, the outcome AST test of Spec 126); enumeration from the built router is the variant that cannot be fooled by registration style.
