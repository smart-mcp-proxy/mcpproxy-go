# Data Model: MCP Credential Lifecycle (Spec 115)

Every storage change is **additive**, and every new JSON field is `omitempty`. A pre-115 binary reading a 115 record ignores the new fields and behaves exactly as before. No migration is needed.

## 1. Credential record: `auth.AgentToken` (extended)

Existing fields are unchanged: `name`, `token_hash`, `token_prefix`, `allowed_servers`, `permissions`, `expires_at`, `created_at`, `last_used_at`, `revoked`, `user_id`, `profile_pin`, `kind`, `client_id`, `profile_mode`, `display_name`, rotation fields, and `connected_at`.

| New field | JSON | Type | Set by | Rule |
|---|---|---|---|---|
| `RevokedAt` | `revoked_at` | `*time.Time` | `RevokeAgentToken*`, `ForgetClientCredential*` | Stamped (UTC) the first time `Revoked` flips to true, and never overwritten. Absent on legacy revoked records. |
| `Issuer` | `issuer` | `*CredentialIssuer` | `CredentialsService.Issue*` | Absent on records minted before 115, and on connect-minted clients (connect is not an issue). |
| `Purpose` | `purpose` | `string` | `CredentialsService.Issue*` | At most 500 characters (`MaxCredentialPurpose`). Display-only and never enforced. Never copied into activity metadata. |

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

## 5. `runtime.GuardState` (extended)

| Field | New | Meaning |
|---|---|---|
| `Config`, `Tokens` | — | as today, holding client credentials |
| `PinnedTokens` | ✓ | Candidate profile-pinned agent tokens evaluated as locked bindings. Only `CredentialsService.IssueToken` sets it, and only when `EnforceGuard` is set |

## 6. Requests (runtime layer)

```go
type IssueClientRequest struct {
    ID, DisplayName, Profile, Purpose string
    Mode      *string   // nil → locked
    ExpiresAt time.Time // required (zero → refused with invalid_expiry when RequireExplicitExpiry)
    RequireProfile, RequireExplicitExpiry, RefuseExistingRecord bool // all true from MCP
}

type IssueTokenRequest struct {
    Name, Profile, Purpose string
    AllowedServers, Permissions []string // REST legacy path only; MCP passes nil
    ExpiresAt    time.Time
    RequireProfile, EnforceGuard bool // true from MCP
}

type CredentialRef struct{ Client, Token string } // exactly one set
```

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
