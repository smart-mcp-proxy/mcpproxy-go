# Contract: `credentials` built-in MCP tool

**Registered on** the same servers as `profiles`: the default `/mcp` server (`mcp.go`), and the call-tool (`/mcp/call`, `/mcp/p/<slug>`) and code-exec (`/mcp/code`) mode servers (`mcp_routing.go`). It is **not** registered on direct mode (`/mcp/all`) and is not callable from `code_execution` JavaScript.

**Visible and callable only by** an admin caller (spec Definitions): credential kind `api_key` or `socket`, not anonymous, with an effective profile that is none or `management_tools: true`. For every other caller the tool is absent from `tools/list`, and a forged `tools/call` answers `isError: true` with the text `unknown tool: credentials`.

## Tool definition

```json
{
  "name": "credentials",
  "description": "Administrator tool to issue, inspect and revoke worker credentials: custom client credentials (create_client) and profile-pinned agent tokens (create_token). Every credential is bound to an existing profile with an explicit expiry; the secret is returned once, in the create result only, and never by list/get. Writes are refused under read_only_mode or disable_management and by the binding guard (binding_bypassable_without_auth); errors are JSON {code, error, field?}.",
  "annotations": {
    "title": "Issue and revoke worker credentials",
    "readOnlyHint": false,
    "destructiveHint": true,
    "idempotentHint": false,
    "openWorldHint": false
  },
  "inputSchema": {
    "type": "object",
    "properties": {
      "operation":    { "type": "string", "enum": ["list", "get", "create_client", "create_token", "revoke"], "description": "Operation to run." },
      "client":       { "type": "string", "description": "create_client, get, revoke: custom client id (lower-case letters, digits, '-' or '_', at most 56 characters)." },
      "token":        { "type": "string", "description": "get, revoke: agent token name." },
      "name":         { "type": "string", "description": "create_token: new token name (letters, digits, '_' or '-'; not starting with client-)." },
      "display_name": { "type": "string", "description": "create_client: display name, at most 64 characters." },
      "profile":      { "type": "string", "description": "create_client, create_token: existing profile to lock or pin to (required, never All servers). list: keep only this profile." },
      "mode":         { "type": "string", "enum": ["locked", "switchable"], "description": "create_client: binding mode; omitted = locked." },
      "expires_in":   { "type": "string", "description": "create_client, create_token: required lifetime such as 30m, 4h or 7d; at most 365d. At most 24h is shown as a task lease." },
      "purpose":      { "type": "string", "description": "create_client, create_token: optional task brief and assumptions shown to the user, at most 500 characters; not enforced." },
      "kind":         { "type": "string", "enum": ["client", "token", "all"], "description": "list: credential kind; omitted = all." },
      "state":        { "type": "string", "enum": ["active", "expired", "revoked", "all"], "description": "list: credential state; omitted = all." }
    },
    "required": ["operation"],
    "additionalProperties": false
  }
}
```

**Secret-shaped input screen (FR-020a).** Before anything else, including operation dispatch, the type check and the unknown-key check, the size of the whole payload is checked (over 16 KiB of canonical JSON answers `arguments_too_large` and nothing of the payload is retained, data-model §8), and then the whole argument payload is screened as in data-model §8 by a screening detector whose enablement, request scanning, categories and payload limit are forced on regardless of `sensitive_data_detection`: every key and every value at every depth, whatever its JSON type, and every hit is collected. A hit answers `secret_in_argument` with `field` = the first offending known argument name in sorted order, or `"(unknown argument)"` when only unknown arguments offend, writes nothing, and never echoes a caller-supplied key or value.

Arguments not listed for an operation are refused with `invalid_argument`. `field` names the first offending key in sorted order only when it is a known argument name of the tool (listed for another operation); for any other key it is the fixed placeholder `"(unknown argument)"`, so a caller-supplied key is never echoed. Every argument is a string. A non-string value answers `invalid_argument` with `"must be a string"`.

## Operations

### `list`

Args: `kind?`, `profile?`, `state?`. Allowed under read_only_mode and disable_management.

```json
{ "credentials": [ CredentialView, ... ], "total": 2 }
```

Rows are sorted by `kind`, then `id`. `CredentialView` is defined in data-model.md §2. Tokens are listed from the **ownerless** namespace only (`UserID == ""`, data-model §9, spec review r5): a server-edition tenant-owned token is never listed or counted, so every listed name is addressable by `get` and `revoke`. Server edition: `kind=client` answers `{"credentials":[],"total":0}`, and `kind=all` lists ownerless tokens only.

### `get`

Args: exactly one of `client` or `token`. If both or neither are given, the call answers `invalid_argument` (field `client`).

```json
{ "credential": CredentialView, "links": Links }
```

An unknown identity answers `identity_not_found`. `token` resolves in the ownerless namespace only (`GetAgentTokenByOwnerAndName("", name)`); a name held only by a tenant-owned token answers `identity_not_found` with the same text.

### `create_client`

Args: `client` (required), `profile` (required), `expires_in` (required), `mode?`, `display_name?`, `purpose?`.

Checks run in order under `bindingWriteMu`. The first one that fails is returned, and nothing is written:

0. size cap (`arguments_too_large`) and secret-shaped input screen (`secret_in_argument`), run by the handler over the whole payload before the service is called; the service then screens the persisted fields again as its own first step (data-model §8.2), which is the same check REST and CLI issuance go through
1. read_only_mode, then disable_management
2. edition supports clients (`unsupported_edition`)
3. `client` id syntax (`invalid_argument`), then a connect-registry id (`reserved_identity`)
4. `profile` missing (`missing_argument`), empty (`profile_required`), unknown (`unknown_profile`)
5. `expires_in` missing (`missing_argument`), invalid (`invalid_expiry`)
6. `mode` and `display_name` and `purpose` length (`invalid_argument`)
7. an existing ownerless record of any state (`identity_exists` with `state`), or a regular ownerless token holding `client-<id>` (`identity_exists`, `state: "conflicting_token"`); tenant-owned tokens are outside this namespace (data-model §9)
8. binding guard (`binding_bypassable_without_auth`)
9. mint (one bbolt tx). This is the commit point. The `CredentialView` is projected from the committed record returned by the mint, with no further store read. Then, best-effort and unable to fail the call: `profile_change{change: issue}`, `credentials.changed`, `client.binding_changed`. After the commit the call always returns the delivery response

Result: see [One-time delivery response](#one-time-delivery-response), with key `client`.

### `create_token`

Args: `name` (required), `profile` (required), `expires_in` (required), `purpose?`.

The checks are the same as for `create_client`, with these differences:
- name syntax per `tokenNameRegex`. A `client-` prefix answers `reserved_identity`.
- the token cap answers `token_limit_reached`
- the duplicate precheck looks up the name in the ownerless namespace only (`GetAgentTokenByOwnerAndName("", name)`); a tenant-owned token of the same name is not a conflict (data-model §9)
- the guard runs with the candidate as a locked binding, and the minted token is stored with `guard_bound: true`, so it stays a binding in every later guard evaluation (FR-012a, data-model §5)

The token is minted with `allowed_servers: ["*"]`, `permissions: [read, write, destructive]` and `profile_pin: <profile>`.

Result: the delivery response with key `token`.

### `revoke`

Args: exactly one of `client` or `token`. Refused under read_only_mode and disable_management.

```json
{ "credential": CredentialView, "changed": true, "client_config_untouched": true }
```

`client_config_untouched` is present for clients only. `token` is revoked through `RevokeAgentTokenForOwner("", name)`, so it can only ever revoke the ownerless token of that name; a name held only by tenant-owned tokens answers `identity_not_found` and revokes nothing (data-model §9). If the identity is already revoked, the call answers `changed: false` and writes no record. An unknown identity answers `identity_not_found`. A connect in flight for that client does **not** block revocation (spec review r3): revoke goes through the existing `forgetLockedOpt` path, which succeeds immediately and invalidates both the current secret and any secret staged by the in-flight connect. The outstanding connect then fails closed at commit with the existing `credential_superseded` error and cannot restore access. This preserves the deliberate behaviour pinned by `TestConnectMinter_ForgetDuringConnectFailsCommitClosed` (`internal/runtime/clients_service_inflight_test.go`).

## One-time delivery response

```json
{
  "client": CredentialView,
  "credential": "mcp_cli_3f9c…",
  "snippet": {
    "generic_http": "{\"mcpServers\":{\"mcpproxy\":{\"url\":\"http://127.0.0.1:8080/mcp\",\"headers\":{\"X-API-Key\":\"mcp_cli_3f9c…\"}}}}",
    "header_name": "X-API-Key"
  },
  "delivery": {
    "shown_once": true,
    "endpoint": "http://127.0.0.1:8080/mcp",
    "header_name": "X-API-Key",
    "alternate_header": "Authorization: Bearer <credential>",
    "install_note": "mcpproxy did not install this credential anywhere. Give it to the worker over a channel you trust and add it to that worker's MCP client config; it cannot be shown again. Revoke it with credentials revoke when the task ends."
  },
  "links": Links
}
```

For `create_token` the first key is `token`. Everything else is identical.

`Links` holds absolute UI URLs built from the listen address, plus `ui_path` forms relative to the UI root. No URL contains `apikey` or any credential.

```json
{
  "identity":        "http://127.0.0.1:8080/ui/clients?client=delegated-worker",
  "profile":         "http://127.0.0.1:8080/ui/profiles/daily-research",
  "effective_tools": "http://127.0.0.1:8080/ui/profiles/daily-research?tab=tools&reason=callable",
  "activity":        "http://127.0.0.1:8080/ui/activity?client=delegated-worker",
  "ui_path": { "identity": "/clients?client=delegated-worker", "...": "..." }
}
```

Tokens use `identity: …/ui/clients?tab=tokens&token=<name>` and `activity: …/ui/activity?token=<name>`.

## Activity body of a `credentials` call

`internal_tool_call` record:

- `arguments`: for a call that passed the screen, the request arguments as received (every key and value at every depth already passed it). For a call refused by the screen, the arguments are **replaced wholesale** by the server-built summary of data-model §8 (`_screened`, `operation` only when it is a known enum value, `offending_fields`, `unknown_offending_count`); no caller key or value is kept, so a second, nested or key-borne secret cannot survive. The record is built only from that form.
- `response` on a successful create: the delivery object with `credential` and `snippet` replaced by the string `"[REDACTED: one-time credential]"`, and `delivery` reduced to `{shown_once, endpoint, header_name}`. This body is built by the server, not derived by key-name redaction.
- `response` on list, get or revoke: the result as-is, which contains no secret.
- `status: error` with the structured error text on refusal.
