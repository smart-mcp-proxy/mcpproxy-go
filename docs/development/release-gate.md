# Release Qualification Gate (Spec 081)

The release qualification gate is a tag-blocking QA harness: on a release tag it
runs the existing test suites plus a new server-type matrix and a set of
end-to-end invariants, then produces **one** machine-readable pass/fail verdict.
Artifact publication depends on that verdict, so a red gate means no release —
mechanically, with no human checklist in the loop.

- **Workflow**: [`.github/workflows/release-qa-gate.yml`](../../.github/workflows/release-qa-gate.yml)
- **Driver**: [`cmd/release-gate`](../../cmd/release-gate) (Go)
- **Report schema + merger**: [`internal/gatereport`](../../internal/gatereport)
- **Fixtures**: [`cmd/mcpfixture`](../../cmd/mcpfixture) (deterministic MCP server,
  reused as a Docker image via [`scripts/gate/build-fixture-image.sh`](../../scripts/gate/build-fixture-image.sh))
  and [`tests/oauthserver`](../../tests/oauthserver) (mock OAuth 2.1 + PKCE IdP).
- **Spec**: [`specs/081-release-qa-gate/spec.md`](../../specs/081-release-qa-gate/spec.md)

## What the gate runs (T1)

The gate is a reusable workflow (`workflow_call`) whose jobs each write a JSON
**report fragment** into a shared directory; a final `verdict` job merges the
fragments against a hardcoded manifest and exits per the verdict.

| Job | Blocking entries | Timeout | Notes |
|-----|------------------|---------|-------|
| `build-candidate` | — | 15 min | Frontend build + embed + `go build` of the candidate `mcpproxy`, the fixtures, and the `release-gate` driver; uploaded as one artifact and reused everywhere. |
| `suite-api-e2e` | `suite/api-e2e` | 15 min | Runs `scripts/test-api-e2e.sh` **unmodified** (FR-003). |
| `suite-race` | `suite/unit-race`, `suite/server-race` | 25 min | `go test -race ./internal/...` + `go test -tags server -race ./internal/serveredition/...`. |
| `suite-scan-eval` | `suite/scan-eval` | 10 min | `go run ./cmd/scan-eval --gate --min-recall 0.90 --max-fp 0.05` over the detect corpus — runs on **every** tag regardless of changed paths (FR-015). |
| `matrix-invariants` | `matrix/{stdio,http,sse,docker,oauth}`, `invariant/{activity-request-id,counters,quarantine-flow,upgrade-in-place}` | 20 min | Boots the candidate against five local fixture upstreams (connect → list → call → kill/reconnect) and asserts the US2 invariants against the live instance. |
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
[`cmd/release-gate/workflow_audit_test.go`](../../cmd/release-gate/workflow_audit_test.go),
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

## Extension slots (T2 / T3 / T4)

The manifest reserves three non-blocking slots, recorded as
`not-run` / `not-implemented-yet` until their stage lands:

- **`reserved/web-ui-sweep` (T2)** — run the existing Playwright Web UI sweep
  (`docs/development/web-ui-verification.md`) against the candidate binary's
  **embedded** frontend, with the matrix fixtures as its upstream data
  (US3, FR-016/017). Add a `web-ui-sweep` job that downloads the `gate-candidate`
  artifact, serves it, runs the sweep, and writes a `reserved/web-ui-sweep`
  fragment; then flip that manifest entry to blocking.
- **`reserved/macos-smoke` (T3)** — a macOS-runner job that launches the tray
  against a running core and uses the `mcpproxy-ui-test` accessibility
  primitives to assert presence, menu items, and state agreement (US4,
  FR-019/020). It **starts advisory** (`advisory-fail` does not block). Its
  promotion criterion (FR-021), to be stated in the workflow when the job is
  added: *promote to blocking once it has passed on 3 consecutive release tags
  with no flaky or infrastructure failure.* Promotion is a one-line change
  (advisory → blocking on the manifest entry / job status handling).
- **`reserved/surface-consistency` (T4)** — one comparison over data the other
  jobs already collect: REST vs CLI (`mcpproxy upstream list -o json`) vs Web UI
  (vs tray, when T3 ran) agreement on each server's identity, admin state, and
  health level, per the unified `health` contract (US5, FR-018).

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
