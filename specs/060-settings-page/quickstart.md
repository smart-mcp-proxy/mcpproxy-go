# Quickstart: Settings Page

## Use

Open the Web UI → **Configuration** (sidebar). Tabs:
- **Security & Access** — API key (masked, show/regenerate), MCP auth, quarantine, Docker isolation, code execution, read-only, sensitive-data detection, reveal secret headers (danger), listen.
- **General** — routing mode, tool limits, response limit, call timeout, log level, telemetry, prompts.
- **Advanced** — accordions per subsystem.
- **Raw JSON** — the full editor (unchanged).
- **Teams** — server edition only.

Change a field → **Save** (saves only that section's changed fields). A toast confirms; a "restart required" badge appears for `listen` / `data_dir` / `api_key` / `tls.*`.

## API

```bash
# Partial update — only changed fields; secrets untouched
curl -X PATCH -H "X-API-Key: $KEY" -H 'Content-Type: application/json' \
  -d '{"quarantine_enabled": false}' \
  http://127.0.0.1:8080/api/v1/config
# → {"success":true,"applied_immediately":true,"requires_restart":false,"changed_fields":["quarantine_enabled"],...}
```

## Verify (mandatory — Chrome extension)

Drive the live Web UI: capture Security/General/Advanced/RawJSON; toggle quarantine and save, re-read to confirm persistence; trigger a danger toggle and confirm the dialog. (QA report produced for review, not committed.)
