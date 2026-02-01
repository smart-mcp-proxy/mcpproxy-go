---
id: sensitive-data-detection
title: Sensitive Data Detection
sidebar_label: Sensitive Data Detection
sidebar_position: 4
description: Configure sensitive data detection for MCP tool calls
keywords: [security, sensitive data, secrets, credentials, detection, entropy]
---

# Sensitive Data Detection

MCPProxy includes automatic sensitive data detection that scans MCP tool call arguments and responses for secrets, credentials, API keys, and other potentially exposed data. This feature helps identify accidental data exposure in your AI agent workflows.

## Full Configuration Schema

```json
{
  "sensitive_data_detection": {
    "enabled": true,
    "scan_requests": true,
    "scan_responses": true,
    "max_payload_size_kb": 1024,
    "entropy_threshold": 4.5,
    "categories": {
      "cloud_credentials": true,
      "private_key": true,
      "api_token": true,
      "auth_token": true,
      "sensitive_file": true,
      "database_credential": true,
      "high_entropy": true,
      "credit_card": true
    },
    "custom_patterns": [
      {
        "name": "acme_api_key",
        "regex": "ACME-[A-Z0-9]{32}",
        "severity": "high",
        "category": "api_token"
      }
    ],
    "sensitive_keywords": ["SECRET_PROJECT", "INTERNAL_KEY"]
  }
}
```

## Configuration Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | boolean | `true` | Enable or disable sensitive data detection entirely |
| `scan_requests` | boolean | `true` | Scan tool call arguments for sensitive data |
| `scan_responses` | boolean | `true` | Scan tool responses for sensitive data |
| `max_payload_size_kb` | integer | `1024` | Maximum payload size to scan in kilobytes |
| `entropy_threshold` | float | `4.5` | Shannon entropy threshold for high-entropy string detection |

## Detection Categories

MCPProxy detects sensitive data across multiple categories. Each category can be individually enabled or disabled.

### Category Reference

| Category | Description | Severity | Examples |
|----------|-------------|----------|----------|
| `cloud_credentials` | Cloud provider credentials | Critical/High | AWS access keys, GCP API keys, Azure connection strings |
| `private_key` | Cryptographic private keys | Critical | RSA, EC, DSA, OpenSSH, PGP private keys |
| `api_token` | Service API tokens | Critical/High | GitHub PATs, Stripe keys, OpenAI keys, Anthropic keys |
| `auth_token` | Authentication tokens | High/Medium | JWT tokens, Bearer tokens |
| `sensitive_file` | Sensitive file paths | High | SSH keys, credentials files, private key files |
| `database_credential` | Database connection strings | Critical/High | MySQL, PostgreSQL, MongoDB, Redis connection strings |
| `high_entropy` | High-entropy strings | Medium | Random strings that may be secrets |
| `credit_card` | Payment card numbers | Critical | Credit card numbers (Luhn-validated) |

### Built-in Detection Patterns

#### Cloud Credentials
- **AWS Access Key**: `AKIA...`, `ASIA...` (20 characters)
- **AWS Secret Key**: 40-character base64 strings
- **GCP API Key**: `AIza...` (39 characters)
- **GCP Service Account**: JSON with `"type": "service_account"`
- **Azure Client Secret**: 34+ character strings with special characters
- **Azure Connection String**: Contains `AccountKey=...`

#### Private Keys
- RSA Private Key: `-----BEGIN RSA PRIVATE KEY-----`
- EC Private Key: `-----BEGIN EC PRIVATE KEY-----`
- DSA Private Key: `-----BEGIN DSA PRIVATE KEY-----`
- OpenSSH Private Key: `-----BEGIN OPENSSH PRIVATE KEY-----`
- PGP Private Key: `-----BEGIN PGP PRIVATE KEY BLOCK-----`
- PKCS#8 Private Key: `-----BEGIN PRIVATE KEY-----`

#### API Tokens
- **GitHub**: `ghp_...`, `gho_...`, `ghs_...`, `ghr_...`, `github_pat_...`
- **GitLab**: `glpat-...`
- **Stripe**: `sk_live_...`, `pk_live_...`, `sk_test_...`
- **Slack**: `xoxb-...`, `xoxp-...`, `xapp-...`
- **SendGrid**: `SG....`
- **OpenAI**: `sk-...`, `sk-proj-...`
- **Anthropic**: `sk-ant-api...`

#### Authentication Tokens
- **JWT**: Base64-encoded tokens starting with `eyJ`
- **Bearer Token**: `Bearer ...` authorization headers

#### Database Credentials
- MySQL connection strings: `mysql://user:pass@host`
- PostgreSQL connection strings: `postgresql://user:pass@host`
- MongoDB connection strings: `mongodb://user:pass@host`
- Redis connection strings: `redis://:pass@host`
- Database password environment variables: `DB_PASSWORD=...`

#### Credit Cards
- Visa, Mastercard, American Express, Discover, JCB, Diners Club
- Validated using the Luhn algorithm
- Known test card numbers are flagged as examples

### Enabling/Disabling Categories

To disable specific categories:

```json
{
  "sensitive_data_detection": {
    "categories": {
      "cloud_credentials": true,
      "private_key": true,
      "api_token": true,
      "auth_token": true,
      "sensitive_file": true,
      "database_credential": true,
      "high_entropy": false,
      "credit_card": true
    }
  }
}
```

Categories not specified in the configuration are enabled by default.

## Custom Patterns Configuration

You can define custom detection patterns for organization-specific secrets or internal credentials.

### Regex-Based Patterns

Use regular expressions to match specific formats:

```json
{
  "sensitive_data_detection": {
    "custom_patterns": [
      {
        "name": "acme_api_key",
        "regex": "ACME-[A-Z0-9]{32}",
        "severity": "high",
        "category": "api_token"
      },
      {
        "name": "internal_service_token",
        "regex": "SVC_[a-zA-Z0-9]{24}_[0-9]{10}",
        "severity": "critical",
        "category": "auth_token"
      },
      {
        "name": "internal_db_password",
        "regex": "(?i)INTERNAL_DB_PASS=[^\\s]+",
        "severity": "critical",
        "category": "database_credential"
      }
    ]
  }
}
```

### Keyword-Based Patterns

Use simple keyword matching for straightforward detection:

```json
{
  "sensitive_data_detection": {
    "custom_patterns": [
      {
        "name": "internal_project_id",
        "keywords": ["PROJ-SECRET", "INTERNAL-KEY", "CONFIDENTIAL-TOKEN"],
        "severity": "medium"
      },
      {
        "name": "legacy_api_marker",
        "keywords": ["X-Legacy-Auth", "OldApiKey"],
        "severity": "low",
        "category": "api_token"
      }
    ]
  }
}
```

### Pattern Configuration Options

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Unique identifier for the pattern |
| `regex` | string | No* | Regular expression pattern |
| `keywords` | array | No* | List of keywords to match (case-insensitive) |
| `severity` | string | Yes | Risk level: `critical`, `high`, `medium`, or `low` |
| `category` | string | No | Category for grouping (defaults to `custom`) |

*Either `regex` or `keywords` must be specified, but not both.

### Severity Levels

| Severity | Description | Use Cases |
|----------|-------------|-----------|
| `critical` | Immediate security risk | Private keys, cloud credentials, production API keys |
| `high` | Significant security concern | API tokens, database passwords, OAuth tokens |
| `medium` | Potential security issue | High-entropy strings, internal tokens |
| `low` | Informational | Keywords, debug markers |

## Sensitive Keywords Configuration

For simple keyword matching without creating full pattern definitions, use the `sensitive_keywords` array:

```json
{
  "sensitive_data_detection": {
    "sensitive_keywords": [
      "SUPER_SECRET",
      "INTERNAL_API_KEY",
      "CONFIDENTIAL_TOKEN",
      "PRIVATE_DATA",
      "DO_NOT_SHARE"
    ]
  }
}
```

Keywords are matched case-insensitively. Each match is reported with:
- **Type**: `sensitive_keyword`
- **Category**: `custom`
- **Severity**: `low`

## Entropy Threshold Tuning

### Understanding Shannon Entropy

Shannon entropy measures the randomness of a string. Higher entropy indicates more randomness, which often suggests a secret or credential.

**Entropy Ranges:**
| Range | Description | Examples |
|-------|-------------|----------|
| < 3.0 | Low entropy | Natural language, repeated characters |
| 3.0 - 4.0 | Medium entropy | Encoded data, UUIDs |
| 4.0 - 4.5 | High entropy | Possibly a secret |
| > 4.5 | Very high entropy | Likely a random secret |

### Adjusting the Threshold

The default threshold of `4.5` balances detection accuracy with false positives:

```json
{
  "sensitive_data_detection": {
    "entropy_threshold": 4.5
  }
}
```

**Lower threshold (e.g., 4.0):**
- More detections
- Higher false positive rate
- Use when security is paramount

**Higher threshold (e.g., 5.0):**
- Fewer detections
- Lower false positive rate
- Use when dealing with many encoded strings

### High-Entropy Detection Behavior

- Scans for strings 20+ characters matching base64-like patterns
- Applies entropy calculation to each candidate
- Skips strings already matched by other patterns (to avoid duplicates)
- Limited to 5 high-entropy matches per scan to prevent noise

## Performance Considerations

### Payload Size Limits

The `max_payload_size_kb` setting controls the maximum size of content scanned:

```json
{
  "sensitive_data_detection": {
    "max_payload_size_kb": 1024
  }
}
```

**Impact:**
- Larger limits increase scan time
- Content exceeding the limit is truncated
- Truncated scans are marked with `truncated: true` in results
- Default of 1024 KB (1 MB) balances thoroughness with performance

### Recommended Settings by Use Case

**High-Security Environments:**
```json
{
  "sensitive_data_detection": {
    "enabled": true,
    "scan_requests": true,
    "scan_responses": true,
    "max_payload_size_kb": 2048,
    "entropy_threshold": 4.0
  }
}
```

**Performance-Sensitive Environments:**
```json
{
  "sensitive_data_detection": {
    "enabled": true,
    "scan_requests": true,
    "scan_responses": false,
    "max_payload_size_kb": 512,
    "entropy_threshold": 4.8,
    "categories": {
      "high_entropy": false
    }
  }
}
```

**Minimal Detection (Critical Only):**
```json
{
  "sensitive_data_detection": {
    "enabled": true,
    "scan_requests": true,
    "scan_responses": true,
    "categories": {
      "cloud_credentials": true,
      "private_key": true,
      "api_token": true,
      "auth_token": false,
      "sensitive_file": false,
      "database_credential": true,
      "high_entropy": false,
      "credit_card": true
    }
  }
}
```

### Detection Limits

- Maximum 50 detections per scan to prevent excessive processing
- High-entropy detection limited to 5 matches per content block
- Patterns are evaluated in order, stopping at detection limit

## Detection Results

When sensitive data is detected, the result includes:

```json
{
  "detected": true,
  "detections": [
    {
      "type": "aws_access_key",
      "category": "cloud_credentials",
      "severity": "critical",
      "location": "arguments",
      "is_likely_example": false
    }
  ],
  "scan_duration_ms": 12,
  "truncated": false
}
```

### Result Fields

| Field | Description |
|-------|-------------|
| `detected` | `true` if any sensitive data was found |
| `detections` | Array of detection details |
| `scan_duration_ms` | Time taken to scan in milliseconds |
| `truncated` | `true` if payload exceeded max size and was truncated |

### Detection Fields

| Field | Description |
|-------|-------------|
| `type` | Pattern name that matched (e.g., `aws_access_key`) |
| `category` | Detection category (e.g., `cloud_credentials`) |
| `severity` | Risk level (`critical`, `high`, `medium`, `low`) |
| `location` | Where the match was found (`arguments` or `response`) |
| `is_likely_example` | `true` if the match appears to be a known test/example value |

## Disabling Detection

To completely disable sensitive data detection:

```json
{
  "sensitive_data_detection": {
    "enabled": false
  }
}
```

Or to scan only requests (not responses):

```json
{
  "sensitive_data_detection": {
    "scan_requests": true,
    "scan_responses": false
  }
}
```

## Related Documentation

- [Activity Log](/cli/activity-commands) - View detected sensitive data in activity logs
- [Security Quarantine](/features/security-quarantine) - Server security and approval
- [Configuration File](/configuration/config-file) - Main configuration reference
