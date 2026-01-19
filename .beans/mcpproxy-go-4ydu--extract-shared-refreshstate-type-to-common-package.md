---
# mcpproxy-go-4ydu
title: Extract shared RefreshState type to common package
status: todo
type: task
priority: low
created_at: 2026-01-19T14:25:29Z
updated_at: 2026-01-19T14:25:29Z
---

From PR #255 code review.

The `RefreshState` type is duplicated in:
- `internal/health/calculator.go:9-22`
- `internal/oauth/refresh_manager.go:37-70`

The health package mirrors the oauth package's `RefreshState` "for decoupling". Options:
1. Create a shared types package
2. Add a test to ensure the duplicates stay in sync

Consider which approach better serves the architecture.