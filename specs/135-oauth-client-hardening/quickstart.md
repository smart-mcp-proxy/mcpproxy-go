# Quickstart: verifying Spec 135

```bash
# unit + integration against the fake authorization server
go test -race ./internal/oauth/... ./internal/upstream/core/... ./internal/storage/... -run 'Iss|Issuer|Swap|Upgrade|ApplicationType'
go test -race -tags server ./internal/serveredition/broker/... -run Iss
# end to end with the built binary
go build -o mcpproxy ./cmd/mcpproxy && ./scripts/test-oauth-client-hardening-e2e.sh
./scripts/test-oauth-token-encryption-e2e.sh   # sealed rows survive the TOFU bind
./scripts/test-api-e2e.sh
```

Manual check against a real provider:
1. `mcpproxy auth login <server>`; `mcpproxy auth status <server>` shows `issuer: https://… (discovered)`.
2. `mcpproxy doctor` lists `issuer_binding.bound` ≥ 1 and no `issuer_validation_disabled` warning.
3. Record the provider's `authorization_response_iss_parameter_supported`, whether `iss` arrived, and the DCR answer in `verification/providers.md`.
