# Contract: `credentials` error catalog

A refusal is an MCP tool result with `isError: true`. Its single text content is a JSON object:

```json
{ "code": "<code>", "error": "<human sentence>", "field": "<argument>", "...": "code-specific keys" }
```

`field` is present when one argument is at fault. Texts never contain secrets, hashes, storage paths or Go error chains (FR-020). New codes are constants in `internal/profile/contract.go`, pinned in `internal/profile/testdata/contract/enums.json` (`credential_error_codes`), and mirrored in `frontend/src/types/contracts.ts`.

| Code | Operations | When | Extra keys | Example `error` |
|---|---|---|---|---|
| `unknown_operation` | all | `operation` is not in the enum | `field: operation` | `unknown operation "create"; valid: list, get, create_client, create_token, revoke` |
| `missing_argument` | all | required argument absent | `field` | `create_token: missing required argument "expires_in"` |
| `invalid_argument` | all | wrong type, bad syntax, both/neither of client/token, unknown key, length over limit | `field` | `invalid argument "client": must be lower-case letters, digits, '-' or '_', at most 56 characters` |
| `profile_required` | create_* | `profile: ""` (All servers) | `field: profile` | `a worker credential must name a profile; All servers is not allowed here` |
| `unknown_profile` | create_* | profile does not exist | `field: profile` | `unknown profile "daily-reserch"` |
| `invalid_expiry` | create_* | unparseable, ≤0 or >365d | `field: expires_in` | `invalid expiry duration: "1y"` / `expiry duration cannot exceed 365 days` |
| `identity_exists` | create_* | any record holds the id/name | `field`, `state: active\|expired\|revoked\|conflicting_token` | `client delegated-worker already exists (revoked); choose a new id` |
| `reserved_identity` | create_* | connect-registry client id; token name `client-…` | `field` | `client id "cursor" is a supported client; connect it from the Web UI or CLI instead` |
| `identity_not_found` | get, revoke | no record | `field` | `token "research-task-42" not found` |
| `token_limit_reached` | create_token | 100 stored tokens | — | `maximum number of agent tokens (100) reached` |
| `read_only_mode` | create_*, revoke | live `read_only_mode: true` | — | `Operation not allowed in read-only mode` (byte-equal to `profiles`) |
| `management_disabled` | create_*, revoke | live `disable_management: true` | — | `Server management is disabled for security` (byte-equal) |
| `unsupported_edition` | create_client; client get/revoke | server edition | — | `client credentials are not available in the server edition` |
| `credentials_unavailable` | all | runtime or store not wired (startup) | — | `credential service not available` |
| `binding_bypassable_without_auth` *(existing)* | create_* | FR-008a guard delta | `bindings`, `fixes` (existing body of `runtime.BindingGuardError`) | `a client bound to profile daily-research could escape it by omitting its credential while require_mcp_auth is off` |
| `connect_in_progress` *(existing)* | revoke (client) | connect claim held | `client_id` | existing text |

Hidden-tool refusal: callers who are not admin callers get the plain text `unknown tool: credentials`, not a JSON body. This matches `profiles`, so a hidden tool and a nonexistent one look alike.

REST mapping (FR-009, FR-010): the REST token and client routes keep their existing status codes and bodies. They do not adopt these codes in this spec.
