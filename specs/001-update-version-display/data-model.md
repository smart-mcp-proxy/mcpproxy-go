# Data Model: Update Check Enhancement & Version Display

**Branch**: `001-update-version-display` | **Date**: 2025-12-15

## Entities

### VersionInfo

In-memory representation of current version and update availability.

| Field | Type | Description | Constraints |
|-------|------|-------------|-------------|
| `current_version` | string | Currently running version | Semver format (e.g., "v1.2.3") or "development" |
| `latest_version` | string | Latest available version from GitHub | Semver format, may be null if check not completed |
| `update_available` | bool | True if latest_version > current_version | Computed from semver comparison |
| `release_url` | string | GitHub release page URL | Full URL to release tag page |
| `release_notes` | string | Brief release notes (optional) | First 500 chars of release body |
| `checked_at` | time.Time | Timestamp of last successful check | ISO 8601 format |
| `check_error` | string | Last error message (if any) | Empty string if no error |
| `is_prerelease` | bool | Whether latest version is a prerelease | From GitHub API |

**Lifecycle**:
- Created on first background check after startup
- Updated every 4 hours
- Reset on server restart (in-memory only)

**State Transitions**:
```
[Not Checked] --> (startup check) --> [Checked: Up-to-date]
[Not Checked] --> (startup check) --> [Checked: Update Available]
[Not Checked] --> (startup check fails) --> [Check Error]
[Checked] --> (4hr timer) --> [Checked: ...] (refreshed)
[Check Error] --> (retry) --> [Checked: ...]
```

---

### GitHubRelease (External)

Represents a GitHub release from the API response.

| Field | Type | Description | Source |
|-------|------|-------------|--------|
| `tag_name` | string | Release tag (e.g., "v1.2.3") | GitHub API |
| `name` | string | Release title | GitHub API |
| `body` | string | Release notes in markdown | GitHub API |
| `prerelease` | bool | Is this a prerelease | GitHub API |
| `html_url` | string | URL to release page | GitHub API |
| `published_at` | string | Publication timestamp | GitHub API |
| `assets` | []Asset | Download assets | GitHub API |

**Note**: This is an external entity from GitHub API, not stored locally.

---

### Asset (External)

Represents a downloadable asset from a GitHub release.

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Asset filename (e.g., "mcpproxy-v1.2.3-darwin-arm64.tar.gz") |
| `browser_download_url` | string | Direct download URL |
| `content_type` | string | MIME type |
| `size` | int | File size in bytes |

---

## Relationships

```
┌──────────────────┐
│   VersionInfo    │ (in-memory, singleton)
│                  │
│ - current_version│
│ - latest_version │◄─────────┐
│ - update_available│          │ Fetched from
│ - release_url    │          │
│ - checked_at     │          │
└──────────────────┘          │
                              │
                    ┌─────────┴─────────┐
                    │   GitHubRelease   │ (external, not stored)
                    │                   │
                    │ - tag_name        │
                    │ - html_url        │
                    │ - prerelease      │
                    │ - assets[]        │
                    └───────────────────┘
```

---

## Storage

### In-Memory Only

Per clarification session:
> Q: Should version cache persist across restarts or be in-memory only?
> A: In-memory only (fresh check on each startup, lost on restart)

**Implementation**: `sync.RWMutex`-protected struct in `UpdateChecker` service.

```go
type UpdateChecker struct {
    mu          sync.RWMutex
    versionInfo *VersionInfo  // nil until first check completes
}
```

---

## Validation Rules

### Version Format
- Must be valid semver with "v" prefix (e.g., "v1.2.3", "v1.2.3-rc1")
- Special case: "development" bypasses all version comparison

### Update Availability
- `update_available = semver.Compare(current, latest) < 0`
- Prerelease check: Only if `MCPPROXY_ALLOW_PRERELEASE_UPDATES=true`

### Check Timing
- Minimum interval: 4 hours (to respect GitHub API rate limits)
- Initial check: On startup, non-blocking

---

## API Contract

### GET /api/v1/info Response Extension

```json
{
  "success": true,
  "data": {
    "version": "v0.11.0",
    "web_ui_url": "http://127.0.0.1:8080",
    "listen_addr": "127.0.0.1:8080",
    "endpoints": {
      "http": "127.0.0.1:8080",
      "socket": "/Users/user/.mcpproxy/mcpproxy.sock"
    },
    "update": {
      "available": true,
      "latest_version": "v0.12.0",
      "release_url": "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/tag/v0.12.0",
      "checked_at": "2025-12-15T10:30:00Z",
      "is_prerelease": false
    }
  }
}
```

**When no update check has completed yet:**
```json
{
  "update": null
}
```

**When update check failed:**
```json
{
  "update": {
    "available": false,
    "latest_version": null,
    "release_url": null,
    "checked_at": "2025-12-15T10:30:00Z",
    "check_error": "network timeout"
  }
}
```

---

## TypeScript Types (Frontend)

```typescript
// To be added to frontend/src/types/contracts.ts

export interface UpdateInfo {
  available: boolean;
  latest_version: string | null;
  release_url: string | null;
  checked_at: string | null;  // ISO date string
  is_prerelease?: boolean;
  check_error?: string;
}

// Extended InfoResponse
export interface InfoResponse {
  version: string;
  web_ui_url: string;
  listen_addr: string;
  endpoints: {
    http: string;
    socket: string;
  };
  update: UpdateInfo | null;
}
```
