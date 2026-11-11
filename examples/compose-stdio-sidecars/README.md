# mcpproxy with stdio servers in sidecars

Docker Compose example: mcpproxy plus a stdio MCP server in its own sidecar container on an `internal: true` network. Walkthrough and limits: [docs/operations/compose-stdio-sidecars.md](../../docs/operations/compose-stdio-sidecars.md).

```bash
./gen-secrets.sh && docker compose up --build --wait
./test.sh   # optional: runs the checks CI runs
```

`gen-secrets.sh` keeps `secrets/` at mode `0700`; the two key files inside are `0644` because the sidecar (uid 1000) and mcpproxy (root) must both read them through the bind mount, so the directory is what keeps other host users out.

`test.sh` runs under its own Compose project (`mcpproxy-sidecars-test`, port 18081), so it can run next to a normal deployment. On exit it removes only that project's containers, network and data volume; the deployment's `mcpproxy_data` volume is not touched. It reuses `secrets/` and the built sidecar image.
