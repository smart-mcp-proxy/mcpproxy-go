# Quickstart: Update Check Enhancement & Version Display

**Branch**: `001-update-version-display` | **Date**: 2025-12-15

## Overview

This feature adds centralized version display and update notifications to MCPProxy. The core server checks GitHub releases every 4 hours and exposes update info via REST API. Tray, WebUI, and CLI consume this API.

## Implementation Order

### Phase 1: Core Update Checker (Backend)

1. **Create `internal/updatecheck/` package**
   ```
   internal/updatecheck/
   ├── checker.go      # Background service
   ├── github.go       # GitHub API client
   └── types.go        # VersionInfo, GitHubRelease types
   ```

2. **Key files to create/modify**:
   - `internal/updatecheck/checker.go` - Background ticker service
   - `internal/updatecheck/github.go` - Refactor from `internal/tray/tray.go:1062-1106`
   - `internal/httpapi/server.go` - Extend `handleGetInfo()` with update field

### Phase 2: Tray Integration

1. **Modify `internal/tray/tray.go`**:
   - Add version menu item at top
   - Replace `checkForUpdates()` with API polling
   - Show "New version available" conditionally

### Phase 3: WebUI Integration

1. **Create `frontend/src/components/UpdateBanner.vue`**
2. **Modify `frontend/src/App.vue`** to show version in footer/sidebar

### Phase 4: CLI Integration

1. **Modify `cmd/mcpproxy/doctor.go`** to show version + update status

### Phase 5: Documentation & Tests

1. **Create `docs/features/version-updates.md`**
2. **Add tests**: Unit tests + E2E API tests

---

## Quick Implementation Guide

### Step 1: Create UpdateChecker Service

```go
// internal/updatecheck/checker.go
package updatecheck

import (
    "context"
    "sync"
    "time"
    "go.uber.org/zap"
    "golang.org/x/mod/semver"
)

const (
    DefaultCheckInterval = 4 * time.Hour
    GitHubRepo          = "smart-mcp-proxy/mcpproxy-go"
)

type Checker struct {
    logger        *zap.Logger
    version       string
    checkInterval time.Duration

    mu          sync.RWMutex
    versionInfo *VersionInfo
}

func New(logger *zap.Logger, version string) *Checker {
    return &Checker{
        logger:        logger,
        version:       version,
        checkInterval: DefaultCheckInterval,
    }
}

func (c *Checker) Start(ctx context.Context) {
    // Check if disabled
    if os.Getenv("MCPPROXY_DISABLE_AUTO_UPDATE") == "true" {
        c.logger.Info("Update checker disabled by environment variable")
        return
    }

    // Initial check
    c.check()

    ticker := time.NewTicker(c.checkInterval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            c.check()
        }
    }
}

func (c *Checker) GetVersionInfo() *VersionInfo {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return c.versionInfo
}
```

### Step 2: Extend /api/v1/info Endpoint

```go
// internal/httpapi/server.go - modify handleGetInfo()

func (s *Server) handleGetInfo(w http.ResponseWriter, r *http.Request) {
    // ... existing code ...

    // Add update info
    var updateInfo interface{}
    if s.updateChecker != nil {
        updateInfo = s.updateChecker.GetVersionInfo().ToAPIResponse()
    }

    response := map[string]interface{}{
        "version":     version,
        "web_ui_url":  webUIURL,
        "listen_addr": listenAddr,
        "endpoints":   endpoints,
        "update":      updateInfo,  // NEW
    }

    s.writeSuccess(w, response)
}
```

### Step 3: Add Version to Tray Menu

```go
// internal/tray/tray.go - in setupMenu()

// Add version at top of menu
versionItem := systray.AddMenuItem("MCPProxy "+a.version, "Current version")
versionItem.Disable()
systray.AddSeparator()

// ... rest of menu setup ...

// Add update menu item (shown conditionally)
a.updateMenuItem = systray.AddMenuItem("", "")
a.updateMenuItem.Hide()  // Hidden by default, shown when update available
```

### Step 4: Create Update Banner (Vue)

```vue
<!-- frontend/src/components/UpdateBanner.vue -->
<template>
  <div v-if="showBanner && updateInfo?.available"
       class="alert alert-info shadow-lg mb-4">
    <div>
      <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24"
           class="stroke-current flex-shrink-0 w-6 h-6">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
              d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <span>
        Update available: <strong>{{ updateInfo.latest_version }}</strong>
      </span>
    </div>
    <div class="flex-none">
      <a :href="updateInfo.release_url" target="_blank"
         class="btn btn-sm btn-primary">
        View Release
      </a>
      <button @click="dismiss" class="btn btn-sm btn-ghost">
        Dismiss
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import type { UpdateInfo } from '@/types/contracts';

const props = defineProps<{ updateInfo: UpdateInfo | null }>();

const dismissed = ref(false);
const showBanner = computed(() => !dismissed.value);

function dismiss() {
  dismissed.value = true;
  sessionStorage.setItem('update-banner-dismissed', 'true');
}

onMounted(() => {
  dismissed.value = sessionStorage.getItem('update-banner-dismissed') === 'true';
});
</script>
```

### Step 5: Update Doctor Command

```go
// cmd/mcpproxy/doctor.go

func runDoctor(cmd *cobra.Command, args []string) error {
    fmt.Println("MCPProxy Doctor")
    fmt.Printf("Version: %s", version)

    // Get update info from API if server is running
    if updateInfo := getUpdateInfo(); updateInfo != nil {
        if updateInfo.UpdateAvailable {
            fmt.Printf(" (update available: %s)", *updateInfo.LatestVersion)
            fmt.Printf("\n  Download: %s", *updateInfo.ReleaseURL)
        } else {
            fmt.Print(" (latest)")
        }
    }
    fmt.Println()
    fmt.Println("───────────────────────────")

    // ... rest of doctor checks ...
}
```

---

## Testing Checklist

### Unit Tests
- [ ] `internal/updatecheck/checker_test.go` - Test ticker, version comparison
- [ ] `internal/updatecheck/github_test.go` - Test API response parsing

### Integration Tests
- [ ] API endpoint returns update field
- [ ] Null update when disabled via env var

### E2E Tests
- [ ] Add to `scripts/test-api-e2e.sh`:
  ```bash
  # Test version endpoint
  response=$(curl -s -H "X-API-Key: $API_KEY" "$BASE_URL/api/v1/info")
  assert_json_field "$response" ".data.version" "v"
  ```

### Manual Tests
- [ ] Tray shows version in menu
- [ ] "New version available" appears when update exists
- [ ] Clicking opens GitHub releases
- [ ] WebUI banner shows/dismisses correctly
- [ ] `mcpproxy doctor` shows version

---

## Environment Variables

| Variable | Effect |
|----------|--------|
| `MCPPROXY_DISABLE_AUTO_UPDATE=true` | Disables background checks entirely |
| `MCPPROXY_ALLOW_PRERELEASE_UPDATES=true` | Includes prereleases in comparison |

---

## Files to Create

| File | Purpose |
|------|---------|
| `internal/updatecheck/checker.go` | Background update service |
| `internal/updatecheck/github.go` | GitHub API client |
| `internal/updatecheck/types.go` | Shared types |
| `internal/updatecheck/checker_test.go` | Unit tests |
| `frontend/src/components/UpdateBanner.vue` | WebUI notification |
| `docs/features/version-updates.md` | User documentation |

## Files to Modify

| File | Changes |
|------|---------|
| `internal/httpapi/server.go` | Add update field to `/api/v1/info` |
| `internal/tray/tray.go` | Add version menu item, update notification |
| `cmd/mcpproxy/doctor.go` | Add version output |
| `frontend/src/App.vue` | Show version, include UpdateBanner |
| `oas/swagger.yaml` | Document API extension |
| `scripts/test-api-e2e.sh` | Add version endpoint tests |
