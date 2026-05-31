# Contract: Consumed Paperclip REST/WS API

This feature **consumes** the Paperclip control-plane API; it does not author one. This document pins the endpoints the cockpit depends on, with the request/response shapes verified against `paperclipai@2026.529.0` (read paths confirmed live; mutate paths confirmed in package source, to be exercised in the D-09 spike). All paths are relative to `http://127.0.0.1:3100`. Auth: `local_trusted` → board actions authenticate via loopback (no key needed locally). Company id: `16edd8ed-8691-4a89-aa30-74ab6b931663` (`:cid`).

> **No GraphQL.** `GET /graphql` serves GraphiQL HTML; `POST /graphql` → "Cannot POST". Use REST + WebSocket only.

## Read (verified live)

| Method | Path | Purpose | Notable response fields |
|---|---|---|---|
| GET | `/api/health` | liveness + version | `{status, version, deploymentMode}` |
| GET | `/api/companies` | company list | `[{id, name, status, requireBoardApprovalForNewAgents, ...}]` |
| GET | `/api/companies/:cid/agents` | roster | `[{id, name, role, adapterType, status, reportsTo, cwd, budgetMonthlyCents, adapterConfig.instructionsFilePath}]` |
| GET | `/api/companies/:cid/goals` | goals | `[{id, title, level, status, parentId, ownerAgentId}]` |
| GET | `/api/companies/:cid/issues` | task graph | `[{id, identifier, title, status, parentId, assigneeAgentId, executionPolicy, executionState, ...}]` |
| GET | `/api/companies/:cid/issues?attention=blocked&includeBlockedInboxAttention=true` | blocked-inbox facet | adds blocked-attention classification (`pending_user_decision`/`pending_board_decision`/`awaiting_decision`) |
| GET | `/api/issues/:id` | issue detail | `+ ancestors, planDocument, blockedBy, blocks, relatedWork, documentSummaries, executionState` |
| GET | `/api/issues/:id/comments` | rationale thread | `[{authorType, authorAgentId, body, createdByRunId}]` |
| GET | `/api/companies/:cid/approvals` | approvals surface | `[{id, type, status, requestedByAgentId, payload, decidedByUserId, decisionNote}]` |
| GET | `/api/companies/:cid/sidebar-badges` | waiting counts | `{inbox, approvals, failedRuns, joinRequests}` |
| GET | `/api/issues/:id/tree-control/preview` | impact preview before hold | affected issues/agents/runs + warnings |
| GET | `/api/issues/:id/tree-control/state` | current hold state | active holds |
| WS | `/api/companies/:cid/events/ws` | realtime | events: `heartbeat.run.*`, `agent.status`, `activity.logged`, `plugin.*` |

## Mutate (used by the pipeline; exercise in D-09 spike before relying on them)

| Method | Path | Purpose | Key body |
|---|---|---|---|
| PATCH | `/api/companies/:cid/agents/:id` | un-pause / pause an agent (revival) | `{status:"idle"}` / `{status:"paused"}` |
| POST | `/api/companies/:cid/goals` | create the run's goal | `{title, description, level:"task", ...}` |
| POST | `/api/companies/:cid/issues` | create root/spec issue (may carry `executionPolicy`) | `{title, description, goalId, assigneeAgentId, executionPolicy}` |
| PATCH | `/api/issues/:id` | advance/return at a gate; set status | `{status, comment}` (e.g. `in_review`→`done` approve, `→in_progress` request-changes) |
| POST | `/api/issues/:id/interactions` | raise Gate-1 confirmation / suggest-tasks | `{kind:"request_confirmation"|"suggest_tasks", payload, continuationPolicy, supersedeOnUserComment:true}` |
| POST | `/api/issues/:id/interactions/:iid/accept` \| `/reject` \| `/respond` | resolve Gate-1 (user) | edited tree / reason |
| POST | `/api/issues/:id/accepted-plan-decompositions` | create children from accepted plan | `{acceptedPlanRevisionId, children:[{title, description, acceptanceCriteria, blockedByIssueIds, blockParentUntilDone}]}` (1–25) |
| POST | `/api/issues/:id/tree-holds` | freeze/cancel/resume subtree | `{mode:"pause"|"cancel"|"resume"|"restore", releasePolicy:{strategy:"manual"}}` |
| POST | `/api/approvals/:id/approve` \| `/reject` \| `/request-revision` \| `/resubmit` | board approval workflow | `{decisionNote}` |

## Invariants the contract must uphold (probe targets)

- A `POST /accepted-plan-decompositions` MUST be refused/avoided until the Gate-1 interaction is `accepted` (Phase A: enforced by CEO instruction; INV-1).
- An issue with a pending user `approval` stage MUST NOT transition out of `in_review` by any actor other than the participant (INV-2).
- `tree-control/preview` MUST return the affected set **before** a `tree-holds` mutation is applied (FR-012 / SC-009).
- Every advance/return decision MUST carry a non-empty rationale `body` (INV-4).
