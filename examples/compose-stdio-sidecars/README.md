# mcpproxy with stdio servers in sidecars

Docker Compose example: mcpproxy plus a stdio MCP server in its own sidecar container on an `internal: true` network. Walkthrough and limits: [docs/operations/compose-stdio-sidecars.md](../../docs/operations/compose-stdio-sidecars.md).

```bash
./gen-secrets.sh && docker compose up --build --wait
./test.sh   # optional: runs the checks CI runs, then tears the stack down
```
