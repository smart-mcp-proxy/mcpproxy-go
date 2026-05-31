# Quickstart: Stand up Phase A and run the dry-run goal

This is the operator runbook for **Phase A** (config + instructions, no fork) and the **dry-run synthetic goal**. It is the executable form of the success criteria. Run from a machine where Paperclip is live at `127.0.0.1:3100`.

> Everything here is **non-destructive** (CN-002): it un-pauses agents, creates a *fresh* goal, and never edits the 49 historical issues. Read-only probes are safe to run anytime.

## 0. Prerequisites & sanity

```bash
CID=16edd8ed-8691-4a89-aa30-74ab6b931663
curl -sf http://127.0.0.1:3100/api/health | jq '{version, deploymentMode}'      # expect 2026.529.x, local_trusted
curl -sf "http://127.0.0.1:3100/api/companies/$CID/agents" | jq -r '.[]|"\(.name)\t\(.status)\t\(.adapterType)"'
gh auth status                                                                   # PR/merge identity (see R-01)
```

## 1. Primitive spike (D-09) — derisk before wiring the pipeline (TDD: probes first)

On a **throwaway** issue (not under the dry-run goal), exercise and capture each mutate flow, then delete the throwaway:
1. Attach an `executionPolicy` approval stage → move issue to `in_review` → confirm only the participant can advance → approve. (Gate 2 mechanics.)
2. Raise a `request_confirmation`/`suggest_tasks` interaction → confirm it blocks → accept with an edit → confirm the edit is honored. (Gate 1 mechanics.)
3. `tree-control/preview` then `tree-holds {mode:pause}` → confirm runs halt → `resume`. (FR-012.)
4. Confirm a `gemini_local` run executes (Critic credentials) — else plan to use the FR-011a waiver in the dry-run.

Record exact request/response in `research.md` addenda. **Do not proceed to step 3 until all four behave as specified.**

## 2. Apply instructions + revive the fleet (non-destructive)

```bash
./scripts/apply-instructions.sh        # push agent-instructions/* into Paperclip managed bundles (idempotent)
./scripts/revive.sh                     # un-pause CEO + BackendEngineer + QATester + Critic ONLY
# Verify: those 4 are idle/active; CTO/PM/CMO remain paused
curl -sf "http://127.0.0.1:3100/api/companies/$CID/agents" | jq -r '.[]|select(.status!="paused")|.name'
```

## 3. Branch protection (the hard pre-merge gate, FR-005 / R-01)

```bash
# Require PR + review + CI on main; ensure the fleet identity CANNOT merge (use a scoped token w/o merge, or rely on required-review).
gh api -X PUT repos/:owner/mcpproxy-go/branches/main/protection ...   # exact payload decided in tasks.md (R-01)
```

## 4. Fire the dry-run goal

```bash
./scripts/create-goal.sh "Add a 'Running the test suite' note to CONTRIBUTING.md"   # creates fresh goal + root issue, assigns CEO
```

Then observe (UI at `http://127.0.0.1:3100`, or poll):

```bash
# Gate 1 should appear and BLOCK (no children yet):
curl -sf "http://127.0.0.1:3100/api/companies/$CID/sidebar-badges"        # inbox/approvals > 0 when a gate is pending
# act in the UI (accept / edit the tree / reject-with-reason), then watch children get created
```

## 5. Walk the gates

| Step | What you do | Expected (probe) |
|---|---|---|
| Gate 1 — plan of attack | Review CEO's proposed breakdown + rationale; accept, or edit/drop/reject-with-reason | Until you act: child issue count = 0 (INV-1). After edit: only accepted items created (SC-004). |
| (autonomy) | nothing | Engineer drafts spec, then waits at Gate 2; zero human input between gates (SC-005). |
| Gate 2 — per-spec design | Approve, or request-changes with a comment | Spec stays `in_review` until approved; request-changes bounces to engineer with comment (INV-2). |
| (autonomy) | nothing | Engineer implements in a worktree (TDD), QA runs mandatory tests + report, Critic reviews (different model). |
| Gate 3 — pre-merge | Review the PR; **you** merge on GitHub | Item stays blocked until you merge; no agent merged (INV-3 / SC-010). |

## 6. Verify success criteria (probes)

```bash
./scripts/probes/run-all.sh            # asserts INV-1..6 / SC-001..011 for this run
```

Spot checks:
- **SC-006**: the goal traversed research → Gate1 → spec → Gate2 → implement → test → QA → PR → Gate3 → merged, each gate observed blocking.
- **SC-007**: `sidebar-badges` + blocked-inbox + approvals count == actual pending items at each step.
- **SC-008 (resilience)**: stop SynapBus, re-run a small goal, confirm it still completes (audit entries skipped, never blocking).
- **SC-009**: `tree-control/preview` shows the affected set before a pause halts work.

## 7. Pause / cancel (FR-012) — anytime

```bash
ISSUE=<root-issue-id>
curl -sf "http://127.0.0.1:3100/api/issues/$ISSUE/tree-control/preview" | jq    # preview impact FIRST
# then pause via UI or: POST /api/issues/$ISSUE/tree-holds {mode:"pause"}
```

## Cleanup

The dry-run goal + its issues may be left as a record, or cancelled via a `tree-holds {mode:"cancel"}`. **Never** delete the historical 49 issues. Revert branch-protection changes only if they were temporary for the test.

---

### Phase B (later) — fused transparency UI
A Paperclip **plugin** (TypeScript) subscribing to `/events/ws`, contributing a "Waiting on YOU" page (fusing sidebar-badges + blocked-inbox + approvals + active tree-holds) and a per-gate reasoning/citations panel, plus auto-attaching execution policies and a pause-hold safety net. Authored under `plugin/` when Phase B begins.

### Phase C (only if needed) — fork
Pre-execution rationale schema field + server-side decompose gate + default-on company setting + native waiting-on-you signal.
