# Mandatory QA merge gate

Makes feature verification a **required, mechanical** check before a PR can
merge to `main` — closing the class of gap that let MCP-1214 ship (a native
macOS tray bug that Web-UI-only QA never exercised).

The `gh pr merge --admin` escape hatch is intentionally retained
(`enforce_admins` stays `false`) for genuine emergencies; normal merges must be
green.

## Required checks

| Check (status context) | Where it runs | Catches |
|---|---|---|
| `swift-test` | `.github/workflows/native-tests.yml` (macOS) | Native tray form logic — validation, dirty detection, tri-state durations. GUI-free, deterministic. |
| `settings-parity` | `.github/workflows/native-tests.yml` (Linux) | Web (`fields.ts`) ↔ native (`SettingsCatalog.swift`) duration-field drift (placeholder / optional). |
| `qa-gate` | Paperclip QATester → `scripts/post-qa-gate-status.sh` | Full feature QA. `success` only when QATester PASSes at the PR's **current head SHA**. |

`swift-test` and `settings-parity` are ordinary GitHub Actions jobs — they
appear automatically once `native-tests.yml` is on `main`.

`qa-gate` is a **commit status** the QATester posts at the end of its run
(keyed to the head SHA). Because it is SHA-keyed, any new push lands on a SHA
with no `qa-gate` status → the check returns to pending → QA must re-bless the
new head. This enforces the spec-075 rule ("PASS valid only while PR head ==
qa_head_sha") in the merge button itself.

## Activation (run after the workflow + scripts are on `main`)

> Order matters: a required check that has **never produced a status** shows as
> pending on every open PR and blocks normal merges immediately. Land
> `native-tests.yml` and the scripts on `main` first, confirm the jobs run, then
> add the contexts to branch protection.

Current required checks (do not drop them — the API call **replaces** the set):

```
Lint, Unit Tests (ubuntu-latest, 1.25), Build (ubuntu-latest),
Build (macos-latest), Build (windows-latest), Build Frontend,
Validate PR title, Verify OpenAPI Artifacts
```

Add the three new contexts (keeps `enforce_admins: false`):

```bash
gh api -X PATCH repos/:owner/:repo/branches/main/protection/required_status_checks \
  -f strict=false \
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
and `settings-parity` (they are deterministic and self-producing).

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
