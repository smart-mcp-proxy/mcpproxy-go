# Data Model: Harden the OAuth Client Role (Spec 135)

## Binding (value type)

A binding is `{issuer, target, source}`. A **write** that decides whether to clear an old grant uses `Binding.Equal`, which compares all three fields regardless of skip. A **send** is checked with `Binding.Matches(stored, current, skip)`, which compares:

- **source class** (`fallback` versus every other source): always (FR-004a, FR-013).
- **target**: always.
- **issuer**: only when `skip` is off. Under skip the declared issuer is still **stored**, so strict mode can be restored without losing continuity.

| Source | `issuer` | `target` |
|---|---|---|
| `discovered` (PRM names an AS) | validated declared issuer | the PRM `authorization_servers[0]` entry |
| `discovered` (no PRM AS) | validated declared issuer | the issuer form of the MCP URL that the successful candidate was derived from |
| `configured` | declared issuer | the configured `oauth.auth_server_metadata_url` |
| `fallback` (FR-004a, origin issuers only) | the discovery-selected issuer | same |
| `synthetic` (operator overrides) | synthetic issuer (origin of the authorization endpoint) | same |

## Snapshot (in memory, immutable)

`oauth.ResolveASMetadata` produces it (plan D0). Each pending flow captures one at authorization time (FR-002).

| Field | Rule |
|---|---|
| `Binding` | as above |
| `AuthorizationEndpoint`, `TokenEndpoint`, `RegistrationEndpoint` | exact URLs. Every POST through a guarded client must carry a marked operation (`SendContext.Op`). Its URL must equal the endpoint for **that** operation. An unmarked or unknown operation fails closed. |
| `IssSupported` | `authorization_response_iss_parameter_supported` from the document |
| `Verified` | `configured` only: the configured URL equals a candidate derived from the declared issuer (FR-008) |
| `Skipped` | the metadata comparison was skipped under the opt-out |
| `Insecure` | non-loopback `http` issuer or endpoint (FR-006a) |
| `Registration` (flow capture only) | client id and the provenance of each client-auth credential the flow will send |

The callback waiter holds `{ch, snapshot, skip}`. It is registered before the authorization URL is exposed and is never persisted.

## Callback decision (in memory)

`DecideAuthorizationResponseIssuer(q, snapshot, skip) → (protected bool, err)`. `protected` is true only when **all** of these hold:

- `iss` was present and equal;
- the source is not `synthetic`;
- `Insecure` is false (a non-loopback `http` issuer or endpoint voids the claim, FR-006a);
- `Skipped` is false;
- for `configured`, `Verified` is true.

The persisted value is `protected`, not a raw string match.

## OAuthTokenRecord (bbolt `oauth_tokens`, existing; four new fields)

| Field | JSON | Rule |
|---|---|---|
| `Issuer` | `issuer,omitempty` | `Binding.issuer` |
| `DiscoveryTarget` | `discovery_target,omitempty` | `Binding.target` |
| `IssuerSource` | `issuer_source,omitempty` | `Binding.source` |
| `MixUpProtected` | `mix_up_protected,omitempty` | the callback decision's `protected` for the login that wrote the row. Cleared by any write that replaces the grant. |

- All four fields sit inside the record body. Under Spec 123 they are sealed with the rest, and the additional data (kid ‖ bucket ‖ bucket key) is unchanged. A record without them marshals to the same bytes as today.
- A row with all binding fields empty is *unbound*: a legacy row, or one last rewritten by an older binary through an ordinary save. Spec 123 envelope-only rewrites keep the fields. A row with some but not all of `Issuer`, `DiscoveryTarget` and `IssuerSource` set is refused as corrupt (`MCPX_OAUTH_REAUTH_REQUIRED`), never treated as unbound.

### Row transitions

```
unbound ──(first use, incl. serving an unexpired token; snapshot valid)──► bound(B)
          one tx: CAS(revision, Spec 116 incarnation+epoch, binding empty)
                  + create absent confidential-client provenance (FR-011, FR-012)
          CAS loss: reload, FR-009 against own snapshot, proceed only on match, else abort
unbound ──(discovery fails, incl. path-issuer fallback refused)──► unbound
          no write, no credential released; MCPX_OAUTH_DISCOVERY_FAILED
unbound ──(re-seal would exceed 1 MiB, FR-011a)──► unbound
          tx rolled back, no plaintext write, no release; MCPX_OAUTH_REAUTH_REQUIRED, size-only log
bound(B) ──(snapshot S with Matches(B, S); under skip possibly !Equal)──► bound(B)
          refresh continues under the full stored B; the unchecked issuer is never adopted
          a login reuses the stored registration only when Equal(B, S); otherwise it uses fresh DCR
          or an operator-configured public client id; any class-4 credential failing provenance refuses it
bound(B) ──(snapshot B' with !Matches(B, B'))──► changed(B→B')
          nothing sent, access token not attached; MCPX_OAUTH_ISSUER_CHANGED
changed / bound(B) ──(first write bound to B' with !Equal — issuer, target, source class, regardless of skip: DCR registration or login)──► bound(B')
          one tx: replace the registration, clear the old tokens and MixUpProtected
          login: CAS on the per-server OAuth write counter observed at flow start (moved by every
          write and delete of the row), or the row's binding Equal to the flow's; else discarded
any ──(row undecryptable, Spec 123)──► undecryptable   [checked first]
```

`changed` is not stored. It is recomputed from the row and the current snapshot, so it clears itself if the upstream returns to the bound target.

## Static client provenance (bbolt `oauth_static_client_bindings`, new)

| Key | Value |
|---|---|
| durable server identity (Spec 116 incarnation, **not** `GenerateServerKey`, which includes the URL) ‖ `sha256(credential)` | `Binding` of that credential's first validated use |

- **One entry per effective client-authentication credential.** These are the FR-010b class 4 keys: `client_secret`, `client_assertion`, `assertion` and their normalised aliases, plus names the sensitive matcher classifies as credential-bearing, taken from `oauth.*` or `oauth.extra_params` after precedence. `client_assertion_type` and client ids get no entry.
- **Created or compared** atomically under the current Spec 116 binding, before the first send of that credential, and in the same transaction as a legacy row's binding (FR-011).
- **Survives** logout, grant clears (a scopes edit, for example), client-id-only edits (by the operator or by DCR), and URL edits that keep the incarnation.
- **Removed** when a credential's effective value changes and no other effective class-4 field still has the old value (entries are per fingerprint) and when the server is deleted or replaced (all entries).
- **Sealed** under Spec 123 when a key is set.

## Extras classes (FR-010b)

Keys are normalised as in `internal/oauth/logging.go:376-419`. The first match wins.

| # | Class | Members | Auth URL / probe / logs | Token form |
|---|---|---|---|---|
| 1 | flow/row-owned | `code`, `code_verifier`, `code_challenge`, `code_challenge_method`, `refresh_token`, `access_token`, `id_token`, `state`, `redirect_uri`, `grant_type`, `response_type`, `scope`, and their normalised aliases | dropped, value-free WARN | dropped, value-free WARN |
| 2 | legacy `client_id` | `client_id` | folded into the effective registration | folded into the effective registration |
| 3 | `client_assertion_type` | itself | never | sent, no provenance |
| 4 | client-auth credentials | `client_secret`, `client_assertion`, `assertion` and their aliases, then the normalised sensitive matcher | never | sent, provenance-checked by the last-hop guard |
| 5 | plain | everything else | as today | as today |

## Config

| Path | Type | Default | Notes |
|---|---|---|---|
| `mcpServers[].oauth.skip_issuer_validation` | bool | false | Per server; see `contracts/config-skip-issuer-validation.md`. Validated as a bool. Non-secret `ServerFieldMaskDecisions` entry (`internal/oauth/serverfields.go:221`). Not part of `OAuthConfigChanged`. Hot-reloadable. |
| `mcpServers[].auth_broker.issuer` (server edition) | string | "" | Turns on the `iss` check on the broker connect callback (US7). |

## Diagnostics / per-server status

See `contracts/doctor-issuer-binding.json`. The same per-server fields are added to the existing scoped server-list projection (`GET /api/v1/servers`, `contracts.Server`). That projection is the one source for `mcpproxy auth status --server=<name>` (`cmd/mcpproxy/auth_cmd.go:332-351`), Web UI ServerDetail (`frontend/src/stores/servers.ts`) and the macOS detail (`APIClient.swift`). No new detail route is added.
