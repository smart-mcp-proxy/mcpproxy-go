# Settings Page (Web UI)

The Web UI **Configuration** page (`/ui/settings`) presents mcpproxy's config as
friendly, prioritized form sections instead of raw JSON:

- **Security & Access** — API key (masked, show/regenerate), require MCP auth,
  quarantine, global Docker isolation, code execution, read-only mode,
  sensitive-data detection, reveal secret headers, listen address.
- **General** — routing mode, tool limits, response limit, call timeout, log
  level, telemetry, prompts.
- **Advanced** — collapsible accordions per subsystem (code execution, Docker
  isolation, sensitive-data detection, output validation, output sanitisation,
  activity retention, logging, TLS, …).
- **Raw JSON** — the full Monaco editor, kept as an escape hatch.
- **Server Edition** — server edition only.

## How saving works

Each section saves **only the fields you changed** via `PATCH /api/v1/config`, a
partial deep-merge that routes through the normal validate → persist → hot-reload
pipeline. Because the merge starts from the live config and overlays just your
changes, unrelated values and masked secrets (API key, secret headers) are never
overwritten.

Fields that need a restart (`listen`, `data_dir`, `api_key`, `tls.*`) show a
**restart** badge; sensitive changes (reveal secret headers, disabling
quarantine/management, binding to a non-loopback address) require an explicit
confirmation before they apply.

```bash
# Equivalent API call — change one field, everything else preserved
curl -X PATCH -H "X-API-Key: $KEY" -H 'Content-Type: application/json' \
  -d '{"quarantine_enabled": false}' http://127.0.0.1:8080/api/v1/config
```

Complex lists/maps (Docker image map, custom detection patterns, environment
vars) and `mcpServers` / `registries` are managed on their own pages or the Raw
JSON tab. See the [configuration reference](../configuration/config-file.md) for the full option list.
