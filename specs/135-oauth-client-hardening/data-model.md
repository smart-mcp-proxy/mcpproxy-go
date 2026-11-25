# Data Model: Harden the OAuth Client Role (Spec 135)

## ExpectedIssuer (in memory, per pending flow)

| Field | Type | Rule |
|---|---|---|
| `Issuer` | string | The `issuer` of the metadata document used to build the authorization URL. Empty only for `synthetic` with no document. |
| `Source` | enum `discovered` \| `configured` \| `synthetic` | `configured` when `oauth.auth_server_metadata_url` is set. `synthetic` when the transport wrapper made the document up, or mcp-go fell back to origin-derived endpoints. `discovered` otherwise. |
| `IssSupported` | bool | `authorization_response_iss_parameter_supported` from the same document. |
| `Target` | string | The discovery target: the PRM `authorization_servers` entry, or the configured metadata URL. It is the binding identity when `skip_issuer_validation` is on (FR-013). |

Lives with the callback waiter (`callbackWaiter{ch, exp, skip}`) and the flow's state and verifier. Never persisted. It is dropped when the waiter is used up or times out.

## OAuthTokenRecord (bbolt `oauth_tokens`, existing; three new fields)

| Field | JSON | Rule |
|---|---|---|
| `Issuer` | `issuer,omitempty` | The issuer that issued the stored token and/or DCR registration. Under skip, the discovery target. Under synthetic, the synthetic issuer (never the callback's `iss`). Written by the login, the DCR write, token-store saves and the TOFU bind. Never cleared by a token save. A DCR write for a different issuer clears the old tokens in the same transaction (FR-012). |
| `IssuerSource` | `issuer_source,omitempty` | Same enum as `ExpectedIssuer.Source`. |
| `MixUpProtected` | `mix_up_protected,omitempty` | True when the login that wrote the row received an `iss` equal to the expected issuer. Shown in surfaces (FR-018). |

- All three fields sit inside the record body. Under Spec 123 they are sealed with the rest, and the additional data (kid ‖ bucket ‖ bucket key) does not change.
- An empty `Issuer` means *unbound* (a legacy row, or one rewritten by an older binary).
- The record keeps its bucket key: `GenerateServerKey(name, url)` is unchanged.

### Binding states (derived at use time)

```
unbound ──(use with validated discovered/configured issuer; tx checks revision + empty issuer)──► bound(I)
bound(I) ──(use, current issuer == I)──► bound(I)
bound(I) ──(discovery resolves J ≠ I)──► changed(I→J)   [no credential sent; access token not attached; MCPX_OAUTH_ISSUER_CHANGED]
changed(I→J) ──(DCR registration with J)──► bound(J), registration only   [I's tokens cleared in the same tx]
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
