# Auto-merge setup — dual-AI-review consensus (spec 064 Session 2)

How the amended FR-005 gate is wired on GitHub. This is the operator/plan reference; the behavioral contract is in `spec.md` (FR-005/FR-005a, US3) and the agent doctrine is in `agent-instructions/reviewer/REVIEWER.md`.

## The model
```
engineer ── opens DRAFT PR ──▶  required checks run (CI)
                                      │ green
            Gemini Critic ──accept─┐  │
            Codex reviewer ─accept─┴──┼──▶  both-accept + checks-green
                                      │        │
            human (optional) ── request-changes / "hold" label ──▶ FREEZE
                                      │ not frozen
                                      ▼
                                 AUTO-MERGE (squash) → delete branch
```

## ⚠ Prerequisite (human action) — BOT IDENTITY
GitHub **forbids a PR author from approving their own PR**. Today the agents act
as the human's `gh` identity (`Dumbris`), and PRs are authored by `Dumbris`, so an
agent "approval" cannot gate a merge. **Auto-merge cannot function until the agents
have a bot identity distinct from the human author.** Options (pick one):
- **Fine-grained PAT bot account** — a second GitHub account added as a collaborator with write (not admin) on `smart-mcp-proxy/mcpproxy-go`; agents use its token for `pr create`/`pr review`. Simplest.
- **GitHub App** — install an app with PR read/write + checks; agents authenticate as the app installation. Cleaner identity, more setup.
The human MUST provision this; the agents cannot create it (and MUST NOT, per the safety fence).

## Branch-protection config (once bot identity exists)
On the target base branch (`main`, and per-spec integration branches as desired):
```
required_status_checks: { strict: true, contexts: [ <the required CI jobs>,
    "ai-review/gemini", "ai-review/codex" ] }   # the two reviewer checks
required_pull_request_reviews: { required_approving_review_count: 2,
    dismiss_stale_reviews: true }               # 2 approvals = the two AI reviewers
enforce_admins: false                            # human can still admin-override / veto
allow_auto_merge: true                           # repo setting
```
Then engineers open PRs with `gh pr create --draft` and, after reviewers are
requested, `gh pr merge --auto --squash` so GitHub merges automatically once the
above are satisfied. (The engineer enabling `--auto` is acceptable here because the
merge still cannot happen until the 2 approvals + checks gate clears — it does not
bypass review.)

## Applied 2026-05-31 — CI-context gate (Phase 1, no bot identity needed)
Live on `main` now (`PUT /repos/smart-mcp-proxy/mcpproxy-go/branches/main/protection`):
- `required_status_checks.strict = false`; **contexts (8):** `Lint`, `Unit Tests (ubuntu-latest, 1.25)`, `Build (ubuntu-latest)`, `Build (macos-latest)`, `Build (windows-latest)`, `Build Frontend`, `Validate PR title`, `Verify OpenAPI Artifacts`.
- `required_pull_request_reviews.required_approving_review_count = 1` (unchanged); `enforce_admins = false` (admin can override).
- **Deliberately NOT required** (requiring them would block unrelated PRs): path-conditional (`frontend-test`, `Cross-Platform Logging`, `Documentation`), heavy/conditional (`OAuth E2E Tests`, `End-to-End Tests`, `Integration`/`Stress`, `CodeQL`/`Analyze`, `dependency-review` — absent on some PRs), flaky OS unit tests (`Unit Tests (windows/macos-latest)` — matrix fail-fast cancellation + known Windows infra flakes), and external deploys (`Cloudflare Pages`).
- ⚠ **Fragile context name:** `Unit Tests (ubuntu-latest, 1.25)` pins Go `1.25`. When CI bumps the Go version, update this required context or every PR will block on a check that never reports. (`Build (<os>)` names carry no version → stable.)
- `strict:false` chosen so a slightly-behind branch can still merge (avoids auto-merge stalls); flip to `true` for "tested against latest main".
- **Not yet wired:** `ai-review/codex` + `ai-review/kimi` contexts (need the reviewer→GitHub status step), and `required_approving_review_count: 2` + `allow_auto_merge` (need the bot identity). Phase 1 enforces "all CI green before merge" today; the AI-review auto-merge is Phase 2.

## Interim fallback (NO bot identity yet) — what's true TODAY
- The two AI reviewers post their verdicts (as Paperclip review stages + PR comments), required CI must be green, but the **human performs the final merge click**. This keeps the model-diverse review gate without needing the bot identity. Current `main` protection already requires 1 approval; raise to 2 + add the reviewer checks when the bot identity lands.

## Reviewer roster — SUBSCRIPTION AUTH ONLY (user directive 2026-05-31)
Both AI reviewers use the user's **paid subscription logins**, NOT API keys.

- **Gemini Critic** — `gemini_local` adapter, **subscription/OAuth auth** (`~/.gemini/google_accounts.json`; no API key). CLI = `@google/gemini-cli` 0.42.0, `previewFeatures: true`.
  - **Model:** pinned `gemini-2.5-pro` (best confirmed). **Gemini 3.5/3 could NOT be verified** — is it available? Unknown: every probe on 2026-05-31 hit `"You have exhausted your capacity on this model"` (subscription **quota exhausted**), so neither model-listing nor a test call succeeded. If a `gemini-3.x` exists on the subscription, switch the pin to it once quota recovers and it's confirmed.
  - **TWO blockers for the Critic** (both must clear before it can `accept`): (1) **quota** — currently exhausted, the Critic literally cannot run; (2) the **empty-`--prompt` adapter bug** on review-stage wake (gemini yargs crash). The quota is the more fundamental one right now.
- **Codex reviewer** — `codex_local` adapter, **ChatGPT subscription auth** (`~/.codex/auth.json`; an `OPENAI_API_KEY` is also present but subscription is preferred per user). CLI = `codex-cli` 0.46.0. **Model `gpt-5-codex`** — verified responding live 2026-05-31. The planned `gpt-5.5` requires a newer codex CLI than 0.46.0, and `gpt-5.4`/`gpt-5.3-codex`/`gpt-5.2` are **not allowed on ChatGPT-account auth** (only `gpt-5-codex`/`gpt-5`/`gpt-5.4-mini`/`codex-auto-review` work); config fixed accordingly (`model_reasoning_effort` `xhigh`→`high`, `model` `gpt-5.5`→`gpt-5-codex`). Stood up as Paperclip agent `CodexReviewer` (`5b94562c-…`).
- **Kimi reviewer** — `opencode_local` adapter, **Gcore API key** (free tier, `gcore` provider in `~/.local/share/opencode/auth.json`), CLI = `opencode` 1.15.13, model **`gcore/moonshotai/Kimi-K2.5`** — verified responding live 2026-05-31. The pragmatic second live reviewer while Gemini is quota-blocked (Moonshot family = genuine diversity from Claude/OpenAI/Gemini); the one non-subscription reviewer, accepted because it needed zero code change and Gemini is down. Stood up as Paperclip agent `KimiReviewer` (`fdaa1d4c-…`).
- **Human** — optional third reviewer + standing veto (RV-6).

> **Practical consequence:** the live two-reviewer set today is **Codex (`gpt-5-codex`) + Kimi (`Kimi-K2.5`)** — both stood up as Paperclip agents with managed instruction bundles and verified responding on 2026-05-31. The Gemini Critic comes online as the *third* reviewer once its subscription quota + empty-prompt adapter bug are resolved.

## Open items before this is live
1. Human provisions the bot identity (above) — the hard blocker for actual auto-merge.
2. Fix the Gemini Critic empty-prompt adapter bug + wait for its subscription quota; then the Critic re-joins as the 3rd reviewer.
3. ~~Stand up the Codex reviewer agent in Paperclip~~ **DONE 2026-05-31.** Both live reviewers created with managed instruction bundles and verified responding: `CodexReviewer` (`codex_local`, `gpt-5-codex`, `5b94562c-…`) and `KimiReviewer` (`opencode_local`, `Kimi-K2.5`, `fdaa1d4c-…`). Both `idle`, heartbeat off (woken by review-stage assignment, not a timer).
4. Apply the branch-protection config + `allow_auto_merge` (needs the bot identity from #1).
5. Update engineer instruction to `--draft` (done in `engineer/AGENTS.md` ENG-4) and reviewer instructions (done in `reviewer/REVIEWER.md`, `codex-reviewer/AGENTS.md`, `kimi-reviewer/AGENTS.md`).
