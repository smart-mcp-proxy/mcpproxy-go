# Handoff prompt → Claude Code: fix the TPA red-team findings

Copy everything inside the fence below as the prompt. Adjust the opencode
invocation to your setup if the CLI differs.

---

You are fixing security holes found by a red-team engagement against this repository's TPA (Tool Poisoning Attack) defenses. Work in the git worktree `/Users/user/repos/mcpproxy-tpa-lab` (branch `feat/tpa-attack-lab`, based on current `main`). Do NOT touch the main checkout.

STEP 0 — Read, in this order (all paths relative to the worktree root):
1. `docs/research/2026-10-11-tpa-attack-lab/llm-report.md` — the findings report (RT-01..RT-07, each with file:line, verbatim evidence, recommended fix, acceptance criteria). This is your specification.
2. `tests/tpa-attack-lab/README.md` — the attack lab (profiles: all/facade/mutants/toctou; stages 1/2/3).
3. `scripts/tpa-lab-redteam-experiments.sh` — the end-to-end verification suite (EXP-1..6).

TASK — Implement fixes in this priority order, one commit-sized change each, following the acceptance criteria in the report:
- RT-01 (CRITICAL): `internal/runtime/tool_quarantine.go:1128-1155` — the "description matches, schema format differs" LAST-RESORT auto-approve must fire ONLY on serialization-only schema diffs (same property set, same `required`, same types/constraints); any structural change (added/removed/retyped property, e.g. a new `sidenote` sink parameter) must hold for review in EVERY trust mode.
- RT-02: scan-mode change gate must not silently auto-approve changed contracts on a clean scan verdict; hold for operator confirmation (see `tool_quarantine.go:1212` path).
- RT-03: annotation-only flips (readOnlyHint/destructiveHint/openWorldHint/idempotentHint) must hold the tool for review; include them in the approved-contract hash or add an explicit annotation-diff check; migrate existing approvals (precedent: `HashSchemaVersion` / `outputSchemaHashMigration`).
- RT-05: detect contract changes for quarantined-but-connected servers during quarantine (hash-compare without indexing), or re-verify synchronously at unquarantine.
- RT-06: make the scanner verdict reflect the contract discovery actually ingested (scan the stored `Current*` snapshot, or report live-vs-stored divergence as a finding).
- RT-04 + RT-07 (detect-engine hardening; do last): confusable-folding/NFKC, de-obfuscation pass before phrase matching, destination matching independent of the verb sentence, param-shape data-sink detection, schema-text scanning. Gate false positives with `cmd/scan-eval` and the existing corpus tests.

RULES (non-negotiable):
1. MANDATORY verification on a local instance — a fix is not done until it is verified end-to-end on this machine:
   a. `go build ./...` and `go test ./internal/... ./tests/tpa-attack-lab/` (add a focused unit test per fix, next to the existing tests; `gofmt`/`go vet` clean).
   b. Rebuild artifacts and run the dockerized suite: `scripts/tpa-lab-redteam-experiments.sh` (fresh; do not pass SKIP_BUILD on the first post-fix run). The malicious upstreams must keep running Docker-isolated (distroless, network none) — never run them bare.
   c. Expected post-fix suite outcomes, per the report: EXP-1 stays HOLDS (`dangerous` — no detection regression); EXP-2b HOLDS after RT-01 (`stock_quote` → changed); EXP-6 HOLDS after RT-02; EXP-5 HOLDS after RT-03; EXP-2a HOLDS after RT-05; EXP-4 HOLDS after RT-06; EXP-3 tightened after RT-04. Record the actual summary lines.
   d. Also run `scripts/run-tpa-lab-docker.sh` once at the end: baseline scan must still return `dangerous`.
2. CROSS-REVIEW with opencode on Sol 6.1 — after implementation and before you consider the work finished, get a fresh-context, READ-ONLY review of the full diff from opencode with model `sol-6.1`, e.g.:
   `opencode run -m sol-6.1 "READ-ONLY cross-review the current uncommitted diff (run: git -C /Users/user/repos/mcpproxy-tpa-lab diff) against the spec in docs/research/2026-10-11-tpa-attack-lab/llm-report.md. For each change: correctness, security regressions, bypass re-introduction, hash-migration hazards, missing tests. Do not modify anything. End with a nonempty verdict: SHIP or REQUEST_CHANGES plus a numbered findings list with file:line evidence."`
   Wait for the reviewer, deduplicate/assess each finding, fix the legitimate ones, re-verify (rule 1), and include the reviewer's final verdict verbatim in your summary.
3. Do not weaken detection baselines to make experiments pass (EXP-1 and the benign controls must stay dangerous/clean respectively). Do not delete or gut the lab, the red-team harness, or the experiment scripts. Keep each fix bounded to its component.
4. Update `docs/research/2026-10-11-tpa-attack-lab/end-to-end-experiments.md` with a "Post-fix results" section: per-EXP before/after and the verification commands you ran with their actual output summaries.

Deliverables when done: commits on `feat/tpa-attack-lab` (imperative messages, no AI co-author tags), the updated docs section, unit tests per fix, the final experiment-suite summary, and the opencode Sol 6.1 review verdict.
