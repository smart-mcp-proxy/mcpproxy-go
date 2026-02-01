# Specification Quality Checklist: Sensitive Data Detection

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-01-31
**Updated**: 2026-01-31
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] No implementation details (languages, frameworks, APIs)
- [x] Focused on user value and business needs
- [x] Written for non-technical stakeholders
- [x] All mandatory sections completed

## Requirement Completeness

- [x] No [NEEDS CLARIFICATION] markers remain
- [x] Requirements are testable and unambiguous
- [x] Success criteria are measurable
- [x] Success criteria are technology-agnostic (no implementation details)
- [x] All acceptance scenarios are defined
- [x] Edge cases are identified
- [x] Scope is clearly bounded
- [x] Dependencies and assumptions identified

## Feature Readiness

- [x] All functional requirements have clear acceptance criteria
- [x] User scenarios cover primary flows
- [x] Feature meets measurable outcomes defined in Success Criteria
- [x] No implementation details leak into specification

## Notes

- Spec validated successfully
- Renamed from "PII Detection" to "Sensitive Data Detection" per user request
- Focus shifted to secrets/credentials over traditional PII (email, phone)
- Credit cards retained due to PCI compliance implications
- Related proposal: `docs/proposals/004-security-attack-detection.md` (Phase 3)
- Plan completed: 2026-01-31

## Plan Artifacts Generated

- `plan.md` - Technical context and integration points
- `research.md` - Pattern sources, library analysis, entropy/Luhn algorithms
- `data-model.md` - DetectionPattern, Detection, SensitiveDataResult types
- `contracts/detection-result.schema.json` - JSON Schema for detection results
- `contracts/config-schema.json` - JSON Schema for configuration
- `contracts/api-extensions.yaml` - OpenAPI extensions for REST API
- `quickstart.md` - Implementation guide with code samples

---

## Research Summary

### Priority Detection Categories (by user request)

| Priority | Category | Examples | Severity |
|----------|----------|----------|----------|
| **1** | Cloud Credentials | AWS keys, GCP API keys, Azure secrets | Critical |
| **1** | Private Keys | RSA, SSH, PGP, OpenSSH keys | Critical |
| **1** | Sensitive File Paths | ~/.ssh/*, .env, .aws/credentials | Critical |
| **2** | API Tokens | GitHub, GitLab, Stripe, Slack | High |
| **2** | Database Credentials | Connection strings with passwords | High |
| **3** | Credit Cards | Luhn-validated card numbers | Medium |
| **3** | High Entropy Strings | Random strings > 4.5 entropy | Medium |

### Recommended Go Libraries

| Library | Use Case | Stars | License |
|---------|----------|-------|---------|
| Custom regex patterns | API keys, tokens | N/A | N/A |
| Luhn validation (built-in) | Credit cards | N/A | N/A |
| Shannon entropy (built-in) | High-entropy secrets | N/A | N/A |
| glob matching (built-in) | File path patterns | N/A | N/A |

### Key Pattern Sources

- **Gitleaks**: 100+ secret patterns with entropy thresholds
- **TruffleHog**: 800+ credential detectors
- **detect-secrets**: Plugin-based with entropy analysis
- **secrets-patterns-db**: Comprehensive regex collection

### Cross-Platform File Path Detection

**SSH & Keys**
| Platform | Paths |
|----------|-------|
| Linux/macOS | `~/.ssh/id_*`, `~/.ssh/authorized_keys` |
| Windows | `%USERPROFILE%\.ssh\*`, `C:\Users\*\.ssh\*` |
| All | `*.pem`, `*.key`, `*.ppk`, `*.p12`, `*.pfx` |

**Cloud Credentials**
| Platform | Paths |
|----------|-------|
| Linux | `~/.aws/credentials`, `~/.config/gcloud/*` |
| Windows | `%USERPROFILE%\.aws\credentials`, `%APPDATA%\gcloud\*` |

**System Files**
| Platform | Paths |
|----------|-------|
| Linux | `/etc/shadow`, `/etc/sudoers`, `/proc/*/environ` |
| macOS | `/etc/master.passwd`, `~/Library/Keychains/*` |
| Windows | `SAM`, `SYSTEM`, `SECURITY` registry hives |

### Activity Log Integration Point

```
internal/runtime/activity_service.go
  → handleToolCallCompleted()
    → Scan arguments and response
    → Store in ActivityRecord.Metadata["sensitive_data_detection"]
```

### Detection Result Schema

```json
{
  "sensitive_data_detection": {
    "detected": true,
    "detections": [
      {
        "type": "aws_access_key",
        "category": "cloud_credentials",
        "severity": "critical",
        "location": "arguments.api_key",
        "is_likely_example": false
      }
    ],
    "scan_duration_ms": 8
  }
}
```

### MCP Security Context

Key threats this feature helps detect:
1. **Data Exfiltration**: Secrets in tool responses being sent to external services
2. **Tool Poisoning**: Malicious tools reading sensitive files
3. **Lethal Trifecta**: Combination of private data access + untrusted content + external communication
