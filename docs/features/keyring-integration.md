---
title: Keyring Integration
description: Securely store and manage secrets using your operating system's native keyring
sidebar_position: 9
keywords: [keyring, keychain, secrets, credentials, security, environment variables, migration]
---

# Keyring Integration

MCPProxy integrates with your operating system's native credential store to securely manage secrets such as API keys, tokens, and passwords. Instead of storing sensitive values in plaintext configuration files, you reference them using the `${keyring:name}` syntax, and MCPProxy resolves them at runtime from the system keyring.

## Overview

### Why Use Keyring Storage?

Storing secrets directly in `mcp_config.json` is convenient but risky:

- Configuration files can be accidentally committed to version control
- Plaintext secrets are visible to anyone with file access
- Secrets in config files are harder to rotate across environments

MCPProxy's keyring integration solves this by:

- **Storing secrets in the OS credential store** -- encrypted at rest by the operating system
- **Resolving references at runtime** -- configuration files contain only references, never actual values
- **Automatic server restart** -- when a secret changes, affected upstream servers restart automatically
- **Log sanitization** -- resolved secret values are automatically masked in all log output

### Platform Support

MCPProxy uses the [zalando/go-keyring](https://github.com/zalando/go-keyring) library (v0.2.6), which supports:

| Platform | Backend | Notes |
|----------|---------|-------|
| **macOS** | Keychain (via Security framework) | Secrets stored under the "mcpproxy" service in Keychain Access |
| **Linux** | Secret Service API (libsecret) | Requires a running secret service (GNOME Keyring, KWallet, etc.) |
| **Windows** | Windows Credential Manager | Secrets visible in Control Panel > Credential Manager |

All secrets are stored under the service name **`mcpproxy`**.

## Secret Reference Syntax

MCPProxy supports two types of secret references that can be used anywhere in your configuration where a string value is expected:

| Syntax | Provider | Description |
|--------|----------|-------------|
| `${keyring:name}` | OS Keyring | Resolves from macOS Keychain, Linux Secret Service, or Windows Credential Manager |
| `${env:VAR_NAME}` | Environment | Resolves from environment variables |

References can be used in:

- **Server environment variables** (`env` field)
- **Server arguments** (`args` field)
- **Server headers** (`headers` field)

> **Note:** The `url` field does not support secret references. If your server URL contains credentials, use environment variables or headers instead.

### Configuration Example

```json
{
  "mcpServers": [
    {
      "name": "github-mcp",
      "url": "https://api.github.com/mcp",
      "protocol": "http",
      "headers": {
        "Authorization": "Bearer ${keyring:github-token}"
      },
      "enabled": true
    },
    {
      "name": "my-stdio-server",
      "command": "python",
      "args": ["-m", "my_server"],
      "protocol": "stdio",
      "env": {
        "API_KEY": "${keyring:my-api-key}",
        "DATABASE_URL": "${env:DATABASE_URL}"
      },
      "enabled": true
    }
  ]
}
```

## CLI Commands

MCPProxy provides a `secrets` command group for managing keyring entries.

### Store a Secret

```bash
# Interactive (prompts for value)
mcpproxy secrets set my-api-key

# Inline value
mcpproxy secrets set my-api-key "sk-abc123..."

# From environment variable
mcpproxy secrets set my-api-key --from-env=MY_API_KEY

# From stdin
echo "secret-value" | mcpproxy secrets set my-api-key --from-stdin
```

| Flag | Default | Description |
|------|---------|-------------|
| `--type` | `keyring` | Secret provider type |
| `--from-env` | | Read value from an environment variable |
| `--from-stdin` | `false` | Read value from stdin |

On success, the command prints the reference syntax to use in your configuration:

```
Secret 'my-api-key' stored successfully in keyring
Use in config: ${keyring:my-api-key}
```

### Retrieve a Secret

```bash
# Masked output (default)
mcpproxy secrets get my-api-key
# Output: my-api-key: sk-****23

# Unmasked output
mcpproxy secrets get my-api-key --masked=false
# Output: my-api-key: sk-abc123...
```

| Flag | Default | Description |
|------|---------|-------------|
| `--type` | `keyring` | Secret provider type |
| `--masked` | `true` | Mask the secret value in output |

### Delete a Secret

```bash
mcpproxy secrets del my-api-key
```

| Flag | Default | Description |
|------|---------|-------------|
| `--type` | `keyring` | Secret provider type |

### List All Secrets

```bash
# List keyring secrets only (default)
mcpproxy secrets list

# List from all providers (keyring + env)
mcpproxy secrets list --all

# JSON output
mcpproxy secrets list -o json

# YAML output
mcpproxy secrets list -o yaml
```

Example table output:

```
NAME              TYPE
github-token      keyring
my-api-key        keyring
db-password       keyring
```

### Migrate Plaintext Secrets

The `migrate` command analyzes your configuration for hardcoded values that look like secrets and suggests migrating them to the keyring:

```bash
# Dry run -- show what would be migrated
mcpproxy secrets migrate --dry-run

# Interactive migration
mcpproxy secrets migrate

# Auto-approve all migrations
mcpproxy secrets migrate --auto-approve
```

Example output:

```
Found 2 potential secrets for migration:

1. Field: Servers[0].Env.API_KEY
   Current value: sk-a****23
   Suggested ref: ${keyring:env_api_key}
   Confidence: 85.0%

2. Field: Servers[1].Headers.Authorization
   Current value: Bea****en
   Suggested ref: ${keyring:headers_authorization}
   Confidence: 72.0%
```

The detection engine considers:

- **Field name keywords** -- fields containing "password", "secret", "key", "token", "auth", "credential", etc.
- **Value characteristics** -- length, base64/hex patterns, entropy
- **Common non-secrets** -- excludes values like URLs, "localhost", "true/false", etc.

| Flag | Default | Description |
|------|---------|-------------|
| `--dry-run` | `false` | Show candidates without making changes |
| `--auto-approve` | `false` | Automatically approve all migrations |
| `--from` | `plaintext` | Source type |
| `--to` | `keyring` | Target type |

## Web UI

MCPProxy's Web UI includes a dedicated **Secrets & Environment Variables** page accessible from the sidebar navigation. This page provides a visual interface for managing all secret references in your configuration.

### Dashboard Overview

The page displays four summary statistics:

- **Keyring Secrets** -- total secrets referenced in configuration that use `${keyring:...}` syntax
- **Environment Variables** -- total environment variable references using `${env:...}` syntax
- **Missing Env Vars** -- environment variables referenced but not currently set
- **Migration Candidates** -- plaintext values detected that could be migrated to secure storage

### Filtering and Search

Filter secrets by category using the filter buttons:

- **All** -- show all secret and environment variable references
- **Keyring Secrets** -- show only `${keyring:...}` references
- **Environment Variables** -- show only `${env:...}` references
- **Missing** -- show only references that cannot be resolved (highlighted with a red border)

A search bar allows filtering by name or reference string.

### Managing Secrets

Each keyring secret card shows:

- The secret name and its reference syntax (e.g., `${keyring:my-api-key}`)
- Status badge: green "Set" if the value exists in the keyring, red "Missing" if not
- Action buttons:
  - **Add Value** -- opens a modal to set the secret value (for missing secrets)
  - **Update** -- opens a modal to change the existing value
  - **Remove** -- deletes the secret from the keyring (with confirmation)

For environment variables, a **How to Set** button provides platform-specific instructions.

### Add Secret Modal

The modal for adding or updating a secret includes:

- **Secret Name** field -- auto-filled when clicking "Add Value" on a missing secret
- **Secret Value** field -- password input (masked)
- **Configuration Reference** preview -- shows the `${keyring:name}` syntax to use

When a secret is added or updated through the Web UI, MCPProxy automatically:

1. Stores the value in the OS keyring
2. Notifies the runtime of the change
3. Restarts any upstream servers that reference the changed secret

### Migration Analysis

The Migration Candidates section at the bottom of the page shows plaintext values in your configuration that appear to be secrets. Each candidate shows:

- The configuration field path
- A masked version of the current value
- The suggested `${keyring:...}` reference
- A confidence percentage

Click **Store in Keychain** to get CLI instructions for migrating a specific candidate.

## REST API

The following REST API endpoints are available for programmatic secret management:

### Store a Secret

```
POST /api/v1/secrets
```

```json
{
  "name": "my-api-key",
  "value": "sk-abc123...",
  "type": "keyring"
}
```

Response:

```json
{
  "message": "Secret 'my-api-key' stored successfully in keyring",
  "name": "my-api-key",
  "type": "keyring",
  "reference": "${keyring:my-api-key}"
}
```

### Delete a Secret

```
DELETE /api/v1/secrets/{name}?type=keyring
```

### Get Config Secret References

```
GET /api/v1/secrets/config
```

Returns all secret and environment variable references found in the current configuration, along with their resolution status.

### List All Secret References

```
GET /api/v1/secrets/refs
```

### Run Migration Analysis

```
POST /api/v1/secrets/migrate
```

All endpoints require API key authentication via `X-API-Key` header or `?apikey=` query parameter.

## How It Works

### Secret Resolution Flow

1. At startup, MCPProxy creates a `Resolver` with two default providers: `keyring` and `env`
2. When an upstream server connects, MCPProxy copies its configuration and resolves all `${type:name}` references:
   - Environment variables (`env` map values)
   - Command arguments (`args` array values)
   - HTTP headers (`headers` map values)
3. Resolved values are passed to the upstream server process or HTTP connection
4. The original configuration file is never modified -- references remain as-is

### Automatic Server Restart

When a secret is stored or deleted (via CLI, Web UI, or API), MCPProxy:

1. Emits a `secrets.changed` event on the internal event bus
2. Scans all server configurations for references matching `${keyring:secret-name}`
3. Restarts any servers whose `env` or `args` contain the changed secret reference (note: `headers` are resolved at connection time but do not trigger auto-restart)
4. Logs which servers were affected and restarted

This means you can update a secret without manually restarting MCPProxy or individual servers.

### Log Sanitization

Resolved secret values are automatically registered with the log sanitizer. Any log line containing a resolved secret value will have it masked (e.g., `sk-a***23`). This prevents accidental secret leakage in log files.

> **Masking formats:** MCPProxy uses two masking functions with slightly different notation. CLI and API output uses `MaskSecretValue` with four asterisks (e.g., `sk-****23`). Log sanitization uses `maskValue` with three asterisks (e.g., `sk-a***23`). Both show the first few and last two characters of the original value.

### Secret Registry

Since OS keyring APIs do not provide a "list all entries" function, MCPProxy maintains an internal registry entry (stored in the keyring itself under the key `_mcpproxy_secret_registry`). This registry is a newline-separated list of secret names, automatically updated when secrets are added or removed.

### Diagnostics Integration

The `mcpproxy doctor` command and the diagnostics API check for missing secrets. If a server's configuration references a `${keyring:...}` or `${env:...}` value that cannot be resolved, it appears in the diagnostics as a missing secret with the server name and reference string.

## Workflow: Config-First Approach

The recommended workflow for using keyring secrets:

**Step 1: Add the secret reference to your configuration**

```json
{
  "mcpServers": [
    {
      "name": "my-server",
      "command": "my-tool",
      "protocol": "stdio",
      "env": {
        "API_KEY": "${keyring:my-api-key}"
      }
    }
  ]
}
```

**Step 2: The secret appears as "Missing" in Web UI or diagnostics**

```bash
mcpproxy doctor
# Shows: Missing secret 'my-api-key' referenced by server 'my-server'
```

**Step 3: Store the actual value**

```bash
mcpproxy secrets set my-api-key
# Enter secret value: ****
```

Or use the Web UI: click **Add Value** next to the missing secret.

**Step 4: Server automatically restarts with the resolved secret**

MCPProxy detects the change and restarts `my-server` with the resolved environment variable.

## Troubleshooting

### Keyring Not Available

**Symptoms:** Error message "keyring is not available on this system" or "provider for keyring is not available."

**macOS:** Keychain Access should work out of the box. If MCPProxy runs as a background daemon (e.g., via launchd), ensure it has access to the login keychain. You may need to unlock the keychain first:

```bash
security unlock-keychain ~/Library/Keychains/login.keychain-db
```

**Linux:** Ensure a Secret Service provider is running:

```bash
# Check if secret service is available
dbus-send --session --dest=org.freedesktop.secrets \
  --type=method_call --print-reply \
  /org/freedesktop/secrets org.freedesktop.DBus.Peer.Ping
```

Common providers: GNOME Keyring (`gnome-keyring-daemon`), KDE Wallet (`kwalletd5`).

For headless Linux servers without a desktop environment, consider using environment variable references (`${env:...}`) instead.

**Windows:** Windows Credential Manager should work without additional setup.

### Secret Not Resolving

**Symptoms:** Server fails to connect or uses the literal string `${keyring:name}` instead of the resolved value.

1. Verify the secret exists:
   ```bash
   mcpproxy secrets get my-api-key
   ```

2. Check the exact name matches (case-sensitive):
   ```bash
   mcpproxy secrets list
   ```

3. Check MCPProxy logs for resolution errors:
   ```bash
   mcpproxy serve --log-level=debug
   ```
   Look for log entries containing "Failed to resolve secret" or "CRITICAL: Failed to resolve secret."

### macOS Keychain Access Prompts

On macOS, the first time MCPProxy accesses the keychain, you may see a system dialog asking to allow access. Click **Always Allow** to prevent repeated prompts. If you accidentally clicked **Deny**, you can fix this in:

1. Open **Keychain Access** application
2. Find the "mcpproxy" entries
3. Right-click > **Get Info** > **Access Control** tab
4. Add `mcpproxy` to the allowed applications list

### Viewing Secrets in OS Credential Store

**macOS:**
Open Keychain Access.app and search for "mcpproxy" to see all stored entries.

**Linux:**
Use `secret-tool` (part of libsecret):
```bash
secret-tool search service mcpproxy
```

**Windows:**
Open Control Panel > Credential Manager > Windows Credentials and look for entries with "mcpproxy" in the name.

### Server Not Restarting After Secret Change

If updating a secret does not trigger a server restart:

1. Ensure the secret name in the config matches exactly (e.g., `${keyring:my-api-key}` requires a secret named `my-api-key`)
2. Check that the server is enabled and was previously running
3. Verify the secret change was made through MCPProxy (CLI, Web UI, or API) -- direct keychain modifications are not detected

## Security Considerations

- **Secrets never appear in configuration files** -- only `${keyring:name}` references are stored
- **Log sanitization** is automatic -- resolved values are masked in all log output
- **API responses never contain secret values** -- the `secrets list` and `secrets/refs` endpoints return only names and types
- **The Web UI `secrets/config` endpoint** checks resolution status without exposing values
- **Secret values are masked** in migration analysis output (showing only first 3 and last 2 characters)
- **OS-level encryption** -- the actual secret storage is handled by the operating system's native credential store, which provides encryption at rest
- **Only keyring type is supported** for store/delete operations via the API -- this is enforced server-side for security
