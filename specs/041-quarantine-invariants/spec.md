# Feature Specification: Quarantine State Machine Invariants & Property Tests

**Feature Branch**: `041-quarantine-invariants`
**Created**: 2026-04-01
**Status**: Draft

## Summary

Add runtime invariant assertions and property-based state machine tests to the tool quarantine system to prevent state transition bugs (like the "changed tool auto-approved on second pass" bug).

## User Scenarios & Testing

### User Story 1 — Runtime Invariant Assertions (Priority: P1)

Add assertion checks at the end of every state transition in `checkToolApprovals` that verify critical safety properties. In tests, violations panic. In production, violations log a critical error and block the transition.

**Acceptance Scenarios**:

1. **Given** a tool in "changed" status, **When** `checkToolApprovals` runs, **Then** the tool MUST NOT transition to "approved" unless the description reverted to the previous (pre-change) version.
2. **Given** a tool in "pending" status, **When** `checkToolApprovals` runs, **Then** the tool MUST NOT transition to "approved" without an explicit user approve action.
3. **Given** any state transition occurs, **Then** `assertToolApprovalInvariant` is called and verified before the transition is committed.

### User Story 2 — Multi-Pass Scenario Tests (Priority: P1)

Write integration tests that call `checkToolApprovals` multiple times in sequence to verify state persistence across reconnections.

**Acceptance Scenarios**:

1. **Given** a tool is discovered and approved, **When** description changes and server reconnects twice, **Then** the tool remains "changed" after both reconnections.
2. **Given** a tool is "changed", **When** the server reconnects with the ORIGINAL description, **Then** the tool is restored to "approved" (legitimate revert).
3. **Given** a tool is "pending", **When** the server reconnects 3 times, **Then** the tool remains "pending" on all passes.

### User Story 3 — Property-Based State Machine Tests with `rapid` (Priority: P2)

Use `github.com/flyingmutant/rapid` to define a state machine model of the quarantine system and verify invariants across hundreds of random action sequences.

**Acceptance Scenarios**:

1. **Given** the `rapid` test suite, **When** run with default iterations, **Then** no invariant violations are found.
2. **Given** any random sequence of: discover tools, change descriptions, reconnect, user-approve, user-approve-all, **Then** the invariant "changed→approved requires user action or description revert" holds.
3. **Given** any random sequence, **Then** the invariant "pending→approved requires user action" holds.

## Requirements

### Functional Requirements

- **FR-001**: `assertToolApprovalInvariant()` function MUST check: changed→approved only with user action or description revert.
- **FR-002**: `assertToolApprovalInvariant()` MUST check: pending→approved only with user action.
- **FR-003**: Assertion MUST be called after every state transition in `checkToolApprovals`.
- **FR-004**: In test mode, violations MUST panic. In production, violations MUST log critical error and prevent the invalid transition.
- **FR-005**: Multi-pass tests MUST cover: discover→change→reconnect→reconnect (tool stays changed).
- **FR-006**: Multi-pass tests MUST cover: discover→change→reconnect-with-original-desc (tool restored).
- **FR-007**: `rapid` state machine test MUST define model with states: pending, approved, changed.
- **FR-008**: `rapid` test MUST define actions: discoverTools, changeDescription, reconnect, userApprove, userApproveAll.
- **FR-009**: `rapid` test MUST run at least 200 iterations per property.

### Key Entities

- **ToolApprovalRecord**: ServerName, ToolName, Status (pending/approved/changed), CurrentHash, ApprovedHash, CurrentDescription, PreviousDescription.
- **State Machine Model** (for rapid): tracks expected status per tool, whether user approved, current and previous descriptions.

## Success Criteria

- **SC-001**: The original "second pass auto-approve" bug is caught by at least 2 of the 3 testing approaches.
- **SC-002**: All existing quarantine tests continue to pass.
- **SC-003**: `rapid` state machine test finds no violations in 1000 iterations.
- **SC-004**: Runtime invariant prevents any future silent changed→approved transitions.

## Files Modified

- `internal/runtime/tool_quarantine.go` — `assertToolApprovalInvariant()`, `enforceInvariant()`, `TransitionReason` constants, integrated at all state transition points
- `internal/runtime/tool_quarantine_invariant_test.go` — invariant unit tests, multi-pass scenario tests, rapid property-based state machine tests
- `internal/management/service_test.go` — fix pre-existing lint error (missing storage import)
- `go.mod` / `go.sum` — add `pgregory.net/rapid` v1.2.0 dependency

## Assumptions

- `pgregory.net/rapid` v1.2.0 is stable and compatible with Go 1.24 (confirmed).
- Runtime invariant assertions log critical error in production and return an error that blocks the transition (no panic, no crash).
- The quarantine state machine has exactly 3 states: pending, approved, changed.
