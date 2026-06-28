# Contracts: MCP Surface Deltas

These are deltas to existing built-in MCP tools. No new tools, no new REST
endpoints. All additions additive; default responses unchanged.

## `retrieve_tools` — new input parameter

```jsonc
{
  "query": "string (required, unchanged)",
  "limit": "number (optional, unchanged)",
  "include_disabled": "boolean (optional, default false)  // NEW (FR-001)"
}
```

Tool description gains exactly one sentence (FR-014):
> "Set `include_disabled:true` to also surface tools that exist but are
> currently locked by config, user, or quarantine, with remediation guidance."

### Response delta (only when `include_disabled=true` AND ≥1 locked match)

```jsonc
{
  "tools": [ /* callable results — UNCHANGED order/shape (FR-002, FR-006) */ ],
  "disabled": [                                  // NEW, after callable, ≤ min(limit,10)
    { "name": "delete_repo", "server": "github",
      "description": "Delete a repository", "status": "disabled_by_config" },
    // Quarantined-tool discovery pass: name-only, description/schema withheld
    // (TPA defense); `name` is the "<server>:<tool>" key. Prepended ahead of
    // index-derived locked entries so the min(limit,10) cap can't drop them.
    { "name": "github:rotate_keys", "server": "github",
      "status": "server_quarantined" }
  ],
  "remediation": {                               // NEW, once, only present statuses
    "disabled_by_config": "Locked by operator policy in mcp_config.json (enabled_tools/disabled_tools). The user cannot enable this from the UI; ask the operator to change the server config.",
    "server_quarantined": "Its server is quarantined for security review. Its tools cannot be called until the user reviews and approves the server in the mcpproxy UI or system tray."
  }
}
```

`status` enum (FR-004): `disabled_by_config`, `disabled_by_user`,
`pending_approval`, `server_disabled`, `disabled_unknown`, `server_quarantined`.
The first five are classifier-assigned to index-discoverable tools; the last is
assigned by the quarantined-tool discovery pass (also re-using `pending_approval`
for tool-level pending/changed approvals), which surfaces name-only entries from
authoritative quarantine state because quarantined tools are not in the index.

### Response delta (0 callable results, ≥1 locked match, flag OFF) — FR-009

A one-line note added to the existing result text:
> "N relevant tools exist but are locked; retry with include_disabled:true for details."
(count only; no entries)

## `upstream_servers` (operation=list / get) — server entry delta

```jsonc
{
  "name": "github",
  "...": "existing fields unchanged",
  "tools": {                       // NEW (FR-010), present ONLY if a non-callable count > 0
    "callable": 12,
    "disabled_by_config": 3,
    "disabled_by_user": 1
    // zero-valued reasons omitted; whole block omitted if all callable
  }
}
```

## `call_tool_*` rejection message — status-aware (FR-008)

Already status-aware via `blockedToolMessageFor(bool)` (landed in #468 PR).
This feature only adds the discovery pointer once `include_disabled` exists:
config-denied → "...NOT user-overridable; ask the operator to edit
mcp_config.json. Run retrieve_tools with include_disabled:true to see locked
capabilities and remediation."

## Backward-compatibility contract

- `include_disabled` absent/false → response byte-for-byte identical to
  pre-049 (SC-001). Regression test asserts this against a fixture.
- All new fields `omitempty`; no field renamed/removed.
- OAS (`oas/swagger.yaml`, `oas/docs.go`) regenerated; `verify-oas-coverage.sh`
  passes.
