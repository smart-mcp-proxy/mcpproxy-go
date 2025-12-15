---
paths: "**/quarantine/**, **/isolation/**, internal/httpapi/middleware*"
---

# Security

## Quarantine System

- **All new servers** added via LLM tools are automatically quarantined
- **Quarantined servers** cannot execute tools until manually approved
- **Tool calls** to quarantined servers return security analysis instead of executing
- **Approval**: System tray menu or config file edit

## Tool Poisoning Attack (TPA) Protection

- Automatic detection of malicious tool descriptions
- Security analysis with comprehensive checklists
- Protection against hidden instructions and data exfiltration

## API Key Authentication

- **Always required** - auto-generated if missing
- Methods: `X-API-Key` header or `?apikey=` query param
- Priority: `MCPPROXY_API_KEY` env > config file > auto-generated
- **Unix socket bypass**: Socket connections skip API key (OS-level auth)

## Network Security

- **Localhost-only by default**: Binds to `127.0.0.1:8080`
- Override: `--listen :8080` or `MCPPROXY_LISTEN` env var
- MCP endpoints (`/mcp`, `/mcp/`) remain unprotected for client compatibility

## Docker Isolation

- stdio servers run in Docker containers by default
- Runtime detection: `uvx` → Python image, `npx` → Node.js image
- Resource limits prevent exhaustion
- Environment passed securely via `-e` flags
- Proper cleanup with cidfile tracking

## Exit Codes (`cmd/mcpproxy/exit_codes.go`)

| Code | Meaning |
|------|---------|
| 0 | Success |
| 2 | Port conflict |
| 3 | Database locked |
| 4 | Config error |
| 5 | Permission error |
