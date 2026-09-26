# Specification Quality Checklist: Navigation, Scope Filters and Cross-Surface UX Consistency

**Purpose**: Validate specification completeness, traceability and cross-surface parity before implementation
**Created**: 2026-09-25
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] CHK001 User stories and success criteria describe user outcomes; packages and types appear only in plan, data-model and contracts (FRs name endpoints and components where the surface contract requires it)
- [x] CHK002 Focused on user value: one "what needs me", informed review, a home for clients, one next step per server, catalog-first add, consistent navigation, a first run that works
- [x] CHK003 The Definitions section binds every term used across surfaces
- [x] CHK004 All mandatory sections are complete (stories, edge cases, FRs, key entities, success criteria, commit conventions)

## Requirement Completeness

- [x] CHK005 No [NEEDS CLARIFICATION] markers; every open choice is decided in research.md D1–D26
- [x] CHK006 Requirements are testable: label tables, rank tables, route/redirect tables and payload shapes are exact
- [x] CHK007 Success criteria are measurable (SC-001 to SC-012)
- [x] CHK008 Edge cases are covered: dual-state servers, legacy approval records, untrusted descriptions, flapping, unknown clients, source timeouts, multi-server paste, missing keyring, old bookmarks, server edition, scoped callers, Spec 108 absent
- [x] CHK009 Scope is bounded: S1, O1, S3 and P1–P7 excluded, with the owner named; server-edition navigation and the annotation re-review trigger are out of scope
- [x] CHK010 Backward compatibility is specified: redirects keep their query, `level` and approval wire values are unchanged, `action` is unchanged except the single FR-010 case (quarantined + sign-in → `login`), CLI flags are aliased, the `activity list` default is unchanged, `/unquarantine` is kept
- [x] CHK011 Dependencies on Spec 108 are explicit per artifact (Coordination table, research D1) and limited to PR 109-l

## Cross-surface parity

- [x] CHK012 Every capability has a parity-matrix row with Web UI, macOS, CLI, MCP and REST columns
- [x] CHK013 Every "—" cell carries a reason
- [x] CHK014 One terminology table binds REST, CLI, Web and macOS names, and lists the retired names
- [x] CHK015 Every inventory contradiction (W1–W5, M1–M5, C1–C6, R1–R3) plus the new ones (X1–X12) has a disposition: Resolved (FR), 108, or Accepted (reason)
- [x] CHK016 Shared enums and golden fixtures are decoded by Go, vitest and Swift (FR-090, T005, T144)

## Finding traceability

| Finding | FRs | PR | Tests |
|---|---|---|---|
| O2 import rows by name only | FR-040 | 109-b | T030, T031 |
| O3 absolute paths | FR-037 | 109-b | T028, T031–T033 |
| O4 done ≠ working | FR-041, FR-042 (+ inline review FR-023) | 109-b, 109-g | T029, T031, T089 |
| O5 import footer | FR-043 | 109-b | T031 |
| O6 telemetry banner | FR-044 | 109-b | T031 |
| N1 no Clients nav | FR-030, FR-031, FR-050 | 109-h, 109-i | T124–T128, T135 |
| N2 duplicate connect/import UIs | FR-032, FR-033 | 109-h | T127 |
| N3 catalog under System | FR-062, FR-066 | 109-j | T100, T102 |
| N4 tokens under Secrets | FR-034 | 109-h | T127, T128 |
| N5 Sessions page | FR-070, FR-030 (sessions column) | 109-k, 109-h | T112, T127 |
| N6 no review queue | FR-021–024 | 109-f, 109-g | T074–T078, T086–T090 |
| N7 naming, edition tab, emoji | FR-056, FR-050 | 109-a, 109-i | T008, T135 |
| N8 empty tab is default | FR-051 | 109-a (interim), 109-d | T006, T057 |
| H1 crowded header | FR-052–054 | 109-i | T136, T137 |
| H2 profile switcher dead end | FR-057 (interim; removal = 108) | 109-a | T011 |
| H3 mode/endpoints in header | FR-031, FR-052 | 109-h, 109-i | T127, T136 |
| H4 version row over modals | FR-055 | 109-a | T007 |
| S2 blind approval | FR-020–024, FR-026 | 109-f, 109-g | T073–T078, T086–T087 |
| S4 card shows every control | FR-013, FR-014 | 109-e | T067–T069 |
| S5 contradictory health | FR-010–012, FR-015 | 109-c | T041–T045 |
| S6 tabs don't update URL | FR-016 | 109-a, 109-k, 109-h | T009, T112, T127 |
| S7 add form | FR-064, FR-065 | 109-j | T098, T100, T102, T103 |
| C1 ranking | FR-060, FR-061, FR-067 | 109-j | T097, T101 |
| C2 "Add to MCP" | FR-063 | 109-a, 109-j | T012, T102 |
| A1 bookkeeping buries calls | FR-070–072 | 109-k | T112 |
| A2 metric shows zero; ticks; wording | FR-073, FR-074 | 109-a, 109-k | T013, T114 |
| A3 nothing says what needs attention | FR-001–006 | 109-d (+ 109-h client feed) | T053–T059, T055a, T124 |
| T1 approval filter overlap | FR-027 | 109-a | T010, T017 |
| URL filter contract + link map | FR-080–083 | 109-k (+ 109-l) | T111–T116, T137, T151 |
| Needs-attention endpoint | FR-001–002, FR-006 | 109-d | T053–T055a |
| Caller classes on new read routes (non-admin user sessions, not just agent tokens) | FR-007 | 109-d, 109-f, 109-j, 109-h | T055b, T075a, T099, T125 |
| X12 Go tray one-click unquarantine | FR-022, SC-005 | 109-f | T078a, T084b, T147 |

## Feature Readiness

- [x] CHK017 Every FR has acceptance coverage in a story or a named test
- [x] CHK018 Each PR (109-a to 109-m) has its own test plan (tasks.md) and live-verification recipe (quickstart.md §3)
- [x] CHK019 Quick wins that do not depend on Spec 108 are in the earliest PRs (109-a, 109-b, 109-c)
- [x] CHK020 Security posture is preserved or improved: X2 fixed first in 109-f, untrusted text rendered inert, agents still cannot approve servers, secrets go to the keyring at add time, new routes classify callers with `auth.IsScopedCaller` (FR-007) and give scoped callers filtered reads or `403`, tested with agent tokens and server-edition user sessions

## Notes

- Validation pass 1 (2026-09-25): all items pass.
- Cross-model review round 1 (zcode / GLM-5.3, read-only plan mode, two chunks, 2026-09-25): verdict FINDINGS on both chunks. There were 25 findings (1 high, 8 medium); all were verified against the code and applied. The high one: 109-k wired `Review.vue` before 109-g created it, so the wiring moved to 109-g, which now depends on k. The medium ones:
  - ConnectModal ownership added to the Coordination table (108 T042/T098 retarget);
  - `has_usable_server` in 109-b no longer reads the `usable` field that 109-c adds;
  - the token-expiring `ready` state keys its button on `actions[0]`;
  - Clients tab/URL sync gained a task and a test;
  - the Tools `status` value is spelled `config-denied`, like the CLI;
  - `tools approve|reject` added as a third CLI alias family (C4/X3);
  - the Editions auth wording now matches the scoped-read rule.

  The low ones: refresh-retrying/failed health rows; the secret regex covers `*_KEY`; a macOS Usage parity row; inert rendering extended to catalog results; SC-005 deletes the dead `unquarantine` client methods (T084a); `/clients` embeds the `/routing` payload; `focus` defined once as a page-local highlight param; CLI `--view` omits `sessions` deliberately; the `CONFIG PATH` column name; the quickstart dead key and the rug-pull baseline; a Tools row "Calls" link task; an a→f dependency edge; the `unknown` tier and `{}` vs nil annotations; T129 wording; `review show --full`/`--yes` goldens.
- Cross-model review round 2 (zcode, 2026-09-25): all round-1 fixes confirmed present. Verdict FINDINGS: 7 medium findings, all verified and applied:
  - a stale 109-k plan row (Review and server-card links);
  - FR-010 now declares its one `action` value change (quarantined + sign-in → `login`) instead of claiming `action` is unchanged;
  - T084a deletes every dead `/unquarantine` caller, and the SC-005 grep excludes tests;
  - the 109-b/109-c `has_usable_server` sequencing rule;
  - the Home usage-strip links move to 109-d (plain links) and 109-i (`linkTo`);
  - `clientInfo.name` is treated as untrusted input (escaped, stripped, capped);
  - server-card stats use a new additive `per_server` array on `/activity/summary`, because the existing endpoint has no per-server error counts.
- Cross-model review round 3 (zcode, 2026-09-25): all round-2 fixes confirmed present. Verdict FINDINGS: 3 medium and 1 low findings, all applied:
  - CHK010 now states the FR-010 `action` exception;
  - the 108-derived attention kind is renamed `client_holds_admin_key`, which is Spec 108's actual warning code;
  - last-seen timestamps go in a new parallel `client_last_seen` map, so the `mcp_clients_seen_ever` `[]string` is not retyped;
  - the paste-command splitter reuses `internal/oauth/spawnargv.go`.

  Found while fixing, and also applied: the presence rows use Spec 108's `ClientView` field names (`display_name`, `kind`), so 108 extends them without renaming.
- Cross-model review round 4 (zcode, 2026-09-25): all round-3 fixes confirmed. Verdict FINDINGS: 2 medium findings, both applied: a leftover `name` in Key Entities is now `display_name`/`kind`; 109-f and 109-m join the quickstart frontend and macOS gate lists, and 109-f's Surfaces column adds Web.
- Cross-model review round 5 (zcode, 2026-09-25): round-4 fixes confirmed; HIGH-only scan. One finding, applied: quickstart recipe 109-d now expects six attention items (github is quarantined on add, so it yields both sign-in and review, which also exercises US1 scenario 3). Two polish items were applied as well: FR-001's SSE payload is `{count, ids}`, and the `ready` label row names the refresh-retrying "View logs" button.
- Cross-model review round 6 (zcode, 2026-09-25): round-5 fixes confirmed. A full HIGH-severity scan (security, PR self-testability, backward compatibility, contradictions, recipe passability) found nothing. **VERDICT: CLEAN.** 6 of the 10-round cap used; 41 findings verified and applied in total.
- Review round 1 after the CLEAN verdict (2026-09-25, 13 findings: 4 high, 6 medium, 3 low): each verified against the code and applied (research D22). One claim was corrected during verification: the Go tray's `/unquarantine` path is live, not dead (`internal/tray/managers.go` quarantine submenu), recorded as X12 and fixed in 109-f.
- Review round 2 after the CLEAN verdict (2026-09-25, 12 findings: 2 high, 5 medium, 5 low): each verified against the code; 12 applied, 0 rejected, two with a different fix than suggested (research D23).

## Codex (gpt-6-sol) review round 1 + cross-spec ownership rule (2026-09-25)

8 findings on Spec 109, each verified against `638fa805a` code; **8 applied, 0 rejected** (research D25):

- (high) FR-021 review payload now goes through the shared secret redaction (`oauth.RedactServerSecretFields`, the fourth door); rest-api#review, data-model §5, mcp-tools, T080, T077b, quickstart 109-f.
- (high) FR-022 `block[]` written in the baseline transaction **before** the unquarantine (verified: `ApproveServer` saved the baseline, then unquarantined as a separate step); plan Complexity Tracking, rest-api, D7, T080, T078b race test.
- (medium) quickstart §3 recipes run through `mp()` (scratch HOME, config and data dir) instead of the PATH binary.
- (medium) Attention threshold timer (≤ 30 s) so time-based items appear on a quiet instance; FR-002, D2, data-model §4, plan mechanism 2, T053, T060.
- (medium) T084b deletes `SynchronizationManager.HandleServerUnquarantine` and widens `App.apiClient` (verified `internal/tray/managers.go`, `internal/tray/tray.go`).
- (medium) FR-015 `--status` union semantics; T043 multi-value golden.
- (medium) FR-055/T019/T007 name `Repositories.vue`'s three `:open` dialogs.
- (low) FR-014 tray fallback: non-executable `actions[0]` open the screen that performs them; T069a, T071.
- (ownership) Spec 109 is the single owner of `useScopeQuery` and the URL contract with **all** parameters (`profile`/`client`/`token` registered hidden until Spec 108-e's `features.scope_filters`), the link map (incl. rows opening 108 pages), the macOS `ScopeFilter`, the Clients page shell, `GET /clients` row shape and `client list|show`, sidebar/header layout, Needs-attention. Spec 108's duplicated tasks were merged here (old 108-j `useScopeQuery`/link map → T111/T117/T119/T122; its Clients shell → T131/T132). 109-l re-scoped to attention kinds (Spec 108's warning names, ranks 4–9) and un-hiding tests. Cross-spec edges recorded in plan.md and tasks.md.

## Codex (gpt-6-sol) cross-spec review round 2 (2026-09-25)

5 findings on the 108/109 pair (1 high, 2 medium, 2 low), each verified against `638fa805a`; **5 applied, 0 rejected** (research D26, Spec 108 research D28).

- (high, Spec 108 side) `GET /connect*` reads administrator-only once they carry `credential_state` (108 FR-025a); noted in the Coordination row, contracts/rest-api.md, data-model.
- (medium) Sessions REST mapping: url-filter-contract macOS clause and T116 now send `profile`/`client`/`token` to `GET /sessions`; US6-9.
- (medium) `--from/--to`: 109-k T121 sole owner; removed from Spec 108 T065.
- (low) FR-080a `400 unsupported_scope_filter` gate (T116b, T121a, T129), contract "Backend gate", rest-api, quickstart 109-k.
- (low) Shared-file ordering: edge 108-k ← 109-i (T141, spec, plan, tasks); later-merge-keeps-both rules for `ClientStatus` (T034) and the connect component (T131).
- Codex (gpt-6-sol) review round 3 (2026-09-25), Specs 108 + 109: 13 findings on Spec 109 (4 high, 7 medium, 2 low), each verified against `638fa805a`; **13 applied, 0 rejected**. Details: research D27.
  - (high) FR-030a: 109-h gates `GET /connect`, `/connect/{client}`, `/connect/{client}/preview` (shared with Spec 108-c FR-025a, first to merge adds it). spec, plan, rest-api, T125a, T129, Dependencies; Spec 108 FR-025a/T030b/T038.
  - (high) Presence `connected` from connect-write/seen evidence, `connection_unverified`, content read only on `GET /clients/{id}`. FR-030, data-model §6, rest-api, T124, T129.
  - (high) FR-022 block = `BlockTools` representation (`approved` + `disabled`) via `scanner.Storage.SaveIntegrityBaselineWithBlocks`. rest-api, plan, T078b, T080.
  - (high) Tools/Servers `status` client-side; Usage `from`/`to` → `window` presets, disabled chip otherwise. url-filter contract (table + rule 5), T111, T113.
  - (medium) Add server at `/add-server` (no `/servers/:serverName` collision). navigation-map, FR-062, US, plan, tasks, research.
  - (medium) FR-082 matches the Coordination table (links registered hidden in 109-k).
  - (medium) T016's `?risk=` URL assertion moved to T111.
  - (medium) T011a: E2E script stops only its own PIDs; quickstart precheck until then (Spec 108 quickstart too).
  - (medium) Review redaction unconditional (ignores `reveal_secret_headers`); no `GET /servers/{id}` read route. FR-021, rest-api, T077b.
  - (medium) `inspect_quarantined` keeps live inspection (`definitions_source`). mcp-tools, T076a.
  - (medium) `CatalogResult` DTO + `toCatalogResult`; `ServerEntry` JSON unchanged. data-model §9, rest-api, plan, T101a.
  - (low) T148a SC-001 traceability check; T148b FR-091 parity-matrix walk. SC-001, FR-091, plan 109-m.
  - (low) Review example `annotations: null` → `tier: unknown`; `{}` → `unannotated` stated.
- Codex (gpt-6-sol) cross-spec review round 4 (2026-09-25), Specs 108 + 109: 17 findings on Spec 109 (3 high, 14 medium) plus Spec 108's AnnotationTier edge mirrored here, each verified against `638fa805a`; **all applied, 0 rejected**. Details: research D28.
  - (high) Per-kind keyring names `<server>-env-<name>` / `<server>-header-<name>`, `-2`… for a taken name, never overwrite; shared `ref_names.json`. FR-065, US5-5, rest-api, cli, T100, T102, T105, T109.
  - (high) URL `tool=server:tool` → REST `server` + bare `tool`. url-filter contract, T111, T113, T115, T116.
  - (high) Admin-key header on every recipe `curl`. quickstart §3.
  - (medium) `server` client-side on Tools/Review; `agent` gated except on `/activity` + `/export`; list + gate function created together by whichever of 109-k / 108-e merges first. FR-080a, url-filter contract, rest-api, T116b, T121a.
  - (medium) `secret_like` = registry flag OR name rule. data-model §9, T099, T105.
  - (medium) 109-f recipe: `GET /servers` list comparison, `reveal_secret_headers` default; `FIXTURE_CALL_LOG` (T078c) makes the race pass condition falsifiable.
  - (medium) `navigation-consistency.spec.ts` added to `run-web-smoke.sh` by T068 (109-e).
  - (medium) Palette tools source only for non-empty input. D16, FR-054, T136.
  - (medium) 109-l ← 108-k; the "before Spec 108" half runs on the post-109-k build. spec Coordination, plan, tasks, quickstart.
  - (medium) `client_disconnected_at`: seen-before-disconnect is not connection evidence. FR-030, data-model §6–7, T124, T129.
  - (medium) Sessions-view link uses `work_session_id`; `session` routed by the `ws-` prefix. url-filter contract, T111.
  - (medium) Per-client 24 h counter in the usage aggregate for `calls_24h`. FR-030, data-model §6, T124, T125, T129.
  - (medium) 109-d removes the Home one-click `approveTools` itself, so T058a passes in 109-d. T058a, T064, T084.
  - (medium) T124 split (T124a in `internal/server`, HTTP half in T125); T078b split by package. No import cycles.
  - (cross-spec) 108-a ← 109-a; `Tier` constants exported (T014, T023); `connect_cmd.go` / `ConnectClientView.swift` shared-file rule (T038); FR-075 `--from/--to` on `list|watch|summary|export` with defined `watch`/`summary` behaviour.
