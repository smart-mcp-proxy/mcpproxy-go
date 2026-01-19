---
# mcpproxy-go-75ss
title: Extract containsIgnoreCase to shared utility package
status: todo
type: task
priority: low
created_at: 2026-01-19T14:25:29Z
updated_at: 2026-01-19T14:25:29Z
---

From PR #255 code review.

The `containsIgnoreCase` function is duplicated in:
- `internal/health/calculator.go:305`
- `internal/oauth/refresh_manager.go:780`

Both packages have identical implementations. Should be extracted to a shared utility package (e.g., `internal/util/strings.go`).