# Contract: diagnostics codes (Spec 135)

| Code | When | Health level / action | User-facing text (summary) | Docs page |
|---|---|---|---|---|
| `MCPX_OAUTH_ISSUER_MISMATCH` | The authorization response's `iss` is wrong (an empty value counts as wrong), or is missing while the issuer advertises support (FR-003, FR-004a, FR-005) | recorded OAuth failure; `login` | Callback page: "The sign-in response could not be confirmed as coming from the expected authorization server (`<expected issuer origin>`). Nothing was signed in. Start the sign-in again from mcpproxy for '`<server>`'." | `docs/errors/MCPX_OAUTH_ISSUER_MISMATCH.md` |
| `MCPX_OAUTH_ISSUER_CHANGED` | A credential bound to binding B, and the current snapshot B' fails `Binding.Matches` (FR-009, FR-012, FR-013) | unhealthy; **`login`** for DCR rows and public static clients, **`configure`** for a confidential static client credential | login: "`<server>` now uses a different authorization server (`<B origin>` → `<B' origin>`). Sign in again to use it." configure: "… The configured client credentials belong to `<B origin>`. Update `oauth.client_secret` (or the assertion) for the new server, or revert the upstream." | `docs/errors/MCPX_OAUTH_ISSUER_CHANGED.md` |
| `MCPX_OAUTH_METADATA_ISSUER_MISMATCH` | The metadata `issuer` is missing, ambiguous, or differs from the issuer its candidate was derived from (FR-006); or the metadata fetch hit a cross-origin redirect (FR-006a) | unhealthy; `configure` | issuer differs: "`<server>`'s authorization server metadata names another issuer. Check the server, or set `oauth.skip_issuer_validation` if the provider is known to misreport it." Missing, ambiguous or redirect: "… The metadata could not be authenticated (`<reason>`). This cannot be overridden." | `docs/errors/MCPX_OAUTH_METADATA_ISSUER_MISMATCH.md` |

Existing codes reused: `MCPX_OAUTH_DISCOVERY_FAILED` (a legacy path-issuer fallback refused, US3.8) and `MCPX_OAUTH_REAUTH_REQUIRED` (a seal-limit row, FR-011a). Both are in `internal/diagnostics/codes.go:33-36`.

Rules:
- The received `iss` value is never rendered on a browser page, in REST `detail`, or in tray text, because it is attacker-controlled. Logs carry it through `ScrubUpstreamText`.
- Issuers appear **only as origins** (`scheme://host[:port]`) in every user-facing surface, REST `detail` included. A configured or opted-out issuer has not passed FR-006, so it is not shown in full.
- Each error page follows the existing `docs/errors/*.md` shape (what happened, why, what to do). A test asserts that each page exists on disk, because the existing catalog test only checks URL strings.
