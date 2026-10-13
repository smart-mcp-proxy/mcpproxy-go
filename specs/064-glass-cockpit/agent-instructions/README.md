# Glass Cockpit agent instructions (spec 064)

These are the **canonical source** for the rewritten agent brains. They evolve the spec-045 instructions to add the three-gate steerability model. They are applied to the running Paperclip company's managed instruction bundles by `../scripts/apply-instructions.sh` (idempotent); the running copies under `~/.paperclip/instances/default/.../agents/<id>/instructions/` are a deployment target, not the source of truth.

## Reading order for every agent
1. `_shared/AGENTS.md` — the three gates + provenance + safety fence (binds everyone).
2. The role file (`ceo/`, `engineer/` + the lane file, `qa-tester/`, `critic/`).

## Key change vs spec 045
045 had a single late binary gate (approve the CEO's finished synthesis). 064 inverts the default to **checkpoint at every design-decision boundary** with structured redirection:
- **Gate 1 (plan-of-attack)** — CEO raises a `request_confirmation`/`suggest_tasks` on its proposed decomposition and waits before creating children.
- **Gate 2 (per-spec design)** — each spec issue carries a user `approval` execution stage; no code before approval.
- **Gate 3 (pre-merge)** — agents open PRs, never merge; the human merges on GitHub (branch protection enforced).

## Behavioral contract
The required behaviors (and their probe tests) are pinned in [`../contracts/agent-instructions-contract.md`](../contracts/agent-instructions-contract.md). The execution-policy JSON shape is in [`../contracts/execution-policy.schema.json`](../contracts/execution-policy.schema.json) (validated by `../contracts/execution_policy_test.go`).

### Reviewer-Liveness Contract (FR-014a)
A `review` stage MAY carry an optional `liveness` block describing **health-based, model-diverse reviewer failover**: an ordered fallback `roster`, an SLA (`slaMinutes`, default 120 = 2h), and a per-head re-trigger budget (`maxFallbacksPerHead` default 1, `maxHops` default 2 → `escalateTo` the CEO). It codifies the MCP-3066 shell backstop (`~/.mcpproxy-gatekeeper/bin/ensure-pr-gates.sh`: `classify_stall` + `route_fallback`), which **remains the source of truth**; the schema mirrors it. A *substantive* `request_changes` on the current head is a mandatory fence and is never routed around, so it is not a permitted `failoverStallModes` value. When `liveness` is omitted, a review stage runs a single reviewer with no failover (existing policies are unchanged). See the worked [`../contracts/reviewer-liveness.example.json`](../contracts/reviewer-liveness.example.json) for the contract values and their rationale.

## Roster mapping (live company `16edd8ed-…`)
| Agent | adapterType | Instruction file | Activate for dry-run? |
|---|---|---|---|
| CEO | claude_local | `ceo/AGENTS.md` | yes |
| BackendEngineer | claude_local | `backend-engineer/AGENTS.md` (+ `engineer/`) | yes (for #538 if backend) |
| FrontendEngineer | claude_local | `frontend-engineer/AGENTS.md` (+ `engineer/`) | yes (for #538 — likely frontend) |
| MacOSEngineer | claude_local | `macos-engineer/AGENTS.md` (+ `engineer/`) | maybe (if #538 is native) |
| QATester | claude_local | `qa-tester/AGENTS.md` | yes |
| Critic | **gemini_local** | `critic/GEMINI.md` | yes |
| ReleaseEngineer | claude_local | (045 release file; not gate-critical for dry-run) | no |
| CTO / PM / CMO | claude_local | (left paused) | no |
