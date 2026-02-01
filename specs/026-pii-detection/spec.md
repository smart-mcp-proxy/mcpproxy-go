# Feature Specification: Sensitive Data Detection

**Feature Branch**: `026-pii-detection`
**Created**: 2026-01-31
**Status**: Draft
**Input**: User description: "Sensitive Data Detection - Detect secrets, API keys, private keys, sensitive file paths in tool calls. Integrate with Activity Log."
**Related Proposal**: `docs/proposals/004-security-attack-detection.md` (Phase 3)

## Overview

This feature adds automatic detection of sensitive data in MCP tool call arguments and responses. The focus is on **secrets and credentials** (API keys, private keys, tokens) and **sensitive file path access** (SSH keys, cloud credentials, environment files). Detection results are recorded in the Activity Log, enabling users to identify potential data exposure or exfiltration risks.

**Design Principle**: Detection-only mode - no automatic blocking or redaction. Users gain visibility into sensitive data flows to make informed decisions.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Detect Secrets in Tool Call Data (Priority: P1)

A security-conscious user wants to know when API keys, tokens, or credentials pass through MCPProxy. When an AI agent accidentally exposes an AWS key or GitHub token in tool arguments or responses, the user should see this flagged in the Activity Log.

**Why this priority**: Secrets are the highest-risk sensitive data. Leaked credentials can lead to account takeover, data breaches, and financial loss. This is the core security value.

**Independent Test**: Execute a tool call with an AWS access key (AKIAIOSFODNN7EXAMPLE) in arguments, view Activity Log, verify detection indicator shows "aws_access_key" type.

**Acceptance Scenarios**:

1. **Given** a tool call contains `AKIAIOSFODNN7EXAMPLE` in arguments, **When** I view the Activity Log, **Then** I see a sensitive data indicator with "aws_access_key" detected
2. **Given** a tool response contains a GitHub PAT (`ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx`), **When** I view activity details, **Then** I see "github_token" type with location "response"
3. **Given** a tool call contains `-----BEGIN RSA PRIVATE KEY-----`, **When** detection runs, **Then** "private_key" type is detected with severity "critical"
4. **Given** a tool call contains a Stripe key (`sk_live_` + 24 chars), **When** I view details, **Then** "stripe_key" is detected

---

### User Story 2 - Detect Sensitive File Path Access (Priority: P1)

A user wants to know when an AI agent attempts to read sensitive files like SSH private keys, AWS credentials, or environment files. This could indicate a compromised or malicious MCP server attempting data exfiltration.

**Why this priority**: File path detection catches exfiltration attempts at the intent stage - before secrets are actually exposed. This is critical for detecting tool poisoning attacks.

**Independent Test**: Execute a tool call with argument `{"path": "~/.ssh/id_rsa"}`, verify Activity Log shows "sensitive_file_path" detection with "ssh_private_key" category.

**Acceptance Scenarios**:

1. **Given** a tool argument contains `~/.ssh/id_rsa` (Linux/macOS) or `C:\Users\user\.ssh\id_rsa` (Windows), **When** detection runs, **Then** "sensitive_file_path" is detected with category "ssh" and severity "critical"
2. **Given** a tool argument contains `/home/user/.aws/credentials` (Linux) or `%USERPROFILE%\.aws\credentials` (Windows), **When** I view details, **Then** "aws_credentials_file" is detected
3. **Given** a tool argument contains `.env.production`, **When** detection runs, **Then** "env_file" is detected with severity "high"
4. **Given** a tool argument contains `/etc/shadow` (Linux), **When** detection runs, **Then** "system_password_file" is detected with severity "critical"
5. **Given** a tool argument contains `C:\Users\user\AppData\Roaming\npm\npmrc` (Windows), **When** detection runs, **Then** "auth_token_file" is detected

---

### User Story 3 - View and Filter Detection Results (Priority: P1)

A compliance officer needs to audit all tool calls that involved sensitive data. They want to filter the Activity Log to show only records where sensitive data was detected, with the ability to filter by detection type and severity.

**Why this priority**: Filtering is essential for practical use at scale. Without it, users must manually scan all records, making security auditing impractical.

**Independent Test**: Execute multiple tool calls (some with secrets, some without), filter Activity Log by "Sensitive Data: Yes" and severity "critical", verify only relevant records appear.

**Acceptance Scenarios**:

1. **Given** the Activity Log contains mixed records, **When** I filter by "Sensitive Data Detected", **Then** only records with detections are shown
2. **Given** I want to find API key exposures, **When** I filter by type "api_key", **Then** only records with API key detections appear
3. **Given** I want critical issues only, **When** I filter by severity "critical", **Then** only critical detections (private keys, cloud credentials) appear
4. **Given** I'm using CLI, **When** I run `mcpproxy activity list --sensitive-data --severity critical`, **Then** filtered results are returned

---

### User Story 4 - CLI Sensitive Data Visibility (Priority: P2)

A developer using MCPProxy via CLI wants to see sensitive data detection results when reviewing activity. The CLI should show detection status in list output and full details in the show command.

**Why this priority**: CLI is a primary interface for developers and automation. Enables scripting and integration with security tools.

**Independent Test**: Run `mcpproxy activity list` after sensitive data detection, verify indicator column. Run `mcpproxy activity show <id>`, verify full detection details.

**Acceptance Scenarios**:

1. **Given** activity records with detections exist, **When** I run `mcpproxy activity list`, **Then** I see a "SENSITIVE" indicator column
2. **Given** an activity record with detected secrets, **When** I run `mcpproxy activity show <id>`, **Then** I see detection types, severities, and locations
3. **Given** I want JSON for automation, **When** I run `mcpproxy activity list --sensitive-data -o json`, **Then** I get structured detection data

---

### User Story 5 - Configure Custom Detection Patterns (Priority: P3)

An enterprise user has organization-specific sensitive data formats (e.g., internal API key format `ACME-KEY-xxxxxxxx`, employee IDs) that should be detected. They want to add custom regex patterns.

**Why this priority**: Custom patterns extend the system to organization-specific needs but are not required for core functionality. Most users benefit from built-in patterns alone.

**Independent Test**: Add custom pattern via configuration, execute tool call with matching data, verify custom pattern detected in Activity Log.

**Acceptance Scenarios**:

1. **Given** I configure `{"name": "acme_api_key", "regex": "ACME-KEY-[a-f0-9]{32}", "severity": "high"}`, **When** a tool call contains "ACME-KEY-abc123...", **Then** it is detected as "acme_api_key"
2. **Given** I add sensitive keywords `["internal-only", "confidential"]`, **When** those words appear in tool data, **Then** they are flagged
3. **Given** an invalid regex pattern, **When** MCPProxy starts, **Then** I see a warning and the invalid pattern is skipped

---

### User Story 6 - Detect Credit Card Numbers (Priority: P3)

A user working with payment-related tools wants to ensure credit card numbers are flagged if they appear in tool call data, as this could indicate PCI compliance issues.

**Why this priority**: Credit cards are a special PII category with regulatory implications (PCI-DSS). Lower priority than secrets because legitimate payment tools may handle card data intentionally.

**Independent Test**: Execute tool call with test card number `4111111111111111`, verify detection with Luhn validation (valid card detected, random 16-digit numbers ignored).

**Acceptance Scenarios**:

1. **Given** a tool call contains `4111111111111111`, **When** detection runs, **Then** "credit_card" is detected (passes Luhn validation)
2. **Given** a tool call contains `1234567890123456` (invalid Luhn), **When** detection runs, **Then** it is NOT flagged as credit card
3. **Given** a tool call contains `4111-1111-1111-1111` (with dashes), **When** detection runs, **Then** "credit_card" is still detected

---

### Edge Cases

- What happens when detection encounters very large payloads (>1MB)? Detection applies to first 1MB with truncation flag.
- How are false positives handled? Users view detection details to assess; no automatic action taken.
- What if a secret pattern matches example/test values? Known test patterns (AKIAIOSFODNN7EXAMPLE) are flagged but marked as "likely_example".
- What happens with base64-encoded secrets? Detection scans raw content; base64-encoded PEM keys are still detected by their markers.
- How are secrets in JSON string escapes handled? Content is unescaped (`\\n` → `\n`) before scanning.

## Requirements *(mandatory)*

### Functional Requirements

**Secret Detection (Tier 1 - Critical)**
- **FR-001**: System MUST detect AWS credentials (access key IDs matching `AKIA[0-9A-Z]{16}` and similar prefixes)
- **FR-002**: System MUST detect private keys (RSA, EC, DSA, OpenSSH, PGP) via PEM header markers
- **FR-003**: System MUST detect GitHub tokens (PAT, OAuth, App tokens matching `gh[pous]_[0-9a-zA-Z]{36,}`)
- **FR-004**: System MUST detect GitLab tokens (`glpat-`, `gldt-`, runner tokens)
- **FR-005**: System MUST detect GCP API keys (`AIza[0-9A-Za-z\-_]{35}`)
- **FR-006**: System MUST detect Azure credentials (client secrets, storage keys)
- **FR-007**: System MUST detect OpenAI/Anthropic API keys
- **FR-008**: System MUST detect JWT tokens via `eyJ` prefix pattern

**Secret Detection (Tier 2 - High)**
- **FR-009**: System MUST detect Stripe keys (`sk_live_`, `sk_test_`, `pk_live_`)
- **FR-010**: System MUST detect Slack tokens (`xoxb-`, `xoxp-`, webhook URLs)
- **FR-011**: System MUST detect SendGrid API keys (`SG\.[a-zA-Z0-9_-]{22}\.`)
- **FR-012**: System MUST detect Twilio credentials (Account SID, Auth Token)
- **FR-013**: System MUST detect database connection strings with embedded credentials
- **FR-014**: System MUST detect high-entropy strings (Shannon entropy > 4.5) as potential secrets

**Sensitive File Path Detection (Cross-Platform)**
- **FR-015**: System MUST detect SSH key paths on all platforms:
  - Linux/macOS: `~/.ssh/id_*`, `~/.ssh/authorized_keys`, `~/.ssh/config`
  - Windows: `%USERPROFILE%\.ssh\id_*`, `C:\Users\*\.ssh\*`
  - All: `*.pem`, `*.key`, `*.ppk`, `*.pub` (when private key indicators present)
- **FR-016**: System MUST detect cloud credential paths on all platforms:
  - Linux: `~/.aws/credentials`, `~/.config/gcloud/*`, `~/.azure/*`, `~/.kube/config`
  - macOS: `~/.aws/credentials`, `~/Library/Application Support/gcloud/*`, `~/.azure/*`, `~/.kube/config`
  - Windows: `%USERPROFILE%\.aws\credentials`, `%APPDATA%\gcloud\*`, `%USERPROFILE%\.azure\*`, `%USERPROFILE%\.kube\config`
- **FR-017**: System MUST detect environment and config files (all platforms):
  - `.env`, `.env.*`, `.env.local`, `.env.production`, `.env.development`
  - `secrets.json`, `credentials.json`, `config.json` (in sensitive contexts)
  - `appsettings.json`, `appsettings.*.json` (ASP.NET)
  - `web.config` (IIS/ASP.NET - may contain connection strings)
- **FR-018**: System MUST detect auth token files on all platforms:
  - Linux/macOS: `.npmrc`, `.pypirc`, `.netrc`, `.git-credentials`, `.docker/config.json`
  - Windows: `%USERPROFILE%\.npmrc`, `%APPDATA%\npm\npmrc`, `%USERPROFILE%\.docker\config.json`
  - All: `.composer/auth.json`, `.gem/credentials`, `.nuget/NuGet.Config`
- **FR-019**: System MUST detect system sensitive files:
  - Linux: `/etc/shadow`, `/etc/sudoers`, `/etc/passwd`, `/proc/*/environ`, `/etc/ssh/sshd_config`
  - macOS: `/etc/sudoers`, `/etc/master.passwd`, `~/Library/Keychains/*`
  - Windows: `SAM`, `SYSTEM`, `SECURITY` (registry hives), `%SYSTEMROOT%\repair\SAM`
- **FR-020**: System MUST normalize paths before matching:
  - Expand: `~`, `$HOME`, `%USERPROFILE%`, `%APPDATA%`, `%LOCALAPPDATA%`, `%SYSTEMROOT%`
  - Handle both forward slashes and backslashes
  - Case-insensitive matching on Windows, case-sensitive on Linux/macOS

**Credit Card Detection**
- **FR-021**: System MUST detect credit card numbers and validate using Luhn algorithm
- **FR-022**: System MUST support card numbers with various separators (spaces, dashes)

**Activity Log Integration**
- **FR-023**: System MUST store detection results in `metadata.sensitive_data_detection` field
- **FR-024**: System MUST record: detected (boolean), types (list), locations (field paths), severities, scan_duration_ms
- **FR-025**: System MUST NOT store actual secret values in detection results (only types and locations)
- **FR-026**: System MUST scan both tool call arguments AND responses
- **FR-027**: System MUST run detection asynchronously without blocking tool responses

**User Interface - Web**
- **FR-028**: Web UI MUST display sensitive data indicator on Activity Log records
- **FR-029**: Web UI MUST show detection details (types, severities, locations) in expanded view
- **FR-030**: Web UI MUST provide filter by "sensitive data detected" (yes/no)
- **FR-031**: Web UI MUST provide filter by detection type and severity

**User Interface - CLI**
- **FR-032**: CLI `activity list` MUST include sensitive data indicator column
- **FR-033**: CLI `activity show` MUST display full detection details
- **FR-034**: CLI MUST support `--sensitive-data` flag to filter detections
- **FR-035**: CLI MUST support `--detection-type <type>` and `--severity <level>` filters

**Custom Patterns (Optional)**
- **FR-036**: System SHOULD allow custom regex patterns via configuration
- **FR-037**: System SHOULD allow custom sensitive keywords list
- **FR-038**: System MUST validate patterns at startup and warn on invalid regex
- **FR-039**: Custom patterns MUST specify: name, pattern/keywords, severity (low/medium/high/critical)

**REST API**
- **FR-040**: GET `/api/v1/activity` MUST support `sensitive_data` query parameter
- **FR-041**: GET `/api/v1/activity` MUST support `detection_type` and `severity` parameters
- **FR-042**: Activity responses MUST include `sensitive_data_detection` in metadata

### Key Entities

- **DetectionPattern**: Name, regex/keywords, severity, category, validation function (optional)
- **SensitiveDataDetectionResult**: detected (bool), detections (list of Detection), scan_duration_ms
- **Detection**: type, severity, location (field path), category, is_likely_example (bool)
- **ActivityRecord.metadata.sensitive_data_detection**: Extension storing detection results

### Detection Categories

| Category | Examples | Default Severity |
|----------|----------|------------------|
| `cloud_credentials` | AWS, GCP, Azure keys | Critical |
| `private_key` | RSA, SSH, PGP keys | Critical |
| `api_token` | GitHub, GitLab, Stripe | High |
| `auth_token` | JWT, OAuth tokens | High |
| `sensitive_file` | ~/.ssh/*, .env, .aws/credentials | Critical/High |
| `database_credential` | Connection strings | High |
| `high_entropy` | Random strings > 4.5 entropy | Medium |
| `credit_card` | Card numbers (Luhn valid) | Medium |
| `custom` | User-defined patterns | Configurable |

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Detection completes within 15ms for typical tool call payloads (<64KB)
- **SC-002**: Built-in patterns detect >95% of common secret formats (AWS, GitHub, Stripe, private keys)
- **SC-003**: False positive rate for credit cards is <5% (due to Luhn validation)
- **SC-004**: False positive rate for API keys is <10% (due to format-specific patterns with prefixes)
- **SC-005**: Users can identify sensitive data records within 3 seconds via Web UI filter
- **SC-006**: CLI users can filter and export sensitive data records in a single command
- **SC-007**: All file path patterns correctly match on Windows, Linux, and macOS with appropriate path expansion and case handling

## Configuration

Sensitive data detection is enabled by default. Configuration in `mcp_config.json`:

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
      "private_keys": true,
      "api_tokens": true,
      "sensitive_files": true,
      "credit_cards": true,
      "high_entropy": true
    },
    "custom_patterns": [
      {
        "name": "acme_api_key",
        "regex": "ACME-KEY-[a-f0-9]{32}",
        "severity": "high",
        "category": "custom"
      }
    ],
    "sensitive_keywords": ["internal-only", "confidential", "do-not-share"]
  }
}
```

## Assumptions

1. Detection is for awareness/auditing only - no automatic blocking or redaction in this phase
2. Detection runs asynchronously after tool completion to avoid impacting response latency
3. Only `tool_call` and `internal_tool_call` activity types are scanned
4. Known example/test values are flagged but marked as `is_likely_example: true`
5. Path detection uses glob-style matching with home directory expansion
6. The existing Activity Log infrastructure supports metadata extension

## Out of Scope

- Automatic secret redaction/masking in stored data
- Blocking tool calls based on detection (future feature)
- Real-time alerts/notifications (future feature)
- ML-based detection (NER/NLP for unstructured PII like names)
- Tool description scanning (separate TPA detection feature)
- International PII patterns (non-US SSN, phone formats)

## References

### Secret Detection Tools Researched
- **Gitleaks**: Pattern-based with entropy, allowlists, composite rules
- **TruffleHog**: 800+ detectors with live verification
- **detect-secrets**: Plugin architecture with entropy analysis

### Sensitive File Categories (Cross-Platform)

**SSH & Keys**
| Platform | Paths |
|----------|-------|
| Linux/macOS | `~/.ssh/id_*`, `~/.ssh/authorized_keys`, `~/.ssh/config` |
| Windows | `%USERPROFILE%\.ssh\*`, `C:\Users\*\.ssh\*` |
| All | `*.pem`, `*.key`, `*.ppk`, `*.p12`, `*.pfx`, `*.keystore`, `*.jks` |

**Cloud Credentials**
| Platform | Paths |
|----------|-------|
| Linux | `~/.aws/credentials`, `~/.config/gcloud/*`, `~/.azure/*`, `~/.kube/config` |
| macOS | `~/.aws/credentials`, `~/Library/Application Support/gcloud/*`, `~/.azure/*` |
| Windows | `%USERPROFILE%\.aws\credentials`, `%APPDATA%\gcloud\*`, `%USERPROFILE%\.azure\*`, `%USERPROFILE%\.kube\config` |

**Environment & Config**
| Platform | Paths |
|----------|-------|
| All | `.env`, `.env.*`, `secrets.json`, `credentials.json` |
| .NET | `appsettings.json`, `appsettings.*.json`, `web.config` |

**Auth Tokens**
| Platform | Paths |
|----------|-------|
| Linux/macOS | `.npmrc`, `.pypirc`, `.netrc`, `.git-credentials`, `.docker/config.json` |
| Windows | `%USERPROFILE%\.npmrc`, `%APPDATA%\npm\npmrc`, `%USERPROFILE%\.docker\config.json` |
| All | `.composer/auth.json`, `.gem/credentials`, `.nuget/NuGet.Config` |

**System Files**
| Platform | Paths |
|----------|-------|
| Linux | `/etc/shadow`, `/etc/sudoers`, `/etc/passwd`, `/proc/*/environ`, `/etc/ssh/sshd_config` |
| macOS | `/etc/sudoers`, `/etc/master.passwd`, `~/Library/Keychains/*` |
| Windows | `SAM`, `SYSTEM`, `SECURITY` (registry hives), `%SYSTEMROOT%\repair\SAM` |

### MCP Security Context
- Simon Willison's "Lethal Trifecta" - access to private data + untrusted content + external communication
- Tool Poisoning Attacks (TPA) - malicious instructions in tool descriptions
- Real incidents: WhatsApp exfiltration, xAI key leak, DeepSeek exposure

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- Use: `Related #[issue-number]` - Links without auto-closing
- Do NOT use: `Fixes #`, `Closes #`, `Resolves #`

### Co-Authorship
- Do NOT include AI tool attribution in commits

### Example Commit Message
```
feat(security): add sensitive data detection engine

Related #XXX

Implement detection for secrets and sensitive file paths in tool calls:
- Tier 1: Cloud credentials (AWS, GCP, Azure), private keys
- Tier 2: API tokens (GitHub, Stripe, Slack), database credentials
- File paths: SSH keys, cloud configs, env files
- Credit cards with Luhn validation

## Changes
- Add internal/security/detector.go with SensitiveDataDetector
- Add internal/security/patterns/ with pattern definitions
- Add internal/security/entropy.go for high-entropy detection
- Integrate with ActivityService.handleToolCallCompleted()

## Testing
- Unit tests for all pattern categories
- Luhn validation tests
- Path normalization tests
- Entropy threshold tests
```
