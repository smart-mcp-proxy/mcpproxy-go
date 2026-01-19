---
# mcpproxy-go-3st0
title: Replace custom toLower with strings.ToLower
status: todo
type: task
priority: low
created_at: 2026-01-19T14:25:30Z
updated_at: 2026-01-19T14:25:30Z
---

From PR #255 code review.

File: `internal/oauth/refresh_manager.go:792-802`

Uses a manual ASCII-only lowercase implementation. Consider using `strings.ToLower()` from stdlib unless there's a specific performance reason for the custom implementation.