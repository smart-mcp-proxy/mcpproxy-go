# Handoff prompt → Claude Code: verify the round-2 findings and protect against them

Copy everything inside the fence below as the prompt. Adjust the opencode
invocation to your setup if the CLI differs.

---

You are picking up a red-team engagement against this repository's TPA (Tool Poisoning Attack) defenses. Round 1 found seven holes (RT-01..RT-07); all were fixed and verified. Round 2 (2026-10-20) found new gaps. Your goal: VERIFY each round-2 finding independently on this machine, then propose and implement protections, each verified end-to-end.

Work in the git worktree /Users/user/repos/mcpproxy-tpa-lab (branch feat/tpa-attack-lab, merge head 39035b828 = fix series merged with origin/main @ 1b5c0d17b). Do NOT touch the main checkout. Malicious upstreams must only ever run inside the lab's Docker isolation (distroless, network none) — the scripts already enforce this.

STEP 0 — Read first (worktree-relative links):
1. docs/research/2026-10-11-tpa-attack-lab/round2-2026-10-20.md — the round-2 report: fix-verification results, new findings R2-1..R2-5, evidence, recommended fixes, acceptance criteria. THIS IS YOUR SPEC.
2. docs/research/2026-10-11-tpa-attack-lab/llm-report.md — round-1 findings (background; already fixed, do not re-litigate).
3. tests/tpa-attack-lab/README.md — the attack lab (profiles all/facade/mutants/toctou, stages 1/2/3).
4. scripts/tpa-lab-redteam-round2.sh — the round-2 experiments (R2-1 auto-baseline poisoning, R2-2 unquarantine race).
5. scripts/tpa-lab-redteam-experiments.sh — the round-1 regression suite (must keep passing).

TASK A — VERIFY each finding by reproducing it before touching anything:
- R2-1 (HIGH, auto-baseline poisoning): run `SKIP_BUILD=1 scripts/tpa-lab-redteam-round2.sh` and confirm the R2-1 HOLE line: an unquarantined first-seen server auto-approves its whole first toolset (`approved_by: "auto-baseline"`, internal/runtime/tool_quarantine.go:682 `isBaselinePass`, approval logged at :901) with no scan, and a poisoned tool (summarize_text) is callable through the proxy. Record the exact output.
- R2-3/R2-4/R2-5 (offline bypasses): run `go run ./cmd/tpa-redteam --verbose` and confirm BYPASS rows for translate-turkish, translate-greek, combining-diacritics, translate-turkish-combined, and shadow-desc-rewrite (name-squat).
- R2-2 (control): confirm it already HOLDS (unquarantine race closed) so you do not "fix" a non-issue.

TASK B — IMPLEMENT protections, one commit-sized change each, in this order:
- P1 (R2-1): gate baseline auto-approval on the offline scan. On the isBaselinePass path (internal/runtime/tool_quarantine.go:861 `case isBaselinePass:`), run scanner.ScanToolMetadataVerdict (internal/security/scanner/inprocess.go — the same fail-closed seam trust_mode:scan already uses, including peer tools for shadowing context) over the first-seen toolset and auto-approve ONLY on a clean verdict with full coverage; warnings/dangerous land pending with the findings recorded as hold evidence. Benign tools must still auto-baseline (no new friction for clean servers). Watch the lock-ordering note at internal/runtime/tool_quarantine.go:611 (the gate already snapshots decisions pre-pass).
- P2 (R2-4): strip Unicode Mn (nonspacing marks, e.g. U+0301 combining acute) in detect.NormalizeForPhrase (internal/security/detect/deobfuscate.go:36) before phrase matching, so base+mark sequences fold to the plain letter. Keep the existing latin-majority/mixed-script false-positive gates; run the corpus tests.
- P3 (R2-3): extend the i18n corpus (internal/security/detect/checks/phrase_injection_i18n.go, table starts near :45) with Turkish at minimum (the round-2 mutation text is in cmd/tpa-redteam/main.go), ideally Greek and Korean; update the coverage comment at the top of that file.
- P4 (R2-5): same tool NAME on another server with a different description currently scans clean (internal/security/detect/checks/shadowing.go — the clone check requires near-duplicate description, token-set containment near :114-131). Make a name-only cross-server collision emit at least a SOFT shadowing finding (review, not block) unless the description genuinely explains the overlap.
- Consider and document (do not implement unless trivial): an llm_judge backstop for uncovered languages.

RULES (non-negotiable):
1. MANDATORY verification on the local instance — a protection is not done until verified end-to-end on this machine:
   a. `go build ./...` and `go test ./internal/... ./tests/tpa-attack-lab/` green, gofmt/go vet clean, one focused unit test per protection next to the existing tests.
   b. Full dockerized regression: `scripts/tpa-lab-redteam-experiments.sh` (fresh state, no SKIP_BUILD on the first post-change run) must still print `suite result: PASS (every experiment holds)` — no round-1 regression, no weakened baselines (EXP-1 must stay dangerous, benign controls must stay clean).
   c. Round-2 flip: `SKIP_BUILD=1 scripts/tpa-lab-redteam-round2.sh` must print `R2-1 HOLDS` (poisoned first-seen tools pending/blocked, not callable) and keep `R2-2 HOLDS`.
   d. Offline flip: `go run ./cmd/tpa-redteam` must show translate-turkish, translate-greek, combining-diacritics and shadow-desc-rewrite no longer BYPASS (BLOCKED or DOWNGRADED-to-blocked), with the overall bypass count at 0.
2. CROSS-REVIEW with opencode on Sol 6.1 — after implementation, before finishing, get a fresh-context READ-ONLY review of the full diff, e.g.:
   opencode run -m sol-6.1 "READ-ONLY cross-review the current uncommitted diff (run: git -C /Users/user/repos/mcpproxy-tpa-lab diff) against docs/research/2026-10-11-tpa-attack-lab/round2-2026-10-20.md. For each change: correctness, false-positive risk on benign multilingual descriptions, lock-ordering in the baseline scan gate, coverage/fail-closed behavior, missing tests. Do not modify anything. End with a nonempty verdict: SHIP or REQUEST_CHANGES plus numbered findings with file:line evidence."
   Wait for the reviewer, assess/deduplicate findings, fix legitimate ones, re-verify (rule 1), include the verdict verbatim in your summary.
3. Do not weaken detection baselines or delete lab/harness/script assets. Keep each change bounded to its component.
4. Update docs/research/2026-10-11-tpa-attack-lab/round2-2026-10-20.md with a "Post-fix results" section: per-finding before/after, the commands you ran, and their actual output summaries.

Deliverables: commits on feat/tpa-attack-lab (imperative messages, no AI co-author tags), unit tests per protection, the two suite outputs (experiments + round2) and the offline harness summary demonstrating the flips, the updated docs section, and the opencode Sol 6.1 verdict.
