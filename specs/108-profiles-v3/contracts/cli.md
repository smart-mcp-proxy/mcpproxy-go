# Contract: CLI (Profiles v3)

All commands talk to the running daemon over the socket (existing `daemon_client.go` pattern), send `X-MCPProxy-Surface: cli`, honour `-o table|json|yaml`, `--json`, `MCPPROXY_OUTPUT`, and `--help-json`. JSON output equals the REST `data` object. Flag names are the kebab-case of REST fields.

## `mcpproxy profile`

```text
profile list                                   # NAME TITLE SERVERS MAX-TIER READ WRITE DESTR UNANNOTATED-HIDDEN USED-BY CALLS-24H BLOCKED-24H
profile show <name> [--effective] [--client <id>] [--reason <r>]
profile create <name> --servers a,b [--title T] [--description D] [--max-tier read|write|destructive]
               [--unannotated deny|as_write|as_read] [--allow p,...] [--deny p,...]
               [--code-execution on|off|inherit] [--management-tools on|off|inherit]
               [--switchable-to x,y]
profile update <name> [same flags; --add-server/--remove-server/--add-allow/--remove-allow/--add-deny/--remove-deny; --clear-<field>]
profile rename <old> <new>
profile delete <name> [--reassign-to <profile>] [--force]
profile classify <name> <server:tool> read|write|destructive|--clear
profile try <name> --query "..." [--limit N] [--set k=v ...]   # draft = saved profile + overrides, nothing persisted
profile anonymous [<name> | --clear]                            # sets anonymous_profile
```

`USED-BY` is filled for administrator credentials only (REST omits `used_by` for non-admin callers, FR-034) and prints `—` otherwise. `--code-execution inherit` clears the field: the global flag applies, except under a `read`/`write` `--max-tier`, where unset resolves **off** (FR-003a); `profile show` prints the effective value with its origin (`on (inherited)`, `off (default under max tier read)`).

Enum values on the CLI are spelled exactly as in config/REST/MCP (`as_write`, `as_read`), so a value copied between surfaces works (FR-001: kebab-case is for flag **names** only); `as-write`/`as-read` are accepted as input aliases and output always uses the canonical spelling (codex round 3; an earlier revision made the kebab form canonical). `profile delete` in use without `--reassign-to/--force` exits 1 printing the `used_by` table.

## `mcpproxy client`

```text
client list [--profile <p|->]                  # Spec 109-h's command and presence columns (incl. CONFIG PATH, kept); this spec APPENDS CREDENTIAL PROFILE MODE SOURCE BLOCKED-24H and adds --profile = current binding (GET /clients?profile=)
client show <id>                               # Spec 109-h's command; this spec adds the binding/credential fields and rotation state
client set-profile <id> <profile|all> [--lock | --switchable]   # neither flag = keep current mode (`all` → switchable); sends no `mode`
client set-profile --from-profile <profile> --to-profile <profile> [--lock|--switchable]   # bulk move = REST from_profile/to_profile (mode unchanged unless given); not --from/--to, which are Spec 109-k's time-range aliases on activity commands
client lock <id> | client unlock <id>
client add <id> [--display-name N] [--profile P] [--lock|--switchable] [--expires-in 90d]   # custom client; <id> per FR-021 (^[a-z0-9][a-z0-9_-]{0,55}$, not a supported client id; else exit 1 with the REST 400 text); prints credential once + snippet; duration format = token expiry rule ("30d", "720h", ≤ 365d)
client rotate <id>                                              # staged (FR-021a): adds the pending secret, then supported clients run the connect preview, ask to confirm (or --yes) and finalize; custom clients print the new credential once and stay pending
client rotate <id> --finalize                                   # POST /clients/{id}/rotate/finalize (idempotent)
client upgrade-admin-key-holders [--profile P] [--yes]         # FR-025 bulk previewed upgrade of every client holding the admin key; then prints the admin-key rotation step
client forget <id> [--disconnect]
```

`client set-profile|lock|unlock` on a client without an active client credential (`credential_state` `none`, `admin_key`, `revoked`, `expired`) exits 1 with the `409 no_client_credential` message and the hint `mcpproxy connect <id> --profile <p>`; bulk `--from-profile/--to-profile` prints skipped clients to stderr and exits 0. `profile delete` of the `anonymous_profile` without `--reassign-to` exits 1 even with `--force` (`409 profile_is_anonymous_profile`).

Any command the FR-008a guard covers (`connect --profile`, `client add --profile`, `client set-profile`, `client lock`, `client upgrade-admin-key-holders --profile` — refused after the preview, nothing minted — and `profile create|update|rename|delete|classify`, which can shrink what a binding reaches or grow what anonymous callers reach) exits 1 with the `409 binding_bypassable_without_auth` text and both fixes (set `require_mcp_auth: true` in the config or Settings → Security / `mcpproxy profile anonymous <p>`) when the FR-008a guard refuses it. Warnings from the response (`anonymous_denied_by_binding_guard`, `client_holds_admin_key`, `client_rotation_pending`, `client_token_name_conflict`, …) print to stderr with the fix command (e.g. `mcpproxy upstream …`/config patch hint) and are included in JSON output under `warnings`.

## `mcpproxy access explain`

```text
access explain --tool github:create_issue (--client cursor | --token ci-bot | --profile work-readonly | --anonymous)
```

Table: `STEP STATUS DETAIL FIX`, then `VERDICT: blocked at tier_cap`. Exit code 0 whenever an explanation is produced (the verdict is data, not an error); 1 on request errors (unknown subject, daemon unreachable).

## Changed commands

| Command | Change |
|---|---|
| `connect <client>` | `--profile <p|all>` (default `all`), `--lock` / `--switchable` (default `--switchable` for `all`, `--lock` when a profile is given), `--keyless` (refused when `require_mcp_auth` is on). Preview output shows the masked client credential and the profile. Never writes the admin API key. Exits 1 with the `409` message and remediation when a regular token already holds `client-<client>` (FR-021). |
| `token create` | `--profile <p>` (new; `--profile-pin` kept as hidden deprecated alias printing a notice); `--servers`/`--permissions` optional when `--profile` is set; names starting `client-` refused. When legacy flags are used without `--profile`, prints `hint: consider --profile <name> instead of --servers/--permissions (legacy scope)`. |
| `token list/show` | columns `KIND CLIENT PROFILE MODE LEGACY-SCOPE`; `token list --profile <p|->` filters by current pin (`GET /tokens?profile=`) |
| `activity list/watch/summary/export` | `--profile`, `--client`, `--token` (`--agent` kept as alias) on all four; `--client-name` (advisory) on `list`, `export` and `watch` only — `list`/`export` pass it to `GET /activity`/`/activity/export`, `watch` applies every filter client-side to the `/events` SSE activity payloads (no REST query; same as its existing `--server`); `summary` rejects it like the REST `/activity/summary` route; `-` = unattributed; table gains `CLIENT PROFILE` columns. **Not this spec's**: the `--from`/`--to` aliases of `--start-time`/`--end-time` are owned and registered by Spec 109-k (its FR-075, T121); this spec neither registers nor re-declares them (a second Cobra registration of the same flag name panics) and only uses them in examples |
| `tools list` | `--profile <p>`, `--client <id>` → "view as": adds `ACCESS REASON` columns (`REASON` values = the data-model §7 EffectiveTool enum); `--client` needs an administrator credential — with a scoped agent token it exits 1 with the REST `403` text (FR-032) |
| `upstream list` | `--profile <p>` restricts to the profile's effective servers |
| `doctor` | new checks `profiles.binding_bypass` — fires when `require_mcp_auth` is off, some client credential (locked **or** switchable) is bound to a named profile, **and** `anonymous_profile` is unset or wider than that binding (FR-008a: servers, tier cap, unannotated handling, any tool admitted by `anonymous_profile` or its `switchable_to` but not by the binding — deny/allow rules included — or code-execution/management capability), i.e. the FR-008a runtime guard is denying every anonymous caller; it calls the same `BindingBypassable` function as the API refusal and the `GET /clients` `anonymous_denied_by_binding_guard` warning (US5-4, data-model §7), so they can never disagree — and `connect.admin_key_in_client_config`, which prints the two FR-025 remediation steps (`mcpproxy client upgrade-admin-key-holders`, then rotate `api_key`) |

Out of scope: a `sessions` command (none exists; `activity list --session` covers it).
