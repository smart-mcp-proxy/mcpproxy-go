---
id: trust-baseline
title: Trust-Baseline Quarantine Model
sidebar_label: Trust-Baseline Model
sidebar_position: 4.4
description: How MCPProxy establishes baseline trust when approving servers and tools, and how auto_approve_tool_changes controls per-server rug-pull protection
keywords: [security, quarantine, trust, baseline, approval, rug pull, auto_approve_tool_changes, skip_quarantine]
---

# Trust-Baseline Quarantine Model

MCPProxy uses a **two-level quarantine system** with a **trust-baseline model** that establishes trust at server-approval time while preserving ongoing rug-pull protection. This document explains how the model works end-to-end.

## The Core Idea

When you approve a server (unquarantine it), you are saying: *"I trust this server's current tool snapshot."* MCPProxy records this as a **baseline** — all tools known at that moment are marked approved. But any future changes to those tools (description or schema changes) are flagged as potential rug-pull attacks and require a fresh approval.

This means:

- **Approving a server is not a blank check** — it trusts what the server says *today*, not what it might say tomorrow
- **Tool changes after approval are detected** by SHA-256 hash comparison
- **Changed tools stay blocked** even after re-approving the server (baseline re-approval only promotes `pending` tools)

## Two-Level Quarantine

| Layer | Scope | What It Protects Against | Trigger |
|-------|-------|--------------------------|--------|
| **Server quarantine** | Entire server | Tool Poisoning Attacks (TPA) — malicious servers injected via AI agent | Server added via AI client or marked as quarantined |
| **Tool quarantine** | Individual tools | Rug-pull attacks — trusted server silently changes tool descriptions/schemas | SHA-256 hash mismatch on reconnection |

See [Security Quarantine](./security-quarantine.md) for server-level quarantine, and [Tool Quarantine](./tool-quarantine.md) for tool-level quarantine.

## Baseline Trust on Server Approval

When a server is **unquarantined** (approved), MCPProxy calls `approveBaselineToolsForServer()` which:

1. **Lists all tool approval records** for that server
2. **Promotes every `pending` tool to `approved`** — these are tools discovered while the server was quarantined that were never reviewed
3. **Leaves `changed` tools untouched** — a tool that was already flagged as a rug-pull before server approval stays blocked

This is the **baseline trust** rule: approving the server means you trust its current snapshot. The promotion uses `approved_by: "system:server-approval-baseline"` so the audit trail distinguishes baseline approval from manual user approval.

### Why `changed` Tools Are Never Auto-Promoted

A tool in `changed` status means its description or schema has diverged from the approved hash. If the server was already approved (baseline set) and a tool later changes, that is a **potential rug-pull** — a previously trusted server now says something different. Re-approving the server should not silently clear this flag because:

- The change happened *after* the original baseline trust was established
- A compromised or malicious server could change tool descriptions to inject harmful instructions
- The operator must explicitly review and approve each `changed` tool

This is enforced by the invariant system in `assertToolApprovalInvariant()`: `changed → approved` transitions require a specific reason (`user_approve`, `description_revert`, `hash_match`, `formula_migration`, `content_match`, or `description_match`). The baseline approval path only attempts `pending → approved`.

## Tool-Level State Machine

```
                    ┌─────────────────┐
                    │   Not Discovered │
                    └────────┬────────┘
                             │ tool discovered
                             ▼
                    ┌─────────────────┐
           ┌───────│     Pending      │◄──────── quarantine disabled globally
           │       └────────┬────────┘         or per-server skip_quarantine/
           │                │ user approve     auto_approve_tool_changes
           │                │ or baseline
           │                ▼
           │       ┌─────────────────┐
           │       │    Approved      │────────────► Approved + Disabled (blocked)
           │       └────────┬────────┘              via block operation
           │                │ hash changes
           │                ▼
           │       ┌─────────────────┐
           └───────│     Changed      │
          revert   └─────────────────┘
          to approved desc
```

### Transition Rules

| From | To | Allowed Reasons | Notes |
|------|----|-----------------|-------|
| `pending` | `approved` | `user_approve`, `auto_approve` | Auto-approve when quarantine disabled or server has `skip_quarantine`/`auto_approve_tool_changes` |
| `changed` | `approved` | `user_approve`, `description_revert`, `hash_match`, `formula_migration`, `content_match`, `description_match` | Never auto-approved on reconnect — always needs user action or proof of no actual change |
| `pending` | `changed` | Hash mismatch | Normal detection flow |
| `approved` | `changed` | Hash mismatch | Rug-pull detection |
| Any | `approved`+disabled | Block operation | Approve + disable atomically |

## Auto-Approve Behavior

### Global Disable

When `quarantine_enabled` is `false` globally:
- All new tools are auto-approved immediately
- Tool-level quarantine is entirely skipped
- No pending/changed states occur

### Per-Server Auto-Approve

Two config fields control per-server behavior:

| Field | Type | Default | Status |
|-------|------|---------|--------|
| `skip_quarantine` | `boolean` | `false` | **Active runtime control** (deprecated) |
| `auto_approve_tool_changes` | `*boolean` (tri-state) | `nil` (unset) | **Config-plumbed, enforcement upcoming** |

**Current behavior** (governed by `skip_quarantine`):
- `skip_quarantine: true` → new tools from this server are auto-approved, changed tools from this server are still blocked (rug-pull protection preserved)
- The server still enters server-level quarantine when added via AI; only the *tool-level* quarantine is skipped

**Migration path** (`auto_approve_tool_changes`):
- On config load, a legacy `skip_quarantine: true` is automatically migrated onto `auto_approve_tool_changes: true` (only when the new field is unset)
- An explicit `auto_approve_tool_changes: false` overrides a legacy `skip_quarantine: true`
- The tri-state `*bool` preserves the distinction between "never set" (`nil`), "explicitly opt in" (`true`), and "explicitly opt out" (`false`)
- The REST API round-trips the tri-state: `PATCH` omitting it leaves the stored value unchanged

When the active per-server control is enabled:
- Newly discovered tools are recorded as `approved` with `approved_by: "auto"`
- Changed tools (rug-pull) are **still blocked** — auto-approve only applies to *new* tools, not changed ones
- The activity log records `tool_auto_approved` events

## Block Operation (Approve + Disable)

When reviewing a pending or changed tool, you may want to **acknowledge it but keep it hidden** from MCP clients. The block operation does this atomically:

1. Approves the tool (clears quarantine status)
2. Disables it (hides from MCP clients)
3. Both mutations happen in a single record write — no window where the tool is approved+enabled

This is useful for dismissing noisy `changed` flags for tools you never intend to use. A blocked tool can be re-enabled later.

## Invariant Enforcement

Every state transition passes through `assertToolApprovalInvariant()` which enforces:

- **`changed → approved`**: Only allowed with `user_approve`, `description_revert`, `hash_match`, `formula_migration`, `content_match`, or `description_match`. This prevents the bug where a `changed` tool gets silently auto-approved on reconnect because `CurrentDescription` was already updated.
- **`pending → approved`**: Only allowed with `user_approve` or `auto_approve`. This prevents tools from spontaneously becoming approved without user action or explicit opt-out.

In production, invariant violations log a critical error and block the transition. In tests, they are caught by assertions.

## Three-Layer Testing

The trust-baseline model is verified by a three-layer testing approach:

| Layer | What | Catches |
|-------|------|---------|
| **1. Invariant assertions** | `assertToolApprovalInvariant()` checks every transition | Invalid state machine moves |
| **2. Multi-pass scenario tests** | `TestMultiPass_*` simulate reconnections | Bugs that only manifest across multiple passes |
| **3. Property-based tests** | `TestRapidQuarantineStateMachine` generates random action sequences | Edge cases in state transitions |

See [Quarantine Testing](./quarantine-testing.md) for details.

## Key Tests

| Test | File | What It Verifies |
|------|------|------------------|
| `TestApproveBaselineToolsForServer_PromotesPendingOnly` | `internal/runtime/tool_quarantine_baseline_test.go:19` | Baseline trust promotes pending tools |
| `TestApproveBaselineToolsForServer_LeavesChangedUntouched` | `internal/runtime/tool_quarantine_baseline_test.go:58` | Baseline trust does NOT clear rug-pull flags |
| `TestQuarantineServer_Unquarantine_BaselineApprovesPending` | `internal/runtime/tool_quarantine_baseline_test.go:104` | End-to-end: unquarantine promotes pending, leaves changed |
| `TestMultiPass_DiscoverChangeReconnectReconnect` | `internal/runtime/tool_quarantine_invariant_test.go` | Changed tool stays changed after multiple reconnects |
| `TestRapidInvariant_ChangedNeverAutoApproved` | `internal/runtime/tool_quarantine_invariant_test.go` | Property-based: changed tools never auto-approve |

## Activity Events

| Event | Description | Trigger |
|-------|-------------|---------|
| `tool_discovered` | New tool found, pending approval | First discovery under quarantine |
| `tool_auto_approved` | New tool automatically approved | Quarantine disabled or server skipped |
| `tool_approved` | Tool manually approved by user | Approve or baseline approval |
| `tool_description_changed` | Tool description/schema changed | Hash mismatch detected |

## Summary

The trust-baseline model gives operators a clear security posture:

1. **Server quarantine** blocks unknown servers until reviewed
2. **Server approval** establishes baseline trust for the current tool snapshot
3. **Tool-level quarantine** detects and blocks any future changes to approved tools
4. **`auto_approve_tool_changes`** (and its predecessor `skip_quarantine`) lets trusted servers bypass the tool-review step for *new* tools, but still blocks rug-pulls
5. **Changed tools are never silently auto-approved** — they require explicit operator action
