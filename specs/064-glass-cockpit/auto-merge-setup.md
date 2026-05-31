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

## Reviewer roster
- **Gemini Critic** (`gemini_local`, model `gemini-2.5-pro` — pinned; `auto` was hitting the empty-prompt adapter bug). Known issue: the gemini adapter crashes on an empty `--prompt` (review-stage wake) — must be fixed or worked around for the Critic to actually post accepts.
- **Codex reviewer** (`codex-local` adapter — verify CLI/creds). Second family.
- **Human** — optional third reviewer + standing veto (RV-6); also the second reviewer when one AI reviewer is unavailable (FR-005f).

## Open items before this is live
1. Human provisions the bot identity (above).
2. Fix the Gemini Critic empty-prompt adapter bug (or the Critic can never `accept`).
3. Stand up the Codex reviewer agent in Paperclip (`codex-local`, verify creds).
4. Apply the branch-protection config + `allow_auto_merge`.
5. Update engineer instruction to `--draft` (done in `engineer/AGENTS.md` ENG-4) and reviewer instructions (done in `reviewer/REVIEWER.md`, `codex-reviewer/AGENTS.md`).
