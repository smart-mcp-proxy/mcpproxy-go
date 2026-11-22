# Contract: public OAuth endpoints

`<origin>` is the scheme and host of the request. All endpoints below answer only when the feature is on, the peer and the Host are loopback, and the build is the personal edition. Otherwise see "Gate".

## Gate

| Feature | Ever used | `/mcp/oauth*` | `/oauth/*`, `/.well-known/oauth-*` |
|---|---|---|---|
| off | no | previous behaviour (the `/mcp/` catch-all) | previous behaviour (the root handler) |
| off | yes | `404` | `404` |
| on | any | served | served |

Non-loopback peer or Host, or server edition, with the feature on: `404`.

## Protected resource

`ANY /mcp/oauth` and `ANY /mcp/oauth/{client}` — the MCP endpoint, same surface as `/mcp`.

- No or invalid bearer token → `401`, `WWW-Authenticate: Bearer resource_metadata="<origin>/.well-known/oauth-protected-resource/mcp/oauth[/{client}]"`.
- `{client}` not a known client id → `404` (also on its metadata).
- Accepts only `Authorization: Bearer mcp_oat_…`. `X-API-Key`, `?apikey=`, `mcp_cli_`, `mcp_agt_` and the admin key → `401`.
- Token for another resource or origin → `401`.

## `GET /.well-known/oauth-protected-resource/mcp/oauth[/{client}]`

```json
{
  "resource": "<origin>/mcp/oauth/cursor",
  "authorization_servers": ["<origin>"],
  "bearer_methods_supported": ["header"]
}
```

## `GET /.well-known/oauth-authorization-server`

```json
{
  "issuer": "<origin>",
  "authorization_endpoint": "<origin>/oauth/authorize",
  "token_endpoint": "<origin>/oauth/token",
  "registration_endpoint": "<origin>/oauth/register",
  "response_types_supported": ["code"],
  "grant_types_supported": ["authorization_code", "refresh_token"],
  "code_challenge_methods_supported": ["S256"],
  "token_endpoint_auth_methods_supported": ["none"]
}
```

## `POST /oauth/register` (RFC 7591)

Request (JSON, at most 16 KiB): `redirect_uris` (required), `client_name`, `software_id`, `software_version`, optional `token_endpoint_auth_method`, `grant_types`, `response_types`.

- `201`: `client_id`, `client_id_issued_at`, `redirect_uris`, `client_name` (sanitised), `token_endpoint_auth_method: "none"`, `grant_types: ["authorization_code","refresh_token"]`, `response_types: ["code"]`. No `client_secret`.
- `400 invalid_redirect_uri`: not loopback `http` and not `https`, has a fragment, more than 5.
- `400 invalid_client_metadata`: an auth method other than `none`, a grant type or response type outside the lists above.
- `429` with `Retry-After`.

## `GET /oauth/authorize`

Query: `response_type=code`, `client_id`, `redirect_uri`, `code_challenge`, `code_challenge_method=S256`, `resource`, `state` (optional), `scope` (ignored).

- Unknown `client_id` or `redirect_uri` not registered (loopback port ignored) → `400` HTML error page, **no redirect**.
- `resource` is `<origin>/mcp/oauth/{client}` with a client id that does not exist → `404` HTML page, **no redirect** (FR-015). Checked before the general `resource` rule.
- Other errors → redirect to `redirect_uri` with `error` (`invalid_request`, `unsupported_response_type`, `invalid_target`, `temporarily_unavailable`) and `state`.
- Any other `resource` that is not `<origin>/mcp/oauth` or `<origin>/mcp/oauth/{known client}` → `invalid_target`.
- Success → `302` to `<origin>/ui/authorize?request=<id>`. Headers on every answer: `Cache-Control: no-store`, `Referrer-Policy: no-referrer`.

After the administrator decides (admin REST), the browser is sent to:

- approve: the stored redirect URI with `code` added
- deny: the stored redirect URI with `error=access_denied` added

Every redirect (these two and the error redirects above) is built by parsing the stored redirect URI, keeping its existing query, and appending percent-encoded parameters. `state` is added only if the client sent one, with its value unchanged.

## `POST /oauth/token`

Form-encoded, at most 16 KiB. Answers carry `Cache-Control: no-store`.

The origin of the token request must equal the issuer stored with the code or the grant. This is checked first, for both grant types: on a mismatch the answer is `invalid_grant` and nothing is consumed, rotated or revoked.

The authorization-code exchange is where the login is stored (data-model, research R15). If the client's binding changed since approval, or the binding guard now refuses it, the answer is `invalid_grant` and nothing is stored.

**`grant_type=authorization_code`**: `code`, `redirect_uri`, `client_id`, `code_verifier`, `resource` (optional; if present must equal the approved one).

**`grant_type=refresh_token`**: `refresh_token`, `client_id`, `resource` (optional, same rule).

`200`:

```json
{ "access_token": "mcp_oat_…", "token_type": "Bearer", "expires_in": 3600, "refresh_token": "mcp_ort_…" }
```

Errors (`400` JSON `{error, error_description}`):

| Case | `error` | Side effect |
|---|---|---|
| unknown, expired or already used code; wrong verifier, client or redirect URI | `invalid_grant` | a code reused within 10 minutes revokes the grant it produced |
| request origin differs from the stored issuer | `invalid_grant` | none |
| binding changed or guard refuses at exchange | `invalid_grant` | none; `deny` activity row |
| request cancelled after approval (client revoked or forgotten, feature turned off) | `invalid_grant` | none |
| registration no longer exists, or the new client id chosen at approval now exists | `invalid_grant` | none |
| refresh token malformed, bad MAC, unknown, revoked or expired grant, wrong `client_id` | `invalid_grant` | **none**; all indistinguishable; an expired login is never extended |
| authentic refresh token that is not the current one | `invalid_grant` | grant revoked (`replay`) |
| `resource` differs from the approved one | `invalid_target` | none |
| other grant type | `unsupported_grant_type` | none |
| over the rate limit | `429` + `Retry-After` | none |

## Token formats

- Access: `mcp_oat_<43 base64url chars>`; lifetime 3600 s.
- Refresh: `mcp_ort_<grant id>_<secret>_<mac>`; lifetime 30 days from last rotation.
