# Data Model: Harden the OAuth Client Role (Spec 135)

## ExpectedIssuer (in memory, per pending flow)

| Field | Type | Rule |
|---|---|---|
| `Issuer` | string | The `issuer` of the metadata document used to build the authorization URL. Empty only for `synthetic` with no document. |
| `Source` | enum `discovered` \| `configured` \| `synthetic` | `configured` when `oauth.auth_server_metadata_url` is set. `synthetic` when the transport wrapper made the document up, or mcp-go fell back to origin-derived endpoints. `discovered` otherwise. |
| `IssSupported` | bool | `authorization_response_iss_parameter_supported` from the same document. |

Lives with the callback waiter (`callbackWaiter{ch, exp, skip}`) and the flow's state and verifier. Never persisted. It is dropped when the waiter is used up or times out.

## OAuthTokenRecord (bbolt `oauth_tokens`, existing; two new fields)

| Field | JSON | Rule |
|---|---|---|
| `Issuer` | `issuer,omitempty` | The issuer that issued the stored token and/or DCR registration. Written by login, token-store saves and the TOFU bind. Never cleared by a token save. |
| `IssuerSource` | `issuer_source,omitempty` | Same enum as `ExpectedIssuer.Source`. |

- Both fields sit inside the record body. Under Spec 123 they are sealed with the rest, and the additional data (kid ‖ bucket ‖ bucket key) does not change.
- An empty `Issuer` means *unbound* (a legacy row, or one rewritten by an older binary).
- The record keeps its bucket key: `GenerateServerKey(name, url)` is unchanged.

### Binding states (derived at use time)

```
unbound ──(use with discovered/configured issuer)──► bound(I)
bound(I) ──(use, current issuer == I)──► bound(I)
bound(I) ──(use, current issuer J ≠ I)──► changed(I→J)   [no request sent; MCPX_OAUTH_ISSUER_CHANGED]
changed(I→J) ──(explicit login against J)──► bound(J)      [row replaced in one write]
any ──(row undecryptable, Spec 123)──► undecryptable          [checked first; issuer unknown]
```

`changed` is not stored. It is recomputed from the row and the current discovery each time, so it clears itself if the upstream goes back to `I`.

## Config

| Path | Type | Default | Notes |
|---|---|---|---|
| `mcpServers[].oauth.skip_issuer_validation` | bool | false | Per server. Skips FR-003, FR-005 and FR-006. Not part of `OAuthConfigChanged`. Hot-reloadable. |
| `mcpServers[].auth_broker.issuer` (server edition) | string | "" | Enables the `iss` check on the broker connect callback. |

## Diagnostics report (doctor / `GET /api/v1/diagnostics`)

New `issuer_binding` object inside `oauth_token_storage` (see `contracts/doctor-issuer-binding.json`).
