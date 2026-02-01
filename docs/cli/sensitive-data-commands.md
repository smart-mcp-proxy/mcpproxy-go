---
id: sensitive-data-commands
title: CLI Sensitive Data Commands
sidebar_label: Sensitive Data Commands
sidebar_position: 4
description: CLI commands for querying activity logs with sensitive data detection
keywords: [activity, logging, sensitive data, secrets, credentials, cli, security]
---

# Sensitive Data Commands

MCPProxy activity logs include sensitive data detection capabilities. When tool calls contain potentially sensitive information (API keys, credentials, tokens, etc.), the activity log captures detection metadata for security auditing and compliance.

## Overview

Sensitive data detection is automatically applied to all tool call arguments and responses. The CLI provides filtering and display options to help you identify and audit activities involving sensitive data.

---

## Activity List with Sensitive Data Filter

### Show Activities with Sensitive Data

Filter the activity list to show only entries where sensitive data was detected:

```bash
# Show only activities with sensitive data detected
mcpproxy activity list --sensitive-data
```

### Filter by Detection Type

Filter activities by the specific type of sensitive data detected:

```bash
# Filter by detection type
mcpproxy activity list --detection-type aws_access_key

# Common detection types:
#   aws_access_key      - AWS Access Key IDs
#   aws_secret_key      - AWS Secret Access Keys
#   github_token        - GitHub Personal Access Tokens
#   api_key             - Generic API keys
#   private_key         - RSA/SSH private keys
#   password            - Password patterns
#   bearer_token        - Bearer authentication tokens
#   connection_string   - Database connection strings
#   jwt                 - JSON Web Tokens
```

### Filter by Severity

Filter activities by the severity level of detected sensitive data:

```bash
# Filter by severity
mcpproxy activity list --severity critical

# Severity levels:
#   critical  - High-risk credentials (private keys, cloud secrets)
#   high      - Authentication tokens, API keys
#   medium    - Potential PII, internal identifiers
#   low       - Informational detections
```

### Combine Filters

Combine multiple filters for precise queries:

```bash
# Combine filters for targeted search
mcpproxy activity list --sensitive-data --severity critical

# Filter by type and server
mcpproxy activity list --detection-type aws_access_key --server github

# Filter by severity with time range
mcpproxy activity list --severity high --start-time "$(date -u +%Y-%m-%dT00:00:00Z)"

# Full combination
mcpproxy activity list \
  --sensitive-data \
  --severity critical \
  --server myserver \
  --limit 100
```

---

## Activity Show Detection Details

View full detection details for a specific activity record:

```bash
# View full detection details for an activity
mcpproxy activity show <activity-id>
```

### Example Output

```
Activity Details
================

ID:           01JGXYZ789DEF
Type:         tool_call
Source:       MCP (AI agent via MCP protocol)
Server:       github
Tool:         create_secret
Status:       success
Duration:     312ms
Timestamp:    2025-01-15T14:22:33Z
Session ID:   mcp-session-xyz789

Arguments:
  {
    "name": "API_KEY",
    "value": "sk-***REDACTED***"
  }

Sensitive Data Detections:
  ┌─────────────────┬──────────┬──────────────────────────────────────┐
  │ TYPE            │ SEVERITY │ LOCATION                             │
  ├─────────────────┼──────────┼──────────────────────────────────────┤
  │ api_key         │ high     │ arguments.value                      │
  └─────────────────┴──────────┴──────────────────────────────────────┘

  Detection Count: 1
  Highest Severity: high

Response:
  Secret 'API_KEY' created successfully
```

### Show with Full Response

```bash
# Show with full response body (may contain redacted values)
mcpproxy activity show 01JGXYZ789DEF --include-response
```

---

## JSON/YAML Output Examples

### JSON Output with Detection Data

```bash
# JSON output with detection data
mcpproxy activity list --sensitive-data -o json
```

Example JSON output:

```json
{
  "activities": [
    {
      "id": "01JGXYZ789DEF",
      "type": "tool_call",
      "source": "mcp",
      "server_name": "github",
      "tool_name": "create_secret",
      "status": "success",
      "duration_ms": 312,
      "timestamp": "2025-01-15T14:22:33Z",
      "request_id": "b2c3d4e5-f6g7-8901-hijk-lm2345678901",
      "sensitive_data": {
        "detected": true,
        "detection_count": 1,
        "highest_severity": "high",
        "detections": [
          {
            "type": "api_key",
            "severity": "high",
            "location": "arguments.value",
            "pattern_matched": "sk-*"
          }
        ]
      }
    },
    {
      "id": "01JGXYZ789ABC",
      "type": "tool_call",
      "source": "mcp",
      "server_name": "aws",
      "tool_name": "put_secret_value",
      "status": "success",
      "duration_ms": 456,
      "timestamp": "2025-01-15T14:20:15Z",
      "request_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
      "sensitive_data": {
        "detected": true,
        "detection_count": 2,
        "highest_severity": "critical",
        "detections": [
          {
            "type": "aws_access_key",
            "severity": "critical",
            "location": "arguments.access_key_id",
            "pattern_matched": "AKIA*"
          },
          {
            "type": "aws_secret_key",
            "severity": "critical",
            "location": "arguments.secret_access_key",
            "pattern_matched": "[REDACTED]"
          }
        ]
      }
    }
  ],
  "total": 2,
  "limit": 50,
  "offset": 0
}
```

### YAML Output

```bash
# YAML output
mcpproxy activity list --sensitive-data -o yaml
```

Example YAML output:

```yaml
activities:
  - id: 01JGXYZ789DEF
    type: tool_call
    source: mcp
    server_name: github
    tool_name: create_secret
    status: success
    duration_ms: 312
    timestamp: "2025-01-15T14:22:33Z"
    request_id: b2c3d4e5-f6g7-8901-hijk-lm2345678901
    sensitive_data:
      detected: true
      detection_count: 1
      highest_severity: high
      detections:
        - type: api_key
          severity: high
          location: arguments.value
          pattern_matched: "sk-*"
  - id: 01JGXYZ789ABC
    type: tool_call
    source: mcp
    server_name: aws
    tool_name: put_secret_value
    status: success
    duration_ms: 456
    timestamp: "2025-01-15T14:20:15Z"
    request_id: a1b2c3d4-e5f6-7890-abcd-ef1234567890
    sensitive_data:
      detected: true
      detection_count: 2
      highest_severity: critical
      detections:
        - type: aws_access_key
          severity: critical
          location: arguments.access_key_id
          pattern_matched: "AKIA*"
        - type: aws_secret_key
          severity: critical
          location: arguments.secret_access_key
          pattern_matched: "[REDACTED]"
total: 2
limit: 50
offset: 0
```

---

## Table Output

The default table output includes a SENSITIVE indicator column when sensitive data is detected:

```bash
mcpproxy activity list --sensitive-data
```

Example table output:

```
ID               SRC  TYPE         SERVER   TOOL              SENSITIVE  INTENT  STATUS   DURATION   TIME
01JGXYZ789DEF    MCP  tool_call    github   create_secret     HIGH       write   success  312ms      5 min ago
01JGXYZ789ABC    MCP  tool_call    aws      put_secret_value  CRITICAL   write   success  456ms      7 min ago
01JGXYZ789GHI    MCP  tool_call    vault    store_password    HIGH       write   success  189ms      10 min ago
01JGXYZ789JKL    CLI  tool_call    secrets  set_token         MEDIUM     write   success  78ms       15 min ago

Showing 4 of 4 records (page 1)
```

**SENSITIVE Column Values:**
- `CRITICAL` - Critical severity detections (displayed in red if color enabled)
- `HIGH` - High severity detections (displayed in yellow)
- `MEDIUM` - Medium severity detections
- `LOW` - Low severity detections
- Empty - No sensitive data detected

---

## Filtering Options Reference

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--sensitive-data` | | false | Filter to show only activities with sensitive data detected |
| `--detection-type` | | | Filter by specific detection type (e.g., `aws_access_key`, `api_key`, `private_key`) |
| `--severity` | | | Filter by minimum severity level: `critical`, `high`, `medium`, `low` |
| `--type` | `-t` | | Filter by activity type (can combine with sensitive data filters) |
| `--server` | `-s` | | Filter by server name |
| `--tool` | | | Filter by tool name |
| `--status` | | | Filter by status: `success`, `error`, `blocked` |
| `--start-time` | | | Filter records after this time (RFC3339) |
| `--end-time` | | | Filter records before this time (RFC3339) |
| `--limit` | `-n` | 50 | Max records to return (1-100) |
| `--offset` | | 0 | Pagination offset |
| `--output` | `-o` | table | Output format: `table`, `json`, `yaml` |
| `--no-icons` | | false | Disable emoji icons in table output |

### Detection Types Reference

| Type | Description | Severity |
|------|-------------|----------|
| `aws_access_key` | AWS Access Key ID (AKIA...) | critical |
| `aws_secret_key` | AWS Secret Access Key | critical |
| `gcp_service_account` | GCP Service Account Key | critical |
| `azure_storage_key` | Azure Storage Account Key | critical |
| `private_key` | RSA/SSH/PGP Private Keys | critical |
| `github_token` | GitHub Personal Access Token | high |
| `github_oauth` | GitHub OAuth Token | high |
| `gitlab_token` | GitLab Personal Access Token | high |
| `npm_token` | NPM Access Token | high |
| `pypi_token` | PyPI API Token | high |
| `slack_token` | Slack Bot/User Token | high |
| `stripe_key` | Stripe API Key | high |
| `bearer_token` | Bearer Authentication Token | high |
| `api_key` | Generic API Key patterns | high |
| `jwt` | JSON Web Token | high |
| `password` | Password field patterns | high |
| `connection_string` | Database Connection String | high |
| `basic_auth` | Basic Authentication Header | medium |
| `email` | Email Address | medium |
| `ip_address` | IP Address | low |

---

## Common Workflows

### Security Audit

```bash
# Find all critical sensitive data exposures in the last 24 hours
mcpproxy activity list \
  --sensitive-data \
  --severity critical \
  --start-time "$(date -u -v-24H +%Y-%m-%dT%H:%M:%SZ)" \
  -o json

# Export for compliance review
mcpproxy activity export \
  --sensitive-data \
  --output sensitive-data-audit.jsonl
```

### Investigate Specific Detection

```bash
# List activities with AWS credentials detected
mcpproxy activity list --detection-type aws_access_key

# Get full details on suspicious activity
mcpproxy activity show 01JGXYZ789ABC --include-response
```

### Monitor in Real-Time

```bash
# Watch for sensitive data in real-time
mcpproxy activity watch --type tool_call

# Filter output with jq for sensitive data
mcpproxy activity watch -o json | jq 'select(.sensitive_data.detected == true)'
```

### Export for SIEM

```bash
# Export sensitive data activities for SIEM ingestion
mcpproxy activity export \
  --sensitive-data \
  --format json \
  --include-bodies \
  --output /var/log/mcpproxy/sensitive-data.jsonl
```

---

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Error fetching activity |
| 2 | Invalid filter parameters |

---

## Tips

- Use `--sensitive-data` as a quick filter to focus on security-relevant activities
- Combine `--severity critical` with `--start-time` for incident response
- Export to JSON for integration with security tools and SIEMs
- The `--detection-type` flag accepts exact matches only
- Sensitive data values are automatically redacted in logs and output
- Use `mcpproxy activity show <id>` for full detection context including field locations
