---
# mcpproxy-go-xguu
title: Document 24-hour token expiry threshold constant
status: todo
type: task
priority: low
created_at: 2026-01-19T14:25:30Z
updated_at: 2026-01-19T14:25:30Z
---

From PR #255 code review.

File: `internal/oauth/refresh_manager.go:665`

```go
if timeSinceExpiry > 24*time.Hour {
```

The 24-hour threshold for giving up on token refresh is undocumented. Should:
1. Extract to a named constant (e.g., `maxTokenExpiryRecoveryWindow`)
2. Add documentation explaining why 24 hours was chosen