---
paths: "**/*_test.go, tests/**, scripts/*test*, scripts/*e2e*"
---

# Testing

## Quick Commands

```bash
./scripts/test-api-e2e.sh         # Required before commits
./scripts/run-all-tests.sh        # Full test suite
./scripts/verify-oas-coverage.sh  # After REST endpoint changes
go test ./internal/... -v         # Unit tests
go test -race ./internal/... -v   # With race detection
```

## E2E Requirements

- Node.js and npm (for `@modelcontextprotocol/server-everything`)
- `jq` for JSON parsing
- Built binary: `go build -o mcpproxy ./cmd/mcpproxy`

## Test Failure Investigation

- Check `/tmp/mcpproxy_e2e.log` for server logs
- Look for "Everything server is connected!"
- Ensure no port conflicts on 8081

## Patterns

- Unit tests alongside source (`*_test.go`)
- E2E tests in `internal/server/e2e_test.go`
- Use testify for assertions and mocking
- Test both success and error cases
- Coverage: `go test -coverprofile=coverage.out ./internal/...`

## OAuth Testing

```bash
./scripts/run-oauth-e2e.sh                                    # OAuth E2E suite
go test ./tests/oauthserver/... -v                            # OAuth server tests
OAUTH_INTEGRATION_TESTS=1 go test ./tests/oauthserver/... -v  # Integration tests
```
