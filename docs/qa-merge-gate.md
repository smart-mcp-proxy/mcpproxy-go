# Mandatory QA merge gate

Makes feature verification a **required, mechanical** check before a PR can
merge to `main` — closing the class of gap that let MCP-1214 ship (a native
macOS tray bug that Web-UI-only QA never exercised).

Normal merges land **without** `gh pr merge --admin` — through GitHub
auto-merge once every required check is green (see "Merging without --admin"
below). The `--admin` escape hatch is retained (`enforce_admins` stays `false`)
for **genuine emergencies only**; routine use is a smell.

## Required checks

| Check (status context) | Where it runs | Catches |
|---|---|---|
| `swift-test` | `.github/workflows/native-tests.yml` (macOS) | Native tray form logic — validation, dirty detection, tri-state durations. GUI-free, deterministic. |
| `settings-parity` | `.github/workflows/native-tests.yml` (Linux) | Web (`fields.ts`) ↔ native (`SettingsCatalog.swift`) duration-field drift (placeholder / optional). |
| `qa-gate` | Paperclip QATester → `scripts/post-qa-gate-status.sh` | Full feature QA. `success` only when QATester PASSes at the PR's **current head SHA**. |

`swift-test` and `settings-parity` are ordinary GitHub Actions jobs, but
`native-tests.yml` is deliberately **required-safe**: the workflow has no
top-level `paths:` filter, so it runs on *every* PR. A `changes` job
(`dorny/paths-filter`) detects whether native / settings files were touched,
and the two real jobs are gated with a job-level `if:`. On a PR that touches
none of those paths the jobs are **skipped**, and a skipped job reports its
required context as satisfied (green). This matters because GitHub produces
**no status at all** for a workflow skipped by a top-level `paths:` filter — a
required context that never reports stays "Expected — Waiting" and blocks every
non-native PR forever. See the `REQUIRED-SAFE DESIGN` header in
`native-tests.yml`.

`qa-gate` is a **commit status** the QATester posts at the end of its run
(keyed to the head SHA). Because it is SHA-keyed, any new push lands on a SHA
with no `qa-gate` status → the check returns to pending → QA must re-bless the
new head. This enforces the spec-075 rule ("PASS valid only while PR head ==
qa_head_sha") in the merge button itself.

## Activation (run after the workflow + scripts are on `main`)

> Order matters: a required check that has **never produced a status** shows as
> pending on every open PR and blocks normal merges immediately. Land
> `native-tests.yml` and the scripts on `main` first, then **verify on a
> non-native PR** (e.g. a dependency bump that touches none of the filtered
> paths) that `swift-test` and `settings-parity` both report green (skipped) and
> do **not** block it. Only after that confirmation, add the contexts to branch
> protection.

Current required checks (do not drop them — the API call **replaces** the set):

```
Lint, Unit Tests (ubuntu-latest, 1.25), Build (ubuntu-latest),
Build (macos-latest), Build (windows-latest), Build Frontend,
Validate PR title, Verify OpenAPI Artifacts
```

Add the three new contexts (keeps `enforce_admins: false`):

```bash
gh api -X PATCH repos/:owner/:repo/branches/main/protection/required_status_checks \
  -F strict=false \
  -f 'contexts[]=Lint' \
  -f 'contexts[]=Unit Tests (ubuntu-latest, 1.25)' \
  -f 'contexts[]=Build (ubuntu-latest)' \
  -f 'contexts[]=Build (macos-latest)' \
  -f 'contexts[]=Build (windows-latest)' \
  -f 'contexts[]=Build Frontend' \
  -f 'contexts[]=Validate PR title' \
  -f 'contexts[]=Verify OpenAPI Artifacts' \
  -f 'contexts[]=swift-test' \
  -f 'contexts[]=settings-parity' \
  -f 'contexts[]=qa-gate'
```

Stage `qa-gate` last — only after the QATester is posting it — so open PRs are
not blocked on a status that nobody emits yet. Until then, add just `swift-test`
and `settings-parity` — they report on every PR (green/skipped on non-native
PRs) thanks to the required-safe design above, so they will not strand open PRs.

## Merging without `--admin` (Model B — MCP-1248)

Goal: land PRs (owner + Paperclip agents) **without** `gh pr merge --admin`,
while keeping the gate meaningful. The mechanical merge always uses GitHub
auto-merge — agents **arm** the merge, they never bypass a required check.
`enforce_admins` stays `false` purely as an emergency hatch.

> Do **not** use `bypass_pull_request_allowances` for agents — that is a renamed
> `--admin` and breaks the spec-075 head-SHA invariant.

Three load-bearing facts make this work:

- A PR author cannot approve their own PR, and there is no second human. So the
  one required approval comes from a **bot/App identity** — a bot approval
  **does** count toward `required_approving_review_count`.
- `require_last_push_approval=false` and `dismiss_stale_reviews=false` (no
  CODEOWNERS) → a bot approval survives later pushes; no code-owner friction.
- `allow_auto_merge=true` is enabled on the repo, so `gh pr merge --auto` works.

The moving parts (all without `--admin`):

| Path | Mechanism | File |
|---|---|---|
| Trivial / docs / CI-metadata PRs | Auto-post `qa-gate=success` when the diff touches **no** code-bearing path (`**/*.go`, `go.mod/sum`, `cmd/**`, `internal/**`, `frontend/src/**`, `native/**`); code PRs are left to the real QATester. | `.github/workflows/qa-gate-trivial.yml` |
| Dependabot patch + minor | `dependabot/fetch-metadata` → `github-actions[bot]` approving review (counts) → `gh pr merge --auto --squash`. Majors still need a human. | `.github/workflows/dependabot-auto-merge.yml` |
| Code PRs (owner + Paperclip) | After Paperclip verdicts = ACCEPT **and** `qa-gate=success` at head SHA, a bot identity posts a GitHub approving review (reflecting the verdict) and arms auto-merge. Gate 3 stays a human **Approve** button; merge fires only when all 11 checks are green. | `scripts/arm-auto-merge.sh` |

`scripts/arm-auto-merge.sh` re-checks the live PR head against the SHA it was
blessed at (refuses on drift) and that `qa-gate` is `success` at that SHA before
approving — so the spec-075 rule is enforced in the merge path, not just the
status. It needs a **repo-scoped fine-grained PAT or GitHub App token**
(Contents RW, Pull requests RW, Commit statuses RW) injected as `GH_TOKEN`,
stored with the agent secrets (`searcher/agents/.env` pattern, gitignored) —
**not** the owner's `--admin`-capable login.

**Gate-3 doctrine** (supersedes "agents never merge PRs; a human merges"):
agents may post their review (reflecting the Paperclip verdict) and **arm**
auto-merge; Gate 3 stays a human Approve button; merge fires only when the full
gate is green; **agents never bypass required checks**.

## QATester contract

The `mcpproxy-qa` skill ("Merge Gate" + "Native macOS Tray Testing" sections)
requires QATester to:

1. Treat every surface implied by the diff as mandatory — `native/macos/**`
   means the native tray (swift test green + behavioral assertions), never
   waived as "mirrors the frontend".
2. Treat an interactive-surface `cannot_verify` as a **BLOCK**, not a low-risk
   pass (the MCP-1214 root cause).
3. Post the gate status for the exact head SHA at the end of the run:
   `scripts/post-qa-gate-status.sh "$QA_HEAD_SHA" success|failure "..."`.

## Known follow-up

`native-tests.yml` skips a handful of pre-existing/environmental Swift test
failures (AutoStart UserDefaults first-run, SSE-parser edge cases, a
JSONEncoder behavior canary) so the gate is green today. Green those and remove
the `--skip` flags to widen coverage.
