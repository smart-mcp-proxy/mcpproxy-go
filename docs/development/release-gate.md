---
title: "Release Qualification Gate"
sidebar_label: "Release Gate"
description: "Tag-blocking QA harness that produces one machine-readable pass/fail verdict before artifacts are published."
---

# Release Qualification Gate (Spec 081)

The release qualification gate is a tag-blocking QA harness: on a release tag it
runs the existing test suites plus a new server-type matrix and a set of
end-to-end invariants, then produces **one** machine-readable pass/fail verdict.
Artifact publication depends on that verdict, so a red gate means no release —
mechanically, with no human checklist in the loop.

- **Workflow**: [`.github/workflows/release-qa-gate.yml`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/.github/workflows/release-qa-gate.yml)
- **Driver**: [`cmd/release-gate`](https://github.com/smart-mcp-proxy/mcpproxy-go/tree/main/cmd/release-gate) (Go)
- **Report schema + merger**: [`internal/gatereport`](https://github.com/smart-mcp-proxy/mcpproxy-go/tree/main/internal/gatereport)
- **Fixtures**: [`cmd/mcpfixture`](https://github.com/smart-mcp-proxy/mcpproxy-go/tree/main/cmd/mcpfixture) (deterministic MCP server,
  reused as a Docker image via [`scripts/gate/build-fixture-image.sh`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/scripts/gate/build-fixture-image.sh))
  and [`tests/oauthserver`](https://github.com/smart-mcp-proxy/mcpproxy-go/tree/main/tests/oauthserver) (mock OAuth 2.1 + PKCE IdP).
- **Spec**: [`specs/081-release-qa-gate/spec.md`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/specs/081-release-qa-gate/spec.md)

## What the gate runs (T1)

The gate is a reusable workflow (`workflow_call`) whose jobs each write a JSON
**report fragment** into a shared directory; a final `verdict` job merges the
fragments against a hardcoded manifest and exits per the verdict.

| Job | Blocking entries | Timeout | Notes |
|-----|------------------|---------|-------|
| `build-candidate` | — | 15 min | Frontend build + embed + `go build` of the candidate `mcpproxy`, the fixtures, and the `release-gate` driver; uploaded as one artifact and reused everywhere. |
| `suite-api-e2e` | `suite/api-e2e`, `suite/api-e2e-cleanup-check` | 30 min | Runs `scripts/test-api-e2e.sh` **unmodified** (FR-003), then a second, real invocation of it under `scripts/test-api-e2e-cleanup-check.sh` proving the E2E cleanup trap holds (no blanket `pkill -f "mcpproxy.*serve"`). |
| `suite-race` | `suite/unit-race`, `suite/server-race` | 25 min | `go test -race ./internal/...` + `go test -tags server -race ./internal/serveredition/...`. |
| `suite-scan-eval` | `suite/scan-eval` | 10 min | `go run ./cmd/scan-eval --gate --min-recall 0.90 --max-fp 0.05` over the detect corpus — runs on **every** tag regardless of changed paths (FR-015). |
| `matrix-invariants` | `matrix/{stdio,http,sse,docker,oauth}`, `invariant/{activity-request-id,counters,quarantine-flow,upgrade-in-place}` | 20 min | Boots the candidate against five local fixture upstreams (connect → list → call → kill/reconnect) and asserts the US2 invariants against the live instance. |
| `surface-consistency` | `invariant/surface-consistency` (T4) | 20 min | Boots the same matrix, adds quarantined/disabled/unhealthy/live servers and asserts the CLI, MCP and rendered Web UI agree with the core on every server's state — see [Surface consistency](#surface-consistency-t4--blocking). |
| `web-ui-sweep` | — (advisory `advisory/web-ui-sweep`, T2) | 20 min | Playwright sweep over the Web UI **served by the candidate binary**, with a live stdio fixture upstream. `continue-on-error: true` — see [Web UI sweep](#web-ui-sweep-t2--advisory). |
| `verdict` | (merges all) | 10 min | `release-gate report` → `gate-report.json` + `$GITHUB_STEP_SUMMARY`; its exit code **is** the gate verdict. |

Every job uploads its fragment with `if: always()`, so a job that dies before
writing a fragment leaves a **missing blocking entry** — which the merger scores
as a FAIL (FR-004, fail-closed: no silent skips).

### Report fragment schema

Each check writes one `Fragment` (see `internal/gatereport/gatereport.go`):

```json
{ "name": "matrix/sse", "status": "pass|fail|flaky|skipped|not-run|advisory-fail",
  "reason": "...", "classification": "infrastructure|product",
  "duration_ms": 0, "retries": 0, "steps": [...], "details": {...} }
```

The merger produces `gate-report.json`: `{verdict, blocking_failures,
advisory_failures, counts, entries, manifest}`. A blocking entry passes only
with status `pass` or `flaky` (a `flaky` is a pass-on-retry, FR-010).

## How publication is gated (FR-002)

Both publisher workflows add a `qa-gate` job that `uses:` this reusable workflow
and list it in the publish job's `needs:`:

- **`release.yml`** — the `release` job (which creates the GitHub Release, and
  from which every public-facing job cascades) gains `qa-gate` in its `needs`.
  The `qa-gate` job is guarded to the same stable-only condition as `release`,
  so the expensive gate does not double-run on RC tags here.
- **`prerelease.yml`** — same, guarded to prerelease **tag** refs only (skipped
  on `next`-branch pushes, matching the `release` job's tag condition).

Because both the gate job and the publish job share the same trigger condition,
a **skipped** gate (non-qualifying ref) skips the publish job too — it can never
silently un-gate a release. The invariant is enforced by an audit test,
[`cmd/release-gate/workflow_audit_test.go`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/cmd/release-gate/workflow_audit_test.go),
which parses both publisher workflows and asserts every artifact-publishing
job's transitive `needs` closure includes the `qa-gate` job (FR-022 / SC-004).
Statically disabled jobs (`if: false…`, e.g. the server-edition `build-docker`)
are excluded — when such a job is enabled it must be re-parented under the gate,
or the audit will fail.

## Dry-running the gate before tagging (FR-001a)

Maintainers can qualify a candidate before cutting a tag:

- **GitHub UI**: Actions → **Release QA Gate** → *Run workflow* → optionally set
  the `ref` input to the branch/SHA to qualify.
- **CLI**: `gh workflow run release-qa-gate.yml -f ref=<branch-or-sha>`

A dry run publishes nothing and never counts as qualification for a later tag —
the stable/RC tag is always re-qualified on its own commit.

Locally you can run the driver directly (see `release-gate --help`):

```bash
go build -tags nogui -o mcpproxy ./cmd/mcpproxy
go build -o mcpfixture ./cmd/mcpfixture
go build -o oauthserver ./tests/oauthserver/cmd/server
go build -o release-gate ./cmd/release-gate
scripts/gate/build-fixture-image.sh
./release-gate matrix --binary ./mcpproxy --fixture ./mcpfixture \
  --oauth-server ./oauthserver --report-dir ./gate-report \
  --state-file ./gate-state.json --work-dir ./tmp-gate
./release-gate invariants --state-file ./gate-state.json --report-dir ./gate-report
./release-gate report --report-dir ./gate-report
```

## Web UI sweep (T2) — advisory

The `web-ui-sweep` job runs the Playwright sweep
([`e2e/web-ui-sweep`](https://github.com/smart-mcp-proxy/mcpproxy-go/tree/main/e2e/web-ui-sweep),
see [Web UI verification](web-ui-verification.md)) against the Web UI **served by
the candidate binary** — embedded frontend, never a dev server — with a live
`mcpfixture` stdio upstream so the servers/tools screens have real data. It
runs six spec files (the launcher's `playwright test` line is the list, and
`TestWebUISweepSpecListIsComplete` fails when it, `e2e/web-ui-sweep` and this
page disagree):

- `web-ui-sweep.spec.ts`: servers list, server detail (+ security tab), tools
  page and its search, activity log, and settings; fails on uncaught page
  exceptions.
- `visual-a11y-sweep.spec.ts`: WCAG contrast, layouts, accessible names, themes.
- `navigation-consistency.spec.ts`: Spec 109 sidebar, header, command palette,
  `+ Add` menu, redirects and filtered deep links.
- `profiles-clients.spec.ts`: Spec 108 Profiles and Clients screens, the goal
  flow, the binding-guard refusal, the Viewing chip, dialogs and layout.
- `profiles-scope.spec.ts`: Spec 108 profile scope on Tools, Activity, Sessions
  and Usage (audit acceptance check 6: the deep links `/tools?client=`,
  `/usage?profile=` and `/sessions?client=` load filtered, with a removable
  chip that survives navigation and Back).
- `profiles-unhide.spec.ts`: Spec 109-l un-hide of Spec 108 (Profiles in the
  sidebar and `+ Add`, the Viewing chip across pages, Clients-row and
  agent-token-row links, `/servers?profile=`, a Spec 108 warning surfacing as a
  needs-attention item, layout and keyboard order).
- `demo-ux-fixes.spec.ts`: the layout findings of the live Web UI demo, measured
  at 1440 and 900 px: the token Profile chip is one line inside its cell, and
  radio labels sit next to their radios in the create-profile, custom-client and
  bulk-move dialogs.
- `usertest-web-fixes.spec.ts`: the first-run user test findings, at 1440 and
  900 px: the header status pill names servers awaiting review, a secret-like
  Manual-add value is masked with a Show toggle, and profile Try it is readable
  and labels unsaved edits.

Setup is not duplicated in YAML: the job calls
[`scripts/run-web-smoke.sh`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/scripts/run-web-smoke.sh),
the same launcher used by hand (`./scripts/run-web-smoke.sh --show-report`),
passing `MCPPROXY_BINARY_PATH` / `MCPPROXY_FIXTURE_PATH` from the downloaded
`gate-candidate` artifact.

It is **advisory**, on purpose while the sweep earns its flake record:

- the job sets `continue-on-error: true`, so its failure never becomes the
  reusable workflow's conclusion — publishers whose `needs:` list the gate still
  publish;
- the driver runs with `--advisory`, so a red sweep is recorded as
  `advisory-fail` under the non-blocking manifest entry `advisory/web-ui-sweep`
  and listed in `advisory_failures` — reported, never silent;
- the Playwright HTML report + server log are uploaded as the
  `web-ui-sweep-playwright-report` artifact (14 days) for post-mortem.

> **Do not rename that artifact casually.** The gate is a *reusable* workflow, so
> its artifacts land in the publisher's own run, and `release.yml` /
> `prerelease.yml` build their release assets by globbing every `*.tar.gz` /
> `*.zip` out of *all* run artifacts. A Playwright report stores its traces as
> `playwright-report/data/<sha>.zip` (retained on failure — exactly what an
> advisory sweep tolerates), so both publishers exclude
> `*/web-ui-sweep-playwright-report/*` from that glob by name. Renaming the
> artifact without updating both publishers would publish unnamed trace zips as
> public release assets;
> `TestPublishersDoNotShipSweepArtifacts` guards this.

**Promotion to blocking** is a two-line change: set `Blocking: true` on the
`advisory/web-ui-sweep` manifest entry and drop `continue-on-error` from the job
(the FR-016 end state). Do it once the sweep has passed on three consecutive
release tags with no flaky or infrastructure failure — the same criterion T3
uses (FR-021).

## Surface consistency (T4) — blocking

`invariant/surface-consistency` (job `surface-consistency`, US5 / FR-018)
proves the surfaces do not lie to each other. The job boots the same
five-type server matrix (stdio/http/sse/docker/oauth), then
`release-gate consistency` adds four servers so every admin state is on
screen — `gate-cons-live` (healthy), `gate-cons-quarantined`,
`gate-cons-disabled` and `gate-cons-broken` (endpoint refuses → unhealthy) —
and compares every surface against the core's `GET /api/v1/servers`:

| Surface | Read path | Fields compared |
|---------|-----------|-----------------|
| CLI | `mcpproxy upstream list -o json --config <gate config>` (daemon client mode) | enabled, quarantined, connected, connection state, `health.level`, `health.admin_state`, `health.status`, `tool_count` |
| MCP | `/mcp` `upstream_servers` `operation=list` — storage + upstream manager + runtime health, an independent projection | enabled, quarantined, connection state, `health.level`, `health.admin_state`, `health.status` (the row carries no tool count) |
| Web UI | rendered server cards on `/ui/servers`, read by `e2e/web-ui-sweep/surface-snapshot.mjs` in one long-lived Chromium page | enabled (⋯ menu Enable/Disable), quarantined (⋯ Review), tool count, status chip colour and text — the expected chip is derived from the core row with `ServerCard.vue`'s rules, because the Web UI never prints `health.level` as text |
| macOS tray | — | not compared; recorded as `not_compared` in the fragment (T3) |

Connection state is compared only for enabled, non-quarantined servers (a
disabled server is `idle` in the StateView and `disconnected` in the manager,
which no user sees). It runs three phases, each retried until the surfaces
agree or `--settle-timeout` (60s) runs out; an attempt counts only when the
core did not change while the surfaces were being read:

1. **settled** — the mixed state, Web UI freshly loaded;
2. **live** — `gate-cons-live` is disabled through REST while the page stays
   open, so the Web UI must follow through its SSE/poll update path;
3. **reload** — the same state after a page reload. Live failing while reload
   passes is reported as a stale live view.

A divergence that survives the retries fails the gate with one line per field:
`webui gate-cons-quarantined webui.chip_class: core=badge-secondary webui=badge-success`.
The fragment's `details` carry the last captured rows of every surface per
phase.

**Run it by hand** next to `scripts/test-api-e2e.sh` (needs `make build` for
an embedded Web UI, Node for the reader):

```bash
./scripts/test-surface-consistency.sh
```

**Prove it fails**: pass `--seed-mismatch surface:server:field=value` to the
script, or dispatch the gate with the `seed_consistency_mismatch` input (read
only on `workflow_dispatch`, never on a tag's `workflow_call`):

```bash
./scripts/test-surface-consistency.sh --seed-mismatch cli:gate-cons-live:health.level=degraded
```

## macOS tray smoke (T3) — advisory

`advisory/macos-smoke` (job `macos-smoke`, US4 / FR-019–FR-021) runs on a hosted
`macos-15` runner. It is **advisory**: `continue-on-error: true` on the job plus
`release-gate run-suite --advisory`, so a failure is listed under
`advisory_failures` and never blocks publication. A missing fragment (runner
never scheduled, job died before upload) is reported the same way.

The job and the by-hand command are the same script, `scripts/test-macos-smoke.sh`:

1. `swift build -c release` the tray (`native/macos/MCPProxy`).
2. `swift test` the whole native suite (`--skip-unit-tests` skips it locally).
3. Start a core with an isolated `--data-dir`/`--config`/socket under a short
   scratch root and scratch `HOME` (never `~/.mcpproxy`).
4. Assemble a minimal ad-hoc-signed `.app` (the bare SwiftPM binary crashes
   without a bundle: `UNUserNotificationCenter`), launch it with
   `MCPPROXY_TRAY_SKIP_CORE=1 MCPPROXY_HOME=… MCPPROXY_SOCKET_PATH=…`, and assert
   from the tray's own log that it reached the core over the Unix socket
   (`connectToCore: core is ready`) and read the core's exact version
   (`got version=<core /api/v1/info version>`).
5. `SIGTERM` the tray: it must exit 0 within 15 s and leave the external core
   running.

What it cannot do on a hosted runner: grant Accessibility / Screen Recording to
the job, so the `mcpproxy-ui-test` primitives (status-bar item, menu items,
screenshot) are **not** used and the tray is not compared in the T4 surface
check yet (still `not_compared`). No signing or notarization secrets are
involved. Logs (`core.log`, `tray.log`, swift build/test output) upload as the
`macos-smoke-logs` artifact.

**Promotion criterion (FR-021):** promote to blocking once the job has passed on
3 consecutive release tags with no flaky or infrastructure failure. Promotion is
`Blocking: true` on the `advisory/macos-smoke` manifest entry plus dropping
`continue-on-error` from the job. Adding the Accessibility-driven menu
assertions (TCC grant on the runner) is the follow-up that lets the tray join
the T4 comparison.

## Runtime budget (FR-005 / SC-007)

Every job has an explicit `timeout-minutes`. The blocking portion targets ≤ 30
minutes wall clock; the critical path is `build-candidate` → `matrix-invariants`
(the suite jobs run in parallel). If the full non-`-short` race suite empirically
blows its budget on standard runners, fall back to `-short` **in the
`suite-race` job only**, with a loud comment noting the FR-003 deviation — the
heavy property/timing tests still run unguarded in `e2e-tests.yml`'s
`stress-tests` job, so coverage is preserved.

## FR-011 correlation note

The activity-log invariant (`invariant/activity-request-id`) empirically probes
whether a caller-supplied `X-Request-Id` lands on the tool-call activity record.
Today `internal/server/mcp.go` synthesizes its own per-call request ids for both
MCP-native and REST tool calls, so header correlation does not round-trip; the
check falls back to locating each call by a unique argument **nonce** and then
proves the **core-recorded** request id resolves through
`GET /api/v1/activity?request_id=`. The report records this as a `limitation`
in the fragment `details` rather than hiding it. Persisting the caller's
`X-Request-Id` end-to-end (so header correlation is exact) is a middleware change
left out of this stage on purpose.
