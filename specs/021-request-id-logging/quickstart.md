# Quickstart: Request ID Logging

**Feature**: 021-request-id-logging
**Date**: 2026-01-07

## Overview

This feature adds request-scoped logging with `X-Request-Id` header support. Every API request gets a unique ID that appears in error responses and server logs, enabling easy debugging and log correlation.

---

## CLI Usage

### Error with Request ID

When a CLI command fails, the Request ID is displayed with a suggestion:

```bash
$ mcpproxy upstream list
Error: Failed to connect to daemon

Request ID: a1b2c3d4-e5f6-7890-abcd-ef1234567890
Run 'mcpproxy logs --request-id a1b2c3d4-e5f6-7890-abcd-ef1234567890' to see detailed logs
```

### Retrieve Logs by Request ID

```bash
# Get all logs for a specific request
$ mcpproxy logs --request-id a1b2c3d4-e5f6-7890-abcd-ef1234567890

2026-01-07T10:30:00Z INFO  handling server list request  request_id=a1b2c3d4-e5f6-7890-abcd-ef1234567890
2026-01-07T10:30:01Z ERROR server connection failed      request_id=a1b2c3d4-e5f6-7890-abcd-ef1234567890

# Limit output
$ mcpproxy logs --request-id a1b2c3d4 --tail 10
```

### Successful Commands

Request ID is NOT displayed on success (to reduce noise):

```bash
$ mcpproxy upstream list
NAME            STATUS    TOOLS
google-drive    ready     15
github-server   ready     23
```

---

## REST API Usage

### Request Header (Optional)

Clients MAY provide their own request ID:

```bash
curl -H "X-Request-Id: my-trace-123" \
     -H "X-API-Key: your-api-key" \
     http://127.0.0.1:8080/api/v1/servers
```

### Response Header (Always Present)

Every response includes `X-Request-Id`:

```bash
$ curl -i http://127.0.0.1:8080/api/v1/servers -H "X-API-Key: ..."

HTTP/1.1 200 OK
X-Request-Id: a1b2c3d4-e5f6-7890-abcd-ef1234567890
Content-Type: application/json

[{"name":"google-drive","status":"ready",...}]
```

### Error Response Body

All errors include `request_id` in JSON:

```bash
$ curl http://127.0.0.1:8080/api/v1/servers/nonexistent -H "X-API-Key: ..."

HTTP/1.1 404 Not Found
X-Request-Id: a1b2c3d4-e5f6-7890-abcd-ef1234567890
Content-Type: application/json

{
  "error": "server_not_found",
  "message": "Server 'nonexistent' not found",
  "request_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "suggestion": "Check server name. Available: google-drive, github-server"
}
```

### Retrieve Logs via API

```bash
# Filter by request ID
curl "http://127.0.0.1:8080/api/v1/logs?request_id=a1b2c3d4" \
     -H "X-API-Key: your-api-key"

# Combined filters
curl "http://127.0.0.1:8080/api/v1/logs?request_id=a1b2c3d4&level=error&limit=50" \
     -H "X-API-Key: your-api-key"
```

---

## Multi-Client Behavior

All clients receive the same response format:

| Client | On Error | User Action |
|--------|----------|-------------|
| CLI | Prints Request ID + log suggestion | Copy ID, run `mcpproxy logs --request-id <id>` |
| Tray | Notification shows Request ID | Click "Copy ID" button |
| Web UI | Error modal shows Request ID | Click "Copy" or "View Logs" |

---

## Integration with OAuth (Spec 020)

OAuth responses include both IDs:

```json
{
  "success": true,
  "server_name": "google-drive",
  "request_id": "req-abc123",
  "correlation_id": "oauth-def456",
  "browser_opened": true
}
```

**Lookup behavior**:
- `--request-id req-abc123`: Find logs for the login HTTP request
- `--correlation-id oauth-def456`: Find logs for entire OAuth flow (including callbacks)

---

## Request ID Format

### Server-Generated (Default)

UUID v4 format: `a1b2c3d4-e5f6-7890-abcd-ef1234567890`

### Client-Provided (Optional)

Must match pattern: `^[a-zA-Z0-9_-]{1,256}$`

Valid examples:
- `my-trace-123`
- `request_2026-01-07_001`
- `user-session-abc`

Invalid (rejected):
- `my trace` (spaces not allowed)
- `<script>` (special chars not allowed)
- String longer than 256 characters

---

## Debugging Workflow

### 1. Encounter an Error

```bash
$ mcpproxy auth login --server=google-drvie
Error: Server 'google-drvie' not found

Request ID: a1b2c3d4-e5f6-7890-abcd-ef1234567890
Run 'mcpproxy logs --request-id a1b2c3d4-e5f6-7890-abcd-ef1234567890' to see detailed logs
```

### 2. Retrieve Detailed Logs

```bash
$ mcpproxy logs --request-id a1b2c3d4-e5f6-7890-abcd-ef1234567890

2026-01-07T10:30:00Z INFO  received login request        request_id=a1b2c3d4 server=google-drvie
2026-01-07T10:30:00Z DEBUG looking up server             request_id=a1b2c3d4 server=google-drvie
2026-01-07T10:30:01Z WARN  server not found              request_id=a1b2c3d4 server=google-drvie available=[google-drive,github-server]
2026-01-07T10:30:01Z ERROR validation failed             request_id=a1b2c3d4 error=server_not_found
```

### 3. Share for Support

Include Request ID in bug reports or support requests:

```
**Issue**: Login failed with "Server not found"
**Request ID**: a1b2c3d4-e5f6-7890-abcd-ef1234567890
**Command**: mcpproxy auth login --server=google-drvie
```

---

## Security Notes

Request IDs are **safe to share**:
- Generated IDs are random UUIDs (no embedded information)
- Client IDs are validated (alphanumeric only)
- IDs do not contain secrets, tokens, or PII
- IDs are not used for authentication

---

## Troubleshooting

### "Invalid X-Request-Id header"

Your client-provided ID contains invalid characters. Use only:
- Letters (a-z, A-Z)
- Numbers (0-9)
- Dashes (-)
- Underscores (_)

### "No logs found for request ID"

Possible causes:
1. Request ID was from a different daemon instance (daemon restarted)
2. Logs were rotated/cleaned
3. Typo in request ID

### Request ID not in response

Check that:
1. You're calling the REST API (not MCP protocol)
2. Response is JSON (not SSE or plain text)
3. Daemon version supports request ID (this feature)
