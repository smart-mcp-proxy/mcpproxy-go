# Tasks: Residual Security Fixes
**Input**: spec.md, plan.md, research.md, data-model.md, contracts/, quickstart.md

## Phase 1 - Setup
- [x] T001 Research issues and initialize SpecKit artifacts in specs/106-security-residual-fixes/.
- [x] T002 Validate requirements checklist in specs/106-security-residual-fixes/checklists/requirements.md.

## Phase 2 - Script detection (US1)
- [x] T003 [US1] Add failing nested-source and parent-input/output detection tests in internal/server/ and internal/runtime/.
- [x] T004 [US1] Separate nested detection source from display truncation in internal/server/mcp_code_execution.go.
- [x] T005 [US1] Scan code_execution parent arguments/output before storage truncation in internal/runtime/activity_service.go.
- [x] T006 [US1] Verify synthetic input/output/long nested output on real MCP process and record evidence in verification.md.

## Phase 3 - Scoped visibility (US3)
- [x] T007 [US3] Add failing malformed-statistics, total-count, SSE identity and status masking regression tests.
- [x] T008 [US3] Attribute detection events in internal/runtime/ and classify them in internal/httpapi/sse_scope.go.
- [x] T009 [US3] Fail closed and preserve availability policy in internal/httpapi/scope.go and statistics producers.
- [x] T010 [US3] Guard nil revoker wiring in internal/serveredition/setup.go and mask status URL in cmd/mcpproxy/status_cmd.go.
- [x] T011 [US3] Verify real scoped SSE, totals and CLI masking; record evidence in verification.md.

## Phase 4 - Tenant revocation (US2)
- [x] T012 [US2] Add failing admin owner/name revocation and live-entitlement tests in internal/serveredition/ and internal/storage/.
- [x] T013 [US2] Add admin-only token listing/revocation in internal/serveredition/api/ and wire optional storage safely.
- [x] T014 [US2] Revalidate current owned-token entitlement through storage hook and setup wiring.
- [x] T015 [US2] Document routes and next-request revocation behavior in docs/features/agent-tokens.md.
- [x] T016 [US2] Verify two-user revocation, unsharing, wildcard and restart on a real server-edition process.

## Phase 5 - Delivery
- [x] T017 Run required local lint, unit and API E2E checks; document outcomes in verification.md.
- [x] T018 Obtain OpenCode Claude Fable cross-review, resolve findings, repeat affected checks.
- [ ] T019 Publish bounded PRs and repair their CI until required checks pass.

## Dependencies and Strategy
Setup precedes all implementation. Each story follows tests, implementation, real-instance verification. US1 and US3 share activity_service.go and therefore execute sequentially. US2 follows setup independently. Delivery follows all stories. Spec 105 has its own plan/tasks and coordinated integration checks; this task list does not mark that work complete implicitly.
