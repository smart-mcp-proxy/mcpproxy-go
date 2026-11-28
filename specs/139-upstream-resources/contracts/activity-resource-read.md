# Contract: `resource_read` activity row

- Type string: `resource_read` (REST `?type=resource_read`, CLI `mcpproxy activity list --type resource_read`, SSE `activity` events, Web UI filter "Resource read").
- One row per read that reaches a registered resource or template handler, on every surface.
- Fields: see [data-model.md](../data-model.md#activity-row-resource_read).
- Visibility: as `prompt_get` — administrators see all; scoped callers see rows of servers in their reach.
- Not an attempt for work-session counters (`storage.AttemptRoleOf` returns the zero role).
- A blocked read also emits one `policy_decision` row with the same `request_id`.
