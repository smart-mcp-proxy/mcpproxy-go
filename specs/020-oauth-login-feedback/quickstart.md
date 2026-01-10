# Quickstart: OAuth Login Error Feedback

**Feature**: 020-oauth-login-feedback
**Date**: 2026-01-06

## Overview

This feature enhances the `mcpproxy auth login` command to provide immediate feedback about browser opening status and the authorization URL. All clients (CLI, tray, Web UI) receive the same response payload and can display the manual URL when the browser fails to open.

---

## CLI Usage

### Successful browser open

```bash
$ mcpproxy auth login --server=google-drive
Opening browser for google-drive authorization...
Complete authentication in browser, then return here.

Correlation ID: a1b2c3d4-e5f6-7890-abcd-ef1234567890
```

### Browser failed to open (headless/SSH)

```bash
$ mcpproxy auth login --server=google-drive
Could not open browser automatically.

Please open this URL manually to authenticate:
  https://accounts.google.com/o/oauth2/auth?client_id=...&redirect_uri=...

After completing authorization, the server will be authenticated automatically.

Correlation ID: a1b2c3d4-e5f6-7890-abcd-ef1234567890
```

### Browser failed to open (error)

```bash
$ mcpproxy auth login --server=google-drive
Could not open browser: xdg-open: command not found

Please open this URL manually to authenticate:
  https://accounts.google.com/o/oauth2/auth?client_id=...&redirect_uri=...

After completing authorization, the server will be authenticated automatically.

Correlation ID: a1b2c3d4-e5f6-7890-abcd-ef1234567890
```

---

## Pre-flight Validation Errors

The command validates the server before attempting OAuth:

### Server not found

```bash
$ mcpproxy auth login --server=google-drvie
Error: Server 'google-drvie' not found

Available servers:
  - google-drive
  - github-server
  - slack-server

Run 'mcpproxy upstream list' to see all servers.
```

### OAuth not supported

```bash
$ mcpproxy auth login --server=local-script
Error: Server 'local-script' does not support OAuth

Reason: stdio protocol does not support OAuth authentication.
OAuth is only available for HTTP/SSE servers.
```

### Server disabled

```bash
$ mcpproxy auth login --server=google-drive
Error: Server 'google-drive' is disabled

Enable it first:
  mcpproxy upstream enable google-drive
```

### Server quarantined

```bash
$ mcpproxy auth login --server=new-server
Error: Server 'new-server' is quarantined

Approve it first via Web UI or tray, then try again.
```

### Flow already in progress

```bash
$ mcpproxy auth login --server=google-drive
Error: OAuth flow already in progress for 'google-drive'

Check your browser window or wait for the current flow to complete.
Existing correlation ID: a1b2c3d4-e5f6-7890-abcd-ef1234567890
```

---

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | OAuth flow started successfully |
| 1 | General error |
| 2 | Validation error (server not found, disabled, etc.) |

### Script example

```bash
#!/bin/bash
mcpproxy auth login --server=google-drive
exit_code=$?

if [ $exit_code -eq 0 ]; then
  echo "OAuth flow started - check browser or use the URL displayed"
elif [ $exit_code -eq 2 ]; then
  echo "Server validation failed - check error message above"
else
  echo "Unexpected error"
fi
```

---

## Multi-Client Behavior

All clients use the same `POST /api/v1/servers/{id}/login` endpoint and receive identical response payloads. Each client handles the response according to its UI:

| Client | browser_opened=true | browser_opened=false |
|--------|--------------------|--------------------|
| CLI | "Opening browser..." | Print URL to terminal |
| Tray | Show notification | Show notification with clickable URL |
| Web UI | "Check browser" toast | Display URL in modal |

### Tray behavior (macOS/Windows/Linux)

When you click "Login" from the tray menu:
- **Browser opens**: Notification shows "Authenticating google-drive..."
- **Browser fails**: Notification shows URL (click to copy)

### Web UI behavior

When you click the Login button on a server card:
- **Browser opens**: Toast shows "Check browser to complete authentication"
- **Browser fails**: Modal displays the auth URL with a copy button

---

## REST API Usage

### Trigger OAuth (default behavior)

```bash
curl -X POST "http://127.0.0.1:8080/api/v1/servers/google-drive/login" \
  -H "X-API-Key: your-api-key"
```

### Response (browser opened)

```json
{
  "success": true,
  "server_name": "google-drive",
  "correlation_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "auth_url": "https://accounts.google.com/o/oauth2/auth?client_id=...",
  "browser_opened": true,
  "message": "OAuth flow started. Complete authorization in browser."
}
```

### Response (browser failed)

```json
{
  "success": true,
  "server_name": "google-drive",
  "correlation_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "auth_url": "https://accounts.google.com/o/oauth2/auth?client_id=...",
  "browser_opened": false,
  "browser_error": "Headless mode - browser not available",
  "message": "OAuth flow started. Open the auth_url manually to complete authorization."
}
```

### Validation error response

```json
{
  "success": false,
  "error_type": "server_not_found",
  "server_name": "google-drvie",
  "message": "Server 'google-drvie' not found in configuration",
  "suggestion": "Check server name spelling. Did you mean 'google-drive'?",
  "available_servers": ["google-drive", "github-server"]
}
```

---

## Detecting OAuth Completion

After starting an OAuth flow, detect completion using existing mechanisms:

### Option 1: SSE Events (recommended)

```bash
# Subscribe to SSE stream
curl -N "http://127.0.0.1:8080/events?apikey=your-api-key"

# Watch for servers.changed event
# event: servers.changed
# data: {"server":"google-drive","event":"status_changed"}
```

### Option 2: Poll server status

```bash
# Check if OAuth is now authenticated
curl "http://127.0.0.1:8080/api/v1/servers" \
  -H "X-API-Key: your-api-key" | jq '.[] | select(.name=="google-drive") | .oauth_authenticated'
```

---

## Troubleshooting

### OAuth flow started but nothing happens

1. Check browser windows - authorization page may be behind other windows
2. Look for the auth URL in CLI output or API response
3. Check correlation ID in daemon logs: `mcpproxy upstream logs google-drive --tail=50`

### Browser doesn't open

This is expected in these scenarios:
- Running over SSH without X forwarding
- `HEADLESS=true` environment variable set
- No default browser configured
- Linux without `xdg-open` installed

**Solution**: Copy the auth URL from CLI output or API response and paste into browser manually.

### "OAuth not supported" error

OAuth requires HTTP/SSE transport. Check server configuration:
```bash
mcpproxy upstream list
```

Servers with `Protocol: stdio` cannot use OAuth - they authenticate via environment variables.

### Log correlation

Use the correlation ID from the response to find related log entries:
```bash
mcpproxy upstream logs google-drive | grep "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
```
