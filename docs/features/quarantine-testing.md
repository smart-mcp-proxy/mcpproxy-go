---
id: quarantine-testing
title: Quarantine State Machine Testing
sidebar_label: Quarantine Testing
sidebar_position: 4.6
description: Runtime invariants, multi-pass scenario tests, and property-based testing for the quarantine state machine
keywords: [security, quarantine, testing, invariants, property-based, rapid, state machine]
---

# Quarantine State Machine Testing

The tool quarantine system is a critical security boundary. A single state machine bug can silently auto-approve malicious tools. This document describes the three-layer testing approach that prevents these bugs.

## Background: The Bug That Motivated This

Commit `c61630c` fixed a bug where a **changed tool was silently auto-approved on the second `checkToolApprovals` pass**. The root cause was a state machine error: on the first pass, `CurrentDescription` was updated to the new (malicious) description. On the second pass, the comparison logic matched against `CurrentDescription` instead of `PreviousDescription`, making the changed tool appear unchanged.

Single-pass unit tests could not catch this because they only called `checkToolApprovals` once. The bug only manifested across multiple invocations.

## Three-Layer Testing Approach

### Layer 1: Runtime Invariant Assertions

Every state transition in `checkToolApprovals` and `ApproveTools` passes through `assertToolApprovalInvariant()` before being committed.

**Invariant rules:**

| Transition | Valid Reasons |
|-----------|--------------|
| `changed → approved` | `hash_match`, `description_revert`, `formula_migration`, `content_match`, `description_match`, `user_approve` |
| `pending → approved` | `user_approve`, `auto_approve` (quarantine disabled) |
| Any → `pending` or `changed` | Always allowed |
| `approved → approved` | Always allowed (no-op) |

**In production**, violations log a critical error and block the transition (the tool stays blocked). **In tests**, violations are caught by assertions.

```go
// Every transition point calls enforceInvariant before saving
if err := r.enforceInvariant(serverName, toolName,
    existing.Status, storage.ToolApprovalStatusApproved,
    ReasonDescriptionRevert); err != nil {
    result.BlockedTools[toolName] = true
    result.ChangedCount++
    continue
}
```

**Key files:**
- `internal/runtime/tool_quarantine.go` — `assertToolApprovalInvariant()`, `enforceInvariant()`, `TransitionReason` constants

### Layer 2: Multi-Pass Scenario Tests

These tests call `checkToolApprovals` multiple times in sequence, simulating server reconnections.

| Test | Scenario | Expected |
|------|----------|----------|
| `TestMultiPass_DiscoverChangeReconnectReconnect` | Approve → change → reconnect × 2 | Tool stays `changed` after both reconnects |
| `TestMultiPass_ChangeAndRevertToOriginal` | Approve → change → revert to original desc | Tool restored to `approved` |
| `TestMultiPass_PendingStaysBlocked` | Discover pending → reconnect × 3 | Tool stays `pending` on all passes |
| `TestMultiPass_PendingOnTrustedServer` | Pending on non-quarantined server → reconnect | Status stays `pending` in storage |
| `TestMultiPass_ApprovedToolStaysApproved` | Approved → reconnect × 5 | Tool stays `approved` |

**Key file:** `internal/runtime/tool_quarantine_invariant_test.go`

### Layer 3: Property-Based State Machine Tests

Using [`pgregory.net/rapid`](https://pkg.go.dev/pgregory.net/rapid), these tests generate hundreds of random action sequences and verify invariants hold across all of them.

**Actions in the random sequence:**
1. `discoverTools` — First discovery of a tool
2. `changeDescription` — Change to a random description from a pool
3. `reconnect` — Re-run `checkToolApprovals` with current description
4. `userApprove` — Explicit approval via `ApproveTools`
5. `userApproveAll` — Approve all via `ApproveAllTools`

**Properties verified:**
- A `changed` tool **never** transitions to `approved` without user action or description revert
- A `pending` tool **never** transitions to `approved` without user action

```bash
# Run with default 100 iterations
go test ./internal/runtime/ -run TestRapid -v

# Run with 200+ iterations (recommended for CI)
go test ./internal/runtime/ -run TestRapid -rapid.checks=200

# Run with 1000 iterations (thorough)
go test ./internal/runtime/ -run TestRapid -rapid.checks=1000
```

**Key tests:**
- `TestRapidQuarantineStateMachine` — Full state machine with all 5 actions
- `TestRapidInvariant_ChangedNeverAutoApproved` — Focused: changed tools never auto-approve on reconnect
- `TestRapidInvariant_PendingNeverAutoApproved` — Focused: pending tools never auto-approve on reconnect

## MCP Security Surface Tests

The quarantine system has a deliberate asymmetry: AI agents (via MCP) have **fewer** quarantine privileges than human operators (via REST API).

| Operation | MCP Tool | REST API |
|-----------|----------|----------|
| Quarantine a server | Yes (`quarantine_security`) | Yes (`POST .../quarantine`) |
| Unquarantine a server | **No** (blocked) | Yes (`POST .../unquarantine`) |
| Unquarantine via patch/update | **No** (silently ignored) | N/A |
| Approve individual tools | Yes (`approve_tool`) | Yes (`POST .../tools/approve`) |
| Approve all tools | Yes (`approve_all_tools`) | Yes (`POST .../tools/approve`) |

### Why Unquarantine Is Blocked via MCP

A compromised AI agent could:
1. Add a malicious MCP server (auto-quarantined)
2. Call `upstream_servers` with `operation: "patch", quarantined: false` to unquarantine it
3. The malicious server's tools are now available

The fix blocks step 2: `buildPatchConfigFromRequest` silently ignores `quarantined: false` when the server is currently quarantined. Quarantining (`false → true`) is still allowed via MCP.

**Key tests:**
- `TestE2E_UnquarantineNotExposedViaMCP` — MCP rejects `unquarantine` operation
- `TestE2E_PatchCannotUnquarantineServer` — Patch/update with `quarantined: false` is silently ignored

## Running the Tests

```bash
# All quarantine tests (invariants + multi-pass + rapid + existing)
go test ./internal/runtime/ -run "TestCheckTool|TestApprove|TestMultiPass|TestAssert|TestRapid" -v

# With race detector
go test -race ./internal/runtime/ -run "TestCheckTool|TestApprove|TestMultiPass|TestAssert" -v

# MCP security surface tests
go test ./internal/server/ -run "TestE2E_Quarantine|TestE2E_Unquarantine|TestE2E_Patch" -v

# REST API quarantine tests
go test ./internal/httpapi/ -run "TestHandle.*quarantine\|TestHandle.*Quarantine" -v -i
```

## Adding New Invariants

To add a new invariant to the quarantine state machine:

1. Add the check to `assertToolApprovalInvariant()` in `tool_quarantine.go`
2. Add unit tests for valid/invalid transitions in `tool_quarantine_invariant_test.go`
3. Add a multi-pass scenario test that would have caught the bug
4. Verify the rapid tests still pass (they test randomly, so they may catch edge cases)
5. Run with `-rapid.checks=1000` to be thorough
