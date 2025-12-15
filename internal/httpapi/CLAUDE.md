# HTTP API

## Base Path

`/api/v1` (legacy `/api` routes removed)

## Core Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/status` | Server status and stats |
| GET | `/api/v1/servers` | List upstream servers |
| POST | `/api/v1/servers/{name}/enable` | Enable/disable server |
| POST | `/api/v1/servers/{name}/quarantine` | Quarantine/unquarantine |
| POST | `/api/v1/servers/{name}/restart` | Restart server |
| POST | `/api/v1/servers/restart-all` | Restart all servers |
| GET | `/api/v1/tools` | Search tools |
| GET | `/api/v1/servers/{name}/tools` | List server's tools |

## Real-time Updates

`GET /events` - Server-Sent Events stream

Event types:
- `servers.changed` - Server state changes
- `config.reloaded` - Config file reloaded

## Authentication

- Required for all `/api/v1/*` and `/events` endpoints
- Methods: `X-API-Key` header or `?apikey=` query param
- MCP endpoints (`/mcp`, `/mcp/`) remain unprotected

## OpenAPI Documentation

All endpoints documented in `oas/swagger.yaml`

After changing endpoints:
```bash
./scripts/verify-oas-coverage.sh
```

## Key Files

| File | Purpose |
|------|---------|
| `server.go` | Main router, endpoint handlers |
| `middleware.go` | Auth, logging, CORS |
| `sse.go` | Server-Sent Events |

## Uses chi Router

```go
r := chi.NewRouter()
r.Route("/api/v1", func(r chi.Router) {
    r.Get("/servers", s.handleListServers)
})
```
