# Research: Update Check Enhancement & Version Display

**Branch**: `001-update-version-display` | **Date**: 2025-12-15

## Research Summary

This document consolidates research findings for implementing centralized version display and update notifications in MCPProxy.

---

## 1. GitHub Releases API

### Decision
Use GitHub Releases API v3 (REST) for fetching release information.

### Rationale
- Already implemented in `internal/tray/tray.go` (`getLatestRelease`, `getLatestReleaseIncludingPrereleases`)
- Simple REST API, no authentication required for public repos (60 req/hr limit)
- Returns all required metadata: version tag, assets, prerelease flag, URLs

### Alternatives Considered
- **GraphQL API**: More flexible but requires authentication, unnecessary complexity
- **RSS/Atom feeds**: Limited metadata, harder to parse
- **Polling GitHub raw file**: Not suitable for release detection

### Implementation Notes
- Endpoint: `https://api.github.com/repos/smart-mcp-proxy/mcpproxy-go/releases/latest`
- For prereleases: `https://api.github.com/repos/smart-mcp-proxy/mcpproxy-go/releases` (first item)
- Repository constant: `repo = "smart-mcp-proxy/mcpproxy-go"` (already defined in tray.go)

---

## 2. Existing Code Analysis

### Current Implementation Location
- **File**: `internal/tray/tray.go` (lines 1008-1150)
- **Functions**: `checkForUpdates()`, `getLatestRelease()`, `getLatestReleaseIncludingPrereleases()`
- **Types**: `GitHubRelease`, `Asset` structs

### Decision
Move update check logic from tray to core server, create shared package.

### Rationale
- Per spec: Core owns update check; tray/WebUI/CLI consume via API
- Avoids duplicate code and redundant GitHub API calls
- Enables headless deployments to benefit from update checks

### Migration Plan
1. Create `internal/updatecheck/` package with:
   - `checker.go`: Background service with ticker
   - `github.go`: GitHub API client (refactored from tray)
   - `types.go`: Shared types (`VersionInfo`, `GitHubRelease`)
2. Remove `checkForUpdates()` logic from tray, replace with API call
3. Update tray to poll `/api/v1/info` (which already exists) or new `/api/v1/version` endpoint

---

## 3. REST API Design

### Decision
Extend existing `/api/v1/info` endpoint with update information rather than creating new `/api/v1/version`.

### Rationale
- `/api/v1/info` already returns version and is used by tray-core communication
- Adding fields is backward-compatible
- Reduces API surface area

### Response Extension
Current `/api/v1/info` response:
```json
{
  "version": "v0.11.0",
  "web_ui_url": "http://127.0.0.1:8080",
  "listen_addr": "127.0.0.1:8080",
  "endpoints": { "http": "...", "socket": "..." }
}
```

Proposed extension:
```json
{
  "version": "v0.11.0",
  "web_ui_url": "http://127.0.0.1:8080",
  "listen_addr": "127.0.0.1:8080",
  "endpoints": { "http": "...", "socket": "..." },
  "update": {
    "available": true,
    "latest_version": "v0.12.0",
    "release_url": "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/tag/v0.12.0",
    "checked_at": "2025-12-15T10:30:00Z"
  }
}
```

### Alternatives Considered
- **New `/api/v1/version` endpoint**: More RESTful but fragments related data
- **Include in `/api/v1/status`**: Status is for runtime info, version is metadata

---

## 4. Background Service Architecture

### Decision
Implement as a runtime service using `time.Ticker` pattern, similar to existing background services.

### Rationale
- Follows existing patterns in `internal/runtime/`
- Constitution II.Actor-Based Concurrency: goroutine with channel communication
- Clean shutdown via context cancellation

### Architecture
```go
type UpdateChecker struct {
    logger      *zap.Logger
    version     string  // Current version from build flags
    checkInterval time.Duration

    mu          sync.RWMutex  // Protect versionInfo
    versionInfo *VersionInfo

    stopCh      chan struct{}
}

func (uc *UpdateChecker) Start(ctx context.Context) {
    ticker := time.NewTicker(uc.checkInterval)
    defer ticker.Stop()

    // Initial check on startup
    uc.check()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            uc.check()
        }
    }
}
```

---

## 5. Version Display Locations

### Tray Menu
**Current**: No version shown
**Proposed**: Add disabled menu item "MCPProxy v1.2.3" at top of menu

```go
systray.AddMenuItem("MCPProxy v"+version, "").Disable()
systray.AddSeparator()
// ... rest of menu
```

### Web Control Panel
**Current**: Version in `RuntimeStatus` but not displayed prominently
**Proposed**: Add version to sidebar footer or header

**Location**: Modify `frontend/src/components/SidebarNav.vue` or `frontend/src/App.vue`

### CLI Doctor
**Current**: `cmd/mcpproxy/doctor.go` - version not prominently shown
**Proposed**: Add version line at start of output

```
MCPProxy Doctor
Version: v0.11.0 (latest)
───────────────────────────
```

---

## 6. Environment Variables

### Existing (to respect)
| Variable | Description | Location |
|----------|-------------|----------|
| `MCPPROXY_DISABLE_AUTO_UPDATE` | Disable all update checks | tray.go:1010 |
| `MCPPROXY_UPDATE_NOTIFY_ONLY` | Check only, no auto-download | tray.go:1022 |
| `MCPPROXY_ALLOW_PRERELEASE_UPDATES` | Include prereleases | tray.go:1064 |

### Implementation
Core update checker must respect `MCPPROXY_DISABLE_AUTO_UPDATE` to disable background checks entirely.

---

## 7. Semver Comparison

### Decision
Use existing `golang.org/x/mod/semver` package (already used in tray.go).

### Rationale
- Already imported and working
- Handles edge cases (v prefixes, prerelease tags)
- Standard Go library recommendation

### Edge Case: Development Builds
```go
// Skip comparison for non-semver versions
if !semver.IsValid("v"+currentVersion) {
    return // No update check for "development" builds
}
```

---

## 8. Frontend Update Banner

### Decision
Create dismissible banner component that reads from `/api/v1/info`.

### Rationale
- DaisyUI has `alert` component that fits this use case
- Should be dismissible per session (localStorage flag)
- Reappear on new session/page reload

### Component Design
```vue
<!-- UpdateBanner.vue -->
<template>
  <div v-if="showBanner && updateAvailable" class="alert alert-info">
    <span>Update available: {{ latestVersion }}</span>
    <a :href="releaseUrl" target="_blank" class="btn btn-sm">View Release</a>
    <button @click="dismiss" class="btn btn-sm btn-ghost">Dismiss</button>
  </div>
</template>
```

---

## 9. Test Strategy

### Unit Tests
1. **UpdateChecker**: Mock HTTP client, test version comparison, ticker behavior
2. **GitHub API parsing**: Test response parsing with sample payloads
3. **Semver edge cases**: "development", prereleases, invalid versions

### Integration Tests
1. **API endpoint**: Test `/api/v1/info` returns update info
2. **E2E script**: Add to `scripts/test-api-e2e.sh`

### Manual Tests
1. Tray menu shows version
2. Update menu item appears when update available
3. Clicking opens correct GitHub URL
4. WebUI banner appears/dismisses correctly

---

## 10. Documentation Requirements

### New Documentation
- `docs/features/version-updates.md`: User-facing guide covering:
  - Where version is displayed
  - How update notifications work
  - Environment variable configuration
  - Troubleshooting

### Updates to Existing Docs
- `AUTOUPDATE.md`: Reference new centralized approach
- `docs/api/rest-api.md`: Document `/api/v1/info` extension

---

## Summary of Key Decisions

| Area | Decision |
|------|----------|
| API source | GitHub Releases API (existing) |
| New package | `internal/updatecheck/` |
| REST endpoint | Extend `/api/v1/info` with `update` field |
| Check interval | 4 hours (background) + on startup |
| Cache | In-memory only |
| Tray integration | Poll API, show "New version available" conditionally |
| WebUI integration | Dismissible alert banner |
| CLI integration | Version line in `mcpproxy doctor` |
