# Contract: diagnostics codes (Spec 135)

| Code | When | Health level / action | User-facing text (summary) | Docs page |
|---|---|---|---|---|
| `MCPX_OAUTH_ISSUER_MISMATCH` | Authorization response `iss` wrong, or missing while the issuer advertises support (FR-003, FR-005) | recorded OAuth failure; `login` | "The sign-in response could not be confirmed as coming from <expected issuer origin>. Nothing was signed in." | `docs/errors/MCPX_OAUTH_ISSUER_MISMATCH.md` |
| `MCPX_OAUTH_ISSUER_CHANGED` | Stored token/registration bound to issuer A, a validated snapshot now resolves B (FR-009) | unhealthy; `login` | "<server> now uses a different authorization server (<A origin> → <B origin>). Sign in again to use it." | `docs/errors/MCPX_OAUTH_ISSUER_CHANGED.md` |
| `MCPX_OAUTH_METADATA_ISSUER_MISMATCH` | Metadata `issuer` differs from the URL it was fetched for (FR-006) | unhealthy; `configure` | "<server>'s authorization server metadata names another issuer. Check the server, or set oauth.skip_issuer_validation if the provider is known to misreport it." | `docs/errors/MCPX_OAUTH_METADATA_ISSUER_MISMATCH.md` |

Rules:
- The received `iss` value is never rendered in a browser page, REST `detail`, or tray text (attacker-controlled). Logs carry it through `ScrubUpstreamText`.
- Issuers are shown as origins (`scheme://host[:port]`) in user-facing text; the full value is in REST `detail` for `ISSUER_CHANGED` (both values come from metadata that passed FR-006).
- Each error page follows the existing `docs/errors/*.md` shape (what happened, why, what to do) and is covered by the codes-to-docs test.
