---
# mcpproxy-go-sskj
title: Add --all flag to auth login command
status: in-progress
type: feature
created_at: 2026-01-19T13:53:58Z
updated_at: 2026-01-19T13:53:58Z
---

Add support for re-authenticating all servers at once with mcpproxy auth login --all. Include confirmation prompt (yes/no) before opening multiple browser tabs, with optional --force flag to bypass confirmation.

## Checklist
- [ ] Explore existing auth command implementation
- [ ] Add --all flag to auth login command
- [ ] Implement confirmation prompt for multiple servers
- [ ] Add --force flag to bypass confirmation
- [ ] Test with multiple servers requiring auth
- [ ] Update any relevant documentation