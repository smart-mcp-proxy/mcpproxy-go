# Data Model: MCP Credential Lifecycle (Spec 115)

Every storage change is **additive**, and every new JSON field is `omitempty`. A pre-115 binary reading a 115 record ignores the new fields and behaves exactly as before. No migration is needed.

## 1. Credential record: `auth.AgentToken` (extended)

Existing fields are unchanged: `name`, `token_hash`, `token_prefix`, `allowed_servers`, `permissions`, `expires_at`, `created_at`, `last_used_at`, `revoked`, `user_id`, `profile_pin`, `kind`, `client_id`, `profile_mode`, `display_name`, rotation fields, and `connected_at`.

| New field | JSON | Type | Set by | Rule |
|---|---|---|---|---|
| `RevokedAt` | `revoked_at` | `*time.Time` | `RevokeAgentToken*`, `ForgetClientCredential*` | Stamped (UTC) the first time `Revoked` flips to true, and never overwritten. Absent on legacy revoked records. |
| `Issuer` | `issuer` | `*CredentialIssuer` | `CredentialsService.Issue*` | Absent on records minted before 115, and on connect-minted clients (connect is not an issue). |
| `Purpose` | `purpose` | `string` | `CredentialsService.Issue*` | At most 500 characters (`MaxCredentialPurpose`). Display-only and never enforced. Never copied into activity metadata. Passes the shared service screen (§8.2) inside `CredentialsService` before anything is minted or stored, on every surface (MCP, REST, CLI). |
| `GuardBound` | `guard_bound` | `bool` | `CredentialsService.IssueToken` with `EnforceGuard` | Set on agent tokens minted through the MCP path. Marks the token's `profile_pin` as a **standing** FR-008a binding: every later guard evaluation (config writes, hot reload, the anonymous request guard, warnings) includes it, not only its issuance (§5). Never set on client records (they are always bindings) or on REST/CLI tokens (Assumption A13). Immutable after mint. |

```go
// CredentialIssuer records who issued a credential, using the Actor fields
// of the profile_change record that announced it.
type CredentialIssuer struct {
    ActorKind string `json:"actor_kind"` // api_key | socket | cli_offline | ...
    ActorName string `json:"actor_name,omitempty"`
    Surface   string `json:"surface"`    // mcp | web | cli | api | macos
}
```

Invariants (checked in the `auth` package tests):
- `RevokedAt != nil` ⇒ `Revoked`.
- `GuardBound` ⇒ `Kind == agent` and `ProfilePin != ""`.
- `Issuer`, `Purpose` and `RevokedAt` never participate in `ValidateTokenInvariants`. They cannot make a valid record invalid, or the reverse.
- The secret is never stored. `token_hash` remains the HMAC and is never projected to any view (§2).

## 2. Credential view: the safe projection

`runtime.CredentialView` is returned by `credentials list|get`, embedded in the create delivery, and used to build the REST token and client rows. The REST token response gains the fields marked † below. Each one is additive.

| Field | Type | Clients | Tokens | Notes |
|---|---|---|---|---|
| `kind` | `"client"\|"token"` | ✓ | ✓ | MCP vocabulary. REST tokens keep `kind: "agent"\|"client"` |
| `id` | string | client id | token name | |
| `token_name` | string | `client-<id>` | = name | |
| `display_name` | string | optional | — | |
| `profile` | string | binding | pin | Never empty for an MCP-issued identity |
| `binding` | `"locked"\|"switchable"\|"pinned"` | mode | `pinned` | The UI renders it as "Locked to X", "Switchable from X" or "Pinned to X" |
| `profile_state` † | `"ok"\|"dangling"\|"none"` | ✓ | ✓ | `dangling` when the profile no longer exists, which means deny-all |
| `state` | `"active"\|"expired"\|"revoked"` | ✓ | ✓ | Revoked takes precedence over expired |
| `created_at`, `expires_at` | time | ✓ | ✓ | |
| `revoked_at` † | time? | ✓ | ✓ | |
| `last_used_at` | time? | ✓ | ✓ | |
| `lease` † | bool | ✓ | ✓ | `expires_at − created_at ≤ 24h` |
| `issuer` † | object? | ✓ | ✓ | §1 |
| `purpose` † | string? | ✓ | ✓ | |
| `token_prefix` | string | ✓ | ✓ | The existing 12-character display prefix (`mcp_cli_xxxx`), not secret-bearing |
| `legacy_scope` | bool | — | ✓ | Always false for MCP-issued tokens |

Never present in any view: `token_hash`, `pending_hash`, `pending_prefix`, `prior_*`, `user_id`, or the raw secret.

## 3. Lifecycle record: `profile_change` (`storage.ActivityRecord`)

The shape is unchanged (`runtime.writeChangeRecord`). Two new `profile.ChangeKind` values are added:

| `change` | Written when | `profile` | `previous_profile` | `client_id` / `token_name` |
|---|---|---|---|---|
| `issue` | A credential is issued through `CredentialsService`: MCP, REST/CLI tokens, or REST/CLI custom client add | the bound or pinned profile | `""` | set |
| `revoke` | A credential is revoked through `CredentialsService`: MCP, or REST/CLI token revoke | the profile held at revocation | same | set |

The `diff` for `issue` and `revoke`, with every key always present:

```json
{
  "credential_kind": "client|token",
  "binding": "locked|switchable|pinned",
  "expires_at": "2026-10-10T13:00:00Z",
  "lease": true,
  "token_prefix": "mcp_cli_ab12",
  "purpose_set": true,
  "via": "credentials|rest|cli"
}
```

`revoke` adds `"changed": true` and `"client_config_untouched": true` for supported clients. Actor metadata (`actor_kind`, `actor_name`, `surface`) and `request_id` are as today.

A revoke with no state change (`changed: false`) writes **no** record. Failed attempts write no `profile_change`. They are captured by the `internal_tool_call` record with `status: error` and the structured error body.

Filters already supported apply: `/api/v1/activity?type=profile_change&client=<id>|token=<name>|profile=<p>`.

## 4. Live event: `credentials.changed`

```json
{ "type": "credentials.changed",
  "data": { "kind": "client|token", "id": "delegated-worker", "token_name": "client-delegated-worker",
            "change": "issue|revoke|forget", "profile": "daily-research" } }
```

The event is an invalidation only. It carries no secret, prefix, purpose or expiry, and subscribers refetch. It is published after the storage write and after the `profile_change` record.

## 5. Guarded bindings: `runtime.GuardState` and `ActiveGuardedBinding`

`GuardState` keeps its shape (`Config`, `Tokens`). What changes is **which records count as bindings** in every guard path. A single predicate replaces `ActiveNamedBinding` at each call site:

```go
// ActiveGuardedBinding is an active named client binding (today's
// ActiveNamedBinding) OR an active guard-bound agent token:
// Kind=agent, GuardBound, !Revoked, ExpiresAt after now, ProfilePin != "".
// A guard-bound token is evaluated as a locked binding to its pin.
func ActiveGuardedBinding(t *auth.AgentToken, now time.Time) bool
```

| Call site (origin/main 8282c3865) | Today | Spec 115 |
|---|---|---|
| `runtime.clientCredentialSnapshot` (`binding_guard_wiring.go:44`), used by `MutateConfig` and `GuardedApplyConfig` (`config_funnel.go:90,192`) | client records only | renamed `guardedBindingSnapshot`; returns clients **and** guard-bound tokens. A store error still fails closed |
| `MCPProxyServer.bindingGuardActive` (`profile_binding_guard.go:23-70`), the per-request anonymous guard that also covers file-watcher hot reloads (`server.go:3681`) | `activeNamedClientBinding` | `ActiveGuardedBinding` in both the coherent and the publication-gap branch |
| `guardEvaluation.bypassable`, `BindingGuardActiveBindings`, `ConservativeBindingGuard` | `ActiveNamedBinding` | `ActiveGuardedBinding` |
| `BindingGuardDelta` / conservative delta `already` map | keyed by `ClientID` | keyed by `TokenName`, which is unique across both kinds (a client's is `client-<id>`); a token has an empty `ClientID` and must not collide |
| `ClientsService.checkGuard` and `CredentialsService.IssueToken` | — | candidate state = `guardedBindingSnapshot` plus the candidate record (a client, or a token with `GuardBound: true`) |
| `runtime.BindingRefOf` | `{ClientID, TokenName, Profile, Mode}` | unchanged; for a token `ClientID` is empty and `Mode` is reported as `locked` |

Consequences:
- After an MCP-issued token exists, a config write that turns `require_mcp_auth` off, removes a confining `anonymous_profile` or widens it, is **refused** by `MutateConfig`/`GuardedApplyConfig` with `binding_bypassable_without_auth` naming the token (`bindings[].token_name`).
- A hand edit or hot reload that does the same is not refused (existing rule), but `bindingGuardActive` then denies **every anonymous request** while the token stays an active guarded binding, so omitting the credential yields no access. The `anonymous_denied_by_binding_guard` warning names the token.
- The binding stops counting once the token is revoked or expired, exactly like a client.

**Dangling pins (spec review r2, FR-012b).** Today `bindingBypassable` (`profile_binding_guard.go:202-205`) returns `false` when the bound profile does not resolve ("a dangling bound base is already deny-all and cannot be bypassed"), and again when no bound-reachable profile resolves (`len(boundPolicies) == 0`). That reasoning covers the credentialed request only: omitting the credential still yields anonymous access, which is wider than deny-all. So `profiles delete force=true` followed by turning auth off disabled the standing guard. Spec 115 changes the predicate:

| Bound reach | Anonymous reach | Verdict (today → Spec 115) |
|---|---|---|
| dangling (base missing, or no reachable profile resolves) | `anonymous_profile` empty (unrestricted) | false → **true** |
| dangling | at least one anonymous-reachable profile resolves | false → **true** |
| dangling | anonymous base dangling too (deny-all both ways) | false → false |
| resolves | any | unchanged |

This is evaluated by the one predicate, so it applies in every path: the API funnel delta (`BindingGuardDelta`), the per-request anonymous guard (`bindingGuardActive`, which covers file-watcher hot reloads), `BindingGuardActiveBindings`, and both binding kinds (a locked client and a guard-bound token). `ConservativeBindingGuard` already counts any active named binding regardless of whether its pin resolves, so it needs no change. `BindingGuardFixes` must not offer the `anonymous_profile = <pin>` fix when the pin is dangling (that would point anonymous at a missing profile); the offered fixes are `require_mcp_auth: true`, revoke, or reassign to an existing profile. A force-delete of the pinned profile while auth is already off and `anonymous_profile` grants anything is itself a config write through `MutateConfig`, so the funnel refuses it with `binding_bypassable_without_auth` too.

Compatibility rule for legacy REST/CLI tokens (Assumption A13): tokens without `guard_bound` (all pre-115 tokens, and every token created through REST/CLI after 115) are not guarded bindings, so their behaviour is byte-identical to today. A test pins that a REST-created token with `profile_pin` set does not change any guard verdict.

## 6. Requests (runtime layer)

```go
// ExpiryDefault says what an EMPTY raw ExpiresIn means. It never applies to a
// non-empty value, which is always parsed by auth.ParseTokenExpiry AFTER the
// screen (spec review r5).
type ExpiryDefault int
const (
    ExpiryRequired  ExpiryDefault = iota // MCP: empty → missing_argument
    ExpiryTokenDefault                   // REST/CLI tokens: empty → 30 days (auth.DefaultTokenExpiry, today's parseExpiry)
    ExpiryClientCap                      // REST/CLI clients: empty → 365 days (today's parseClientExpiry)
)

type IssueClientRequest struct {
    ID, DisplayName, Profile, Purpose string
    Mode      *string       // nil → locked
    ExpiresIn string        // RAW caller text; parsed by the service after the screen (r5)
    Expiry    ExpiryDefault // ExpiryRequired from MCP, ExpiryClientCap from REST
    Now       time.Time     // the handler's clock reading, so REST keeps one "now" for expiry and view
    RequireProfile, RefuseExistingRecord bool // both true from MCP
}

type IssueTokenRequest struct {
    Name      string
    Profile   string // RAW `profile` alias as the caller sent it
    ProfilePin string // RAW `profile_pin` alias (REST only; MCP has no such argument, passes "")
    Purpose   string
    AllowedServers, Permissions []string // REST legacy path only; MCP passes nil
    ExpiresIn string        // RAW caller text; parsed after the screen (r5)
    Expiry    ExpiryDefault // ExpiryRequired from MCP, ExpiryTokenDefault from REST
    RequireProfile, EnforceGuard bool // true from MCP; EnforceGuard also stamps GuardBound
}

type CredentialRef struct{ Client, Token string } // exactly one set; Token addresses the OWNERLESS namespace only (§9)
```

**Raw inputs, not pre-parsed ones (spec review r5).** The requests carry the caller's raw `expires_in` text and, for tokens, both raw profile aliases. No handler may parse expiry (`parseExpiry`, `parseClientExpiry`, `auth.ParseTokenExpiry`) or reconcile `profile`/`profile_pin` before calling the service, because `auth.ParseTokenExpiry` quotes a malformed value (`invalid expiry duration: %q`, `internal/auth/agent_token.go:183,191`) and the alias merge discards one of the two raw values. Today `handleCreateClient` parses expiry before `svc.Add` (`client_bindings.go:230-233`) and `handleCreateToken` merges the aliases (`tokens.go:186-193`) and parses expiry (`tokens.go:212`) in the handler; T023/T024 move all three into the service, behind the screen. The resulting `time.Time` and the merged pin are internal to the service.

## 7. State transitions

```
          issue                    revoke (any surface)
(none) ─────────► active ─────────────────────────────► revoked (terminal; revoked_at)
                    │  expires_at passes
                    └──────────────► expired ──revoke──► revoked
binding changes (profiles assign/lock/unlock): active → active (profile/mode change)
profile deleted with force: active → active + profile_state=dangling (deny-all)
```

Revoked and expired states are terminal for MCP. Re-issuing the same id or name is refused (`identity_exists`).

## 8. Secret-shaped input screen

Runs first, before any other check and before anything is persisted or echoed, over **every** string argument of every `credentials` operation (`operation`, `client`, `token`, `name`, `display_name`, `profile`, `mode`, `expires_in`, `purpose`, `kind`, `state`), and over unknown keys' values too.

A value is secret-shaped when any of these holds:
1. it contains `mcp_agt_` or `mcp_cli_` (the issued-credential prefixes), case-insensitive;
2. it contains the daemon's configured `api_key` value (compared in constant time; skipped when the key is empty);
3. the **screening detector** reports a finding in it. This is a dedicated `internal/security.Detector` instance built by `credentialScreenDetectorConfig(live)`, which copies the live `sensitive_data_detection` config only for its custom patterns and entropy threshold, and then **forces** (spec review r3): `Enabled = true`, `ScanRequests = true`, every category enabled (built-in and custom), and `MaxPayloadSizeKB` = `MaxCredentialArgumentsBytes/1024 + 1`, so that no screened string is ever truncated. The user's toggles (`enabled: false`, `scan_requests: false`, disabled categories, a small `max_payload_size_kb`) therefore never weaken the screen; they keep governing upstream traffic exactly as before. The screen calls `Scan(value, "")` and also treats `Result.Truncated == true` as a hit (fail closed), so even a future change to the size rule cannot let an unscanned tail through.

**Size cap (spec review r3).** Before the walk, the handler measures the canonical JSON serialization of the decoded `arguments` object. When it exceeds `MaxCredentialArgumentsBytes` (16 KiB; the largest legitimate call is a 500-character purpose plus a few ids, well under 2 KiB), the call is refused with `arguments_too_large`, **nothing of the payload is retained**, and the stored `internal_tool_call.arguments` is replaced wholesale with `{ "_screened": "oversized input; arguments not stored", "operation": "<known enum value or omitted>", "size_bytes": <n> }`. The oversized payload is never scanned partially, never logged, and never reaches the type, unknown-key or service checks. Any single screened string is therefore at most 16 KiB, below the screening detector's forced payload limit, so the scan of every string is complete.

Coverage (spec review r2): the screen runs over the **whole payload**, not only the known string arguments. It walks the decoded `arguments` object recursively and screens:
- every value at every depth, whatever its JSON type: strings as-is; numbers and booleans as their JSON text (a numeric API key is still caught); arrays and objects element by element;
- every key at every depth, known or unknown (an argument whose **name** is the credential is caught);
- additionally the canonical JSON serialization of each top-level argument, so a secret split across adjacent structure in one argument is caught by the detector as it would be in an upstream payload.

It collects **all** hits, it does not stop at the first.

Outcome: `secret_in_argument`. `field` is the first offending top-level argument in sorted order when that argument name is one of the known names listed above; otherwise it is the fixed placeholder `"(unknown argument)"`. A caller-supplied key is never echoed, in `field`, the error text, or anywhere else. `offending_fields` lists the known offending argument names (sorted, deduplicated) and the count of unknown offending arguments, never their names. The error text names the known field (or says "an unrecognised argument") and never echoes any argument value.

Recording: on a hit the handler does **not** record the caller's arguments with substitutions. It replaces the `arguments` of the `internal_tool_call` record **wholesale** with a server-built summary:

```json
{ "_screened": "secret-shaped input; arguments not stored",
  "operation": "<only when it is one of the five enum values, else omitted>",
  "offending_fields": ["purpose", "display_name"],
  "unknown_offending_count": 1 }
```

No other caller key or value is kept, so a second secret, a nested secret, or a secret used as a key cannot survive into activity, export, SSE, `sensitive_data.detected` rows, logs or the DB. The record is built only from this summary. The handler's own logging (any level) uses the same summary; the generic upstream `handleCallTool` debug line (`mcp.go:3710`) is not on this path because `credentials` is a built-in tool with its own handler, and a test pins that.

Passing calls: because the screen covered every key and value completely (forced detector, no truncation, size cap), the arguments of a call that passes are stored as-is. Unknown keys of a passing call are still refused with `invalid_argument` and `field: "(unknown argument)"`, never their name.

### 8.1 Where the screen runs

The screen is one function, `runtime.ScreenCredentialInput(values ScreenInput) *SecretInputError`, in `internal/runtime/credential_input_screen.go`, owned by `CredentialsService` together with its screening detector and the live API key. It is called from two places:

1. **MCP handler, whole payload** (this section): `handleCredentials` calls it over the whole decoded `arguments` object first, so that a hit can replace the recorded arguments wholesale before any record is built.
2. **Service, persisted fields (§8.2)**: every `CredentialsService.Issue*` call screens again, whatever the surface.

### 8.2 Shared service screen (all surfaces, spec review r3)

`CredentialsService.IssueClient` and `IssueToken` run `ScreenCredentialInput` as their **first** step, under `bindingWriteMu`, before validation, before `GenerateToken`, and before any store call, over every caller-supplied string that can be persisted or echoed: `client`/`name`, `display_name`, `profile` (and the REST alias `profile_pin`, screened in its raw form before the handler merges it into `profile`), `mode`, `expires_in` (in its original text form when the surface passes text), `purpose`, and **every element** of the REST token arrays `allowed_servers` and `permissions` (spec review r4). The array elements are screened before `ValidatePermissions` and `validateAllowedServers` run, because both validators quote the offending element (`invalid permission: %q`, `unknown server: %q`). A hit on any element refuses the whole request, even when the other elements are valid; the refusal names the array field (`allowed_servers` or `permissions`), never the element or its index. This is the authoritative pre-mint check for REST `POST /api/v1/tokens`, REST `POST /api/v1/clients`, the CLI (which calls REST), and the MCP path (where it is a second, cheap pass over the same values). A hit returns the typed `*SecretInputError{Fields}`; nothing is generated, minted, stored, audited (`profile_change` is not written) or published (`credentials.changed` is not emitted).

REST mapping: a `*SecretInputError` answers **400 Bad Request** in the route's existing error envelope with the text `argument "<known field>" looks like a credential or secret; it was not stored. Remove it and retry`. The text names the field and never the value. This is the only new REST refusal in this spec and is additive: no request that succeeds today and does not carry a secret-shaped value changes outcome. The REST handlers do not log request bodies (verified: no `io.ReadAll(r.Body)` or body field in `internal/httpapi`), and a test pins that the refusal path logs only the field name.

Validation order inside `IssueToken` (spec review r4, r5): screen (all fields above, all array elements, the raw `expires_in` text, and **both** raw profile aliases `profile` and `profile_pin` separately) → name syntax → reserved `client-` prefix → profile-alias reconciliation (`"profile" and "profile_pin" name different profiles; send one of them`, unchanged text, never quoting either value) → permissions → expiry parse (`auth.ParseTokenExpiry` on the raw text, or the `ExpiryDefault` when it is empty) → allowed servers → profile. Inside `IssueClient` (r5): screen (`id`, `display_name`, `profile`, `mode`, raw `expires_in`, `purpose`) → the existing order, with the expiry parse where `handleCreateClient` used to run it, now after the screen. Every existing validator, the alias reconciliation and the expiry parser therefore only ever see values that already passed the screen, so the existing error texts that quote a value (T023/T024 keep them, including `invalid expiry duration: %q`) can quote only a non-secret value. The handlers pass the raw text and do no parsing or merging of their own (§6).

### 8.3 REST request decoding (spec review r4)

The service screen runs only on a decoded request. A JSON decoding error happens before it and today returns the decoder text verbatim: `POST /api/v1/clients` uses `decodeProfileBody` (`DisallowUnknownFields`), whose error `json: unknown field "<key>"` quotes the caller's key, and `badProfileBody` returns `err.Error()` unchanged; `POST /api/v1/tokens` returns `"Invalid JSON body: "+err.Error()`. On the two issuance routes the handler therefore decodes through a new `decodeIssuanceBody(r, v, strict bool) error` (in `internal/httpapi/credentials_decode.go`) that returns a typed `*IssuanceDecodeError{Kind, Field}` and never the decoder text:

| Decoder error | `Kind` | 400 text (route's existing envelope) | `field` |
|---|---|---|---|
| `json: unknown field "…"` (clients route, strict) | `unknown_field` | `invalid request body: unrecognised field (its name is not echoed)` | `"(unknown argument)"` |
| `*json.UnmarshalTypeError` | `wrong_type` | `invalid request body: field "<known>" has the wrong type` when the top-level segment of `Field` is one of the route's own JSON field names; otherwise `invalid request body: a field has the wrong type` | the known name, or `"(unknown argument)"` |
| `*json.SyntaxError` | `syntax` | `invalid request body: malformed JSON at byte <Offset>` | none |
| `io.EOF` | `empty` | `request body is required` (unchanged) | none |
| anything else (incl. trailing data, `*json.InvalidUnmarshalError`) | `other` | `invalid request body` | none |

`Field` from `UnmarshalTypeError` is a dotted path that, for a map or a future nested struct, can include caller-chosen keys; only its first segment is compared against the route's fixed field-name list and only a matching known name is echoed. `UnmarshalTypeError.Value` (which for some inputs carries the literal, e.g. `number 12345`) is never used. Strictness is unchanged per route: the clients route keeps refusing unknown fields, the tokens route keeps ignoring them (values of ignored keys are dropped by the decoder and never stored, logged or echoed). The handlers do not log the raw decoder error at any level. `decodeProfileBody` and `badProfileBody` keep their current behaviour on the profile routes, which issue no credential.

Connect-minted clients (`ConnectMinter`) are not covered by §8.2: their `display_name` comes from the server-side connect registry, not from the caller.

Independently of the screen, error texts echo only values that already passed it **and** passed their syntax check (an id or name matching its regex, or an enum value). Free text (`purpose`, `display_name`) and unparsed values (`expires_in`, an unknown `operation`) are never echoed.

## 9. Token namespace: ownerless only (spec review r5)

Agent-token names are unique **per owner** (`CreateAgentToken` returns `ErrAgentTokenNameExists` only for the same owner; the server edition stores tenant-owned tokens with `UserID != ""`). `ListAgentTokens` returns every owner's tokens, while the bare-name helpers `GetAgentTokenByName` and `RevokeAgentToken` resolve only the ownerless namespace (`internal/storage/agent_tokens.go:324-325,401,432-433`). Per-user tokens are out of scope (A1), so every token operation of this spec addresses the **ownerless** namespace, explicitly and in one place:

| Operation | Storage call | Owned (`UserID != ""`) tokens |
|---|---|---|
| `list` (MCP and the `CredentialsService.List` that backs it) | `ListAgentTokens()` filtered to `UserID == ""` | never listed, never counted in `total` |
| `get` | `GetAgentTokenByOwnerAndName("", name)` | `identity_not_found` (same text as a missing name; no hint that a tenant holds it) |
| `create_token` duplicate precheck (`identity_exists`) | `GetAgentTokenByOwnerAndName("", name)` | not a conflict: a tenant's token of the same name does not block an ownerless one; the mint still relies on `CreateAgentToken`'s per-owner check and the legacy owner-blind index rule (`claimAgentTokenNameSlot`) unchanged |
| `create_client` conflicting-token precheck (`client-<id>`) | `GetAgentTokenByOwnerAndName("", "client-"+id)` | not a conflict (clients are personal-edition only anyway) |
| `revoke` | `RevokeAgentTokenForOwner("", name)` | `identity_not_found`; the owned token and its sessions are untouched |

`identity_exists` therefore means "an ownerless record holds the id/name" (contracts/errors.md). REST `GET /api/v1/tokens` keeps listing every owner exactly as today (no REST behaviour change; it is the operator's view), but the `credentials` tool and every `CredentialView` returned by `CredentialsService.List/Get` never include an owned token, so every row the tool lists can be addressed by `get` and `revoke` with its name. REST `DELETE /api/v1/tokens/{name}` already targets only ownerless tokens via `RevokeAgentToken` and keeps doing so through `RevokeAgentTokenForOwner("", name)`.
