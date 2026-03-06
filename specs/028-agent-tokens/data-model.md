# Data Model: Agent Tokens

## Entities

### AgentToken

Stored in BBolt bucket `agent_tokens`, keyed by HMAC-SHA256 hash of token value.

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Unique human-readable name (e.g., "openClaw-coding") |
| `token_hash` | string | HMAC-SHA256 hex digest of the full token value |
| `token_prefix` | string | First 12 characters of the token (e.g., "mcp_agt_a8f3") for display/logging |
| `allowed_servers` | []string | List of upstream server names, or `["*"]` for wildcard |
| `permissions` | []string | Subset of `["read", "write", "destructive"]` |
| `expires_at` | time.Time | Token expiry timestamp (mandatory, max 365 days from creation) |
| `created_at` | time.Time | Token creation timestamp |
| `last_used_at` | *time.Time | Last time token was used (nil if never used) |
| `revoked` | bool | Whether token has been revoked |

**Indexes**:
- Primary key: `token_hash` (HMAC-SHA256 of token value)
- Secondary index: `name` → `token_hash` (for CLI/API lookup by name)

**Validation rules**:
- `name` must be unique, 1-64 characters, alphanumeric + hyphens + underscores
- `allowed_servers` must be non-empty; each server must exist in config (except `"*"`)
- `permissions` must include `"read"` (always required); valid values are `read`, `write`, `destructive`
- `expires_at` must be in the future, max 365 days from now
- Default expiry: 30 days if not specified

### AuthContext

In-memory only. Set on request context by auth middleware. Not persisted.

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | `"admin"` (global API key) or `"agent"` (agent token) |
| `agent_name` | string | Agent token name (empty for admin) |
| `token_prefix` | string | First 12 chars of token (for logging, empty for admin) |
| `allowed_servers` | []string | Server scope (`nil` = all for admin) |
| `permissions` | []string | Permission scope (`nil` = all for admin) |

### ActivityRecord Extension

Existing `ActivityRecord.Metadata` map extended with optional auth fields:

| Metadata Key | Type | Description |
|-------------|------|-------------|
| `auth_type` | string | `"admin"` or `"agent_token"` |
| `agent_name` | string | Agent token name (only when auth_type = "agent_token") |
| `token_prefix` | string | First 12 chars (only when auth_type = "agent_token") |

## State Transitions

### Agent Token Lifecycle

```
Created → Active → Expired
  │         │
  │         └→ Revoked
  │
  └→ (validation fails) → Rejected at creation
```

- **Created**: Token generated, hash stored, secret displayed once
- **Active**: Token is valid, not expired, not revoked — can authenticate requests
- **Expired**: `expires_at` has passed — rejected with "token expired"
- **Revoked**: `revoked` set to true — rejected with "token revoked"
- **Regenerated**: New secret generated, old hash replaced, same metadata preserved

## Storage Schema (BBolt)

```
config.db
├── agent_tokens/                    # Bucket: keyed by token_hash
│   ├── <hmac_hash_1> → AgentToken JSON
│   └── <hmac_hash_2> → AgentToken JSON
├── agent_token_names/               # Bucket: name → token_hash mapping
│   ├── "openClaw-coding" → <hmac_hash_1>
│   └── "research-bot" → <hmac_hash_2>
├── agent_token_hmac_key/            # Bucket: HMAC key (if keyring unavailable)
│   └── "key" → <32-byte-key>
└── [existing buckets unchanged]
```
