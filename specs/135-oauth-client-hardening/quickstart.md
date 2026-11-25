# Quickstart: verifying Spec 135

## Automated gates

Run each package whole, not filtered by test name, so that every gate named in the plan's test table runs:

```bash
go list -m github.com/mark3labs/mcp-go            # must be >= v1.4.0 (FR-019)
go test -race ./internal/oauth/... ./internal/upstream/core/... ./internal/storage/... ./internal/config/... ./internal/diagnostics/... ./internal/management/...
go test -race -tags server ./internal/serveredition/broker/... ./internal/serveredition/api/...
cd frontend && npx vitest run                  # skip_issuer_validation editor field, issuer status line
```

These suites contain: the last-hop guard, the final-token-form and extras-classification tests, the broker redirect test, the callback matrix, the resolver tests, the surface goldens and the AST guards.

## End-to-end

```bash
go build -o mcpproxy ./cmd/mcpproxy
./scripts/test-oauth-client-hardening-e2e.sh   # fake AS: login, mix-up, swap, warm-handler swap, redirect refusal
./scripts/test-api-e2e.sh
```

**Upgrade (trust on first use) check.** This is separate from `scripts/test-oauth-token-encryption-e2e.sh`, which logs in with the binary under test and is only a Spec 123 regression run. `test-oauth-client-hardening-e2e.sh --upgrade`:

1. seeds `config.db` with pre-upgrade rows (no binding fields), one plaintext and one `enc:v1`, plus one row at the seal limit and one path-issuer fallback row;
2. starts the new binary;
3. asserts: no re-login; bindings written; sealed rows still sealed; the seal-limit row reports `MCPX_OAUTH_REAUTH_REQUIRED`; the fallback row reports `MCPX_OAUTH_DISCOVERY_FAILED` and is not bound.

## Real-provider matrix (SC-004)

For each provider (Cloudflare, Smithery, GitHub MCP, Notion, Atlassian, Sentry), record the following in `verification/providers.md`:

- metadata URL used, declared issuer, whether they match
- `authorization_response_iss_parameter_supported`, whether `iss` arrived and whether it matched
- whether metadata needs a cross-origin redirect
- DCR answer to `application_type`
- outcome in strict mode and outcome with `skip_issuer_validation: true`

| Provider shape | Expected strict | Expected with opt-out |
|---|---|---|
| consistent metadata and `iss`, no cross-origin redirect | signs in, `mix_up_protected` true if `iss` sent | signs in, `mix_up_protected` false |
| metadata issuer or `iss` mismatch only | `METADATA_ISSUER_MISMATCH` / `ISSUER_MISMATCH` | signs in, warning `issuer_validation_disabled` |
| metadata behind a cross-origin redirect | `METADATA_ISSUER_MISMATCH` | `METADATA_ISSUER_MISMATCH` |

Commands for one provider:

```bash
mcpproxy auth login --server=<name>
mcpproxy auth status --server=<name>       # issuer origin, source, mix_up_protected
mcpproxy doctor --output=json | jq '.oauth_issuer_status'
```
