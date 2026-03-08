# Quickstart: Building Personal vs Teams Edition

## Build Personal Edition (default)

```bash
make build
# or directly:
go build -ldflags "..." -o mcpproxy ./cmd/mcpproxy
./mcpproxy version
# MCPProxy v0.21.0 (personal) darwin/arm64
```

## Build Teams Edition

```bash
make build-teams
# or directly:
go build -tags teams -ldflags "..." -o mcpproxy-teams ./cmd/mcpproxy
./mcpproxy-teams version
# MCPProxy v0.21.0 (teams) linux/amd64
```

## Build Teams Docker Image

```bash
make build-docker
# or directly:
docker build -t mcpproxy-teams:latest .
docker run -p 8080:8080 mcpproxy-teams:latest
```

## Verify Edition

```bash
# CLI
./mcpproxy version          # shows "personal"
./mcpproxy-teams version    # shows "teams"

# API
curl http://localhost:8080/api/v1/status | jq .edition
```

## Development

```bash
# Run tests (both editions)
go test ./internal/... -v                    # personal (default)
go test -tags teams ./internal/... -v        # teams (includes teams tests)

# Lint
./scripts/run-linter.sh
```
