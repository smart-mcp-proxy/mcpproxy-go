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

## Interim fallback (NO bot identity yet) — what's true TODAY
- The two AI reviewers post their verdicts (as Paperclip review stages + PR comments), required CI must be green, but the **human performs the final merge click**. This keeps the model-diverse review gate without needing the bot identity. Current `main` protection already requires 1 approval; raise to 2 + add the reviewer checks when the bot identity lands.

## Reviewer roster — SUBSCRIPTION AUTH ONLY (user directive 2026-05-31)
Both AI reviewers use the user's **paid subscription logins**, NOT API keys.

- **Gemini Critic** — `gemini_local` adapter, **subscription/OAuth auth** (`~/.gemini/google_accounts.json`; no API key). CLI = `@google/gemini-cli` 0.42.0, `previewFeatures: true`.
  - **Model:** pinned `gemini-2.5-pro` (best confirmed). **Gemini 3.5/3 could NOT be verified** — is it available? Unknown: every probe on 2026-05-31 hit `"You have exhausted your capacity on this model"` (subscription **quota exhausted**), so neither model-listing nor a test call succeeded. If a `gemini-3.x` exists on the subscription, switch the pin to it once quota recovers and it's confirmed.
  - **TWO blockers for the Critic** (both must clear before it can `accept`): (1) **quota** — currently exhausted, the Critic literally cannot run; (2) the **empty-`--prompt` adapter bug** on review-stage wake (gemini yargs crash). The quota is the more fundamental one right now.
- **Codex reviewer** — `codex-local` adapter, **ChatGPT subscription auth** (`~/.codex/auth.json` has `auth_mode` + `tokens`; an `OPENAI_API_KEY` is also present but subscription is preferred per user). CLI = `codex-cli` 0.46.0, default model **`gpt-5.5`** (adapter also knows `gpt-5.4`, `gpt-5.3-codex`, `gpt-5`). **Ready to use now** — this is the reliable reviewer while Gemini is quota-blocked.
- **Human** — optional third reviewer + standing veto (RV-6); and, per FR-005f, the **de-facto second reviewer right now** because Gemini is quota-exhausted (Codex + human = the two accepts until Gemini recovers).

> **Practical consequence:** until the Gemini subscription quota recovers (and the empty-prompt bug is fixed), the only working AI reviewer is **Codex (`gpt-5.5`)**. So the live two-reviewer set today is **Codex + human**; the Gemini Critic comes online as the second AI reviewer once its quota + adapter bug are resolved.

## Open items before this is live
1. Human provisions the bot identity (above).
2. Fix the Gemini Critic empty-prompt adapter bug (or the Critic can never `accept`).
3. Stand up the Codex reviewer agent in Paperclip (`codex-local`, verify creds).
4. Apply the branch-protection config + `allow_auto_merge`.
5. Update engineer instruction to `--draft` (done in `engineer/AGENTS.md` ENG-4) and reviewer instructions (done in `reviewer/REVIEWER.md`, `codex-reviewer/AGENTS.md`).
