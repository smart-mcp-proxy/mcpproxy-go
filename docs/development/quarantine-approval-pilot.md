# Quarantine/approval testing pilot

The pilot checks MCPProxy's quarantine and approval behaviour against an
**independent reference model**, reports which selected requirements and
scenarios ran, and uses **mutation testing** to show that the checks catch
deliberately introduced security regressions. It runs on every PR as three
status checks (`Quarantine Pilot: Conformance`, `Quarantine Pilot: Coverage`,
`Quarantine Pilot: Mutation`) and has a scheduled exploration lane.

It is a pilot: it covers a bounded set of rules for one server at a time. It
does not prove the implementation correct.

## Layout

| Path | Role |
|---|---|
| `internal/qapilot/model.go` | The reference state machine (server quarantine, enablement, trust mode, tool records, dispatch classification). Imports no production package; computes every expectation from its own state, the action and the encoded policy. |
| `internal/qapilot/policy.go` | Decision tables: add-time admission, the agent deny-list, permitted approval-transition reasons, the 896-cell dispatch classification table. |
| `internal/qapilot/scenarios.go` | The **mandatory deterministic scenario inventory** (S01…S29), fixture contracts, and the recorded known deviations. |
| `internal/qapilot/requirements.go` | Selected requirements QP-01…QP-20 with their policy sources, and the list of mandatory standalone checks. |
| `internal/qapilot/ledger.go` | `qapilot.Track(t, id, reqs…)` records each check's real outcome (passed / failed / skipped) to `$QAPILOT_REPORT_DIR`. |
| `internal/runtime/qapilot_conformance_test.go` | Runtime **adapter**: maps model actions onto `checkToolApprovals`, `ApproveTools`, `ApproveAllTools`, `QuarantineServer`, `EnableServer`, `BlockTools`, `SetToolEnabled` and a real close/reopen restart; projects storage into a snapshot and compares after every step. Also the invariant table, the stale-review and server-replaced denial checks, the fixture-verdict check, the known-deviation test and the rapid exploration lane. |
| `internal/preflight/qapilot_classify_test.go` | Every dispatch decision cell against the model. |
| `internal/config/qapilot_admission_test.go` | Add-time quarantine default for every trust mode / global switch / legacy field. |
| `internal/httpapi/qapilot_authz_test.go` | Agent vs admin on every denied REST operation: the exact gate body (the gate returns before the handler) **and** zero controller calls on an instrumented controller; the admin request reaches the controller for the ten operations whose controller call the mock exposes. |
| `internal/server/qapilot_server_test.go` | Dispatch with a counting upstream (refusals make zero upstream calls, admission exactly one); the MCP surface cannot release a server; the security-approval scan gate. |
| `cmd/qapilot` | `report` (coverage gate) and `mutate` (mutation harness). |
| `tests/qapilot/mutations.json` | The versioned mutation set. |
| `.github/workflows/quarantine-pilot.yml` | The CI lanes. |

## Selected requirements

The IDs, titles and sources are in `internal/qapilot/requirements.go`
(`go run ./cmd/qapilot list`). In short:

| ID | Rule | Checked by |
|---|---|---|
| QP-01 | New server quarantined on add unless global quarantine off or trust `auto`; empty/unknown trust is `manual` | admission table |
| QP-02 | Quarantined server: dispatch refused, no upstream call | S01, S15, S22, S25, counting upstream |
| QP-03 | Release = baseline trust: pending → approved, changed stays changed | S02, S03, S15, S20, S22 |
| QP-04 | `security/approve` needs a scan report with no hard-tier finding unless forced; a refusal leaves the server quarantined and writes no baseline | security-approve table |
| QP-05 | Agent tokens cannot mutate servers/tools over REST: 403 and no controller call | authz table (23 operations) |
| QP-06 | MCP has no unquarantine op; `upstream_servers` patch/update ignores `quarantined:false` | MCP checks |
| QP-07 | New tool on a quarantined manual/scan server is pending and stays pending across reconnects | S01, S02, S18, S26 |
| QP-08 | Trusted server's first discovery is the baseline; durable baseline decision recorded | S04 |
| QP-09 | Post-baseline additions: manual → pending; scan → clean approved / dangerous pending; auto or global off → approved | S05, S11, S12, S15, S16, S17 |
| QP-10 | Changed definition held in manual (whatever the verdict); scan approves it on a clean verdict when the structural gate finds nothing and holds it on a dangerous one; auto approves | S03, S06, S08, S09, S13, S14, S16, S28, S29 |
| QP-11 | Changed stays changed across reconnects; revert restores approved | S06, S07, S19 |
| QP-12 | Operator approval approves the *current* definition; reverting afterwards is a change | S09, S10, S25 |
| QP-13 | Permitted transition reasons for changed→approved and pending→approved | invariant table (99 combinations) |
| QP-14 | Dispatch classification precedence for every input combination | classify table (896 cells) |
| QP-15 | Quarantine, enablement, records, blocks and the baseline decision survive a restart (also when the config file omits `quarantined`) | S18, S19, S20, S24, S26 |
| QP-16 | Approved+enabled tool dispatched exactly once; pending/changed/blocked tools and disabled servers refused with zero upstream calls | S21, S23, S24, counting upstream |
| QP-17 | Stale `expected_hashes` → `review_out_of_date`, nothing written | stale-hash check |
| QP-18 | Wrong `expected_incarnation` → `server_replaced`, nothing written | server-replaced check |
| QP-19 | Fixture contracts carry the scan verdict the model assumes | fixture-verdict check |
| QP-20 | Under scan a change that widens the approved contract (spec 120 structural gate) is held whatever the verdict; a narrowing with a clean verdict is approved | S28, S29 |

### Policy readings the pilot had to choose

The sources disagree or are silent in a few places. The model encodes the
reading below; each is also recorded in the requirement's `Note`.

- **Scan mode and a changed tool.** Spec 119 (2026-10-22) approves a changed
  tool on a clean verdict under `scan` and keeps the RT-02 hold under `manual`;
  the pilot encodes that split. Spec 120 adds the structural gate in front of
  the verdict: the model carries one widening shape (`BenignWide`, a new
  defaulted `webhook_url` parameter) and holds a scan-mode change that widens
  the approved shape (QP-20); the per-keyword rules are pinned by
  `TestContractDelta_SharedFixture` in `internal/runtime`, and hint flips stay
  outside the model (no scenario flips hints).
- **Baseline marker.** The snapshot compares the durable marker on its own
  (`baseline_decided`) as well as `has_baseline`, so a marker that is never
  written or is lost across a restart cannot hide behind the approved records.
  The model sets it on a discovery pass that leaves an approved or changed
  record (FR-022) and never on the legacy unquarantine path (spec 117 A7); the
  implementation agrees.
- **`quarantine_enabled: false` and a changed tool.** The docs say disabling
  quarantine globally auto-approves tool changes. The implementation records
  the tool as `changed` but, because the gate is off, still dispatches it.
  This is recorded as **known deviation D01** (`TestQAPilot_KnownDeviations`
  asserts it is still exactly that), not as a mandatory scenario: callability
  matches the policy, the record status does not. It needs a policy decision,
  not a test change. The randomized exploration lane excludes the
  `quarantine_enabled: false` setup for the same reason (S17 covers its
  additions deterministically).
- **Scan-mode approval of a new tool on a quarantined server.** Spec 086
  FR-013 gates additions on the scan without mentioning the server flag; the
  pilot encodes "approved by scan, dispatch still refused by the server-level
  verdict" (S15).
- **Permitted transition reasons.** `docs/features/quarantine-testing.md`
  predates `baseline_trust`, `auto_approve_changes` and `scan_approved`; the
  pilot's lists include them per `docs/features/security-quarantine.md`, and
  exclude `scan_approved` for changed→approved (RT-02).

### Observations outside the pilot's gate

- With an agent token, a mutating request for a server that does **not exist**
  returns 404 before the 403 gate (the `/servers/{id}` group resolves the
  server first). An agent can therefore probe server-name existence. Not a
  pilot requirement; worth a follow-up.
- The pre-existing `TestRapidQuarantineStateMachine` copied the stored status
  into its expected state on `changeDescription` and `reconnect`, so its oracle
  agreed with whatever the implementation did on those actions. The pilot
  rewrote those two branches to compute the expectation from the model's own
  state and added an exact status comparison.

## What is outside the pilot

Multi-server interactions, tool removal from the inventory (replaced
inventories, authoritative empty refreshes), trust-mode changes on a live
server, prompts, profiles and agent-token *scope* on dispatch, OAuth, the
review-dirty state, legacy collapsed (pre-105) records, scan freshness as an
approval input (advisory only in the current policy), the Web UI and the tray.
`cmd/release-gate` still covers its one happy-path add → block → approve →
call flow against a built binary; the pilot does not replace it.

## Running it locally

```bash
go test ./internal/qapilot/ ./cmd/qapilot/                         # model self-tests, gate tests
go test -race -run 'TestQAPilot_' ./internal/preflight/ ./internal/config/ \
  ./internal/httpapi/ ./internal/runtime/ ./internal/server/        # conformance (~30 s)
QAPILOT_REPORT_DIR=/tmp/ledger go test -run 'TestQAPilot_' ./internal/... && \
  go run ./cmd/qapilot report --dir /tmp/ledger                     # coverage gate
go run ./cmd/qapilot mutate --repo . --mutations tests/qapilot/mutations.json  # mutation gate (commit first)
QAPILOT_EXPLORE=1 go test -run TestQAPilot_Explore -rapid.checks=50 ./internal/runtime/  # exploration
```

The conformance tests assert on their own without the ledger; with
`QAPILOT_REPORT_DIR` set they also append one line per check, from the test's
real outcome (`t.Failed()` / `t.Skipped()`), so a skip is never reported as a
pass. `qapilot report` fails when the ledger is empty, when a mandatory
requirement has no passing check, when a mandatory scenario or standalone
check is missing, skipped or failed, when any of the 896 decision cells is
missing or failed, when a required mutation survived, or when the mutation
baseline or any mutation run had an infrastructure outcome.

## Mutation testing

### Tool evaluation

`gremlins` (v0.5-dev) and `go-mutesting` both install and run on the Go 1.26
toolchain (verified: `gremlins unleash --dry-run ./internal/toolreview` lists
12 runnable mutants). Both generate **syntactic operator mutants** (negated
conditionals, boundary changes, arithmetic swaps) over whole packages. That
does not fit the pilot's question, which is whether the tests catch a
*policy* regression such as "an agent may release a server" or "a clean
verdict re-approves a changed contract" — mutants a syntactic operator cannot
express — and whole-package operator runs over `internal/runtime` and
`internal/server` take far longer than a PR budget. The pilot therefore uses
a small deterministic harness and keeps gremlins as an optional exploration
tool.

### Harness

`go run ./cmd/qapilot mutate` reads the versioned set
(`tests/qapilot/mutations.json`, `version: 1`). For each mutation it:

1. creates a disposable `git worktree add --detach <tmp> HEAD` (the caller's
   checkout is never modified; it refuses to run with uncommitted tracked
   changes, since those are invisible to the worktree);
2. replaces an anchor string that must occur **exactly once** in the target
   file (any other count is a `runner_error`, not a detection);
3. runs `go vet` on the test packages (a compile failure is `build_error`);
4. runs the mutation's designated tests and classifies the run:
   `detected` only when a **designated** test fails; `unrelated_failure` when
   other tests fail; `survived` when all pass; `timeout`; `runner_error`;
5. removes the worktree (also on cancellation; `--keep-worktrees` for
   inspection).

The unmutated baseline must pass the union of the designated tests first,
run with `-v` so the harness can confirm every designated test **actually
ran** (a renamed test is a `baseline_failed`, not a silently undetectable
mutation); otherwise every mutation is reported `baseline_failed` and the gate
fails. An empty selection, a mutation without detecting tests, or a `-run`
pattern that matches no test is refused. Cancellation kills the whole process
group of a running `go test` and bounds the wait on its output pipes.

Limitations: anchors are exact strings, so a refactor of the anchored line
needs the set updated (the harness says so); one mutation per run; detection
is by test name, so a designated test must be the one that fails.

### The set (version 1)

| ID | Requirement | Mutation | Detecting test(s) | Outcome | Time |
|---|---|---|---|---|---|
| M01 | QP-05 | Remove the agent-token denial for unquarantine (agents may release a server over REST) | `TestQAPilot_AgentAuthorizationTable` | detected | 8.057s |
| M02 | QP-05 | Drop the agent deny-list entirely (every caller is treated as admin) | `TestQAPilot_AgentAuthorizationTable` | detected | 7.729s |
| M03 | QP-02 | Dispatch ignores server-level quarantine | `TestQAPilot_ClassifyTable, TestQAPilot_DispatchCountingUpstream` | detected | 14.288s |
| M04 | QP-14 | Tool-level quarantine gate never applies (pending and changed tools become callable) | `TestQAPilot_ClassifyTable, TestQAPilot_Scenarios` | detected | 13.015s |
| M05 | QP-16 | A changed (rug-pulled) tool classifies as ready | `TestQAPilot_ClassifyTable, TestQAPilot_DispatchCountingUpstream` | detected | 23.576s |
| M06 | QP-09 | trust_mode scan accepts a non-clean verdict as scan evidence (dangerous new tools auto-approved) | `TestQAPilot_Scenarios/S12` | detected | 11.262s |
| M07 | QP-13 | The transition guard lets the plain auto_approve reason (pending-only) clear a changed record | `TestQAPilot_InvariantTable` | detected | 7.397s |
| M08 | QP-07 | A quarantined server's first discovery is treated as a trusted baseline (tools auto-approved while quarantined) | `TestQAPilot_Scenarios/S01, TestQAPilot_Scenarios/S02` | detected | 8.639s |
| M09 | QP-03 | Releasing a server no longer promotes its pending tools (release leaves tools held) | `TestQAPilot_Scenarios/S02` | detected | 12.532s |
| M10 | QP-15 | A recorded quarantine is not retained across a restart whose config file omits the key (admission gate retain rule removed) | `TestQAPilot_Scenarios/S26, TestQAPilot_Scenarios/S27` | detected | 11.603s |
| M11 | QP-01 | New manual-trust servers are admitted unquarantined | `TestQAPilot_AdmissionTable` | detected | 2.406s |
| M12 | QP-06 | MCP upstream_servers patch honours quarantined:false (an agent-reachable release path) | `TestQAPilot_MCPCannotReleaseServer` | detected | 10.979s |
| M13 | QP-04 | Security approval ignores hard-tier findings without force | `TestQAPilot_SecurityApprovalTable` | detected | 13.846s |
| M14 | QP-04 | Security approval no longer requires a scan report without force | `TestQAPilot_SecurityApprovalTable` | detected | 14.429s |
Full run (macOS arm64, local, see the PR for the commit): baseline passed; detected=14 survived=0 other=0; total 3m23s including the baseline run. All 14 mutations are `required`.


Exclusions: no mutation of the stale-`expected_hashes` validator is included;
expressing it needs a stable anchor inside the validator and the behaviour is
covered by `TestQAPilot_StaleReviewWritesNothing`. No whole-repository
mutation score is pursued.

## Remote enforcement

The workflow adds the three status checks to every PR. They block a merge only
when branch protection on `main` lists them as required contexts; see the PR
description for the state at the time of the pilot.
