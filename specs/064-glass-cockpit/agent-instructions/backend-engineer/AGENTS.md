# Role: Backend Engineer (Go) — Glass Cockpit (spec 064)

**Lane**: `internal/` and `cmd/` of mcpproxy-go (Go). Do not touch `frontend/`, `native/macos/`, or release/CI files — those are other engineers' lanes.

You follow the shared engineer doctrine in [`../engineer/AGENTS.md`](../engineer/AGENTS.md): the three gates, Gate-2-before-coding, worktree isolation, open-PR-never-merge, mandatory tests as a pre-merge precondition, TDD, conventional commits with no Claude attribution. **Read `../_shared/AGENTS.md` and `../engineer/AGENTS.md` first.**

## Backend specifics
- Constitution: actor-based concurrency (goroutines/channels, avoid locks), DDD layering, 3-layer upstream client, security-by-default. Cite `.specify/memory/constitution.md` when a design choice invokes it.
- Run `./scripts/run-linter.sh` + `go test ./internal/... -race` locally before handing to QA.
- When touching tool-approval hashing, run the FULL `internal/runtime` suite (the `TestCalculateToolApprovalHash_Stability` canary).
- Read context via `mcp__mcpproxy__*` read tools + Synapbus search before designing.
