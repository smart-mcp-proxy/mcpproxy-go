<!-- GENERATED FILE — do not edit by hand. -->
<!-- Source: roadmap.yaml  ·  Generator: scripts/gen-roadmap.py -->
<!-- Regenerate: python3 scripts/gen-roadmap.py  (or scripts/gen-roadmap) -->

# MCPProxy Roadmap

> **Generated — do not edit by hand.** This file is rendered from [`roadmap.yaml`](./roadmap.yaml) by [`scripts/gen-roadmap.py`](./scripts/gen-roadmap.py). Edit `roadmap.yaml` and re-run the generator.

The roadmap models cross-spec **epics → tasks** with a dependency DAG, execution `status`, `priority`, and links — the things a per-spec `tasks.md` checkbox list cannot express. Per-spec checkbox progress is recomputed live from each `specs/<NNN>/tasks.md`.

[`roadmap.yaml`](./roadmap.yaml) holds the **working set** (todo · in-progress · in-review · blocked · parked). Cold shipped epics are swept into [`roadmap.archive.yaml`](./roadmap.archive.yaml) and surface in the [Shipped](#shipped-archived) table below, so the working file stays small while provenance survives. A `depends_on:` edge into the archive is satisfied by definition.

## How to regenerate

```bash
python3 scripts/gen-roadmap.py     # writes ROADMAP.md
scripts/gen-roadmap                # convenience wrapper (same thing)
python3 scripts/gen-roadmap.py --check          # CI canary: fail if ROADMAP.md is stale
python3 scripts/gen-roadmap.py --check-github   # cross-check statuses vs live GitHub PR state,
                                                # spec links, depends_on ids, and status sanity
                                                # (add --strict to fail on warnings; needs gh)
python3 scripts/gen-roadmap.py --archive --dry-run   # preview the cold-done sweep
python3 scripts/gen-roadmap.py --archive             # sweep into roadmap.archive.yaml + regenerate
```

## roadmap.yaml schema (short form)

- **epics[]** — each has `id` (stable slug, DAG node), `title`, `status` (todo·in_progress·in_review·blocked·done), `priority` (P0–P3), `depends_on: [ids]` (DAG edges, prerequisite→dependent), optional `parked: true`, and links `spec:` / `pr:` / `mcp:` (external MCP-xxxx).
- **epics[].tasks[]** — child tasks with the same fields; their `depends_on` may reference sibling tasks or other epics.
- See the header comment in `roadmap.yaml` for the full field reference.

## Roadmap at a glance

The cross-epic dependency graph — **one node per epic**, edges point prerequisite → dependent. Task-level detail lives in the collapsible sections below, and dependency-free epics are listed under the graph rather than drawn as disconnected boxes, so this stays legible at default zoom. Node colour = status: 🟢 done · 🔵 in-progress · 🟡 in-review · 🔴 blocked · ⚪ todo · ⚫ parked.

```mermaid
graph LR
  sandbox_isolation["Non-Docker sandbox isolation (Landlock)"]
  scanner_v2["Spec 076 deterministic offline tool-sca…"]
  ux_audit["Web UI + macOS app UX audit"]
  action_log_transparency["Action log / transparency — info at a g…"]
  analytics_dashboard["Analytics dashboard as default page"]
  scanner_simplification["Scanner simplification (deterministic d…"]
  tpa_db["tpa-db: versioned TPA signature databas…"]
  remote_access_tunnel["Remote access tunnel (feature-flagged M…"]
  schema_deferred["Deferred-schema serialization for the d…"]
  token_bench["Token-efficiency benchmark: measured sa…"]
  tool_graph["Tool co-occurrence graph (experimental,…"]
  telemetry_identity["Telemetry identity & data quality (mach…"]
  telemetry_v7_churn["Telemetry v7: honest funnel + churn ins…"]

  scanner_v2 --> sandbox_isolation
  ux_audit --> action_log_transparency
  ux_audit --> analytics_dashboard
  scanner_v2 --> scanner_simplification
  scanner_simplification --> tpa_db
  tpa_db --> remote_access_tunnel
  ux_audit --> remote_access_tunnel
  analytics_dashboard --> remote_access_tunnel
  schema_deferred --> token_bench
  analytics_dashboard --> tool_graph
  telemetry_identity --> telemetry_v7_churn

  classDef done fill:#1f7a1f,stroke:#0d3d0d,color:#ffffff;
  classDef in_progress fill:#1f6feb,stroke:#0b3d91,color:#ffffff;
  classDef todo fill:#6e7781,stroke:#3d4248,color:#ffffff;
  class sandbox_isolation,scanner_v2,analytics_dashboard,scanner_simplification,schema_deferred done;
  class ux_audit,action_log_transparency,token_bench,telemetry_identity,telemetry_v7_churn in_progress;
  class tpa_db,remote_access_tunnel,tool_graph todo;
```

**Independent epics** (15) — no cross-epic prerequisites; each stands alone:

- 🔵 **Release qualification gate (auto-QA matrix blocks the tag)** — In progress · P0
- 🔵 **Planning/docs truth automation** — In progress · P2
- 🔵 **Discovery-quality eval harness (Spec 065 second half)** — In progress · P3
- ⚪ **MCP protocol upgrade to 2026-07-28 revision** — Todo · P1
- ⚪ **Windows native tray app** — Todo · P2
- ⚫ **Server marketplace** — Todo · P3 · parked
- ⚫ **Audit SIEM integration** — Todo · P3 · parked
- ⚫ **Paid-tier MVP (billing / seats / license)** — Todo · P3 · parked
- ⚫ **SDK v1 migration** — Todo · P3 · parked
- ⚫ **SSO (server edition)** — Todo · P3 · parked
- ⚪ **Security gateway Tracks C/D (per-arg least-privilege + signature provenance)** — Todo · P3
- 🟢 **Upgrade awareness & guided update** — Done · P0
- 🟢 **Connect step trust: preview, visible backup, one-click undo** — Done · P0
- 🟢 **Registries — easier search + add-server** — Done · P1
- 🟢 **Tray↔core decoupling: socket/REST API only, no config-file reads** — Done · P2

## Epic details

Each epic's child tasks, their internal dependency graph, and tracker/PR links — **collapsed by default**, expand the ones you care about. Full metadata (priority, spec progress) is in the [Epics](#epics) table below.

<details>
<summary>🔵 Web UI + macOS app UX audit — In progress · P0</summary>

> End-to-end UX pass across Web UI and the macOS tray app; the umbrella for the polish push. (No spec yet — 064 is the unrelated agent-fleet glass-cockpit spec.) 2026-08-29 truth-sync: both sweeps and every finding they raised SHIPPED in August (this file had them at todo). Audits: docs/qa/ux-audit-webui-2026-08.md (36 findings) and docs/qa/ux-audit-macos-tray-2026-08.md (16 findings); both carry a Resolution section mapping finding -> PR. What is left is regression-proofing, not findings.

```mermaid
graph LR
  ux_audit_webui_sweep["Web UI heuristic + Playwright UX sweep"]
  ux_audit_macos_sweep["macOS tray app UX sweep (settings parity, flo…"]
  ux_audit_webui_fixes["Close the 36 Web UI findings"]
  ux_audit_macos_fixes["Close the 16 macOS tray findings"]
  ux_audit_recheck_defects["Five new defects found while re-checking the…"]
  ux_audit_sweep_regressions["Fold the audit's regression assertions into t…"]
  ux_audit_sweep_found_defects["Three defects the completed sweep found on ma…"]
  ux_audit_tray_live_verify["Verify the 16 tray fixes on a running tray"]

  ux_audit_webui_sweep --> ux_audit_webui_fixes
  ux_audit_macos_sweep --> ux_audit_macos_fixes
  ux_audit_webui_fixes --> ux_audit_recheck_defects
  ux_audit_webui_fixes --> ux_audit_sweep_regressions
  ux_audit_sweep_regressions --> ux_audit_sweep_found_defects
  ux_audit_macos_fixes --> ux_audit_tray_live_verify

  classDef done fill:#1f7a1f,stroke:#0d3d0d,color:#ffffff;
  classDef todo fill:#6e7781,stroke:#3d4248,color:#ffffff;
  class ux_audit_webui_sweep,ux_audit_macos_sweep,ux_audit_webui_fixes,ux_audit_macos_fixes,ux_audit_recheck_defects,ux_audit_sweep_regressions,ux_audit_sweep_found_defects done;
  class ux_audit_tray_live_verify todo;
```

| Task | Status | Refs |
| --- | --- | --- |
| Web UI heuristic + Playwright UX sweep | 🟢 Done | #1046 |
| macOS tray app UX sweep (settings parity, flows) | 🟢 Done | #1043 |
| Close the 36 Web UI findings | 🟢 Done | #1044 #1048 #1049 #1050 #1051 #1052 #1053 #1054 |
| Close the 16 macOS tray findings | 🟢 Done | #1055 #1056 |
| Five new defects found while re-checking the Web UI audit on v0.61.0 | 🟢 Done | #1062 #1072 #1077 |
| Fold the audit's regression assertions into the committed sweep (e2e/web-ui-sweep) | 🟢 Done | — |
| Three defects the completed sweep found on main | 🟢 Done | — |
| Verify the 16 tray fixes on a running tray | ⚪ Todo | — |

</details>

<details>
<summary>🔵 Release qualification gate (auto-QA matrix blocks the tag) — In progress · P0</summary>

> Attacks the return cliff (48% of installs are one-and-done; day-1 return 31% — corrected 2026-07-10, identity-deduped; the earlier '17.7% day-2' figure was un-deduped anonymous_id churn) and the one conceded competitor advantage: stability. No release tag until the surface x server-type matrix (MCP/REST/CLI/Web UI x stdio/http/sse/docker/oauth) plus invariants (activity-log/token counters move, quarantine flow, reconnect survival, in-place upgrade) pass automatically; macOS app smoke is advisory until promoted (3 consecutive passes, spec 081 US4). Assembles existing assets: test-api-e2e.sh, Playwright sweep, scan-eval gate, mcpproxy-ui-test.

Spec: [081-release-qa-gate](./specs/081-release-qa-gate/)

```mermaid
graph LR
  release_qa_gate_matrix["T1: tag-blocking release-gate workflow: serve…"]
  release_qa_gate_playwright["T2: wire the Playwright Web UI sweep into the…"]
  release_qa_gate_macos["T3: macOS app smoke on a macos runner, adviso…"]
  release_qa_gate_consistency["T4: surface-state consistency check (tray/Web…"]

  release_qa_gate_matrix --> release_qa_gate_playwright
  release_qa_gate_matrix --> release_qa_gate_macos
  release_qa_gate_matrix --> release_qa_gate_consistency

  classDef done fill:#1f7a1f,stroke:#0d3d0d,color:#ffffff;
  classDef todo fill:#6e7781,stroke:#3d4248,color:#ffffff;
  class release_qa_gate_matrix,release_qa_gate_playwright done;
  class release_qa_gate_macos,release_qa_gate_consistency todo;
```

| Task | Status | Refs |
| --- | --- | --- |
| T1: tag-blocking release-gate workflow: server-type matrix (stdio/http/sse/docker/oauth) + invariants (activity-log/request-id, token+telemetry counters, quarantine flow, reconnect, upgrade-in-place), publish jobs gated on the verdict, scan-eval unconditional on tags | 🟢 Done | #819 |
| T2: wire the Playwright Web UI sweep into the gate (currently manual-trigger only) | 🟢 Done | #1030 |
| T3: macOS app smoke on a macos runner, advisory until 3 consecutive passes (today zero CI automation for the tray app) | ⚪ Todo | — |
| T4: surface-state consistency check (tray/Web UI/CLI agree with core on server states) | ⚪ Todo | — |

</details>

<details>
<summary>🔵 Action log / transparency — info at a glance — In progress · P1</summary>

> Surface the most important activity/security/connection signals at a glance; reduce digging. Vision pillar 'feel control → transparency' — the activity log is a headline feature, polish it and bring it to the tray menu. Builds on the shipped activity-log backend + retention (spec 024, 95% shipped — this epic is the at-a-glance UX on top, not the backend, so 024 is not the progress driver).

```mermaid
graph LR
  sessions_web_ui["Sessions in the Web UI: meaningful session na…"]
  action_log_glance_view["At-a-glance action log view (top signals, hea…"]
  action_log_tray_menu["Activity in the tray menu (recent tool calls…"]
  tray_menu_open_telemetry["tray_menu_opened counter: Swift menuWillOpen…"]
  action_log_retention_tie_in["Tie activity retention/size into the glance v…"]
  activity_storage_bounds["Bound every activity-adjacent store: response…"]

  action_log_glance_view --> action_log_tray_menu
  action_log_glance_view --> action_log_retention_tie_in

  classDef done fill:#1f7a1f,stroke:#0d3d0d,color:#ffffff;
  classDef in_progress fill:#1f6feb,stroke:#0b3d91,color:#ffffff;
  classDef todo fill:#6e7781,stroke:#3d4248,color:#ffffff;
  class sessions_web_ui done;
  class activity_storage_bounds in_progress;
  class action_log_glance_view,action_log_tray_menu,tray_menu_open_telemetry,action_log_retention_tie_in todo;
```

| Task | Status | Refs |
| --- | --- | --- |
| Sessions in the Web UI: meaningful session names in the Activity Log filter + the existing /sessions page linked in the sidebar | 🟢 Done | — |
| At-a-glance action log view (top signals, health) | ⚪ Todo | — |
| Activity in the tray menu (recent tool calls + security events, jump to full log) | ⚪ Todo | — |
| tray_menu_opened counter: Swift menuWillOpen (MCPProxyApp.swift:192) -> lightweight POST /api/v1/telemetry/tray-menu-opened -> registry counter -> heartbeat tray_menu_opened_24h | ⚪ Todo | — |
| Tie activity retention/size into the glance view | ⚪ Todo | — |
| Bound every activity-adjacent store: response truncation on the write path (#1173/#1174), per-server tool_calls buckets (#1176), omitempty zero-erasure (#1175) | 🔵 In progress | #1174 |

</details>

<details>
<summary>🔵 Token-efficiency benchmark: measured savings, published results — In progress · P1</summary>

> Measure the real token cost of every routing/savings mode combination — baseline, compact signatures (spec 085), deferred schemas (spec 102), optimistic calling via self-healing pre-dispatch validation, code_execution (spec 096) and stored scripts (spec 097) — on replayed real sessions and on public benchmarks, then publish the results on mcpproxy.app/blog. Every savings number we quote today is an estimate; this turns them into reproducible measurements. Sequenced after schema-deferred so the newest mode is in the matrix. Spec 103 landed 2026-08-31 (#1137 spec+plan, #1139 tasks) after 13 cross-model review rounds; three of its findings changed the design rather than the wording: a recording carries no prompt/conversation/completion oracle so replay CANNOT show agent behaviour (US1 deterministic cost vs US2 live loop are now separate stories); replay needs a FLEET INPUT because the export has no fleet snapshot; and bodies-off yields menu costs plus one cross-mode delta, never an absolute workload cost. The matrix is 5 distinct behaviours, not a 3x2x2 product.

Spec: [103-token-bench](./specs/103-token-bench/) · PR: #1141 #1147 #1151 #1153 #1160

```mermaid
graph LR
  token_bench_harness["Replay harness: activity-log sessions re-run…"]
  token_bench_public["Run public suites locally (τ-bench / BFCL / M…"]
  token_bench_blog["Publish results + methodology on mcpproxy.app…"]
  token_bench_telemetry["Heartbeat v10: per-tool_response_mode token c…"]

  token_bench_harness --> token_bench_public
  token_bench_public --> token_bench_blog

  classDef done fill:#1f7a1f,stroke:#0d3d0d,color:#ffffff;
  classDef todo fill:#6e7781,stroke:#3d4248,color:#ffffff;
  class token_bench_harness done;
  class token_bench_public,token_bench_blog,token_bench_telemetry todo;
```

| Task | Status | Refs |
| --- | --- | --- |
| Replay harness: activity-log sessions re-run under each mode combo; tokens per completed task + first-call success + retries | 🟢 Done | #1141 #1147 #1151 #1153 #1160 |
| Run public suites locally (τ-bench / BFCL / MCP-specific — final list verified by a research pass) and record reproducible results | ⚪ Todo | — |
| Publish results + methodology on mcpproxy.app/blog | ⚪ Todo | — |
| Heartbeat v10: per-tool_response_mode token counters for real-world cohort validation | ⚪ Todo | — |

</details>

<details>
<summary>🔵 Telemetry identity &amp; data quality (machine_id + CI-filter hardening) — In progress · P1</summary>

> 2026-07-11 source audit: the CLIENT half is DONE and RELEASED — the old framing ('add a hashed machine_id (schema v6)') is stale. machine_id (HMAC-SHA256 of the OS machine id, internal/telemetry/machine_id.go) is emitted unconditionally in every heartbeat (telemetry.go:789; telemetry is opt-out) and shipped in v0.47.0; the client schema is already v7, a version past the v6 this note described; CI is filtered client-side by disabling telemetry outright (env_overrides.go). Worker verified live in prod 2026-07-07 (machine_id 100% populated). The audit also found that the 79%-unknown launch_source was a CLIENT bug misfiled to mcpproxy-dash — a dashboard cannot display a value the client is incapable of sending. That is now fixed (see telemetry-launchsource-tray). Remaining: dashboard consumption (mcpproxy-dash) + snapshot-cron alerting.

```mermaid
graph LR
  telemetry_machineid_client["Hashed machine_id in heartbeat (schema v6)"]
  telemetry_machineid_worker["Worker migration: machine_id column + extract…"]
  telemetry_launchsource_tray["Emit launch_source=tray — the 'tray' value wa…"]
  telemetry_machineid_dash["Dashboard identityExpr prefers machine_id; ex…"]
  telemetry_snapshot_alerting["Alerting on external-downloads snapshot cron…"]

  telemetry_machineid_client --> telemetry_machineid_worker
  telemetry_machineid_worker --> telemetry_machineid_dash
  telemetry_launchsource_tray --> telemetry_machineid_dash

  classDef done fill:#1f7a1f,stroke:#0d3d0d,color:#ffffff;
  classDef todo fill:#6e7781,stroke:#3d4248,color:#ffffff;
  class telemetry_machineid_client,telemetry_machineid_worker,telemetry_launchsource_tray done;
  class telemetry_machineid_dash,telemetry_snapshot_alerting todo;
```

| Task | Status | Refs |
| --- | --- | --- |
| Hashed machine_id in heartbeat (schema v6) | 🟢 Done | https://github.com/smart-mcp-proxy/mcpproxy-go/pull/796 |
| Worker migration: machine_id column + extraction (repo mcpproxy-telemetry) | 🟢 Done | mcpproxy-telemetry#3 |
| Emit launch_source=tray — the 'tray' value was UNREACHABLE in the client, and that (not the dashboard) was the 79%-unknown root cause | 🟢 Done | — |
| Dashboard identityExpr prefers machine_id; exclude %-dev versions from human cohort (repo mcpproxy-dash) | ⚪ Todo | — |
| Alerting on external-downloads snapshot cron (34-day outage went unnoticed) | ⚪ Todo | — |

</details>

<details>
<summary>🔵 Telemetry v7: honest funnel + churn instrumentation — In progress · P1</summary>

> 2026-07-06 recheck DEBUNKED the 72.4% connect-skip story: genuine never-connected skip = 0% (wizard dismiss stamped 'skipped' on users who connected via ConnectModal/CLI/manual config). 2026-07-10 recheck debunked the OTHER two spec-080 headline metrics as well: 'day-2 return 17.7%' was un-deduped anonymous_id churn (true, identity-deduped: one-and-done 48%, day-1 return 31%, day-7 16.6% — matches dashboard); '42% retrieve_tools -> 16% real call' was lifetime-flag vs windowed-counter asymmetry (true conversion ~90%; missing piece is a first_real_tool_call_ever activation flag). Real cliff = 48% one-and-done. 2026-07-11 source audit + fix: ALL of spec 080 is shipped and released in v0.47.0 (#813) — T1 wizard completed_external, T2 funnel fields, T3 pre-churn snapshot, US4 schema v7. The previous note claimed 'only cross-repo churn analytics (T4) remains'; that was WRONG — first_real_tool_call_ever had ZERO occurrences in Go code and was in-repo CLIENT work. It is now implemented, so the retrieve->call funnel is finally measurable lifetime-vs-lifetime. P0->P1: only cross-repo T4 now remains.

Spec: [080-telemetry-v7-churn](./specs/080-telemetry-v7-churn/)

```mermaid
graph LR
  telemetry_v7_wizard_fix["T1: wizard metric fix - on dismiss record con…"]
  telemetry_v7_funnel_fields["T2: funnel observability fields (wizard_shown…"]
  telemetry_v7_prechurn_snapshot["T3: pre-churn snapshot (previous_shutdown cle…"]
  telemetry_v7_realcall_flag["first_real_tool_call_ever activation flag (sy…"]
  telemetry_v7_churn_events["T4: cross-repo churn_events materialization +…"]

  telemetry_v7_wizard_fix --> telemetry_v7_churn_events
  telemetry_v7_funnel_fields --> telemetry_v7_churn_events
  telemetry_v7_prechurn_snapshot --> telemetry_v7_churn_events

  classDef done fill:#1f7a1f,stroke:#0d3d0d,color:#ffffff;
  classDef todo fill:#6e7781,stroke:#3d4248,color:#ffffff;
  class telemetry_v7_wizard_fix,telemetry_v7_funnel_fields,telemetry_v7_prechurn_snapshot,telemetry_v7_realcall_flag done;
  class telemetry_v7_churn_events todo;
```

| Task | Status | Refs |
| --- | --- | --- |
| T1: wizard metric fix - on dismiss record connect step as completed_external (not skipped) when the user already connected via another path | 🟢 Done | #813 |
| T2: funnel observability fields (wizard_shown, web_ui_opened counter, days_since_install, active_days_30d) | 🟢 Done | #813 |
| T3: pre-churn snapshot (previous_shutdown clean\|crash via BBolt flag, last_error_code) so the final heartbeat doubles as cause-of-death | 🟢 Done | #813 |
| first_real_tool_call_ever activation flag (symmetric to first_retrieve_tools_call_ever) so the retrieve->call funnel step is measurable lifetime-vs-lifetime | 🟢 Done | — |
| T4: cross-repo churn_events materialization + dash Churn page with H1-H4 hypothesis signatures (repos mcpproxy-telemetry / mcpproxy-dash; tracked here for DAG visibility, out of scope of spec 080) | ⚪ Todo | — |

</details>

<details>
<summary>🔵 Planning/docs truth automation — In progress · P2</summary>

> Automate the consistency checks this very audit had to do by hand: roadmap vs GitHub PR state, tasks.md updates on implementation PRs, volatile CLAUDE.md/README facts, and quickstart contract tests.

```mermaid
graph LR
  hygiene_roadmap_github_check["gen-roadmap --check-github: cross-check roadm…"]
  hygiene_tasks_reconcile["CI rule: PR touching specs/<id> implementatio…"]
  hygiene_spec_evidence_check["scripts/check-spec-evidence.py: deterministic…"]
  hygiene_spec_gardener["Weekly cloud routine: LLM judges only the res…"]
  hygiene_docs_facts["Generate volatile CLAUDE.md/README facts (Go…"]
  hygiene_quickstart_contract["Run top quickstart.md scenario per spec as co…"]

  hygiene_spec_evidence_check --> hygiene_spec_gardener

  classDef done fill:#1f7a1f,stroke:#0d3d0d,color:#ffffff;
  classDef todo fill:#6e7781,stroke:#3d4248,color:#ffffff;
  class hygiene_roadmap_github_check,hygiene_spec_evidence_check,hygiene_spec_gardener done;
  class hygiene_tasks_reconcile,hygiene_docs_facts,hygiene_quickstart_contract todo;
```

| Task | Status | Refs |
| --- | --- | --- |
| gen-roadmap --check-github: cross-check roadmap.yaml statuses vs gh PR state + dangling spec links | 🟢 Done | #800 |
| CI rule: PR touching specs/<id> implementation paths must update tasks.md | ⚪ Todo | — |
| scripts/check-spec-evidence.py: deterministic check that every TICKED task cites code that exists | 🟢 Done | — |
| Weekly cloud routine: LLM judges only the residue the evidence-check cannot decide, opens/updates one propose-only PR | 🟢 Done | #824 #870 #999 #842 |
| Generate volatile CLAUDE.md/README facts (Go version, built-in tool list, sample config) from code with --check | ⚪ Todo | — |
| Run top quickstart.md scenario per spec as contract test in test-api-e2e.sh | ⚪ Todo | — |

</details>

<details>
<summary>🔵 Discovery-quality eval harness (Spec 065 second half) — In progress · P3</summary>

> IN PROGRESS — 2026-08-31 audit, corrected on cross-model review: both halves of the HARNESS shipped INDEPENDENTLY (not via token-bench-harness), but spec 065 is NOT fully met, so this is not done. FR-009 and SC-005 require CI to FAIL on a discovery regression beyond tolerance; the retrieval-D1 job is continue-on-error on pull requests, so on the PR path it does not fail — eval.yml itself records the promotion to PR-blocking as still open (MCP-742). A second, weaker tension to adjudicate rather than assume: CN-002 asks that scoring never run against a live drifting corpus, and D1 does boot a live mcpproxy serving 7 reference servers — but #931 pinned all seven upstreams to freeze-era versions and the job gates on the exact corpus ID set, so the corpus is reproducible in practice. Decide whether that satisfies CN-002 or whether a committed snapshot is required. Remaining work is therefore the gating promotion, not the harness. The earlier 'superseded / folded into token-bench-harness' framing was wrong on its own terms: token-bench-harness is still unbuilt, so nothing could have been folded into it. Security recall/FP half: cmd/scan-eval, backing the Spec 076/077 gate in eval.yml. Discovery-quality half: the eval.yml retrieval-d1 job boots mcpproxy and scores retrieval_golden_v1.json against a committed baseline at --tolerance 0.05 via the pinned external mcp-eval repo — note continue-on-error is scoped to github.event_name == 'pull_request', so the job is REPORT-ONLY on PRs (npx/uvx fetch flake) and BLOCKING on both the nightly schedule and manual workflow_dispatch runs. Promoting it to PR-blocking after a green soak is still open (MCP-742). NB the workflow's own inline comment says 'blocking on the nightly schedule' and omits workflow_dispatch. A second in-repo implementation lives in bench/: metrics.go defines RecallAtK/NDCGAtK, and the SC-003 recall@5 = 0.68 +/- 0.05 parity gate through the production Bleve index is asserted in bench/armindex_test.go (armindex.go supplies the production index wiring, not the assertion). Kept as a stable depends_on target; do not build a standalone harness.

Spec: [065-evaluation-foundation](./specs/065-evaluation-foundation/)

```mermaid
graph LR
  discovery_eval_pr_blocking["Promote retrieval-D1 from report-only to PR-b…<br/>MCP-742"]


  classDef todo fill:#6e7781,stroke:#3d4248,color:#ffffff;
  class discovery_eval_pr_blocking todo;
```

| Task | Status | Refs |
| --- | --- | --- |
| Promote retrieval-D1 from report-only to PR-blocking (spec 065 FR-009/SC-005), and adjudicate the CN-002 frozen-corpus question | ⚪ Todo | `MCP-742` |

</details>

<details>
<summary>⚪ tpa-db: versioned TPA signature database for the offline scanner — Todo · P1</summary>

> Vision pillar 'feel protected': the deterministic detect engine (Spec 076/077) ships with built-in checks but no updatable knowledge of in-the-wild Tool Poisoning Attacks. Build a versioned, offline-first signature/pattern database (known TPA campaigns, malicious phrase corpora, IoC hashes) that the engine consumes — bundled with the binary, refreshable out-of-band, community-contributable, and guarded by the existing scan-eval recall/FP CI gate. SPEC STAGE: specs/101-tpa-db MERGED as PR #1028 on 2026-08-27 (kept in this note, not in pr:, because pr: is implementation evidence and a docs(specs) merge is not that); no implementation has started, so the epic and every child task stay todo (the pr: link is the spec, not the build).

Spec: [101-tpa-db](./specs/101-tpa-db/)

```mermaid
graph LR
  tpa_db_format["Signature DB format + loader (versioned, sign…"]
  tpa_db_corpus["Seed corpus: catalog known public TPA campaig…"]
  tpa_db_refresh["Out-of-band refresh (offline-friendly: manual…"]

  tpa_db_format --> tpa_db_corpus
  tpa_db_format --> tpa_db_refresh

  classDef todo fill:#6e7781,stroke:#3d4248,color:#ffffff;
  class tpa_db_format,tpa_db_corpus,tpa_db_refresh todo;
```

| Task | Status | Refs |
| --- | --- | --- |
| Signature DB format + loader (versioned, signed, bundled default) | ⚪ Todo | — |
| Seed corpus: catalog known public TPA campaigns/patterns into the DB | ⚪ Todo | — |
| Out-of-band refresh (offline-friendly: manual file drop + optional fetch), eval-gated | ⚪ Todo | — |

</details>

<details>
<summary>⚪ MCP protocol upgrade to 2026-07-28 revision — Todo · P1</summary>

> STABLE GATE CLEARED 2026-09-02: mark3labs/mcp-go v1.0.0 (stable) released; go.mod still pins v0.57.0. Raised P3->P1 on the 2026-09-02 issue-prioritization pass — spec 058 has spec.md only, next step is speckit.plan then tasks (tracker #532). Earlier: UNBLOCKED 2026-08-12: the mcp-go gate cleared — v1.0.0-beta.1 (mark3labs/mcp-go#951) ships full 2026-07-28 support with per-request era detection (the pin was v0.55.x, topping out at 2025-11-25). Spec 058 revision MERGED as PR #1033 on 2026-08-27 (kept in this note, not in pr:, because pr: is implementation evidence and a docs(specs) merge is not that — implementation has not started, hence todo): final error-code renumbering (-32020/-32021/-32022), FR-001..006 / FR-014..016 recast as adopt-and-verify, FR-028 legacy-only transport pin as the safe merge state, plus Risks & Watch Items. CROSS-SPEC CONFLICT still open and now the mandatory first task: FR-012 forbids per-connection */list variation; SHIPPED Spec 057 selects toolset by URL path /mcp/p/<slug> — Option A/B decided at plan time (acceptance = the 2 tests failing under beta.1). 028 agent-token scoping is already compatible (header-carried). Tracker: #532.

Spec: [058-mcp-2026-upgrade](./specs/058-mcp-2026-upgrade/)

</details>

<details>
<summary>⚪ Windows native tray app — Todo · P2 · MCP-43</summary>

> No spec: link — this epic is the native TRAY app; specs/002-windows-installer is the unrelated INSTALLER spec (35/60) and its badge said nothing about tray progress (wrong link removed 2026-07-10). Option C: WebView2 window reusing shipped Web UI. Most exit criteria already ship; gaps = native window, toasts, profile submenu, Win11 smoke. Telemetry: Windows = ~23% of GitHub downloads but only ~4% of active installs (downloads→actives ~12:1 vs macOS ~4:1) — gate WebView2 work on finding the funnel break first. 2026-08-31 audit: reset from in_review to todo. Scoped precisely: Windows tray support DID ship in 2025 via the cross-platform Go/systray build (#74, merged 2025-10-23, cmd/mcpproxy-tray/ + internal/tray under GOOS=windows) — what this epic tracks is the NATIVE WebView2 replacement, and for that no PR is open or merged and native/windows/ holds only a README placeholder with no WebView2 code anywhere in the tree. So 'in review' had no PR to point at.

```mermaid
graph LR
  windows_tray_funnel_qa["Windows first-run QA pass (downloads→actives…"]
  windows_tray_window["WebView2 native window + profile submenu<br/>MCP-43"]

  windows_tray_funnel_qa --> windows_tray_window

  classDef todo fill:#6e7781,stroke:#3d4248,color:#ffffff;
  class windows_tray_funnel_qa,windows_tray_window todo;
```

| Task | Status | Refs |
| --- | --- | --- |
| Windows first-run QA pass (downloads→actives 12:1 vs macOS 4:1 — find the funnel break before WebView2 work) | ⚪ Todo | — |
| WebView2 native window + profile submenu | ⚪ Todo | `MCP-43` |

</details>

<details>
<summary>⚪ Remote access tunnel (feature-flagged MVP, spec 089) — Todo · P2</summary>

> One-button Web UI exposure of /mcp via external tunnel binary (cloudflared quick tunnel first) so Claude custom connectors (all tiers incl. Free, syncs to iOS/Android) can reach local MCP servers (e.g. Obsidian) — behind a feature flag, off by default, mandatory OAuth 2.1+PKCE+DCR gate, per-server exposure allowlist, remote-origin activity logging. Research: docs/research/remote-access-tunnel-research-2026-07-29.html (25/25 claims verified; niche unoccupied — Docker MCP Gateway lacks it). Sequenced after tpa-db (+ shipped scanner work 086-088), macOS tray redesign (ux-audit) and analytics-dashboard per owner decision 2026-07-29. No hosted relay/payments in MVP.

Spec: [089-remote-access-tunnel](./specs/089-remote-access-tunnel/)

```mermaid
graph LR
  tunnel_oauth_gate["OAuth 2.1 authorization-server gate for tunne…"]
  tunnel_orchestration["cloudflared quick-tunnel orchestration (detec…"]
  tunnel_exposure_allowlist["Per-server exposure allowlist (default none;…"]
  tunnel_webui_tray["Web UI open/close button + URL/QR/instruction…"]

  tunnel_oauth_gate --> tunnel_exposure_allowlist
  tunnel_orchestration --> tunnel_webui_tray
  tunnel_exposure_allowlist --> tunnel_webui_tray

  classDef todo fill:#6e7781,stroke:#3d4248,color:#ffffff;
  class tunnel_oauth_gate,tunnel_orchestration,tunnel_exposure_allowlist,tunnel_webui_tray todo;
```

| Task | Status | Refs |
| --- | --- | --- |
| OAuth 2.1 authorization-server gate for tunnel-origin traffic (PKCE, DCR, Anthropic callback allowlist, token lifecycle/revocation) | ⚪ Todo | — |
| cloudflared quick-tunnel orchestration (detect/launch/supervise/parse URL) + feature flag + never-auto-start | ⚪ Todo | — |
| Per-server exposure allowlist (default none; quarantined non-exposable; hot-reload) | ⚪ Todo | — |
| Web UI open/close button + URL/QR/instructions + warning banner; tray active-state indicator; remote-origin activity marker | ⚪ Todo | — |

</details>

<details>
<summary>⚪ Tool co-occurrence graph (experimental, feature-flagged) — Todo · P2</summary>

> Local-only co-occurrence graph mined from the activity log: suggests likely-next tools to agents and surfaces usage-chain analytics. Everything sits behind experimental.tool_graph, off by default — nothing leaves the machine.

```mermaid
graph LR
  tool_graph_core["Co-occurrence graph from the activity log + r…"]
  tool_graph_ranking["Session-aware rank boost in retrieve_tools"]
  tool_graph_mining["Workflow mining: frequent chains → suggested…"]
  tool_graph_analytics["Usage-chain analytics on the dashboard/stats…"]

  tool_graph_core --> tool_graph_ranking
  tool_graph_core --> tool_graph_mining
  tool_graph_core --> tool_graph_analytics

  classDef todo fill:#6e7781,stroke:#3d4248,color:#ffffff;
  class tool_graph_core,tool_graph_ranking,tool_graph_mining,tool_graph_analytics todo;
```

| Task | Status | Refs |
| --- | --- | --- |
| Co-occurrence graph from the activity log + related_tools hint in call_tool responses (flag-gated) | ⚪ Todo | — |
| Session-aware rank boost in retrieve_tools | ⚪ Todo | — |
| Workflow mining: frequent chains → suggested stored scripts (spec 097 synergy) | ⚪ Todo | — |
| Usage-chain analytics on the dashboard/stats page | ⚪ Todo | — |

</details>

<details>
<summary>⚪ Security gateway Tracks C/D (per-arg least-privilege + signature provenance) — Todo · P3</summary>

> Track A→Spec 056, Track B→Spec 059 (both shipped). UNBUILT: Track C per-ARGUMENT allow-listing (per-tool scope exists in mcp_direct_scope.go); Track D provenance + human-readable signature diff (SHA-256 pinning exists via Spec 032). Build ON 032/028, don't re-implement; honor the rug-pull re-quarantine interaction rule vs 032 auto-approve.

Spec: [054-mcp-security-gateway](./specs/054-mcp-security-gateway/)

</details>

<details>
<summary>⚫ Server marketplace — Todo · parked · P3 · MCP-37</summary>

> PARKED. ~60% already ships (browse/search/one-click add). No spec yet; gaps tracked as MCP-3246..3250 (tray entries, metadata, telemetry). (070 is the registries-search-add spec, not a marketplace spec.)

</details>

<details>
<summary>⚫ Audit SIEM integration — Todo · parked · P3 · MCP-39</summary>

> PARKED. Splunk HEC / Elastic _bulk / syslog shippers reusing JSONL export pipeline.

</details>

<details>
<summary>⚫ Paid-tier MVP (billing / seats / license) — Todo · parked · P3 · MCP-40</summary>

> PARKED. Server-edition revenue motion: Ed25519 license tokens, seats, Stripe checkout. Behind //go:build server.

</details>

<details>
<summary>⚫ SDK v1 migration — Todo · parked · P3</summary>

> PARKED. Migrate to the v1 MCP Go SDK surface.

</details>

<details>
<summary>⚫ SSO (server edition) — Todo · parked · P3</summary>

> PARKED. Single sign-on for the multi-user server edition.

</details>

<details>
<summary>🟢 Upgrade awareness &amp; guided update — Done · P0</summary>

> Corrected CI-filtered telemetry (2026-07-02): ~60% of last-14d active installs run pre-v0.40; latest stable v0.46.0 only 18.7%. Turn the existing internal/updatecheck background poll into a universal, non-intrusive, channel-aware upgrade nudge across every surface. Never blocks/modals; silent offline/CI. 2026-08-29 truth-sync: this epic was marked done, but FR-002's release/age delta was never built — the four shipped tasks are US-slices and the FR belonged to none of them. Spec 079 has only spec.md (no plan.md/tasks.md), so no checkbox surface could catch it; the deferral survived only as TODO(spec-079/FR-002) code comments, which is how an outside contributor found it (#1081). RESOLVED 2026-08-29 in #1085; all five tasks are now done and the epic is complete.

Spec: [079-upgrade-nudge](./specs/079-upgrade-nudge/)

```mermaid
graph LR
  upgrade_nudge_status_log["US1 slice: update availability in mcpproxy st…"]
  upgrade_nudge_surfacing["US1 remainder: dismissible Web UI banner + up…"]
  upgrade_nudge_channel["US2: channel-aware guided update command (bre…"]
  upgrade_nudge_quiet["US3: operator control + CI/offline quiet + no…"]
  upgrade_nudge_delta["FR-002 remainder: human-readable 'N releases…"]

  upgrade_nudge_status_log --> upgrade_nudge_surfacing
  upgrade_nudge_surfacing --> upgrade_nudge_channel
  upgrade_nudge_surfacing --> upgrade_nudge_quiet
  upgrade_nudge_channel --> upgrade_nudge_delta

  classDef done fill:#1f7a1f,stroke:#0d3d0d,color:#ffffff;
  class upgrade_nudge_status_log,upgrade_nudge_surfacing,upgrade_nudge_channel,upgrade_nudge_quiet,upgrade_nudge_delta done;
```

| Task | Status | Refs |
| --- | --- | --- |
| US1 slice: update availability in mcpproxy status + deduped startup log | 🟢 Done | #798 |
| US1 remainder: dismissible Web UI banner + update_check config block | 🟢 Done | #805 |
| US2: channel-aware guided update command (brew/dmg/deb/rpm/docker/go-install detection, build-time channel marker) | 🟢 Done | #818 |
| US3: operator control + CI/offline quiet + no prerelease downgrade nudges | 🟢 Done | #911 |
| FR-002 remainder: human-readable "N releases / M weeks behind" delta on status, doctor, startup log, Web UI and the tray | 🟢 Done | #1085 |

</details>

<details>
<summary>🟢 Connect step trust: preview, visible backup, one-click undo — Done · P0</summary>

> Legacy wizard telemetry APPEARED to show 72.4% of engaged users skipping the connect step - debunked 2026-07-06: an instrumentation artifact, genuine never-connected skip = 0% (the wizard stamped skipped on users who connected via ConnectModal/CLI/manual config); real cliff is one-and-done installs ~48% (day-1 return 31%, identity-deduped 2026-07-10), see specs/080. Completers retain ~50% at two weeks vs 6% for non-engaged (correlation with engagement, not causation by the connect step). Backups already exist (internal/connect/backup.go) but are invisible in the Web UI. Close the trust gap: preview the exact config diff, surface the backup, offer one-click undo, explain the macOS TCC prompt.

Spec: [078-connect-trust-preview](./specs/078-connect-trust-preview/)

```mermaid
graph LR
  connect_trust_preview["US1: preview API + wizard diff UI (exact entr…"]
  connect_trust_backup_visibility["US1: surface backup_path in Web UI + retentio…"]
  connect_trust_undo["US2: one-click undo/disconnect in wizard"]
  connect_trust_tcc_copy["US2: pre-emptive macOS TCC explanation in wiz…"]


  classDef done fill:#1f7a1f,stroke:#0d3d0d,color:#ffffff;
  class connect_trust_preview,connect_trust_backup_visibility,connect_trust_undo,connect_trust_tcc_copy done;
```

| Task | Status | Refs |
| --- | --- | --- |
| US1: preview API + wizard diff UI (exact entry, API-key masking) | 🟢 Done | #802 |
| US1: surface backup_path in Web UI + retention policy | 🟢 Done | #799 |
| US2: one-click undo/disconnect in wizard | 🟢 Done | #804 |
| US2: pre-emptive macOS TCC explanation in wizard | 🟢 Done | #910 |

</details>

<details>
<summary>🟢 Non-Docker sandbox isolation (Landlock) — Done · P1 · MCP-34</summary>

> Landlock LSM + setrlimit native sandbox for stdio upstreams; no userns (Ubuntu 24.04 safe). Originated from roadmap item #11 (no dedicated spec — 054 is the unrelated security-gateway spec). Code in internal/sandbox/; PRs #754/#759/#768/#781/#782.

```mermaid
graph LR
  sandbox_spike["Landlock sandbox spike (MCP-34.1)<br/>MCP-3232"]
  sandbox_mode_config["isolation.mode enum + resolver (MCP-34.2)<br/>MCP-3233"]
  sandbox_launcher["Native sandbox launcher Landlock+rlimits (MCP…<br/>MCP-3234"]
  sandbox_scanner_parity["Scanner-flow parity under sandbox (MCP-34.4)<br/>MCP-3235"]
  sandbox_snap_docker_it["snap-docker integration tests + CI (MCP-34.5)<br/>MCP-3236"]

  sandbox_spike --> sandbox_mode_config
  sandbox_mode_config --> sandbox_launcher
  sandbox_launcher --> sandbox_scanner_parity
  sandbox_scanner_parity --> sandbox_snap_docker_it

  classDef done fill:#1f7a1f,stroke:#0d3d0d,color:#ffffff;
  class sandbox_spike,sandbox_mode_config,sandbox_launcher,sandbox_scanner_parity,sandbox_snap_docker_it done;
```

| Task | Status | Refs |
| --- | --- | --- |
| Landlock sandbox spike (MCP-34.1) | 🟢 Done | `MCP-3232` #754 |
| isolation.mode enum + resolver (MCP-34.2) | 🟢 Done | `MCP-3233` #759 |
| Native sandbox launcher Landlock+rlimits (MCP-34.3) | 🟢 Done | `MCP-3234` #768 |
| Scanner-flow parity under sandbox (MCP-34.4) | 🟢 Done | `MCP-3235` #781 |
| snap-docker integration tests + CI (MCP-34.5) | 🟢 Done | `MCP-3236` #782 |

</details>

<details>
<summary>🟢 Spec 076 deterministic offline tool-scanner — Done · P1 · MCP-3574</summary>

> Deterministic offline signal pipeline replaces ~10%-recall scanner; scan-eval --gate (recall>=0.90 / FP<=5%) in CI.

Spec: [076-deterministic-tool-scanner](./specs/076-deterministic-tool-scanner/)

```mermaid
graph LR
  scanner_v2_foundation["detect-engine foundation (T1)<br/>MCP-3575"]
  scanner_v2_hard_checks["3 hard checks + scanner wiring (US1 MVP)<br/>MCP-3576"]
  scanner_v2_soft_checks["3 soft checks + patterns confidence (US2)<br/>MCP-3577"]
  scanner_v2_consensus["Consensus risk-score + report transparency (U…<br/>MCP-3578"]
  scanner_v2_eval_gate["Eval corpus + CI recall/FP gate (US3)<br/>MCP-3579"]
  scanner_v2_docs["Tool-scanner detect-engine docs (T22)<br/>MCP-3683"]

  scanner_v2_foundation --> scanner_v2_hard_checks
  scanner_v2_foundation --> scanner_v2_soft_checks
  scanner_v2_hard_checks --> scanner_v2_consensus
  scanner_v2_soft_checks --> scanner_v2_consensus
  scanner_v2_hard_checks --> scanner_v2_eval_gate
  scanner_v2_eval_gate --> scanner_v2_docs

  classDef done fill:#1f7a1f,stroke:#0d3d0d,color:#ffffff;
  class scanner_v2_foundation,scanner_v2_hard_checks,scanner_v2_soft_checks,scanner_v2_consensus,scanner_v2_eval_gate,scanner_v2_docs done;
```

| Task | Status | Refs |
| --- | --- | --- |
| detect-engine foundation (T1) | 🟢 Done | `MCP-3575` #769 |
| 3 hard checks + scanner wiring (US1 MVP) | 🟢 Done | `MCP-3576` #770 |
| 3 soft checks + patterns confidence (US2) | 🟢 Done | `MCP-3577` #775 |
| Consensus risk-score + report transparency (US4) | 🟢 Done | `MCP-3578` #776 |
| Eval corpus + CI recall/FP gate (US3) | 🟢 Done | `MCP-3579` #777 |
| Tool-scanner detect-engine docs (T22) | 🟢 Done | `MCP-3683` #780 |

</details>

<details>
<summary>🟢 Analytics dashboard as default page — Done · P1</summary>

> Per-server / per-tool token-drain graphs; make the dashboard the default landing page. 2026-07-10 truth-sync: spec 069 is SHIPPED (25/26 — the only open task is a Playwright verification sweep), so the graphs half is done. 2026-08-31 audit: the default-landing half shipped too - frontend/src/router/index.ts routes path '/' to the Dashboard component, guarded by frontend/tests/unit/dashboard-default-landing.spec.ts. Spec 069's one remaining task (T023) is a local Playwright verification sweep that leaves no committed artifact: a human can run it and tick the box, but no code evidence can ever confirm it, so it cannot gate the epic. The epic is complete.

Spec: [069-observability-usage-graphs](./specs/069-observability-usage-graphs/)

```mermaid
graph LR
  analytics_token_drain_graphs["Per-server / per-tool token-drain graphs"]
  analytics_default_landing["Make dashboard the default landing page"]

  analytics_token_drain_graphs --> analytics_default_landing

  classDef done fill:#1f7a1f,stroke:#0d3d0d,color:#ffffff;
  class analytics_token_drain_graphs,analytics_default_landing done;
```

| Task | Status | Refs |
| --- | --- | --- |
| Per-server / per-tool token-drain graphs | 🟢 Done | — |
| Make dashboard the default landing page | 🟢 Done | #1044 |

</details>

<details>
<summary>🟢 Registries — easier search + add-server — Done · P1</summary>

> Lower the friction of finding a server in a registry and adding it; lean on the official registry protocol work. 2026-07-10 truth-sync: both children shipped — spec 070 is 21/24 (the 3 open tasks are pre-PR chores: worktree baseline, run gates, apply gate decisions) and 071 is 12/12. depends_on [ux-audit] dropped: a done epic cannot depend on a todo one.

Spec: [070-registry-easy-upstream-add](./specs/070-registry-easy-upstream-add/)

```mermaid
graph LR
  registries_search_ux["Improved registry search UX"]
  registries_official_protocol["Official registry protocol integration"]


  classDef done fill:#1f7a1f,stroke:#0d3d0d,color:#ffffff;
  class registries_search_ux,registries_official_protocol done;
```

| Task | Status | Refs |
| --- | --- | --- |
| Improved registry search UX | 🟢 Done | — |
| Official registry protocol integration | 🟢 Done | — |

</details>

<details>
<summary>🟢 Scanner simplification (deterministic default, opt-in deep scan) — Done · P1</summary>

> Make the Spec 076 detect engine the always-on offline default; demote Docker scanners + source extraction to opt-in deep scan that never blocks/degrades the baseline; single unified report. COMPLETE: US1 #786, US2 #792, US4 #794, US3 + deep-scan trust fixes + docs truth sweep (T037-T039) #793 — all merged; shipped in v0.47.0-rc.2. Remaining 4 unchecked tasks in tasks.md are documented scope-outs. First of the 5 personal-edition polish verticals.

Spec: [077-scanner-simplification](./specs/077-scanner-simplification/)

```mermaid
graph LR
  scanner_simpl_baseline["US1: deterministic offline baseline default +…"]
  scanner_simpl_unified_report["US2: single merged report + cross-scanner con…"]
  scanner_simpl_deep_optin["US3: opt-in deep scan (off by default), never…"]
  scanner_simpl_notifications["US4: collapse scan-notification storm into on…"]
  scanner_simpl_deepscan_fixes["Deep-scan trust fixes: nil-Security gating bu…"]

  scanner_simpl_baseline --> scanner_simpl_unified_report
  scanner_simpl_baseline --> scanner_simpl_deep_optin
  scanner_simpl_unified_report --> scanner_simpl_deep_optin
  scanner_simpl_unified_report --> scanner_simpl_notifications
  scanner_simpl_deep_optin --> scanner_simpl_deepscan_fixes

  classDef done fill:#1f7a1f,stroke:#0d3d0d,color:#ffffff;
  class scanner_simpl_baseline,scanner_simpl_unified_report,scanner_simpl_deep_optin,scanner_simpl_notifications,scanner_simpl_deepscan_fixes done;
```

| Task | Status | Refs |
| --- | --- | --- |
| US1: deterministic offline baseline default + curated hard phrase_injection check (delete duplicate legacy rules) | 🟢 Done | #786 |
| US2: single merged report + cross-scanner consensus confidence | 🟢 Done | #792 |
| US3: opt-in deep scan (off by default), never blocks/degrades baseline; config migration | 🟢 Done | #793 |
| US4: collapse scan-notification storm into one debounced settled event (MCP-2207) | 🟢 Done | #794 |
| Deep-scan trust fixes: nil-Security gating bug (source fetch runs with deep scan off on default configs), FR-014 verdict inversion (Dangerous deep finding < Warning), surface silently-skipped Docker scanners (non-nil deep_scan descriptor + CLI hint on security enable) | 🟢 Done | #793 |

</details>

<details>
<summary>🟢 Deferred-schema serialization for the direct tools/list surface (spec 102) — Done · P1</summary>

> Direct mode enumerates every upstream tool but always ships full inputSchema (~30K tokens for a 100-tool fleet; Spec 083 profiling put ~77% of the payload in schemas agents rarely read). Deferred serialization keeps every tool name, description and annotation and appends the Spec 085 compact signature instead of the schema, with describe_tool on the direct surface to recover it and the shipped pre-dispatch validation turning a wrong guess into one self-healing retry. Not a new routing_mode — a serialization mode of the direct surface, on the same tool_response_mode axis that already governs retrieve_tools. Spec merged in #1035 (issue #971, maintainer-accepted direction). COMPLETE: all 89 tasks shipped in #1063; the settings UI for both serialization axes followed in #1082; #1083/#1084 fixed in #1086. MEASURED SAVINGS FELL WELL SHORT OF THE ~88% ORIGINALLY PROJECTED: 29.7% on the frozen 45-tool reference corpus and 34.8% on a 527-tool snapshot, with 38.9% the arithmetic ceiling even if both the schema and the signature were deleted. The projection assumed schemas dominate the payload; names, descriptions and annotations turn out to carry most of it. SC-001 was RESTATED per corpus shape (maintainer decision 2026-08-29): now >=25% on the 45-tool corpus and >=30% at fleet scale, both asserted in internal/server/mcp_routing_deferred_tokens_test.go, with the original 70% kept as an upper tripwire. Unblocks token-bench — and that measured shortfall is the first thing token-bench has to explain.

Spec: [102-schema-deferred](./specs/102-schema-deferred/) · PR: #1063

</details>

<details>
<summary>🟢 Tray↔core decoupling: socket/REST API only, no config-file reads — Done · P2</summary>

> Architecture rule (CLAUDE.md): the tray holds no state and talks to the core only via socket/REST + SSE. 2026-07-11 source-of-truth re-audit + fix: Swift tray was already clean (MCPProxyApp.swift opens the config in an external editor, never parses it); the Go tray's update-check gate was already reworked to core-API gating (#805). The last violation — config.LoadFromFile in the Go tray's OAuth login path, live since ff03db92 (2026-05-18, #477) — turned out to be FUNCTIONALLY DEAD: the loaded config fed only two debug log lines, while the actual trigger was already the core-API TriggerOAuthLogin. Deleted rather than ported to REST. Bootstrap reads (socket path, config PATH without parsing, CA cert) are allowed and remain. Now enforced by a test so the rule cannot silently rot.

```mermaid
graph LR
  tray_oauth_config_read["Delete the dead config read in the Go tray OA…"]
  tray_config_import_guard["Test guard (internal/tray/config_import_guard…"]

  tray_oauth_config_read --> tray_config_import_guard

  classDef done fill:#1f7a1f,stroke:#0d3d0d,color:#ffffff;
  class tray_oauth_config_read,tray_config_import_guard done;
```

| Task | Status | Refs |
| --- | --- | --- |
| Delete the dead config read in the Go tray OAuth path (config.LoadFromFile) + drop the now-unused internal/config import. GetConfigPath stays on the interface — openConfigDir still needs the path to reveal the dir in the file manager. | 🟢 Done | — |
| Test guard (internal/tray/config_import_guard_test.go) failing any tray-side call to config.LoadFromFile/Load/SaveConfig/... Parses source on disk, so a violation cannot hide behind an inactive build tag. Bans config FILE I/O, not the config package — cmd/mcpproxy-tray legitimately references the config.LogConfig type for its own logger. | 🟢 Done | — |

</details>

## Epics

| Epic | Status | Priority | Progress | Spec | PR |
| --- | --- | --- | --- | --- | --- |
| Web UI + macOS app UX audit | In progress | P0 | — |  |  |
| Release qualification gate (auto-QA matrix blocks the tag) | In progress | P0 | — | [081-release-qa-gate](./specs/081-release-qa-gate/) |  |
| Action log / transparency — info at a glance | In progress | P1 | — |  |  |
| Token-efficiency benchmark: measured savings, published results | In progress | P1 | 62/64 (97%) | [103-token-bench](./specs/103-token-bench/) | #1141 #1147 #1151 #1153 #1160 |
| Telemetry identity & data quality (machine_id + CI-filter hardening) | In progress | P1 | — |  |  |
| Telemetry v7: honest funnel + churn instrumentation | In progress | P1 | — | [080-telemetry-v7-churn](./specs/080-telemetry-v7-churn/) |  |
| Planning/docs truth automation | In progress | P2 | — |  |  |
| Discovery-quality eval harness (Spec 065 second half) | In progress | P3 | — | [065-evaluation-foundation](./specs/065-evaluation-foundation/) |  |
| tpa-db: versioned TPA signature database for the offline scanner | Todo | P1 | — | [101-tpa-db](./specs/101-tpa-db/) |  |
| MCP protocol upgrade to 2026-07-28 revision | Todo | P1 | — | [058-mcp-2026-upgrade](./specs/058-mcp-2026-upgrade/) |  |
| Windows native tray app `MCP-43` | Todo | P2 | — |  |  |
| Remote access tunnel (feature-flagged MVP, spec 089) | Todo | P2 | — | [089-remote-access-tunnel](./specs/089-remote-access-tunnel/) |  |
| Tool co-occurrence graph (experimental, feature-flagged) | Todo | P2 | — |  |  |
| Security gateway Tracks C/D (per-arg least-privilege + signature provenance) | Todo | P3 | — | [054-mcp-security-gateway](./specs/054-mcp-security-gateway/) |  |
| Server marketplace `MCP-37` | Todo (parked) | P3 | — |  |  |
| Audit SIEM integration `MCP-39` | Todo (parked) | P3 | — |  |  |
| Paid-tier MVP (billing / seats / license) `MCP-40` | Todo (parked) | P3 | — |  |  |
| SDK v1 migration | Todo (parked) | P3 | — |  |  |
| SSO (server edition) | Todo (parked) | P3 | — |  |  |
| Upgrade awareness & guided update | Done | P0 | — | [079-upgrade-nudge](./specs/079-upgrade-nudge/) |  |
| Connect step trust: preview, visible backup, one-click undo | Done | P0 | — | [078-connect-trust-preview](./specs/078-connect-trust-preview/) |  |
| Non-Docker sandbox isolation (Landlock) `MCP-34` | Done | P1 | — |  |  |
| Spec 076 deterministic offline tool-scanner `MCP-3574` | Done | P1 | 22/24 (92%) | [076-deterministic-tool-scanner](./specs/076-deterministic-tool-scanner/) |  |
| Analytics dashboard as default page | Done | P1 | 25/26 (96%) | [069-observability-usage-graphs](./specs/069-observability-usage-graphs/) |  |
| Registries — easier search + add-server | Done | P1 | 21/24 (88%) | [070-registry-easy-upstream-add](./specs/070-registry-easy-upstream-add/) |  |
| Scanner simplification (deterministic default, opt-in deep scan) | Done | P1 | 38/42 (90%) | [077-scanner-simplification](./specs/077-scanner-simplification/) |  |
| Deferred-schema serialization for the direct tools/list surface (spec 102) | Done | P1 | 89/89 (100%) | [102-schema-deferred](./specs/102-schema-deferred/) | #1063 |
| Tray↔core decoupling: socket/REST API only, no config-file reads | Done | P2 | — |  |  |

## Shipped (archived)

Swept out of the working set by `--archive` once done, merged and cooled off. Full entries — notes, child tasks, PR refs — live in [`roadmap.archive.yaml`](./roadmap.archive.yaml).

| Epic | Shipped | Archived | PRs |
| --- | --- | --- | --- |
| Profiles v2 (per-profile tool views) `MCP-33` | 2026-06-24 | 2026-07-10 | #756 #761 #766 #767 |
| TypeScript code-execution GA + cookbook `MCP-38` | 2026-06-24 | 2026-07-10 | #753 |

## Per-spec progress (recomputed from `specs/<NNN>/tasks.md`)

Legend: `shipped` ≥95% checked · `in-flight` 1–94% · `drafted` 0% · `—` no `tasks.md`. This aggregate is regenerated here rather than overwriting the hand-maintained [`specs/README.md`](./specs/README.md), which keeps its curated prose, runbooks and design-doc links.

| # | Status | Progress |
| --- | --- | --- |
| [001-code-execution](./specs/001-code-execution/) | `in-flight` | 74/127 (58%) |
| [001-fix-skipped-auth-tests](./specs/001-fix-skipped-auth-tests/) | — | — |
| [001-oas-endpoint-documentation](./specs/001-oas-endpoint-documentation/) | `in-flight` | 36/69 (52%) |
| [001-oauth-scope-discovery](./specs/001-oauth-scope-discovery/) | — | — |
| [001-update-version-display](./specs/001-update-version-display/) | `in-flight` | 39/58 (67%) |
| [002-windows-installer](./specs/002-windows-installer/) | `in-flight` | 35/60 (58%) |
| [003-tool-annotations-webui](./specs/003-tool-annotations-webui/) | `in-flight` | 37/64 (58%) |
| [004-management-health-refactor](./specs/004-management-health-refactor/) | `in-flight` | 73/101 (72%) |
| [005-rest-management-integration](./specs/005-rest-management-integration/) | `shipped` | 45/45 (100%) |
| [006-oauth-extra-params](./specs/006-oauth-extra-params/) | `in-flight` | 43/65 (66%) |
| [007-oauth-e2e-testing](./specs/007-oauth-e2e-testing/) | `in-flight` | 93/103 (90%) |
| [008-oauth-token-refresh](./specs/008-oauth-token-refresh/) | `in-flight` | 57/64 (89%) |
| [009-proactive-oauth-refresh](./specs/009-proactive-oauth-refresh/) | `in-flight` | 47/87 (54%) |
| [010-release-notes-generator](./specs/010-release-notes-generator/) | `in-flight` | 24/36 (67%) |
| [011-resource-auto-detect](./specs/011-resource-auto-detect/) | `shipped` | 38/39 (97%) |
| [012-docusaurus-docs-site](./specs/012-docusaurus-docs-site/) | `in-flight` | 74/89 (83%) |
| [012-unified-health-status](./specs/012-unified-health-status/) | `shipped` | 44/44 (100%) |
| [013-structured-server-state](./specs/013-structured-server-state/) | `shipped` | 46/46 (100%) |
| [013-tool-change-notifications](./specs/013-tool-change-notifications/) | `in-flight` | 26/45 (58%) |
| [014-cli-output-formatting](./specs/014-cli-output-formatting/) | `in-flight` | 62/66 (94%) |
| [015-server-management-cli](./specs/015-server-management-cli/) | `shipped` | 50/50 (100%) |
| [016-activity-log-backend](./specs/016-activity-log-backend/) | `in-flight` | 44/50 (88%) |
| [017-activity-cli-commands](./specs/017-activity-cli-commands/) | `in-flight` | 50/60 (83%) |
| [018-intent-declaration](./specs/018-intent-declaration/) | `shipped` | 69/69 (100%) |
| [019-activity-webui](./specs/019-activity-webui/) | `shipped` | 72/73 (99%) |
| [020-oauth-login-feedback](./specs/020-oauth-login-feedback/) | — | — |
| [021-request-id-logging](./specs/021-request-id-logging/) | `in-flight` | 35/42 (83%) |
| [022-oauth-redirect-uri-persistence](./specs/022-oauth-redirect-uri-persistence/) | `in-flight` | 23/25 (92%) |
| [023-oauth-state-persistence](./specs/023-oauth-state-persistence/) | `shipped` | 38/39 (97%) |
| [023-smart-config-patch](./specs/023-smart-config-patch/) | `shipped` | 52/53 (98%) |
| [024-expand-activity-log](./specs/024-expand-activity-log/) | `shipped` | 63/66 (95%) |
| [026-pii-detection](./specs/026-pii-detection/) | `shipped` | 127/130 (98%) |
| [027-status-command](./specs/027-status-command/) | `shipped` | 25/25 (100%) |
| [028-agent-tokens](./specs/028-agent-tokens/) | `in-flight` | 36/43 (84%) |
| [029-mcpproxy-teams](./specs/029-mcpproxy-teams/) | `shipped` | 28/29 (97%) |
| [033-typescript-code-execution](./specs/033-typescript-code-execution/) | `shipped` | 19/19 (100%) |
| [034-expand-secret-refs](./specs/034-expand-secret-refs/) | `shipped` | 17/17 (100%) |
| [035-enhanced-annotations](./specs/035-enhanced-annotations/) | — | — |
| [037-macos-swift-tray](./specs/037-macos-swift-tray/) | — | — |
| [038-mcp-accessibility-server](./specs/038-mcp-accessibility-server/) | — | — |
| [039-connect-and-dashboard](./specs/039-connect-and-dashboard/) | — | — |
| [039-scanner-qa-audit](./specs/039-scanner-qa-audit/) | — | — |
| [039-security-scanner-plugins](./specs/039-security-scanner-plugins/) | — | — |
| [040-server-ux](./specs/040-server-ux/) | `in-flight` | 28/35 (80%) |
| [041-quarantine-invariants](./specs/041-quarantine-invariants/) | — | — |
| [042-telemetry-tier2](./specs/042-telemetry-tier2/) | `in-flight` | 65/91 (71%) |
| [043-linux-package-repos](./specs/043-linux-package-repos/) | `shipped` | 41/41 (100%) |
| [044-diagnostics-taxonomy](./specs/044-diagnostics-taxonomy/) | `in-flight` | 59/106 (56%) |
| [044-retention-telemetry-v3](./specs/044-retention-telemetry-v3/) | `in-flight` | 55/70 (79%) |
| [045-paperclip-cockpit](./specs/045-paperclip-cockpit/) | `in-flight` | 40/47 (85%) |
| [046-local-first-onboarding](./specs/046-local-first-onboarding/) | — | — |
| [046-local-launcher-for-http-sse](./specs/046-local-launcher-for-http-sse/) | — | — |
| [047-cpu-hotpath-fix](./specs/047-cpu-hotpath-fix/) | `in-flight` | 26/46 (57%) |
| [048-tray-refetch-elimination](./specs/048-tray-refetch-elimination/) | `in-flight` | 18/31 (58%) |
| [049-agent-discoverable-disabled-tools](./specs/049-agent-discoverable-disabled-tools/) | `shipped` | 18/18 (100%) |
| [050-global-tools-page](./specs/050-global-tools-page/) | `in-flight` | 24/26 (92%) |
| [051-readme-hero-demo](./specs/051-readme-hero-demo/) | — | — |
| [053-oss-repo-improvements](./specs/053-oss-repo-improvements/) | — | — |
| [054-mcp-security-gateway](./specs/054-mcp-security-gateway/) | — | — |
| [055-docs-diataxis](./specs/055-docs-diataxis/) | — | — |
| [055-frontend-major-upgrades](./specs/055-frontend-major-upgrades/) | `shipped` | 23/24 (96%) |
| [056-output-schema-validation](./specs/056-output-schema-validation/) | `in-flight` | 22/24 (92%) |
| [057-in-proxy-profiles](./specs/057-in-proxy-profiles/) | `in-flight` | 20/25 (80%) |
| [058-mcp-2026-upgrade](./specs/058-mcp-2026-upgrade/) | — | — |
| [059-output-sanitisation](./specs/059-output-sanitisation/) | `shipped` | 24/25 (96%) |
| [060-settings-page](./specs/060-settings-page/) | `shipped` | 16/16 (100%) |
| [064-glass-cockpit](./specs/064-glass-cockpit/) | — | — |
| [065-evaluation-foundation](./specs/065-evaluation-foundation/) | — | — |
| [069-observability-usage-graphs](./specs/069-observability-usage-graphs/) | `shipped` | 25/26 (96%) |
| [070-registry-easy-upstream-add](./specs/070-registry-easy-upstream-add/) | `in-flight` | 21/24 (88%) |
| [071-official-registry-protocol](./specs/071-official-registry-protocol/) | `shipped` | 12/12 (100%) |
| [073-activity-size-retention](./specs/073-activity-size-retention/) | `in-flight` | 13/14 (93%) |
| [074-discovery-intervals](./specs/074-discovery-intervals/) | `in-flight` | 17/19 (89%) |
| [075-macos-tcc-connect](./specs/075-macos-tcc-connect/) | `shipped` | 30/30 (100%) |
| [076-deterministic-tool-scanner](./specs/076-deterministic-tool-scanner/) | `in-flight` | 22/24 (92%) |
| [077-scanner-simplification](./specs/077-scanner-simplification/) | `in-flight` | 38/42 (90%) |
| [078-connect-trust-preview](./specs/078-connect-trust-preview/) | — | — |
| [079-upgrade-nudge](./specs/079-upgrade-nudge/) | — | — |
| [080-telemetry-v7-churn](./specs/080-telemetry-v7-churn/) | — | — |
| [081-release-qa-gate](./specs/081-release-qa-gate/) | — | — |
| [082-work-sessions](./specs/082-work-sessions/) | — | — |
| [083-discovery-profiler](./specs/083-discovery-profiler/) | `in-flight` | 4/41 (10%) |
| [084-toon-output](./specs/084-toon-output/) | `in-flight` | 40/43 (93%) |
| [085-compact-router](./specs/085-compact-router/) | `shipped` | 44/46 (96%) |
| [086-tpa-scanner-approval](./specs/086-tpa-scanner-approval/) | — | — |
| [087-tpa-daily-refresh](./specs/087-tpa-daily-refresh/) | — | — |
| [088-scanner-trust-ui](./specs/088-scanner-trust-ui/) | `shipped` | 29/29 (100%) |
| [089-remote-access-tunnel](./specs/089-remote-access-tunnel/) | — | — |
| [090-tray-glance-v2](./specs/090-tray-glance-v2/) | `shipped` | 35/35 (100%) |
| [091-connect-client-form](./specs/091-connect-client-form/) | `shipped` | 26/26 (100%) |
| [092-auto-updater](./specs/092-auto-updater/) | — | — |
| [093-concurrency-limits](./specs/093-concurrency-limits/) | — | — |
| [094-filter-diagnostics](./specs/094-filter-diagnostics/) | `shipped` | 14/14 (100%) |
| [095-update-failure-ux](./specs/095-update-failure-ux/) | `shipped` | 28/28 (100%) |
| [096-batched-call-tools](./specs/096-batched-call-tools/) | `in-flight` | 15/16 (94%) |
| [097-stored-scripts](./specs/097-stored-scripts/) | `in-flight` | 13/14 (93%) |
| [098-tools-preflight](./specs/098-tools-preflight/) | `in-flight` | 28/33 (85%) |
| [099-describe-check-mode](./specs/099-describe-check-mode/) | `in-flight` | 9/10 (90%) |
| [100-prompt-rugpull-baseline](./specs/100-prompt-rugpull-baseline/) | — | — |
| [101-tpa-db](./specs/101-tpa-db/) | — | — |
| [102-schema-deferred](./specs/102-schema-deferred/) | `shipped` | 89/89 (100%) |
| [103-token-bench](./specs/103-token-bench/) | `shipped` | 62/64 (97%) |
