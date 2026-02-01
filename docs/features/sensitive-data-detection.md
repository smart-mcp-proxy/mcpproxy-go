---
id: sensitive-data-detection
title: Sensitive Data Detection
sidebar_label: Sensitive Data Detection
sidebar_position: 8
description: Automatically detect and flag sensitive data in AI agent tool calls
keywords: [security, sensitive data, credentials, secrets, compliance, audit]
---

# Sensitive Data Detection

MCPProxy includes automatic sensitive data detection to identify potential credential leakage, secrets exposure, and other security risks in AI agent tool calls. This feature helps protect against Tool Poisoning Attacks (TPA) and provides compliance auditing capabilities.

## Overview

When AI agents interact with MCP tools, they may inadvertently expose sensitive information such as:

- **Credentials** passed in tool arguments or returned in responses
- **API tokens** leaked through error messages or debug output
- **Private keys** embedded in configuration data
- **Database connection strings** with embedded passwords

MCPProxy scans all tool call arguments and responses for sensitive data patterns, logging detections in the activity log for security review and compliance auditing.

## Supported Detection Types

### Cloud Credentials

| Provider | Pattern | Severity |
|----------|---------|----------|
| AWS Access Key ID | `AKIA[0-9A-Z]{16}` | critical |
| AWS Secret Access Key | 40-character base64 strings | critical |
| GCP API Key | `AIza[0-9A-Za-z-_]{35}` | critical |
| GCP Service Account | JSON with `type: service_account` | critical |
| Azure Storage Key | Base64 storage account keys | critical |
| Azure Connection String | `DefaultEndpointsProtocol=...` | critical |

### Private Keys

| Key Type | Detection Method | Severity |
|----------|-----------------|----------|
| RSA Private Key | `-----BEGIN RSA PRIVATE KEY-----` | critical |
| EC Private Key | `-----BEGIN EC PRIVATE KEY-----` | critical |
| DSA Private Key | `-----BEGIN DSA PRIVATE KEY-----` | critical |
| OpenSSH Private Key | `-----BEGIN OPENSSH PRIVATE KEY-----` | critical |
| PGP Private Key | `-----BEGIN PGP PRIVATE KEY BLOCK-----` | critical |
| PKCS8 Private Key | `-----BEGIN PRIVATE KEY-----` | critical |
| Encrypted Private Key | `-----BEGIN ENCRYPTED PRIVATE KEY-----` | high |

### API Tokens

| Service | Pattern | Severity |
|---------|---------|----------|
| GitHub Token | `ghp_`, `gho_`, `ghu_`, `ghs_`, `ghr_` prefixes | critical |
| GitHub Fine-grained Token | `github_pat_` prefix | critical |
| GitLab Token | `glpat-` prefix | critical |
| Stripe API Key | `sk_live_`, `sk_test_`, `rk_live_`, `rk_test_` | critical |
| Slack Token | `xoxb-`, `xoxp-`, `xoxa-`, `xoxr-` | critical |
| Slack Webhook | `hooks.slack.com/services/` URLs | high |
| SendGrid API Key | `SG.` prefix with base64 | critical |

### LLM/AI Provider API Keys

| Provider | Pattern | Severity |
|----------|---------|----------|
| OpenAI | `sk-`, `sk-proj-`, `sk-svcacct-`, `sk-admin-` prefixes | critical |
| Anthropic | `sk-ant-api03-`, `sk-ant-admin01-` prefixes | critical |
| Google AI/Gemini | `AIzaSy` prefix (39 chars) | critical |
| xAI/Grok | `xai-` prefix (48+ chars) | critical |
| Groq | `gsk_` prefix (52 chars) | critical |
| Hugging Face | `hf_` prefix (37 chars) | critical |
| Hugging Face Org | `api_org_` prefix | critical |
| Replicate | `r8_` prefix (40 chars) | critical |
| Perplexity | `pplx-` prefix (53 chars) | critical |
| Fireworks AI | `fw_` prefix (20+ chars) | critical |
| Anyscale | `esecret_` prefix | critical |
| Mistral AI | Keyword context required | high |
| Cohere | Keyword context required | high |
| DeepSeek | `sk-` with keyword context | high |
| Together AI | Keyword context required | high |

### Database Credentials

| Database | Pattern | Severity |
|----------|---------|----------|
| MySQL | `mysql://user:pass@host` | critical |
| PostgreSQL | `postgres://user:pass@host` | critical |
| MongoDB | `mongodb://user:pass@host` or `mongodb+srv://` | critical |
| Redis | `redis://user:pass@host` or `rediss://` | high |
| Generic JDBC | `jdbc:` URLs with credentials | high |

### Credit Cards

Credit card numbers are detected using pattern matching combined with Luhn algorithm validation:

| Card Type | Pattern | Severity |
|-----------|---------|----------|
| Visa | 4xxx-xxxx-xxxx-xxxx | high |
| Mastercard | 5[1-5]xx-xxxx-xxxx-xxxx | high |
| American Express | 3[47]xx-xxxxxx-xxxxx | high |
| Discover | 6011-xxxx-xxxx-xxxx | high |

:::note Luhn Validation
Credit card detection includes Luhn checksum validation to reduce false positives from random 16-digit numbers.
:::

### High-Entropy Strings

Strings with high Shannon entropy that may indicate secrets:

| Type | Characteristics | Severity |
|------|-----------------|----------|
| Base64 Secrets | High entropy, 20+ chars, base64 charset | medium |
| Hex Secrets | High entropy, 32+ chars, hex charset | medium |
| Random Tokens | High entropy, mixed alphanumeric | low |

### Sensitive File Paths

Detection of file paths that typically contain sensitive data:

| Category | Examples | Severity |
|----------|----------|----------|
| SSH Keys | `~/.ssh/id_rsa`, `~/.ssh/id_ed25519` | high |
| Cloud Credentials | `~/.aws/credentials`, `~/.config/gcloud/` | high |
| Environment Files | `.env`, `.env.local`, `.env.production` | medium |
| Key Files | `*.pem`, `*.key`, `*.p12`, `*.pfx` | high |
| Kubernetes Secrets | `kubeconfig`, `~/.kube/config` | high |

## Detection Categories and Severities

### Categories

| Category | Description |
|----------|-------------|
| `cloud_credentials` | AWS, GCP, Azure credentials |
| `private_key` | RSA, EC, DSA, OpenSSH, PGP private keys |
| `api_token` | GitHub, GitLab, Stripe, Slack, OpenAI tokens |
| `auth_token` | JWT, Bearer tokens, session tokens |
| `sensitive_file` | Paths to credential files |
| `database_credential` | Database connection strings with passwords |
| `high_entropy` | Suspicious high-entropy strings |
| `credit_card` | Credit card numbers (Luhn validated) |

### Severities

| Severity | Description | Action |
|----------|-------------|--------|
| `critical` | Direct credential exposure, immediate risk | Investigate immediately |
| `high` | Sensitive data that could enable access | Review within 24 hours |
| `medium` | Potentially sensitive, context-dependent | Review during audit |
| `low` | Informational, may be false positive | Monitor trends |

## Activity Log Integration

When sensitive data is detected, it is recorded in the activity log metadata:

```json
{
  "id": "01JFXYZ123ABC",
  "type": "tool_call",
  "server_name": "filesystem-server",
  "tool_name": "read_file",
  "status": "success",
  "timestamp": "2025-01-15T10:30:00Z",
  "metadata": {
    "sensitive_data_detected": true,
    "sensitive_data": [
      {
        "type": "aws_access_key",
        "category": "cloud_credentials",
        "severity": "critical",
        "location": "response",
        "context": "AKIA...XXXX (redacted)"
      },
      {
        "type": "private_key",
        "category": "private_key",
        "severity": "critical",
        "location": "response",
        "context": "RSA PRIVATE KEY detected"
      }
    ]
  }
}
```

:::caution Redaction
Detected sensitive values are automatically redacted in the activity log to prevent secondary exposure. Only the type, category, and partial context are stored.
:::

## Web UI Usage

The Activity Log page in the web UI provides filtering and visualization for sensitive data detections.

### Filtering by Sensitive Data

1. Navigate to **Activity Log** in the sidebar
2. Use the **Sensitive Data** filter dropdown to show only activities with detections
3. Filter by severity level (critical, high, medium, low)
4. Click on an activity row to view detection details

### Detection Indicators

Activities with sensitive data detections are marked with visual indicators:

- Red shield icon for critical severity
- Orange warning icon for high severity
- Yellow info icon for medium severity
- Gray info icon for low severity

### Detail View

Clicking on an activity with detections shows:

- List of all detected sensitive data types
- Location (arguments or response)
- Redacted context for verification
- Timestamp and duration

## CLI Usage

### List Activities with Sensitive Data

```bash
# Show all activities with sensitive data detections
mcpproxy activity list --sensitive-data

# Filter by severity
mcpproxy activity list --sensitive-data --severity critical

# Combine with other filters
mcpproxy activity list --sensitive-data --server github-server --status success
```

### View Detection Details

```bash
# Show full details including sensitive data metadata
mcpproxy activity show 01JFXYZ123ABC

# JSON output for scripting
mcpproxy activity show 01JFXYZ123ABC --output json
```

### Export for Compliance

```bash
# Export activities with sensitive data for security review
mcpproxy activity export --sensitive-data --output security-audit.jsonl

# Export critical severity only
mcpproxy activity export --sensitive-data --severity critical --output critical-findings.jsonl
```

### Summary Statistics

```bash
# Show sensitive data detection summary
mcpproxy activity summary --period 24h

# Output includes detection counts by category and severity
```

## Configuration

Sensitive data detection is enabled by default. Configure via `mcp_config.json`:

```json
{
  "sensitive_data_detection": {
    "enabled": true,
    "scan_arguments": true,
    "scan_responses": true,
    "severity_threshold": "low",
    "categories": {
      "cloud_credentials": true,
      "private_key": true,
      "api_token": true,
      "auth_token": true,
      "sensitive_file": true,
      "database_credential": true,
      "high_entropy": true,
      "credit_card": true
    }
  }
}
```

| Setting | Default | Description |
|---------|---------|-------------|
| `enabled` | true | Enable/disable sensitive data detection |
| `scan_arguments` | true | Scan tool call arguments |
| `scan_responses` | true | Scan tool call responses |
| `severity_threshold` | "low" | Minimum severity to log (low, medium, high, critical) |
| `categories.*` | true | Enable/disable specific detection categories |

See [Configuration](/configuration/sensitive-data-detection) for complete reference.

## Cross-Platform Support

Sensitive file path detection adapts to the operating system:

| Platform | Path Patterns |
|----------|---------------|
| **macOS** | `~/Library/`, `~/.ssh/`, `~/.aws/`, `~/.config/` |
| **Linux** | `~/.ssh/`, `~/.aws/`, `~/.config/`, `/etc/ssl/private/` |
| **Windows** | `%USERPROFILE%\.ssh\`, `%USERPROFILE%\.aws\`, `%APPDATA%\` |

Path detection normalizes separators and expands home directory references for consistent cross-platform detection.

## Security Best Practices

### Compliance Auditing

Use sensitive data detection for regular security audits:

```bash
# Weekly security audit export
mcpproxy activity export \
  --sensitive-data \
  --start-time "$(date -v-7d +%Y-%m-%dT00:00:00Z)" \
  --output weekly-security-audit.jsonl

# Generate summary report
mcpproxy activity summary --period 7d --output json > weekly-summary.json
```

### Real-time Monitoring

Monitor for critical detections in real-time:

```bash
# Watch for sensitive data detections
mcpproxy activity watch --sensitive-data --severity critical
```

### Integration with SIEM

Export activity logs for integration with Security Information and Event Management (SIEM) systems:

```bash
# Continuous export for SIEM ingestion
mcpproxy activity export --format json --output - | \
  your-siem-forwarder --input -
```

### Incident Response

When a critical detection is identified:

1. **Review the activity**: `mcpproxy activity show <id>`
2. **Identify the source**: Check server name and tool name
3. **Assess impact**: Determine if credentials were exposed externally
4. **Rotate credentials**: If exposed, rotate the affected credentials immediately
5. **Investigate root cause**: Review how sensitive data entered the tool call

### Prevention Recommendations

1. **Use Docker isolation** for untrusted servers with `network_mode: "none"`
2. **Enable quarantine** for new servers added by AI agents
3. **Review tool descriptions** for potential data exfiltration patterns
4. **Set up alerts** for critical severity detections
5. **Regular audits** of activity logs for security compliance

## Related Features

- [Activity Log](/features/activity-log) - Core activity logging functionality
- [Security Quarantine](/features/security-quarantine) - Protection against Tool Poisoning Attacks
- [Docker Isolation](/features/docker-isolation) - Container-based server isolation
- [Intent Declaration](/features/intent-declaration) - Track operation types and data sensitivity
