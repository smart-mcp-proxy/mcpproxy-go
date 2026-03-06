# Quickstart: Agent Tokens Development

## Prerequisites

- Go 1.24+
- Node.js 18+ (for frontend)
- Built mcpproxy binary: `make build`

## Development Setup

```bash
# Switch to feature branch
git checkout 028-agent-tokens

# Build
make build

# Run with debug logging
./mcpproxy serve --log-level=debug

# In another terminal, verify it's running
curl -H "X-API-Key: $(jq -r .api_key ~/.mcpproxy/mcp_config.json)" \
  http://localhost:8080/api/v1/status
```

## Testing Agent Tokens

### Create a token
```bash
./mcpproxy token create \
  --name "test-agent" \
  --servers github,filesystem \
  --permissions read,write \
  --expires 1d
```

### Use the token with MCP
```bash
# Using the token to make an MCP request
curl -X POST http://localhost:8080/mcp \
  -H "Authorization: Bearer mcp_agt_<your-token>" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"retrieve_tools","arguments":{"query":"search"}},"id":1}'
```

### Test via REST API
```bash
API_KEY=$(jq -r .api_key ~/.mcpproxy/mcp_config.json)

# Create token via API
curl -X POST http://localhost:8080/api/v1/tokens \
  -H "X-API-Key: $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name":"test-api","allowed_servers":["*"],"permissions":["read"],"expires_in":"1d"}'

# List tokens
curl http://localhost:8080/api/v1/tokens -H "X-API-Key: $API_KEY"

# Revoke token
curl -X DELETE http://localhost:8080/api/v1/tokens/test-api -H "X-API-Key: $API_KEY"
```

## Running Tests

```bash
# Unit tests
go test ./internal/auth/... -v
go test ./internal/storage/... -v -run TestAgentToken
go test ./internal/httpapi/... -v -run TestToken

# Race detection
go test -race ./internal/auth/... ./internal/storage/... ./internal/httpapi/...

# E2E tests
./scripts/test-api-e2e.sh

# Linter
./scripts/run-linter.sh
```

## Key Files to Edit

| File | Purpose |
|------|---------|
| `internal/auth/agent_token.go` | Token model, HMAC hashing, validation |
| `internal/auth/context.go` | AuthContext type, context helpers |
| `internal/storage/agent_tokens.go` | BBolt CRUD |
| `internal/httpapi/server.go` | Auth middleware extension |
| `internal/httpapi/tokens.go` | REST API handlers |
| `internal/server/mcp.go` | Scope enforcement in retrieve_tools and call_tool_* |
| `cmd/mcpproxy/token_cmd.go` | CLI commands |
| `frontend/src/views/AgentTokens.vue` | Web UI |
