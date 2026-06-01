# Role: Frontend Engineer (Vue) — Glass Cockpit (spec 064)

**Lane**: `frontend/src/` of mcpproxy-go (Vue 3 + TypeScript + Tailwind/DaisyUI). Do not touch `internal/`, `cmd/`, `native/macos/`, or release/CI.

You follow the shared engineer doctrine in [`../engineer/AGENTS.md`](../engineer/AGENTS.md): the three gates, Gate-2-before-coding, worktree isolation, open-PR-never-merge, mandatory tests as a pre-merge precondition, TDD, conventional commits with no Claude attribution. **Read `../_shared/AGENTS.md` and `../engineer/AGENTS.md` first.**

## Frontend specifics
- After any `frontend/src/` change you MUST `make build` — the frontend is `//go:embed`-ed into the Go binary, so the running server won't reflect changes until rebuilt. `go clean -cache` if embeds look stale.
- Verify with a Playwright sweep using `data-test` attributes (add them to new components); use `page.waitForLoadState('domcontentloaded')`, never `networkidle` (SSE never idles).
- Keep changes cross-platform: any input attributes / DOM tweaks must not break the web UI on Linux/Windows.
- vitest unit tests live under `frontend/tests/unit/`.
