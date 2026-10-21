# Tasks: Spec 101 tpa-db — User Story 1 (signed format + downgrade-proof loader)

**Input**: `/specs/101-tpa-db/` (spec.md, plan.md, research.md, data-model.md, contracts/, quickstart.md)
**Scope of this file**: User Story 1 (merged, #1701) and User Story 3 (the section "User Story 3 — refresh lifecycle (Spec 087) + publication channel + adoption loop" at the end, tasks T300+), each one PR against `main`. US2 (seed corpus, `signatures[]`, `cmd/scan-eval --bundle`, contributor docs; sibling PR #1702) is **out of scope** for this file and listed in "Out of scope for this PR" so nothing is silently dropped.

**TDD rule (constitution V)**: every `test` task is written first and must FAIL for the stated behavioural reason on a build that compiles (add a stub where a new symbol is needed — a missing symbol is a build failure, not a bite). Prove bite by reverting the code hunk, never by deleting symbols.

**Format**: `[ID] [P?] Description`. `[P]` = parallelizable with the other `[P]` tasks of the same wave (disjoint files).

---

## Plan re-validation (2026-10-20 delta note)

plan.md was written 2026-08-26 against Go 1.25.5. Re-validated against `main` @ 1b5c0d17b (Go 1.26.0). What no longer matches, and what this PR does instead:

| # | plan.md says | Tree today | This PR |
|---|---|---|---|
| Δ1 | Go 1.25.5 | `go.mod` is `go 1.26.0` | Re-assert the `encoding/json` duplicate-key and lenient-base64 quirks as tests, not prose. |
| Δ2 | Reuses "Spec 087 FR-010's atomic in-memory swap" | No such swap: 087 is unimplemented; the only mechanism is the `activeBundleMu` RWMutex pair in `tpa_bundle_source.go` | US1 owns the activation seam: one `activationMu` serializes every pipeline run; `(check, info)` publish under one lock. |
| Δ3 | `scanner/tpa_bundle_state.go` reads/writes "through storage" | Import cycle: `internal/storage` imports `scanner` | State types + a `TPABundleStateStore` interface live in `scanner`; `*storage.Manager` implements it (house pattern: `IncarnationStorage`, `ServerApprovalCommitter`). |
| Δ4 | Loader wired through the scanner `Service` | The bundle also loads where no `Service` exists (stdio, `server.go:259`), and loads twice per startup/reload | Package-level `scanner.SetBundleRuntime(store, dataDir)` injected right after `runtime.New`; activation is idempotent. |
| Δ5 | "the file watcher picks the bundle up" (quickstart) | Only `mcp_config.json` is watched | `POST /api/v1/security/bundle/reload` + `mcpproxy security bundle reload` run the same single-flight pipeline (not a second lifecycle; FR-018). Quickstart corrected. |
| Δ6 | `coverageOK` is a one-line edit | The async admission path (`maybeAutoApproveScanSettled`) never sees coverage, so a degraded install would still auto-approve new `scan`-mode servers | Degrade both seams: `coverageOK` in `inprocess.go` and `shouldAutoApproveScanSettled` in `server.go`. |
| Δ7 | Windows gets an explicitly weaker guarantee | `internal/codescripts/open_windows.go` already does a handle-based no-follow open (`FILE_FLAG_OPEN_REPARSE_POINT` + `GetFileInformationByHandle`) with `x/sys` already a dependency | Windows reaches parity on the open; the only residual is the parked reader on a stalled network FS (both platforms). |
| Δ8 | Sidecar has five keys (D3, data-model §2, quickstart) | `contracts/sidecar-format.md` §2 is explicit: four keys, `sequence` deliberately removed | Four keys are normative; `sequence_mismatch` is dropped from the vocabulary. |
| Δ9 | Telemetry: four whitelisted `tpa_scanner` keys | Five (v9 funnel added two); `SchemaVersion` is 14 | Bump to 15; typed bundle keys in their own group; whole-tuple rule (research D7). |
| Δ10 | "backend-only, no frontend" | FR-020 requires the Web UI; CLI and UI hard-code `signature_bundle` fields | Status fields added to CLI, REST and the Web UI security page. |
| Δ11 | `make swagger` after the status change | `/security/overview` has no `@Router` annotation | New bundle endpoints carry annotations; `make swagger` regenerates. |
| Δ12 | `cmd/tpa-db` absent from the tree diagram | quickstart needs `keygen` + `sign` | `cmd/tpa-db keygen|sign|verify` ship in US1; `validate` and the gate-before-sign are US2. |
| Δ13 | Config keys `tpa_operator_keys`, `tpa_active_authority` | `GET /api/v1/config` masks any key containing `KEY` or `AUTH` | Renamed to `tpa_operator_signers`, `tpa_active_root` (public keys are not secrets; masking would break GET→PATCH round-trips). |
| Δ14 | Activation self-check stage (FR-008) | Spec 087 owns it and is unimplemented; the gate lives in `package main` | US1 ships the stage as an injectable hook (no-op default) mapped to `gate_failure`; the real self-check is US2 D8 / Spec 087. |

## Design decisions recorded for the PR (zero-interruption assumptions)

- **A1 publisher trust anchors**: `internal/security/scanner/tpa_trust_anchors.go` holds the embedded publisher signer set (**empty today** — no publisher key exists yet), `publisherTrustEpoch = 0`, and a declared fallback baseline. With no publisher key, every publisher-signed candidate is `key_not_trusted`; the operator authority is the only signed path until a release adds a key (US3 release task). Publisher baseline = the embedded manifest's `sequence` when present (the US2 corpus session owns that file and is asked to stamp it), else the declared fallback.
- **A2 reason vocabulary**: `bundlesig` returns the 13 contract codes verbatim; the scanner maps them onto `last_rejection.reason` (`signature_invalid`/`key_id_mismatch` → `tamper`, `sidecar_too_large` → `ceiling_exceeded`, other `sidecar_*` → `sidecar_invalid`) and keeps the fine code in `detail`. Added reasons the spec needs but the vocabulary lacked: `signature_required`, `invalid_bundle`, `epoch_ahead`, `deny_listed`, `read_error`.
- **A3 key lookup order**: union of both trust sets → unknown id `key_not_trusted` → verify (`signature_invalid`) → key of the non-active authority `authority_not_active` (SC-002a b2 must report the latter).
- **A4 pin semantics**: while a pin binds (its authority active, its epoch current) only the pinned digest may activate at or below the watermark; exact deny-list matches are always refused; a candidate strictly above the watermark activates normally and clears the pin.
- **A5 epoch scoping**: per-authority state carries the epoch it is scoped to; any mismatch with the current trusted epoch re-scopes (re-baseline from the authority's baseline, retire pins and deny entries, loud log).
- **A6 unsigned external candidates** skip epoch/sequence/watermark stages entirely (their manifest is never trusted), never advance the watermark, are refused by `require_signed_bundle` or by the ratchet, and activate **degraded**. They get a history row with `signature_verified=false`.
- **A7 "sidecar absent"** means `ENOENT` only; a present-but-invalid sidecar is a rejection, never a fallback to the unsigned path.
- **A8 the never-written FIFO** of SC-011(c) is refused immediately as `not_regular_file` (the `O_NONBLOCK` open never starts a read); the 5 s budget and the one-parked-reader-per-path cap are proven with an injectable blocking reader.
- **A9 config**: `require_signed_bundle` (plain bool, default **false**, maintainer decision D2), `tpa_active_root` (`publisher`|`operator`, default publisher), `tpa_operator_signers` (base64 raw Ed25519 public keys), `tpa_operator_baseline` (`*uint64`, absent = unbaselined → degraded), `tpa_operator_epoch` (`*uint64`, absent = 0). Malformed/duplicate signers fail `ValidateDetailed` (write-time) and are dropped with a log at load (never boot-fatal). No env aliases: the trust root is config-resident by design.
- **A11 baselines are exclusive for activation**: a baseline SEEDS the authority's watermark (FR-009b), so normal activation needs a sequence strictly above it (equal sequence with a different digest is the FR-009 equal-sequence refusal); only an explicit rollback target may sit AT its authority's baseline (FR-010 "at or above"). Quickstart signs sequence 2 against operator baseline 1 for this reason.
- **A10 no config writes**: all persisted state is BBolt; Spec 116 `Runtime.Commit` is not involved.

---

## Wave 0 — Setup and shared contract (sequential)

- [x] T001 Commit this tasks.md.
- [x] T002 Shared contract (no behaviour): `internal/security/scanner/tpa_bundle_state.go` (state value types, `TPABundleStateStore` interface, authority constants), `tpa_bundle_reasons.go` (rejection + degraded reason constants), `BundleInfo` new fields in `tpa_bundle_source.go`, `SecurityConfig` fields + nil-safe accessors in `internal/config/config.go`. Builds; no test changes.

## Wave 1 — Independent foundations (parallel, disjoint packages)

### (a) `internal/security/bundlesig` — sidecar grammar + Ed25519 (FR-003, D3, D10)
- [x] T010 [P] test: `internal/security/bundlesig/sidecar_test.go` + `testdata/`: `valid.json`/`valid.json.sig`, `accept-trailing-newline`, `accept-all-zero-signature`, one `reject-<code>` per contract row 1–13 asserting the **code**, row-precedence (`{"foo":1}` → `sidecar_missing_field`), exactly 4096 bytes accepted / 4097 `sidecar_too_large`, duplicate key, base64 edge cases (non-canonical trailing bits, embedded newline, 87/89 chars, URL alphabet, missing padding), whitespace/key-order change still verifies (SC-001 non-requirement).
- [x] T011 [P] impl: `sidecar.go` (total grammar, explicit duplicate-key scan, strict base64 + round-trip), `verify.go` (`TrustSet` with duplicate-id refusal, `KeyID`, `ParsePublicKey`, `Verify`, `CheckKeyIDAgreement`, `SignSidecar`). Pure: no I/O, no globals, stdlib only.
- [x] T012 [P] test+impl: `cmd/tpa-db` (`keygen`, `sign --bundle --key --out`, `verify --bundle --signer`), stamping nothing into the bundle (operators sign bundles that already carry `sequence`/`key_id`/`trust_epoch`); `sign` refuses a bundle whose manifest `key_id` ≠ the signing key's id.

### (c) Descriptor-based candidate open (D5, FR-011, SC-011)
- [x] T020 [P] test: `internal/security/scanner/candidate_open_test.go` (+ `_unix_test.go`): regular file read; size ceiling exactly-at / one-over from the same descriptor; FIFO refused `not_regular_file` without blocking; symlink-to-FIFO and symlink-to-regular refused (no follow); swap-to-FIFO between resolution and open via the test seam refused; missing file → `ENOENT`-classified; injectable blocking reader → `read_timeout` within the budget and a second call on the same path refused fast while the first reader is parked (one parked reader per path).
- [x] T021 [P] impl: `candidate_open.go` (budgeted reader, parked-reader registry, `readCandidateFile(ctx, path, max)`), `candidate_open_unix.go` (`//go:build unix`: `O_RDONLY|O_NOFOLLOW|O_NONBLOCK|O_CLOEXEC`, `ELOOP`/`EMLINK` → not regular, `fstat`, clear `O_NONBLOCK`), `candidate_open_windows.go` (copy `internal/codescripts/open_windows.go`: `CreateFile` + `FILE_FLAG_OPEN_REPARSE_POINT`, `GetFileInformationByHandle`, refuse reparse points/directories), `candidate_open_other.go` stub. Never `SetReadDeadline`.

### (d1) Storage for the anti-downgrade state (D6, FR-011a)
- [x] T030 [P] test: `internal/storage/tpa_bundle_state_test.go`: absent bucket reads `(nil, nil)` and is not created by a read; commit state + history row in one `Update` (fault-inject a marshal failure → neither written); history append-only and ordered across reopen (SC-010 replay of N entries); server removal (`purge`) leaves the bucket intact.
- [x] T031 [P] impl: `internal/storage/models.go` `TPABundleStateBucket`, `internal/storage/tpa_bundle_state.go` (`BoltDB` raw methods + `Manager` wrappers implementing `scanner.TPABundleStateStore`; nested `history` bucket keyed by big-endian `NextSequence`), `purge.go` "Not purged" note.

### (f) Config (FR-004, FR-004b, FR-005a, FR-007, FR-009b; D2)
- [x] T040 [P] test: `internal/config/tpa_bundle_trust_test.go`: defaults (require off, root publisher, epoch 0, baseline absent); accessors nil-safe; `ValidateDetailed` rejects unknown root, malformed signer, duplicate signer ids; boot-time load never fails on them; JSON round-trip preserves explicit `0` baseline/epoch; `GET /api/v1/config` does not mask the new keys (redaction walk).
- [x] T041 [P] impl: validation in `ValidateDetailed`; `internal/runtime/config_hotreload.go` security clause → `jsonEqual`; settings/nullable fixtures if required by guard tests.

### (i) Telemetry backstop (FR-021, D7)
- [x] T050 [P] test: `internal/telemetry/anonymity_test.go` cases: each typed key accepted; free-string `bundle_version` rejected; `sequence` with `other` rejected; publisher line without `sequence` rejected; `source=file` + `signature_verified=false` + publisher line rejected; non-enum `source` rejected; non-bool `signature_verified` rejected; `sequence` > 2^53−1 rejected.
- [x] T051 [P] impl: `anonymity.go` typed group + whole-tuple rule; `telemetry.go` `TPABundleSnapshot` provider seam + merge into `tpa_scanner` (emitted even when scan counters are zero); `SchemaVersion` 15 + changelog; pinned schema tests updated.

## Wave 2 — Candidate pipeline, state, rollback, coverage (scanner core; one owner, sequential inside)

- [x] T060 test: `internal/security/scanner/tpa_candidate_test.go` — FR-008 stage order with a stage recorder; verify-before-parse proven by a compile counter (zero compiles on tamper); read-once (the verified buffer is the parsed buffer); SC-001 four tamper classes; `require_signed_bundle` + no sidecar → `signature_required`; present-but-invalid sidecar never falls back to unsigned.
- [x] T061 test: ceilings SC-010a — bundle 8 MiB activates / 8 MiB+1 `ceiling_exceeded`; 2000 rules / 2001; sidecar 4096 / 4097; read > budget `read_timeout` (injected budget); last-known-good serving throughout.
- [x] T062 test: manifest grammar — `sequence` decimal-string grammar (reject `0`,`01`,`+1`,` 1`,`1e3`, bare number, 21 digits, 2^64; accept `1`, `18446744073709551615`), `trust_epoch` integer, `key_id` agreement (row 13 → `tamper`).
- [x] T063 test: `tpa_bundle_state_test.go` anti-downgrade — US1 independent test (a)–(i); SC-002 same-authority same-epoch; equal sequence same digest = idempotent no-op, different digest = `downgrade`; SC-002a (a) max-sequence then epoch bump recovers, (a2) stale-epoch replay refused, (b) authorities isolated, (b2) `authority_not_active`, (c) operator baseline after wipe / unbaselined degraded; `epoch_ahead`; FR-004b declaration switch deactivates the serving bundle; SC-012 substitution (lowered watermark, cleared ratchet, forged pin) — only the out-of-data-dir anchors are asserted.
- [x] T064 test: crash consistency FR-011a — fault injected after LKG write / before commit, after commit / before publish; every recovery lands on a consistent (bundle, watermark) pair; a preserved rollback never loses its deny-list; startup reconciles orphan `tpa-lkg/*` files; a substituted LKG file (digest mismatch) falls back to embedded.
- [x] T065 test: `tpa_bundle_rollback_test.go` — FR-010/SC-010: rollback to embedded and to a named file by full digest; 12-hex-prefix collision fixture refused `rollback_target_mismatch`; watermark unchanged; deny-listed release not re-activated by the next reload; operator target measured against the operator baseline; baseline-less operator rollback proceeds degraded; unpin returns to watermark governance; N-activation history replay across restart.
- [x] T066 test: coverage `inprocess_coverage_test.go` — the four degrade conditions and the must-NOT-degrade cases (fresh install on embedded; embedded ≥ publisher watermark after the external file was removed; operator root declared but never activated; operator watermark 900 vs embedded 100); held reason `scan_coverage` end to end through `ScanToolMetadataVerdict`.
- [x] T067 impl: `tpa_candidate.go` (source-neutral `Candidate{Bundle, Sidecar []byte; Source; Path}` + pure `decide` + `activate`), `tpa_bundle_source.go` (thin file-drop adapter; `ConfigureBundle` kept as a wrapper; `ConfigureBundleFromConfig`; `SetBundleRuntime`; idempotence; `last_rejection` carried across loads), `tpa_bundle.go` (manifest fields, sequence grammar, rule-count ceiling), `tpa_bundle_state.go` (seeding, epoch scoping, ratchet, pin/deny), `tpa_bundle_lkg.go` (content-addressed LKG: temp → fsync → rename → fsync dir → commit → publish; reconcile), `tpa_bundle_rollback.go`, `tpa_trust_anchors.go`, `inprocess.go` (`coverageOK` degrade), self-check hook.

## Wave 3 — Surfaces and wiring (parallel, disjoint files)

- [x] T070 [P] test+impl: server wiring `internal/server/server.go` — `SetBundleRuntime(rt.StorageManager(), cfg.DataDir)` before the first configure; `configureTPABundle` → `ConfigureBundleFromConfig`; `shouldAutoApproveScanSettled` refuses while `scanner.BundleCoverageDegraded()`; telemetry provider wired. Tests under the CI skip regex.
- [x] T071 [P] test+impl: REST `internal/httpapi` — `GET /security/bundle`, `GET /security/bundle/history`, `POST /security/bundle/rollback`, `POST /security/bundle/unpin`, `POST /security/bundle/reload`; admin-read for GETs, a new agent-denied server op for mutations; swagger annotations; `make swagger`; gating/route-table guard tests.
- [x] T072 [P] test+impl: CLI `cmd/mcpproxy/security_cmd.go` — `security bundle [-o json|yaml]`, `bundle history`, `bundle rollback (--embedded | --path P) --digest <64 hex>`, `bundle unpin`, `bundle reload`; `signatureBundleLines` shows the new fields and a degraded line with its reason.
- [x] T073 [P] test+impl: Web UI `frontend/src/utils/signatureBundle.ts`, `frontend/src/views/Security.vue` — full digest on demand, sequence/authority/epoch/verified, degraded banner with reason, last rejection; vitest in `frontend/tests/unit/`.

## Wave 4 — Verify, review, ship

- [x] T080 `./scripts/test-api-e2e.sh`; `go test -race ./internal/security/... ./internal/storage/... ./internal/config/... ./internal/telemetry/...`; the CLAUDE.md skip-regex race run over `internal/server`/`httpapi`; golangci-lint v2 bare and `--build-tags server`.
- [x] T081 Review workflow (dimensions → 3 refuters per finding → fix confirmed).
- [x] T082 Cross-model review (opencode Sol 6.1; ≤10 rounds).
- [x] T083 Docs: `docs/features/tpa-bundle-signing.md` (operator: verify, drop, reload, roll back, read status, air-gapped walkthrough, Windows/NFS residual), `docs/features/security-quarantine.md` + tool-scanner docs (degraded states), `docs/configuration.md` (new keys), quickstart.md corrections (Δ5, Δ8, Δ13, sequence ≥ baseline), `CLAUDE.md` Recent Changes, `roadmap.yaml` + `scripts/gen-roadmap.py`.
- [x] T084 Open the PR; tick this file.

---

## Out of scope for this PR

- **US2 (sibling session)**: `signatures[]` section parsing and integrity (D4, `contracts/bundle-signatures-section.md`); the seed corpus in `internal/security/scanner/bundled/scanner-bundle.json` (**including stamping its `sequence`/`trust_epoch`/`generated_at`** — handed over); `cmd/scan-eval --bundle` + `BundleCheck` registration + `categoryCheck` entries + D9 negative control; `cmd/tpa-db validate` and gate-before-sign; contributor docs; `.github/workflows/eval.yml`.
- **US3 / Spec 087**: now planned in "User Story 3 — refresh lifecycle (Spec 087) + publication channel + adoption loop" below (T300+): network fetch + daily tick + single-flight lifecycle, the real activation self-check, post-activation re-scan (FR-019), freshness status, telemetry v16. Still out of scope there: creating the publication channel itself (D1) and shipping the first publisher key in a release (see that section's "Out of scope" list).

---

## User Story 3 — refresh lifecycle (Spec 087) + publication channel + adoption loop

**Input**: `specs/087-tpa-daily-refresh/{spec,plan}.md`, `specs/101-tpa-db/{spec,plan,research,data-model,quickstart}.md`, the US1 sections above, US1 as merged (#1701), and the 2026-10-21 subsystem maps (pipeline, gate, lifecycle, surfaces, reprotect).
**Scope**: one PR against `main` that implements Spec 101 US3 together with Spec 087 US1 and US2. Spec 087 US3 (release awareness) is already delivered by Spec 079 (B23).
**Must not depend on or touch US2 (#1702)**: `internal/security/tpacorpus/**`, `cmd/scan-eval/**`, `internal/security/scanner/bundled/scanner-bundle.json`, `specs/065-evaluation-foundation/datasets/detect_corpus_v1.json`, `.gitattributes`, `.github/workflows/eval.yml`, `specs/065-evaluation-foundation/datasets/README.md`, `detect_corpus_test.go`, and the US2-added scanner symbols (`LoadBundleCheck`, `EmbeddedBundle`, `ParseBundleSignatures`, `Signature*`, `tpa_bundle_eval.go`, `tpa_bundle_signatures.go`). The only US2 coupling is the merge-order gate on the self-check corpus copy (B14), which is a checklist step, not a code dependency.
**TDD rule**: the same as US1. Every `test` task is written first and must FAIL for the behavioural reason it states, on a build that compiles. Wave 0 stubs **every** symbol any T3NN test references, each in the file that will hold its final implementation, so groups replace function bodies rather than move stubs. A missing symbol is a build failure, not a bite. Prove that a test bites by reverting the code hunk it covers, never by deleting symbols.
**Format**: `- [ ] T3NN [P?] test|impl|docs: <description> (<exact paths>)`. `[P]` means the task can run in parallel with the other `[P]` tasks of the same wave; their files are disjoint.

---

### 2026-10-21 re-validation delta

Spec 087 plan.md is dated 2026-07-16 and Spec 101 plan.md 2026-08-26. Both were re-validated against `main` @ 14cb288fc, with US1 merged and US2 open.

| # | Spec 087 plan / Spec 101 plan says | Tree today | US3 does instead |
|---|---|---|---|
| Δ15 | 087: new package `internal/security/bundle` (`loader.go`, `verify.go`, `refresh.go`, `fetch.go`, `selftest.go`) | Does not exist. US1 shipped the pipeline as `internal/security/scanner/tpa_*.go` plus the leaf package `internal/security/bundlesig`. Spec 101 D10/D11 retire the name. | No new package. The scheduler and the fetch adapter are new files in package `scanner` (`tpa_refresh.go`, `tpa_fetch.go`) and feed the existing source-neutral `evaluateCandidate` (`tpa_candidate.go`). The verifier is never forked (D11). |
| Δ16 | 087: nested config block `security.bundle{path, refresh, fetch.{enabled,url,public_key}}` | US1 uses flat `security.tpa_*` keys. `public_key` would be masked by `GET /api/v1/config` (the name contains KEY; Δ13). The trust set already exists (publisher anchors plus `tpa_operator_signers`). | Flat keys `tpa_refresh_interval`, `tpa_fetch_enabled`, `tpa_fetch_url`, `tpa_stale_after_days` (B1). No per-fetch key field. A fetched pair is verified against the US1 authority trust set. |
| Δ17 | 087: `selftest.go` reuses `cmd/scan-eval/gate.go` "verbatim"; 101 FR-016a(c): the activation self-check MUST run "the same candidate-loading path" as CI | `gate.go` is `package main` and cannot be imported. US2 is rewriting it (`gateChecks(bundle)`). | A gate port in `internal/security/scanner/tpa_selfcheck.go`, guarded by AST, corpus-byte and production-surface parity tests, without editing `cmd/scan-eval` (B14). This is a recorded **deviation** from FR-016a(c); the shared gate package is a follow-up. |
| Δ18 | 087: embedded self-check corpus is a "compact subset" | A subset changes the denominators and therefore the decision: 1/21 FP passes, 1/10 fails. | A full byte copy at `internal/security/scanner/bundled/selfcheck-corpus.json`, plus a parity test against `specs/065-…/detect_corpus_v1.json`. |
| Δ19 | 087: bundle status goes on `/api/v1/info` next to `update` | `/info` is `scopeFiltered` (agent tokens can read it). Bundle status is admin-only (`security_bundle.go:15-19`). | `/api/v1/info` is left alone. Status grows on `GET /api/v1/security/bundle` and `/security/overview`. Release status stays separate (087 FR-019). Flagged for maintainer sign-off. |
| Δ20 | 087: refresher "mirrors `updatecheck.Checker`" | The Checker has no Stop and is never joined. The refresher writes BBolt and `tpa-lkg/`. | Lifecycle copies the `telemetry.Service` Start/Stop/done pattern and is joined in `Runtime.Close` before `StopAsync` (B19). Only the generation-guard and seam patterns come from updatecheck. |
| Δ21 | 087: Go 1.24 | `go 1.26.0` | — |
| Δ22 | 101 plan D1: "immutable per-sequence asset names, latest via the Releases API". 101 research D1: content-addressed names plus a **signed release index**, a stable cacheable URL, ETag, jitter, rate-limit-is-not-verification. | The two contradict each other. The repo `smart-mcp-proxy/tpa-db` does not exist (404). The publisher signer set is empty. | Channel = GitHub Releases API `releases/latest` with per-sequence asset names used **as selection hints only**. Integrity comes from the signed pair plus the per-authority watermark (B4). Research D1's ETag, jitter and rate-limit rules are adopted in full (B6). A "direct" URL mode serves mirrors and air-gap staging. The signed release index is deferred (out of scope). |
| Δ23 | 101 tasks Δ2: "one `activationMu` serializes every run" | The identifier is `bundleRT.mu` (`tpa_bundle_runtime.go:86`), a non-reentrant `sync.Mutex`. It serializes but does not coalesce. | Network I/O happens outside `bundleRT.mu`. Coalescing is the refresher's own joinable in-flight cycle (B8). |
| Δ24 | 087 FR-013/014: "operator-configured trusted public key; no key → fail closed" | Trust comes from US1 FR-004/004b: the active root's trust set. The publisher set is empty, so with the default root `publisher` every fetched pair would be `key_not_trusted`. | When the active root's trust set is empty, the fetch issues **zero requests** and reports `disabled/no_trusted_signer`, surfaced in status and doctor (B10, B16). Today a fetch works only with `tpa_active_root=operator` plus `tpa_operator_signers` (plus `tpa_operator_baseline` for non-degraded coverage). |
| Δ25 | 087 US1: "once per day mcpproxy notices the dropped file" | No periodic file read and no watcher. Only config apply, REST/CLI reload and startup run the pipeline. | The daily cycle runs a **file leg** (the existing path read, network-free, always on) followed by an optional **fetch leg** (B7). |
| Δ26 | (implicit) the fetched bundle persists like a file drop | **Hazard A**: with `tpa_bundle_path==""`, `fallbackLocked` (:469), `canSkipLocked` (:425) and `restorePinLocked` (:518) treat external bundles as removed. A fetched bundle would be deactivated at the next `config.reloaded` and at every restart, and reconcile would delete its LKG files. | Those three guards use `externalConfigured(policy, state)`; the file read, the file-candidate evaluation and the file-leg `loadErr` stay Path-only (B9). This is the first failing test (T320). |
| Δ27 | US1 hook `bundleSelfCheck` applies to every evaluation mode (nil, so not observable) | Called in `compileAndGate` for `evalNormal`, `evalLKG` (startup restore, pin restore) and `evalRollback`, under `bundleRT.mu`. | Self-check runs for `evalNormal` and `evalRollback` only and is skipped for `evalLKG` (B13). The hook is installed from production wiring, not `init()`, and a server-level test proves it is installed (T365). |
| Δ28 | 101 FR-019: "surface as review findings" | `GetReviewQueue` (`review.go:196`) and attention kinds ignore approved-only servers. `Service.StartScan` connects to upstreams (`EnsureConnected`, live `tools/list`) and schedules Pass 2. | A new cached-contracts scan entry. Findings appear in the scan summary, report, badge, overview, activity and the per-tool review verdict on the server's review payload (`GetServerReview`, `reviewToolScanVerdict`). A held-evidence refresh covers held tools. A new attention kind is deferred pending a maintainer decision (B17, B18). |
| Δ29 | 087 FR-016: env override for every refresh setting | US1 stance: trust config has no env alias (A9). An env-supplied URL would let the environment redirect the fetch. | Only `MCPPROXY_TPA_FETCH_ENABLED` exists (bidirectional, read inside the accessor, never persisted). The URL stays config-resident (B2). |
| Δ30 | 087 edge case: "an in-flight refresh completes against the settings it started with" | — | An in-flight download finishes, but its candidate is offered under the **live** policy. If any fetch or trust setting changed, it is discarded as `superseded` (B8). Flagged for maintainer sign-off; 087 spec.md is amended to match. |
| Δ31 | 087 FR-016: "refresh enable/interval" | — | There is **no refresh-enable key**: the file leg is network-free, idempotent and always on; only its interval is configurable. The fetch leg has its own enable (B1). |

### Design decisions (zero-interruption assumptions)

- **B1. Config keys.** All keys are flat under `security`, follow the US1 naming, and contain none of the masked substrings KEY, AUTH, TOKEN, PASS, CERT or SECRET.
  - `tpa_refresh_interval`: `config.Duration`, `swaggertype:"string"`. Absent or `0` means 24h. The runtime clamps to at least 1h; a test seam goes lower.
  - `tpa_fetch_enabled`: plain `bool`, default **false**.
  - `tpa_fetch_url`: string, default **empty**.
  - `tpa_stale_after_days`: `*int`. Absent means 30; `0` disables the stale flag and the doctor stale warning.
  - Shape checks run in a new `tpaRefreshErrors()` appended to `ValidateDetailed` (write-time only), never in `validateDetailedCore` (boot-fatal). It refuses: interval under 1h; a scheme other than https unless the host is a loopback literal or `localhost`; userinfo, query or fragment; fetch enabled with an empty URL; fetch enabled with `tpa_active_root=operator` and an empty `tpa_operator_signers`; negative stale days. Messages never echo the URL. The publisher-root-with-empty-built-in-set case cannot be seen by `internal/config` (it must not import scanner), so it is reported at runtime (`no_trusted_signer`, B10) and by doctor (B16).
  - A malformed value at boot is logged and treated as "fetch off".
  - `DetectConfigChanges` needs no edit, because the fields sit inside `SecurityConfig`, which is compared with `jsonEqual`; a hot-reload test is still added (T311).
- **B2. Env override.** `MCPPROXY_TPA_FETCH_ENABLED=true|false` is read inside `IsTPAFetchEnabled()`, the same pattern as `EffectiveTPABundlePath`. It is not mirrored into `process_overrides.go`, so `SaveConfig` never persists it. Enabling via env still fetches only the config-resident URL, verified against config-resident keys, so the environment cannot redirect the fetch. There is no env alias for the URL, interval or stale threshold. `MCPPROXY_DISABLE_AUTO_UPDATE` does **not** silence the bundle fetch (087 FR-018/FR-019 keep the two distinct).
- **B3. No working default URL.** An empty `tpa_fetch_url` means the fetch leg is off (`disabled/no_url`, zero requests). No URL is compiled into the binary until a release ships the first publisher key and the corpus repo exists. Docs give the intended channel URL shape as an example only.
- **B4. Channel shape and integrity.** The mode is detected from the URL **path only**, so `httptest` can exercise both modes.
  - **Releases mode**: the path matches `^/repos/[^/]+/[^/]+/releases/latest$`. GET the JSON with `Accept: application/vnd.github+json`, `User-Agent: mcpproxy/<version>`, a 1 MiB cap and `If-None-Match`. Select exactly one asset named `^scanner-bundle-([0-9]{1,20})\.json$` with a sibling `<name>.sig`. Zero or several candidate pairs, or a missing `.sig` asset, give `channel_invalid`. Download both through `browser_download_url` with `Accept: application/octet-stream`.
  - **Direct mode** (any other path): GET the URL as the bundle and `URL + ".sig"` as the sidecar, with `If-None-Match` on the bundle.
  - The naming matches the file-drop convention (`<path>` + `<path>.sig`, `tpa_bundle_runtime.go:871`), so an air-gapped operator drops the downloaded pair as-is under any one name pair.
  - The filename sequence is an **unsigned hint**, used only to skip a download when it is at or below the active root's watermark (`unchanged`). It is never trusted for activation.
  - Integrity rests only on what US1 already enforces: Ed25519 over the raw bundle bytes; key-id, epoch and sequence agreement; the per-authority watermark and the equal-sequence-different-digest refusal; the deny-list; the self-check. A substituted or replayed asset therefore fails verification (`tamper`) or is a replay (`downgrade`). A withheld asset (freeze) is surfaced by staleness (B15). This is why research D1's signed index can be deferred without weakening activation.
- **B5. Candidate construction and signed-only enforcement.**
  - Bodies are read through `io.LimitReader(MaxBundleBytes+1)` and `io.LimitReader(MaxSidecarBytes+1)`. Over-limit bytes are handed to the pipeline as they are, so the existing `ceiling_exceeded` stage rejects them, with no second ceiling implementation.
  - The candidate is `Candidate{Bundle, Sidecar, SidecarPresent, Source: BundleSourceFetch, Path: "<scheme>://<host>"}`. The path is never the full URL.
  - Signed-only is enforced **per evaluation**, never by mutating the policy: a new `evalEnv.MustSign bool` is consulted in `unsigned()` alongside `policy.RequireSigned` and is set only for the offered evaluation. A policy copy with `RequireSigned=true` would also be passed to fallback step b and deactivate a serving unsigned file-drop bundle (`tpa_candidate.go:196`, `tpa_bundle_runtime.go:472`).
  - In direct mode, a `404` on `.sig` becomes `SidecarPresent=false` plus `MustSign`, so the pipeline itself emits `signature_required`. Other `.sig` HTTP failures are `fetch_unavailable/http_status`. In Releases mode a missing `.sig` asset is `channel_invalid`.
- **B6. Network-failure semantics.** These outcomes are a **non-verification** refresh status, never a `last_rejection`, and they never touch the watermark: network error, timeout, non-2xx, `403`/`429`, `X-RateLimit-Remaining: 0`, `Retry-After`, DNS failure. They report `fetch_unavailable` with reason `network_error|timeout|http_status|rate_limited`, and LKG keeps serving.
  - `304` gives `not_modified`. ETags are cached in memory per URL (restart-scoped).
  - Rate-limited: the next attempt is `max(next tick, reset or Retry-After)`, capped at 24h. A plain failure retries at the next tick, with no exponential stretch beyond the interval (087 FR-015).
  - `consecutive_failures` counts `fetch_unavailable` and `channel_invalid` cycles and resets on `activated`, `unchanged`, `not_modified` and `rejected`.
  - Logs are deduplicated on `(outcome, reason)`: log on change, or once per 24h while the outcome persists.
  - Jitter: the first cycle fires at a uniform random point in `[30s, 5m]` after Start, each later one at `interval ± 10%`. The timer is re-armed after each cycle completes (a single `time.Timer`, monotonic clock), so a suspended laptop or a forward clock jump yields **one** cycle on wake, never a catch-up burst, and a backward wall-clock jump cannot wedge it. Rand and clock are injectable.
- **B7. Precedence and per-cycle noise.**
  - A cycle runs the **file leg** (the existing path run, only when `tpa_bundle_path` is set), then the **fetch leg** (only when B10 allows it).
  - The fetch leg calls `runLocked(policy, log, runOptions{offered: &c})`, which evaluates **only** the offered candidate as the fresh one. On refusal the current serving bundle stays, via fallback step b with `Reuse`. The leg never re-reads the file and never writes the file-leg `loadErr`; its refusal goes to new `selection.offeredRej/offeredDigest`, recorded through `recordRejectionLocked` (deduplicated).
  - The "same as active" and negative-cache comparisons happen **inside** the offered branch of `runLocked`, under `bundleRT.mu`, before `evaluateCandidate`: bytes against `rt.active.cand` (the `canSkipLocked` compare) and the digest pair against a mutex-guarded cache of the last refused fetched pair. A match gives `unchanged` with zero compiles and zero self-checks. Nothing reads `rt.state`/`rt.active` unlocked.
  - The pre-download hint skip reads per-authority watermarks from a small snapshot published under `bundleRT.mu` by `publishLocked` (`atomic.Pointer`), never `rt.state` and never `BundleStatus()`.
  - A stale file below a fetched watermark is refused once (deduplicated), only on the file leg, and reported as the file leg's `load_error`; it does not mask fetch status.
  - Both candidates valid and above the watermark: the file wins first, the fetch wins at the next cycle. Monotone, no flapping.
  - A deny-listed or pinned-around remote release (after `rollback`) is refused by the pipeline as `deny_listed`/`downgrade`, then hits the negative cache on later cycles: quiet, no reactivation, no observer fire (B16 quiet set). A strictly newer signed release activates and clears the pin (US1 A4).
- **B8. Single-flight and hot reload.**
  - The refresher holds one in-flight cycle. A manual refresh, a timer tick or a config nudge arriving during a cycle **joins** it and receives that cycle's result: one download, one activation per distinct candidate (087 "two triggers race"). `reload` and `config.reloaded` runs still serialize on `bundleRT.mu` and stay idempotent, so tick + reload + refresh yields one activation per distinct candidate.
  - A generation counter is bumped on any change to the fetch settings (enabled, URL) **or** to any input of the offered-run policy fingerprint (active root, operator signers/baseline/epoch, `require_signed_bundle`). A finished download is offered only if the generation is unchanged; otherwise it reports `superseded`. An interval change re-arms the timer. A `Nudge` after `Stop` began is a no-op.
  - The HTTP fetch runs outside `bundleRT.mu` with its own context: a 20s total per-cycle deadline (under the CLI's hard 30s, `security_cmd.go:2542`) and a 10s response-header timeout, so a trickled body is cut by the cycle deadline.
- **B9. Fetched bundles survive reloads and restarts.**
  - `externalConfigured(policy *bundlePolicy, st *TPABundleState) bool` = `policy.Path != "" || policy.FetchEnabled || (st.Active != nil && st.Active.Source == BundleSourceFetch)`. It is a function, because `bundlePolicy` holds config only.
  - Sites: `canSkipLocked` (:425; with `Path==""` it skips iff the serving external bundle still matches the recorded Active), `fallbackLocked` steps b/c (:469), `restorePinLocked` (:518). The file read (:260), the file-candidate evaluation in `chooseLocked` (:447) and the file-leg `loadErr` in `finishLocked` (:338) stay `policy.Path != ""`.
  - Disabling the fetch therefore **keeps** the last verified fetched bundle serving; dropping silently to the weaker embedded corpus would be an unrequested FR-009a degrade. `security bundle rollback --embedded` is the explicit way back. Rollback to a **prior fetched** release needs that file in the `tpa_bundle_path` directory (`confineRollbackPath`); documented.
  - `FetchEnabled` and the URL are added to `policy.Fingerprint`. The interval and stale threshold are not (B15).
- **B10. When the fetch leg may issue requests.** Only when all hold: `IsTPAFetchEnabled()`; the URL is non-empty and valid; the active root's trust set is non-empty (publisher anchors, or `tpa_operator_signers` when the root is `operator`); the state store is loadable (not `loadFailed`). Otherwise the leg reports `disabled` with reason `fetch_off|no_url|invalid_url|no_trusted_signer|state_unavailable`. SC-005 and 087 FR-012 are asserted as **zero requests**.
- **B11. Transport and egress.**
  - https is required; http is allowed only when the configured host is a loopback literal or `localhost`.
  - Redirects: at most 5 hops; a hop may not downgrade https → http. Hops may cross hosts, because integrity comes from the signature and GitHub asset downloads redirect off `api.github.com`.
  - A dial-time `net.Dialer.Control` always refuses link-local (169.254/16 including metadata, fe80::/10), unspecified and multicast addresses. It refuses loopback and RFC1918/ULA/CGNAT unless the configured host itself resolved to that class (a LAN mirror), so a public URL cannot redirect inward.
  - When `HTTP(S)_PROXY` is set, the same predicate is applied to the resolved target host at the application layer.
  - The predicates are written minimally in `tpa_fetch.go`; extracting a shared leaf from `internal/registries/ssrf.go` is a follow-up.
- **B12. Host-only logging.** The host is computed once with `u.Hostname()`. Every zap field, status field and telemetry value uses only that host. `*url.Error` is unwrapped and only `urlErr.Err` plus the host is logged; redirect hops are logged by host. A test asserts the URL path, query and redirect targets never appear in logs, `BundleInfo`, `last_rejection` or error strings.
- **B13. Activation self-check.**
  - `internal/security/scanner/tpa_selfcheck.go` defines `activationSelfCheck(*BundleCheck) error` and `EnableActivationSelfCheck()`. The latter assigns `bundleSelfCheck` and is called once from production wiring in `internal/server/server.go`, next to `configureTPABundleRuntime`, so US1 fixtures and `clearBundleRuntime` keep the nil default. A server-level test proves the hook is non-nil after construction (T365).
  - The check builds its own engine: `detect.NewEngine(detect.Options{Checks: append(selfCheckBuiltins(), cand)})` over each entry's tool plus peers. It never calls `BundleStatus`, `defaultBundleCheck`, `activeBundleSnapshot` or `ScanToolMetadataVerdict` (deadlock through the lazy install under `bundleRT.mu`).
  - The decision is identical to the CI gate. Built-ins: UnicodeHidden, Shadowing, PayloadDecoded, PhraseInjection. Category map: the five HEAD entries plus `strings.HasPrefix(cat,"tpa_") → "tpa.bundle"`. Rule: `recall < 0.90 || hard-negative FP > 0.05 → reject`, same float expressions.
  - Fail-closed additions: either vacuity guard (gated malicious == 0, hard negatives == 0), corpus parse/empty, and `Coverage.ChecksFailed > 0`. A package-level corpus override seam (`selfCheckCorpus func() []byte`) lets tests feed vacuous corpora.
  - The rejection detail is terse: counts and corpus ids only, never candidate pattern text.
  - It runs for `evalNormal` and `evalRollback` and is skipped for `evalLKG` (restore of a bundle that already passed), avoiding deactivation at upgrade and the startup cost. The embedded default bypasses the hook as today and is guarded by a unit test.
  - Operator-signed bundles are **not** exempt.
  - The recall leg is knowingly inert on the 57-entry HEAD corpus until US2's `tpa_*` samples land; the test logs this loudly.
  - Fixture consequence: any test bundle that runs through the real self-check (T391) must keep at most 1 false positive over the 21 HEAD hard negatives (2/21 fails), so its added signature uses a narrow, fixture-specific pattern.
- **B14. Self-check corpus location and drift guard.** A byte copy at `internal/security/scanner/bundled/selfcheck-corpus.json`, embedded with `//go:embed`. Guards:
  - (a) byte parity with `specs/065-…/detect_corpus_v1.json` after `\r\n → \n`; the failure message prints the `cp` command;
  - (b) go/parser parity with `cmd/scan-eval/gate.go` and `main.go`, read-only, **tolerant to both shapes** (`gateChecks()`, `gateChecks(bundle)`, `builtinGateChecks()`), keyed on `checks.X` composite literals and `categoryCheck` string pairs: built-in list, pairs, `tpa.bundle` only on `tpa_` keys, `ThreatLevelDangerous` in `scanEntryFlagged`, flag defaults 0.90 and 0.05;
  - (c) identical per-entry hard-flags between `baselineEngineChecks()` and the self-check built-ins;
  - (d) an optional differential `go run ./cmd/scan-eval --gate`, skipped when `go` is absent;
  - (e) non-empty provenance and licence fields on every corpus entry (087 FR-021).
  - Test names use the `TestSelfCheck*` prefix (off eval.yml's `-run` regex).
  - **Merge-order gate (T396 checklist)**: whichever of US2/US3 merges second refreshes the copy. If US3 merges second, it rebases and runs the `cp` in the same PR. If US3 merges first, the PR body and a comment on #1702 tell US2 to run the `cp`; guard (a) is red on US2's branch by design until then.
- **B15. Freshness fields and settings.** All new `BundleInfo` fields are pointer or omitempty:
  - `last_checked_at *time.Time`: end of any pipeline run, including `canSkipLocked` no-ops.
  - `last_activated_at *time.Time`: `state.Active.ActivatedAt` when the Active digest equals the serving digest; nil for embedded.
  - `stale bool` and `stale_after_days *int`: computed at **read time** next to `StalenessDays`. A future `generated_at` is never stale and never a negative age.
  - `refresh *BundleRefreshStatus{fetch_enabled, fetch_host, interval_seconds, last_trigger, last_outcome, last_reason, last_attempt_at, consecutive_failures, next_check_at}`.
  - `reprotect *BundleReprotectStatus{state: idle|running|suspended|failed, reason, last_completed_at, servers_scanned, servers_with_hits}`, set from the server through `scanner.SetReprotectStatus` (B17).
  - Display settings (stale threshold, interval, fetch enabled, fetch host) live in a small struct under its own mutex next to `histMu`, refreshed by `configureWithPolicy` on **every** apply (before `canSkipLocked`) and by `Nudge`. A threshold edit therefore takes effect on the next read without rerunning the pipeline. Refresh status and `last_checked_at` live in the same struct and are merged into `BundleStatus()`/`statusFromSnapshot()` copies, so cycles never churn the published (check, info) pair.
  - Trigger vocabulary (closed): `startup` (the construction-time `ConfigureBundleFromConfig`), `config` (a `config.reloaded` re-apply), `timer`, `manual`.
  - Outcome vocabulary (closed): `activated`, `unchanged`, `not_modified`, `rejected` (detail in `last_rejection`), `fetch_unavailable`, `channel_invalid`, `superseded`, `disabled`.
  - `last_rejection` keeps meaning "a candidate was refused" and stays in memory, restart-scoped (US1 behaviour).
- **B16. Doctor.** The pure function `scanner.BundleDoctorWarnings(info BundleInfo, now time.Time) []string` (`tpa_bundle_doctor.go`) is registered once with `mgmtService.AddRuntimeWarningSource`. It returns at most three short strings with no digest, path, URL or host:
  - (1) **stale**: `stale` AND (source is `file` or `fetch`, OR fetch enabled). A stale embedded corpus on an install that never configured a refresh shows in status only; binary upgrades are Spec 079's nudge.
  - (2) **stuck on last-known-good**: `last_rejection` present, newer than `last_activated_at` (or process start for embedded), reason outside the quiet set {`downgrade`, `deny_listed`}; or `refresh.consecutive_failures ≥ 3`.
  - (3) **fetch configured but inactive**: fetch enabled and the leg is `disabled` with `no_url|invalid_url|no_trusted_signer` (087 edge case "no key configured: a clear configuration error is surfaced").
  - These warnings **intentionally** feed the doctor verdict, the MCP doctor tool and the telemetry `runtime_warnings` pass/fail bit: each is an actionable state on an install that configured a refresh. Calling the source on a never-published runtime lazily installs the embedded default without holding `bundleRT.mu` first; a test proves it does not hang.
- **B17. Re-protection sweep (FR-019, SC-009, RT-02).**
  - Trigger: `scanner.AddBundleActivationObserver(fn) (unsubscribe func())`. It fires only when a history row was written (activation or rollback), **after** publish, dispatched on a goroutine outside `bundleRT.mu`, with an in-flight counter tests can drain. Never on an LKG restore, an embedded fallback, an idempotent rerun or a refusal. Payload: digest, sequence, authority, source, rollback flag, previous digest.
  - **Leg (i), held-evidence refresh, always runs.** For every server with `pending` or `changed` records, **including quarantined servers**: `Runtime.RefreshHeldScanEvidence(server)` takes the tool-approval lock and re-runs `scanToolEvidence` plus `recordScanHold` on the stored `Current*`. Evidence only: Status, ApprovedHash and ApprovedBy are never written; no unquarantine, no approve. A signature removed by a newer bundle leaves the tool held, with verdict clean `awaiting_review` (Spec 101 edge case).
  - **Leg (ii), cached informational scan, gated by `security.auto_baseline_scan`** (the existing informational-scan kill switch, env included). For every **non-quarantined** server with at least one `approved` record, any trust mode: `Service.StartScanFromCachedContracts(ctx, server, digest)` builds `ToolDefinitions` from the approved records' cached contracts and peers from the StateView, with `ScanContext.SourceMethod="tool_definitions_only"`, accurate `ToolContractDigests`, scanner `tpa-descriptions`, `SourceDir=""`. It never calls `IsConnected`, `EnsureConnected` or `GetServerTools`, never schedules Pass 2, and is excluded from `scans_completed`/`scans_with_findings`. Quarantined servers are skipped, so `maybeAutoApproveScanSettled` and `maybeCaptureReviewDefinitions` (both quarantined-only) can never act on a reprotect job.
  - With the switch off, leg (ii) does not run and `reprotect.state=suspended`, `reason=auto_baseline_scan_off` is shown on every status surface. This dependency is recorded in the Spec 101 FR-019 amendment and flagged for maintainer sign-off; no new config key.
  - Pacing: serial under `infoScanRunMu`, a 2s pause between servers, busy re-arm (`ErrScanInProgress` re-arms without spending a failure), at most 3 attempts per server, one job per (server, digest).
  - Storm bounds: one sweep emits at most one finding per tool (structural, `detect.aggregate`), at most one activity record per server, and a bounded number of SSE events; a rollback is one more history row and therefore one more sweep; the same digest re-fired yields nothing.
  - Persistence: `storage.BundleReprotectState{FullDigest, CompletedAt}` is written only when a sweep completes with zero failures. A startup catch-up after `setSecurityScanner` re-runs the sweep when the marker differs from the active digest (covers the construction-time activation, a crash mid-sweep and a binary upgrade that changes the embedded corpus).
  - Shutdown: handler goroutines are tracked by a `WaitGroup` on the Server and bound to `informationalScanContext()`, with `ctx.Err()` checked before every store write. `Server.Shutdown` (`server.go:1354`) unsubscribes and waits for them before `s.runtime.Close()` (:1408).
  - stdio mode has no scanner `Service`, so leg (ii) is inert there (documented). Leg (i) runs wherever a Runtime exists.
  - The report lists all current hard hits; "new" is not diffed against the previous bundle. The new signature's TPA id is in the finding's signals.
- **B18. Where reprotect findings surface.** The existing scan summary, report, server-list badge, Security overview, activity record, and the per-tool `scan_verdict` in the server review payload (`GetServerReview`, `reviewToolScanVerdict`, which uses findings when the cached scan covers the tool's current contract). SC-009's "surfaced for review" is asserted on that review payload (T391). No new attention or review-queue kind: an approved-only server does not enter `/review` or Home attention. That is recorded as an **open maintainer decision** (needs the Spec 109 parity matrix, Swift and Web), not as a silently weakened MUST.
- **B19. Lifecycle ownership.**
  - `scanner.NewBundleRefresher(BundleRefresherOptions{Clock, Rand, HTTPClient, Transport, Resolver, ConfigProvider, Logger, Version})`, installed with `scanner.SetBundleRefresher` (package-level, replaceable slot).
  - Constructed in `internal/server/server.go` right after `rt.SetVersion`, registered with `Runtime.SetBundleRefresher`, started in `StartBackgroundInitialization` with `r.appCtx` on every transport, including dev builds (no semver gate).
  - Stopped and joined in `Runtime.Close` before `storageManager.StopAsync`; `Close` also clears the scanner slot if it still points at this instance. A Start after Stop is a no-op (telemetry pattern).
  - `RefreshBundle` checks the slot's refresher is running; otherwise it falls back to `ReloadBundle`.
  - Hot reload: the existing `config.reloaded` listener's `reapplyScannerSecurityConfig` calls `refresher.Nudge(sec)`, non-blocking. The refresher reads live `SecurityConfig` through its provider every cycle and never takes `Runtime.mu`.
- **B20. Manual refresh.**
  - `mcpproxy security bundle refresh [-o json|yaml]` → `POST /api/v1/security/bundle/refresh` (`requireServerOp(auth.ServerOpBundleManage)`, agent-denied) → `Service.RefreshBundle(ctx)` → `scanner.RefreshBundle(ctx, logger)`: one joined cycle (file leg plus fetch leg), trigger `manual`. Synchronous 200 with `BundleInfo`; the outcome is in `refresh`.
  - `reload` stays as it is: the local path only, never network. A test asserts zero HTTP requests on `reload` with fetch enabled.
  - The Web UI gets no "Check now" button.
- **B21. Telemetry (schema 16).** Five non-negative integer counters on the `tpa_scanner` object through `tpaScannerScalarKeys`, **not** through the typed `tpaBundleKeys` whole-tuple group: `bundle_refresh_cycles`, `bundle_refresh_activations`, `bundle_refresh_rejections`, `bundle_refresh_unavailable`, `bundle_reprotect_hits` (servers whose reprotect scan had at least one hard finding).
  - Reset on accept; wired through `Record*On`, `RegistrySnapshot`, `TPAScannerStats`, `Snapshot`, `Drain`, `HasPendingCounters`, `Reset` and `isZero`.
  - `internal/telemetry` must not import scanner (`telemetry/tpa_bundle.go:5-11`). The class is a telemetry-local string enum `telemetry.TPABundleRefreshClass` in `internal/telemetry/tpa_bundle_refresh.go`. The producer `scanner.SetBundleRefreshObserver(func(RefreshOutcomeClass))` carries only scanner's own enum, and `internal/server/server.go` maps it to the telemetry enum.
  - `internal/server/tpa_bundle_telemetry.go` maps `source=fetch`, which `anonymity.go` already allows. The provenance gate stays: an operator-signed fetch reports `other` with no sequence.
  - The heartbeat never carries age, `generated_at`, `last_checked_at`, URL, host, reason strings or digests (FR-021, SC-006). The telemetry opt-out suppresses the counters like every other key.
  - The server-side freshness join (SC-006) lives in the receiver and is out of scope; T394 verifies the receiver accepts v16 and the new counters, live, before and after deploy (memory: verify-telemetry-in-browser).
- **B22. No new dependencies.** Stdlib `net/http` and `crypto` only.
- **B23. Spec 087 US3 (release awareness) is covered by Spec 079 and not touched.** `internal/updatecheck` already checks daily (24h default), surfaces `update` on `/api/v1/info`, the tray and the CLI, and respects the opt-outs. 087 FR-019's "distinct items" holds structurally: release status is on `/info`, bundle status on `/security/bundle`. 087 FR-018, FR-019, SC-007 and US3 AS1-AS3 map to Spec 079 with no US3 task.

### Spec amendments

Done in the PR that appends this section: the Spec 087 `spec.md` Status line and its short note. The rest is T300.

- **Spec 101 `spec.md`**:
  - SC-007 keeps only the manual-drop clause, with a pointer "fetch-cycle clause: Spec 087 SC-009".
  - FR-017's `[NEEDS CLARIFICATION]` becomes "Assumed (zero-interruption), pending maintainer confirmation: GitHub Releases on the corpus repo; per-sequence names are selection hints; integrity from the signed pair and watermark; signed index deferred (tasks B4). The publication half is not implemented until the corpus repo and first publisher key exist."
  - FR-016a(c) gains "Deviation (tasks Δ17/B14): the activation self-check is a parity-tested port in package scanner, because `cmd/scan-eval` is `package main`; a shared gate package is a follow-up."
  - FR-019 gains "Review surface: the per-tool scan verdict on the server review payload and the server's scan summary (tasks B18). A dedicated attention/review-queue kind for approved tools is an open maintainer decision. The cached scan leg runs only while `auto_baseline_scan` is on; when off, status shows reprotect suspended (tasks B17)."
- **Spec 087 `spec.md`**:
  - A "Mapping note" with the 087 table below; US3, FR-018/019 and SC-007 are "covered by Spec 079, unchanged".
  - Add **SC-009** (moved from Spec 101 SC-007): "A newly published signature reaches detection within one daily refresh cycle on a fetch-enabled install, measured end to end: publish → active → the new signature's fixture fires."
  - FR-013/FR-014: the trust set is Spec 101 FR-004/004b; no per-fetch key; an empty active trust set means zero requests plus a status/doctor configuration error.
  - FR-016: no refresh-enable key (the file leg is network-free and always on); env override covers fetch enable only.
  - Edge case "Hot-reload flips…": in-flight candidates under changed settings are `superseded`.
  - FR-017: surfaces are `/security/bundle`, `/security/overview`, CLI, doctor and Web UI, not `/info`.
- **Spec 087 `plan.md`**: banner "Superseded by specs/101-tpa-db/tasks.md US3 (Δ15–Δ31). The package, config block and file layout below were never built."

**Spec 087 FR/SC → task map**

| Spec 087 | Tasks |
|---|---|
| FR-001, FR-002, FR-005, FR-006, FR-007 | US1 (shipped) |
| FR-003 | T320, T321 |
| FR-004 | T327 (daily file pickup, manual refresh with fetch off), T370–T373 (manual surface) |
| FR-008 | T323, T340–T343, T365 (production wiring), T391 |
| FR-009 | T321, T325, T327 |
| FR-010 | T327 (single-flight), T329 (atomic, non-blocking) |
| FR-011 | US1, plus T321 (fetch leg idempotence) |
| FR-012 | T326 |
| FR-013, FR-014 | T310, T321, T326, T374 |
| FR-015 | T324, T325, T327 |
| FR-016 | T310–T313, T327 (hot reload) |
| FR-017 | T322, T370–T377 |
| FR-018, FR-019, SC-007, US3 | Spec 079 (B23) |
| FR-020, FR-022 | US1 (unchanged); FR-022 re-run in T393 |
| FR-021 | T341 (provenance guard e) |
| SC-001, SC-008 | US1 / CI (unchanged) |
| SC-002 | T327 |
| SC-003 | T321, T325, T391 |
| SC-004 | T340, T342, T391 |
| SC-005 | T326, T391 |
| SC-006 | T329 |
| SC-009 (new) | T391 |

**Spec 101 FR/SC → task map (US3 part)**

| Spec 101 | Tasks |
|---|---|
| FR-016a(c) | T340–T343 (deviation recorded, Δ17) |
| FR-017 consumer half | T324, T391 |
| FR-017 publication half | Out of scope (repo missing, publisher key set empty) |
| FR-018 | B7, B8; T327 |
| FR-019 | T361–T366, T391 |
| FR-020 | T322, T370–T377 |
| FR-021 | T350–T352 |
| FR-022 | T392 |
| SC-006 | Heartbeat tuple: US1; counters: T350; server-side age join: out of scope (receiver), checked by T394 |
| SC-007 | Manual-drop clause: US1 + T327; fetch clause: moved to 087 SC-009 → T391 |
| SC-009 | T362, T366, T391 |

---

## Wave 0: shared contract and spec amendments (sequential, one owner, no behaviour)

- [ ] T300 docs: apply the remaining amendments above to `specs/101-tpa-db/spec.md` (SC-007, FR-016a(c), FR-017, FR-019), `specs/087-tpa-daily-refresh/spec.md` (mapping note, SC-009, FR-013/014/016/017, edge case) and `specs/087-tpa-daily-refresh/plan.md` (superseded banner).
- [ ] T301 impl (stubs only, each in its final file; builds; all existing tests green):
  - `internal/config/config.go`: `SecurityConfig.TPARefreshInterval`, `TPAFetchEnabled`, `TPAFetchURL`, `TPAStaleAfterDays` (json/mapstructure tags); nil-safe accessors returning defaults `EffectiveTPARefreshInterval`, `IsTPAFetchEnabled` (no env yet), `EffectiveTPAFetchURL`, `EffectiveTPAStaleAfterDays`; const `EnvTPAFetchEnabled`; `tpaRefreshErrors()` returning nil, already appended in `ValidateDetailed`.
  - `internal/security/scanner/tpa_bundle_source.go`: `BundleSourceFetch` and the new `BundleInfo` fields (B15).
  - `internal/security/scanner/tpa_bundle_policy.go`: `bundlePolicy.FetchEnabled`, `FetchURL`, and `externalConfigured(policy, st)` returning `policy.Path != ""`.
  - `internal/security/scanner/tpa_bundle_runtime.go`: `runOptions.offered *Candidate` (ignored), `selection.offeredRej/offeredDigest`, and `runOffered(c Candidate, log) BundleInfo` that delegates to a normal run.
  - `internal/security/scanner/tpa_candidate.go`: `evalEnv.MustSign` (ignored).
  - New `internal/security/scanner/tpa_refresh_status.go`: `BundleRefreshStatus`, `BundleReprotectStatus`, outcome/reason/trigger constants, `RefreshOutcomeClass`, `SetReprotectStatus` (no-op).
  - New `internal/security/scanner/tpa_refresh.go`: `BundleRefresher`, `BundleRefresherOptions` (Clock, Rand, HTTPClient, Transport, Resolver, ConfigProvider, Logger, Version, IntervalOverride), `NewBundleRefresher`, `SetBundleRefresher`, `RefreshBundle(ctx, logger) BundleInfo` (falls back to `ReloadBundle`), `Start`, `Stop`, `Nudge`, `Running`, all no-op.
  - New `internal/security/scanner/tpa_fetch.go`: `fetchResult`, `fetchCandidate(ctx, settings) fetchResult` returning `disabled`, `egressAllowed(ip, configuredClass)` returning true.
  - New `internal/security/scanner/tpa_bundle_observer.go`: `BundleActivation`, `AddBundleActivationObserver` (no-op unsubscribe), `SetBundleRefreshObserver`, `drainBundleObserversForTest`.
  - New `internal/security/scanner/tpa_selfcheck.go`: `EnableActivationSelfCheck()` no-op, `activationSelfCheck` returning nil, `selfCheckCorpus` seam.
  - New `internal/security/scanner/tpa_bundle_doctor.go`: `BundleDoctorWarnings` returning nil.
  - `internal/security/scanner/service.go`: `Service.RefreshBundle(ctx)` (delegates) and `Service.StartScanFromCachedContracts` returning `errors.New("not implemented")`.
  - `internal/httpapi/security_scanner.go`: `RefreshBundle` on `SecurityController`; mock in `internal/httpapi/security_bundle_test.go`.
  - `internal/server/server.go`: `StartScanFromCachedContracts` on the `securityScannerService` interface; fake in `internal/server/scan_admission_test.go`.
  - New `internal/server/scan_bundle_reprotect.go`: `handleBundleActivation(BundleActivation)`, `runBundleReprotectCatchUp(ctx)`, `bundleReprotectWG`, all no-op.
  - New `internal/telemetry/tpa_bundle_refresh.go`: `TPABundleRefreshClass` constants, `RecordTPABundleRefreshOn(reg, class)` and `RecordTPABundleReprotectHitOn(reg)` no-op.
  - `internal/runtime/runtime.go`: field `bundleRefresher`, `SetBundleRefresher`, and a `closeOrderHook func(step string)` test seam. New `internal/runtime/tool_quarantine_held_refresh.go`: `RefreshHeldScanEvidence(server) error` returning nil.
  - `internal/storage/models.go` + `internal/storage/manager.go`: `BundleReprotectState`, `LoadBundleReprotectState`/`SaveBundleReprotectState` returning zero/nil.
- [ ] T302 verify (Wave 0 exit): `go build ./... && go build -tags server -o /dev/null ./cmd/mcpproxy && go vet ./... && go vet -tags server ./... && go test -count=1 -run '^$' ./...` (compile-only). Each Wave 1 group's first commit (its tests) must compile against these stubs; `go vet` on the group's packages is the per-group check.

## Wave 1: Phase-1 groups (parallel worktrees; disjoint files; each group writes its tests first)

### G1: Config (087 FR-013/014/016, B1, B2)
- [ ] T310 [P] test: create `internal/config/tpa_refresh_config_test.go`. Defaults (24h, fetch off, empty URL, stale 30); accessor nil-safety on a zero `Config`; env `MCPPROXY_TPA_FETCH_ENABLED` true/false overrides config and is not persisted by a save round-trip; `ValidateDetailed` rejects interval < 1h, `http://` on a non-loopback host, userinfo, query, fragment, fetch enabled with an empty URL, fetch enabled with root `operator` and no signers, negative stale days, with messages that never echo the URL; `Validate()` (boot) accepts all of them; explicit `tpa_stale_after_days: 0` survives a JSON round-trip. Fails on the stubs, which ignore env and validation.
- [ ] T311 [P] test: in `internal/oauth/configview_tpa_test.go` and `internal/oauth/configview_tpa_scope_test.go`, assert the four new keys survive `RedactedConfig` verbatim (a plain https URL is not masked) and round-trip GET→PATCH. In `internal/config/zero_value_preservation_test.go`, add the `numericOmitemptyAllowlist` entry `Config.Security.TPARefreshInterval` with a reason. In `internal/runtime/config_hotreload_test.go`, extend `TestDetectConfigChanges_TPATrustRoot` (or a sibling) so each new key reports `security` changed. No change to the nullable-bool fixture (no new `*bool`).
- [ ] T312 [P] impl: in `internal/config/config.go`, `tpaRefreshErrors()`, the env read inside `IsTPAFetchEnabled`, and the clamp in `EffectiveTPARefreshInterval`.
- [ ] T313 [P] docs: rows for the four keys and the env var, with the "no working default URL" and "fetch needs an operator root today" notes, in `docs/configuration.md` (sample JSON, table, prose).

### G2: Runtime core — external-source persistence, offered candidate, fetch adapter, scheduler, status (087 FR-003/004/009–015/017, B4–B12, B15, B19)
- [ ] T320 test: `internal/security/scanner/tpa_refresh_runtime_test.go`, the Hazard A regression. `newRTFixture` with `TPABundlePath=""`, `TPAActiveRoot=operator`, `TPAOperatorBaseline=u64p(1)`, fetch enabled; offer an operator-signed seq 2 candidate (`Source=fetch`) through `runOffered`. Assert it activates with one history row, then: (a) `ConfigureBundleFromConfig` again keeps it serving, `Active` unchanged, LKG files present; (b) `f.restart()` restores it from LKG; (c) disabling fetch keeps it serving (B9); (d) `rollback --embedded` returns to embedded, and the next offer of the rolled-away digest reports `deny_listed`/`unchanged` quietly (one log line total), with no reactivation and no observer fire. Fails today: the `Path != ""` guards deactivate the bundle and reconcile deletes its LKG.
- [ ] T321 test: same file, offered-run semantics.
  - The offered run never reads the file and never sets the file-leg `load_error`; its refusal is recorded once in `last_rejection`.
  - Direct-mode unsigned (`SidecarPresent=false`, `MustSign`) is refused as `signature_required` with `require_signed_bundle=false`.
  - An unsigned file-drop bundle is serving; a refused fetch offer leaves it serving (no `deactivated`).
  - Over-limit bodies give `ceiling_exceeded`.
  - An offered candidate equal to the active bytes gives `unchanged` with zero compiles (`countCompiles`); a refused pair offered again gives `unchanged` with zero compiles and no second log.
  - Fetch active plus a stale file: the file leg logs one `downgrade`; the fetched bundle keeps serving; fetch status is not masked.
  - Equal sequence, different digest on the fetch leg: `downgrade`.
  - A byte-identical fetched pair while the file copy is active: source stays `file`, no activation, no observer fire.
  - Pinned after rollback, a strictly newer seq offered: activates and clears the pin (US1 A4).
  - Operator fetch with no `tpa_operator_baseline`: activates degraded (`operator_authority_unbaselined`).
  - Parity (US3 AS1): the same signed pair via `runOffered` and via file drop gives the identical (digest, sequence, authority, signature_verified, coverage) tuple, differing only in source.
  - `BundleInfo.Source == "fetch"`, `Path` is host-only.
  - `policy.Fingerprint` changes when `FetchEnabled` or the URL changes, and not when the interval or stale threshold changes.
- [ ] T322 test: `internal/security/scanner/tpa_refresh_status_test.go`. `last_checked_at` advances on a `canSkipLocked` no-op; `last_activated_at` equals `Active.ActivatedAt` for an external bundle and is nil for embedded; `stale`/`stale_after_days` at read time with an injected clock (29/30/31 days, threshold 0 never stale, `generated_at` in the future never stale and no negative age); a `tpa_stale_after_days` edit applied through `ConfigureBundleFromConfig` changes `stale` on the next read without a pipeline rerun; `last_trigger` is `startup` for the first configure and `config` for a re-apply; refresh status merges into both `BundleStatus()` and `statusFromSnapshot()` without republishing (the `storeBundle` count is unchanged).
- [ ] T323 test: `internal/security/scanner/tpa_candidate_selfcheck_mode_test.go`, `withSelfCheck` counting calls: `evalNormal` 1, `evalRollback` 1, startup LKG restore 0, pin restore 0. Fails today: the hook runs in every mode.
- [ ] T324 test: `internal/security/scanner/tpa_fetch_test.go` (httptest, loopback http). Releases mode: one `scanner-bundle-<n>.json` plus `.sig` → candidate; zero, two, or a missing `.sig` asset → `channel_invalid`; asset seq at or below the watermark → `unchanged` with no download; ETag sent and `304` → `not_modified` with no download; the JSON body is capped at 1 MiB (oversize → `channel_invalid`). Direct mode: bundle plus `URL+".sig"`, `If-None-Match` on the bundle, `.sig` 404 → `SidecarPresent=false`. `User-Agent` and `Accept` present.
- [ ] T325 test: `internal/security/scanner/tpa_fetch_egress_test.go`.
  - `https` → `http` redirect refused; more than 5 hops refused.
  - Redirect from a public-configured host to loopback or link-local refused through the dial-control seam (injected resolver); link-local and metadata always refused; with `HTTPS_PROXY` set and an inward-resolving target, refused at the application layer.
  - Status mapping: `403`/`429`/`Retry-After`/`X-RateLimit-Remaining: 0` → `fetch_unavailable/rate_limited` and `next_check_at` moves to the reset (cap 24h); timeout → `timeout`; DNS failure → `network_error`; a trickled body (headers fast, body slow, fake clock) is cut by the 20s cycle deadline → `timeout`. None writes `last_rejection` or changes the watermark.
  - Host-only logging: a URL whose path is `/s3cr3t-path` never appears in zap observer logs, `BundleInfo`, `last_rejection` or error strings.
- [ ] T326 test: `internal/security/scanner/tpa_fetch_gate_test.go`. With fetch enabled, **zero requests** for: empty URL; invalid URL; root `publisher` with the empty publisher set (`no_trusted_signer`); root `operator` with empty `tpa_operator_signers`; a `loadFailed` store; and fetch disabled across a full simulated day of cycles.
- [ ] T327 test: `internal/security/scanner/tpa_refresh_scheduler_test.go`, fake clock and rand.
  - Cadence: the first cycle lands in `[30s, 5m]`; later cycles at `interval ± 10%`; a 10-day forward jump yields exactly one cycle; a backward jump does not wedge the timer; an interval change via `Nudge` re-arms it.
  - File leg (087 US1, FR-004, SC-002): a valid signed file dropped after `Start` activates on the next cycle with no restart (`source=file`, `last_trigger=timer`); `RefreshBundle` with fetch disabled activates a dropped file, returns the new `BundleInfo`, and issues zero HTTP requests; a refused dropped file sets `last_rejection` and the previous bundle keeps serving.
  - Single-flight: a manual `RefreshBundle` during an in-flight cycle joins it (one download, one activation); tick + `ReloadBundle` + `RefreshBundle` racing yield one activation per distinct candidate.
  - Mid-flight `Nudge` that changes the URL, disables fetch, changes the active root or operator signers, or flips `tpa_bundle_path` → `superseded`, no activation; a `Nudge` while `Stop` is in progress is a no-op.
  - `Stop` cancels in-flight HTTP and returns only after the goroutine exits; a later `Start` is a no-op; `RefreshBundle` on a stopped refresher falls back to `ReloadBundle`; `SetBundleRefresher(nil)` clears the slot.
  - `ReloadBundle` issues zero HTTP requests with fetch enabled (B20).
  - `consecutive_failures` increments on `fetch_unavailable`/`channel_invalid` and resets on `not_modified`, `unchanged`, `activated` and `rejected`; after a plain failure `next_check_at` equals the next jittered cycle.
  - Failure logs are deduplicated per (outcome, reason); the refresh observer receives exactly one class per cycle.
- [ ] T328 test: `internal/security/scanner/tpa_bundle_observer_test.go`. The activation observer fires once per history row (activation and rollback), not on an idempotent rerun, LKG restore, refusal or embedded fallback; it runs outside `bundleRT.mu` (an observer calling `BundleStatus()` does not deadlock; 10s timeout); unsubscribe stops delivery; `drainBundleObserversForTest` waits for in-flight dispatches.
- [ ] T329 test: `internal/security/scanner/tpa_refresh_concurrency_test.go` (087 FR-010, SC-006). Scans run in a loop through `ScanToolMetadataVerdict` while (a) the fetch leg is blackholed and (b) an activation runs with a gated, deliberately slow self-check hook. Every result is drawn from exactly the old or the new digest, and scan latency during the run stays within a relative factor of a steady-state baseline measured in the same test. The latency assertion is skipped under `-race`; the old-or-new assertion always runs.
- [ ] T330 impl:
  - `internal/security/scanner/tpa_bundle_policy.go`: fetch fields, `externalConfigured`, fingerprint.
  - `internal/security/scanner/tpa_bundle_runtime.go`: offered-only branch with the under-lock unchanged/negative-cache compare; `offeredRej` routing; the three guard sites (B9); `buildInfoLocked` source from `sv.cand.Source`; `last_checked_at`/`last_activated_at`; the watermark snapshot; observer fire after publish; display-settings refresh in `configureWithPolicy`.
  - `internal/security/scanner/tpa_candidate.go`: `MustSign` in `unsigned()`; self-check only for `evalNormal` and `evalRollback`; comment updated.
  - `internal/security/scanner/tpa_bundle_source.go`: read-time stale fields and the status merge.
  - `internal/security/scanner/tpa_bundle_rollback.go`: observer fire on rollback.
  - `internal/security/scanner/tpa_bundle_observer.go`, `internal/security/scanner/tpa_refresh_status.go`.
- [ ] T331 impl:
  - `internal/security/scanner/tpa_fetch.go`: mode detection, bounded reads, ETag cache, rate-limit parsing, redirect policy, dial-control and proxy-target predicate, host-only error unwrapping.
  - `internal/security/scanner/tpa_refresh.go`: scheduler, jitter, re-armed timer, generation counter, joinable in-flight cycle, file leg then fetch leg, Start/Stop/done, `RefreshBundle`, `Nudge`, `Running`, refresh observer.

### G3: Activation self-check (087 FR-008/FR-021, SC-004, 101 FR-016a(c), B13, B14)
- [ ] T340 [P] test: `internal/security/scanner/tpa_selfcheck_test.go`, one-rule regex `tool_description` bundles over the embedded corpus: catch-all `(?s).+` rejects (FP 21/21); `(?i)\bthe\b` rejects; `(?i)system prompt` rejects (2/21); `(?i)password|secret|token|credential` passes (1/21, boundary); `ZZZ-NEVER` passes and logs "recall leg inert" when the corpus has no `tpa_*` malicious entries, or rejects when it has some; the embedded default passes (`loadEmbeddedBundleCheck`). Through the `selfCheckCorpus` seam: a corpus with no gated malicious, one with no hard negatives, and an unparsable one all reject; `ChecksFailed > 0` rejects. The detail contains no candidate pattern text. Fails on the stub, which accepts everything.
- [ ] T341 [P] test: `internal/security/scanner/tpa_selfcheck_parity_test.go`, B14 guards (a), (b) tolerant to both gate.go shapes, (c), (e) and optional (d). Fails until the corpus copy exists. `TestSelfCheck*` names only.
- [ ] T342 [P] test: `internal/security/scanner/tpa_selfcheck_pipeline_test.go`, after `EnableActivationSelfCheck()` (restored on cleanup): a catch-all file drop through `ConfigureBundle(path)` gives `last_rejection.reason == gate_failure` while embedded keeps serving; the embedded bytes written as a file activate as `source=file`. Cost is asserted as "at most one self-check per distinct candidate" (counted), not by wall time. Real self-checks in the suite are kept to a handful. (The fetch-sourced catch-all case is in T391, since it needs G2.)
- [ ] T343 [P] impl: `internal/security/scanner/tpa_selfcheck.go` (port per B13; `selfCheck*` identifiers only; no `bundleRT`/`activeBundle*` access) and `internal/security/scanner/bundled/selfcheck-corpus.json` (byte copy of `specs/065-evaluation-foundation/datasets/detect_corpus_v1.json`, provenance and licence fields intact).

### G4: Telemetry v16 (101 FR-021, SC-006, B21)
- [ ] T350 [P] test:
  - `internal/telemetry/tpa_bundle_refresh_test.go` and `internal/telemetry/anonymity_test.go`: `SchemaVersion == 16`; the five counters accepted as non-negative ints and rejected as strings, negatives or floats; counters alone emit `tpa_scanner` (the `isZero` gap); `Drain`/`Reset` zero them and `HasPendingCounters` sees them; with telemetry disabled no counter or `tpa_scanner` key is sent; the heartbeat never contains age, URL, host or reason keys.
  - Bump every schema-15 pin to 16, found by `grep -rnE 'schema_version.*15|SchemaVersion.*15|want 15|\b15\b' internal/telemetry/*_test.go` (includes `payload_v2_test.go:92`'s message); these fail until T351 bumps the constant.
  - `internal/server/tpa_bundle_telemetry_test.go`: `source=fetch` with a publisher-verified bundle reports `fetch`; an operator-signed fetch reports `other` with no sequence.
  - Fails today: counters are unknown, the schema is 15, and fetch maps to `file`.
- [ ] T351 [P] impl: `internal/telemetry/anonymity.go` (append to `tpaScannerScalarKeys`); `internal/telemetry/registry.go` (fields, Record/RecordOn, snapshot, drain, pending, reset); `internal/telemetry/tpa_scanner.go` (struct fields and `isZero`); `internal/telemetry/tpa_bundle_refresh.go` (real recorders over the telemetry-local enum); `internal/telemetry/telemetry.go` (`SchemaVersion = 16` plus changelog); `internal/server/tpa_bundle_telemetry.go` (fetch mapping).
- [ ] T352 [P] docs: `docs/features/telemetry.md`: backfill the v15 `bundle_*` rows and add the v16 counter rows.

### G5: Server and runtime wiring, plus the re-protect sweep (101 FR-019, SC-009, B13, B16, B17, B19)
- [ ] T360 [P] test: `internal/runtime/bundle_refresher_lifecycle_test.go`. `SetBundleRefresher` then `StartBackgroundInitialization` starts it; `Close` joins it **before** `StopAsync` (via `closeOrderHook`) and clears the scanner slot; a `Close` racing a late `Start` leaves no goroutine (goroutine counter).
- [ ] T361 [P] test: `internal/runtime/tool_quarantine_held_refresh_test.go`, with the `rescanRuntime` fixtures of `tool_quarantine_auto_rescan_test.go`. `RefreshHeldScanEvidence` updates `HeldVerdict`/`HeldSignals` on `pending` and `changed` records from the newly published bundle (including a pending record whose stored verdict `tool_quarantine.go:1199` would skip); it never changes `Status`, `ApprovedHash`, `ApprovedBy` or `Quarantined`; a signature removed by a newer bundle leaves the record held with verdict `clean`/`awaiting_review`; approved records are untouched.
- [ ] T362 [P] test: `internal/server/scan_bundle_reprotect_test.go`, built from `newRescanTestServer` and `fakeSecurityScanner`, calling `handleBundleActivation` with a synthetic `BundleActivation` (no G2 dependency).
  - One `StartScanFromCachedContracts` per non-quarantined server with approved records; `RefreshHeldScanEvidence` for servers with held records, quarantined included; quarantined servers never get a scan job.
  - The same digest re-fired produces no job; a rollback activation produces exactly one more sweep.
  - Busy servers re-arm without spending a failure; failures cap at 3.
  - `auto_baseline_scan=false`: no scan jobs, leg (i) still runs, and `scanner.SetReprotectStatus` receives `suspended/auto_baseline_scan_off`.
  - No `EnsureConnected`, `RestartServer` or `GetServerTools`; no `ApproveServer` or `ApproveTools`.
  - Storm bounds: one sweep over N servers → at most one activity record per server and a bounded number of SSE events.
  - Startup catch-up runs when `BundleReprotectState.FullDigest` differs from the active digest and is skipped when equal; the marker is written only on a zero-failure completion.
  - `Shutdown` during a running sweep: the observer is unsubscribed, the sweep goroutine exits, and no store write happens after `runtime.Close` begins.
- [ ] T363 [P] test: `internal/security/scanner/service_cached_scan_test.go`. `StartScanFromCachedContracts` builds a job from the ingested approved contracts with `SourceMethod "tool_definitions_only"` and accurate `ToolContractDigests`; calls the server-info provider's `IsConnected`, `EnsureConnected` and `GetServerTools` **zero** times; schedules no Pass 2 with deep scan on; is not counted in `scans_completed`; a server with zero records reports unscanned, never clean.
- [ ] T364 [P] test: `internal/storage/bundle_reprotect_state_test.go`. Round trip; absent → zero value without creating the bucket; a corrupt value → error, not panic; the record is not touched by server purge.
- [ ] T365 [P] test: `internal/server/tpa_bundle_wiring_test.go`. After server construction: `bundleSelfCheck` is non-nil; a `config.reloaded` event reaches the refresher's `Nudge` (fake refresher); the doctor warning source is registered, and calling it on a fresh, never-published runtime returns within the timeout; the refresh observer increments the telemetry counters; the activation observer is registered at construction and unsubscribed on `Shutdown`. Fails on the W0 no-op wiring.
- [ ] T366 [P] test: `internal/server/scan_bundle_reprotect_rt02_test.go` (real runtime, real tool-approval store, real scanner `Service`, real `EventTypeSecurityScanSettled` handler). A non-quarantined server holds approved and held (`pending`, `changed`) tools; an unsigned file-drop bundle that removes the held tools' signature is activated with `ConfigureBundle`, then `handleBundleActivation` runs. Assert the held tools stay `pending`/`changed` with `ApprovedHash` unchanged, the server stays unquarantined, no `ApproveTools`/unquarantine/`ApproveServer` is called, and `maybeAutoApproveScanSettled`/`maybeCaptureReviewDefinitions` take no action on the cached job's settle event.
- [ ] T367 [P] impl: `internal/runtime/runtime.go` (refresher field, setter, `Close` join and slot clear), `internal/runtime/lifecycle.go` (Start in `StartBackgroundInitialization`), `internal/runtime/tool_quarantine_held_refresh.go` (`RefreshHeldScanEvidence`).
- [ ] T368 [P] impl: `internal/security/scanner/service.go`: `RefreshBundle` and `StartScanFromCachedContracts` (reuses `withDivergentIngestedContracts`, `toolDefinitionHashes`, `allServerToolsProvider`; a callback adapter that never starts Pass 2 and is marked non-telemetry).
- [ ] T369 [P] impl:
  - `internal/storage/models.go` and `internal/storage/manager.go`: real `BundleReprotectState` Load/Save, modelled on `BaselineSweepState`.
  - `internal/server/scan_bundle_reprotect.go`: handler, serial sweep, catch-up, `WaitGroup`, `SetReprotectStatus` calls.
  - `internal/server/server.go`: `EnableActivationSelfCheck()` next to `configureTPABundleRuntime`; refresher construction after `rt.SetVersion`; `Nudge` in `reapplyScannerSecurityConfig`; refresh observer → telemetry enum mapping; activation observer registration next to `registerToolChangeRescan`; catch-up next to `go s.runBaselineSweep(ctx)`; unsubscribe and `WaitGroup` wait in `Shutdown` before `s.runtime.Close()`; `AddRuntimeWarningSource(func() []string { return scanner.BundleDoctorWarnings(scanner.BundleStatus(), time.Now()) })`.
  - Run `scripts/check-upstream-write-sites.sh` and `scripts/check-no-unquarantine-callers.sh` (and their `.test.sh`) in the group.

### G6: Surfaces — REST, CLI, doctor and Web UI (101 FR-020, 087 FR-017, B15, B16, B20)
- [ ] T370 [P] test: `internal/httpapi/security_bundle_test.go`: `POST /api/v1/security/bundle/refresh` in the `bundleRoutes` table (agent token 403, admin 200 with `BundleInfo` including `refresh`, unauth 401); `GET /api/v1/security/bundle` and `GET /api/v1/security/overview` carry `last_checked_at`, `last_activated_at`, `stale`, `refresh` and `reprotect` from the mock. `internal/httpapi/agent_token_gating_test.go`: `bundle-refresh` in `mutatingServerRoutes`. The tenant walk covers the route automatically. Fails today with 404 and missing fields.
- [ ] T371 [P] impl: `internal/httpapi/server.go` (route next to `/bundle/reload` with `requireServerOp(auth.ServerOpBundleManage)`), `internal/httpapi/security_bundle.go` (`handleRefreshSecurityBundle` with swag annotations; GET `/security/bundle` and refresh annotated as `contracts.SuccessResponse{data=scanner.BundleInfo}`).
- [ ] T372 [P] test: `cmd/mcpproxy/security_bundle_cmd_test.go`. `security bundle refresh` POSTs `/bundle/refresh`; human output shows `Last checked`, `Last activated`, `Refresh: <outcome> (<reason>), next check …`, `Fetch host` when fetch is enabled, `Re-protection: <state>`, and a `STALE` line when `stale`; JSON/YAML pass the fields through; embedded with no refresh shows no stale line and no host line. Fails on the missing subcommand and lines.
- [ ] T373 [P] impl: `cmd/mcpproxy/security_cmd.go`: `newSecurityBundleRefreshCmd` registered next to reload (help text updated) and `signatureBundleLines` extended; one edit covers status, overview and mutation output.
- [ ] T374 [P] test: `internal/security/scanner/tpa_bundle_doctor_test.go`, the B16 truth table: stale external; stale embedded with fetch on/off; a stuck rejection newer than activation; the quiet set; ≥3 consecutive fetch failures; fetch enabled with `no_url`/`invalid_url`/`no_trusted_signer`; strings contain no hex digest, `/` path or host. `cmd/mcpproxy/doctor_cmd_test.go`: a runtime warning renders under Runtime Warnings and suppresses "All systems operational".
- [ ] T375 [P] impl: `internal/security/scanner/tpa_bundle_doctor.go`.
- [ ] T376 [P] test: `frontend/tests/unit/signature-bundle-refresh.spec.ts`: `formatSignatureBundle` emits "Last checked", "Last activated", "Refresh" and "Re-protection" rows next to Age; `refreshProblem` is non-null only for a B16-style condition and `degraded` stays coverage-only; `SignatureBundlePanelBody.vue` renders the rows; the panel has no mutation button and no `/unquarantine` or approve references. Fails on the current formatter.
- [ ] T377 [P] impl: `frontend/src/utils/signatureBundle.ts` (interface fields, rows, `refreshProblem`, outcome labels) and `frontend/src/components/security/SignatureBundlePanelBody.vue`.

## Wave 2: integration, docs, verification (sequential; after G1–G6 merge into the US3 branch)

- [ ] T390 impl: `make swagger` regenerates `oas/swagger.yaml` and `oas/docs.go` (config keys, refresh route, `BundleInfo`/`BundleRefreshStatus`/`BundleReprotectStatus` schemas); then `make swagger-verify`.
- [ ] T391 test: `internal/server/tpa_bundle_fetch_e2e_test.go`, end to end with production wiring (real self-check via `EnableActivationSelfCheck`, real refresher, real activation observer). A loopback httptest serves Releases-API JSON whose `browser_download_url`s point back at it, with an operator-signed seq baseline+1 pair adding one narrow, fixture-specific signature (≤1 FP over the 21 hard negatives, B13).
  - Fetch enabled, interval seam 100ms: activates within one cycle with `source=fetch`, `signature_verified=true`, `generated_at`, refresh `activated`; the signature's fixture fires through `ScanToolMetadataVerdict` (087 SC-009, 101 SC-007 fetch clause).
  - Re-protection (101 SC-009): an approved tool whose cached description matches the new signature gets a cached scan with a `dangerous` summary naming `tpa.<id>.*`, and its `scan_verdict` in `GetServerReview` reflects the finding; the record stays `approved`, ApprovedHash unchanged; the server is not quarantined; the fake upstream gets zero calls; `bundle_reprotect_hits` is 1.
  - A catch-all fetched pair: `gate_failure`, the previous bundle keeps serving (087 SC-004 at activation).
  - A tampered pair: `tamper`, LKG serving, no sweep fired.
  - The httptest server stopped: `fetch_unavailable`, bundle unchanged.
  - An unsigned direct-mode body: `signature_required`.
- [ ] T392 docs: `docs/features/tpa-bundle-signing.md` ("Daily refresh and optional fetch": keys, operator-root prerequisite, channel shapes, air-gapped drop of the downloaded pair, host-only logs, outcomes vs rejections, `refresh` vs `reload`, rollback for fetch-only installs, status-field and command table rows); `docs/features/security-quarantine.md` ("Re-protection after a bundle activates": cached-only, RT-02 invariants, `auto_baseline_scan` dependency, stdio limitation); `docs/features/tool-scanner.md` (the self-check gate); `docs/cli/security-commands.md` (`bundle refresh`); `specs/101-tpa-db/quickstart.md` (optional fetch step against a local mirror); `CLAUDE.md` Recent Changes. No new docs page, so no docs-site allowlist edit.
- [ ] T393 verify: `./scripts/test-api-e2e.sh`; `go test -race ./internal/security/... ./internal/config/... ./internal/telemetry/... ./internal/runtime/... ./internal/storage/...`; the CLAUDE.md `-tags server -skip` race run over `internal/server`, `httpapi`, `storage`; `go test -tags server ./internal/serveredition/... -race`; golangci-lint v2 bare and with `--build-tags server`; `scripts/check-upstream-write-sites.sh` and `scripts/check-no-unquarantine-callers.sh`; `cd frontend && npm run test:unit`; the Web UI Playwright sweep per `docs/development/web-ui-verification.md`.
- [ ] T394 verify: telemetry receiver and dashboard (repo-external). Before merge, confirm the receiver accepts schema 16 and the five counters (staging or a dry-run payload) and load the live dashboard before and after deploy. If the receiver needs a change, it is a separate receiver PR that blocks this merge. The server-side SC-006 age join stays out of scope.
- [ ] T395 review: review workflow, then cross-model review (≤ 10 rounds; chunked, file-named briefs). The concurrency (`tpa_refresh.go`, runtime offered branch), egress (`tpa_fetch.go`) and RT-02 (`scan_bundle_reprotect.go`, held refresh) diffs go to the hard-problem reviewer.
- [ ] T396 docs: open the PR (`Related #` issue refs, one per issue). The body checklist: (1) the B14 merge-order gate (copy refreshed here if US2 merged first; otherwise a comment on #1702); (2) the empty publisher key set and "fetch works today only with operator-signed bundles"; (3) zero-interruption assumptions needing maintainer sign-off: Δ30 `superseded`, Δ19 status off `/info`, B9 keep-on-disable, B13 `evalLKG` skip, B17 `auto_baseline_scan` gating, B18 no attention kind, Δ17 FR-016a(c) deviation, Δ22/FR-017 channel. Tick this file.

---

### Out of scope (recorded so nothing is silently dropped)

- Creating the `smart-mcp-proxy/tpa-db` corpus repository, its release and signing workflow, and the "refuse to replace an existing asset" automation (research D1): the publication half of 101 FR-017.
- Shipping the first publisher key or bumping `publisherTrustEpoch` (US1 A1). This is a binary-release event.
- Research D1's **signed release index**, content-addressed asset names and a stable cacheable index URL. US3's integrity does not depend on them (B4); they become necessary for freeze or selection protection with expiry.
- A compiled-in default fetch URL (B3).
- A dedicated attention or review-queue kind for "approved tool now matches signature X" (open maintainer decision; needs the Spec 109 parity matrix, Swift and Web; B18). Diffing "new" against the previous bundle's hits (B17).
- The receiver-side SC-006 freshness join (publication date × reported version/sequence); only its acceptance of v16 is checked (T394).
- A Web UI "Check now" button and an SSE event for bundle changes; panels keep the 30s poll.
- Persisting `last_rejection`, refresh status or ETags across restarts (restart-scoped; re-established by the first jittered cycle).
- Extracting a shared SSRF leaf package from `internal/registries` (B11 duplicates the predicate minimally).
- Moving the gate into a shared importable package used by both `cmd/scan-eval` and the scanner (needs `cmd/scan-eval` edits after US2); until then Δ17 is a recorded deviation from 101 FR-016a(c).
- Re-running `RefreshPrompts` on activation (an existing drop-gate; needs its own decision).
- Spec 087 US3 / FR-018 / FR-019 / SC-007 release awareness: already delivered by Spec 079 (B23).
- Settings UI or native catalogue entries for the new keys (consistent with the existing `tpa_*` keys: config file and CLI only).

### Phase 1 groups

| Group | Tasks | Files owned (exclusive in Wave 1) | Depends on |
|---|---|---|---|
| W0 shared contract | T300–T302 | `specs/101-tpa-db/spec.md`, `specs/087-tpa-daily-refresh/{spec,plan}.md`; the stub hunks and new stub files listed in T301 | — |
| G1 Config | T310–T313 | `internal/config/config.go` (validation and accessors), `internal/config/tpa_refresh_config_test.go`, `internal/config/zero_value_preservation_test.go`, `internal/oauth/configview_tpa_test.go`, `internal/oauth/configview_tpa_scope_test.go`, `internal/runtime/config_hotreload_test.go`, `docs/configuration.md` | W0 |
| G2 Runtime core and fetch | T320–T331 | `internal/security/scanner/{tpa_bundle_runtime.go, tpa_bundle_policy.go, tpa_bundle_source.go, tpa_candidate.go, tpa_bundle_rollback.go, tpa_bundle_observer.go, tpa_refresh_status.go, tpa_refresh.go, tpa_fetch.go}` and new tests `tpa_refresh_runtime_test.go`, `tpa_refresh_status_test.go`, `tpa_candidate_selfcheck_mode_test.go`, `tpa_fetch_test.go`, `tpa_fetch_egress_test.go`, `tpa_fetch_gate_test.go`, `tpa_refresh_scheduler_test.go`, `tpa_bundle_observer_test.go`, `tpa_refresh_concurrency_test.go` | W0 (env-driven cases use G1's accessor behaviour only after merge; inside G2 they set config fields directly) |
| G3 Self-check | T340–T343 | `internal/security/scanner/{tpa_selfcheck.go, tpa_selfcheck_test.go, tpa_selfcheck_parity_test.go, tpa_selfcheck_pipeline_test.go}`, `internal/security/scanner/bundled/selfcheck-corpus.json` | W0 |
| G4 Telemetry v16 | T350–T352 | `internal/telemetry/{anonymity.go, registry.go, tpa_scanner.go, telemetry.go, tpa_bundle_refresh.go}` and their `*_test.go` (incl. every schema-pin file), `internal/server/tpa_bundle_telemetry{,_test}.go`, `docs/features/telemetry.md` | W0 |
| G5 Wiring and re-protect | T360–T369 | `internal/runtime/{runtime.go, lifecycle.go, tool_quarantine_held_refresh.go}` and their new tests, `internal/security/scanner/service.go` and `service_cached_scan_test.go`, `internal/storage/{models.go, manager.go, bundle_reprotect_state_test.go}`, `internal/server/{server.go, scan_bundle_reprotect.go, scan_admission_test.go, scan_bundle_reprotect_test.go, scan_bundle_reprotect_rt02_test.go, tpa_bundle_wiring_test.go}` | W0 (T362/T366 call the handler directly; the observer-driven end to end is T391) |
| G6 Surfaces | T370–T377 | `internal/httpapi/{server.go, security_bundle.go, security_bundle_test.go, agent_token_gating_test.go}`, `cmd/mcpproxy/{security_cmd.go, security_bundle_cmd_test.go, doctor_cmd_test.go}`, `internal/security/scanner/tpa_bundle_doctor{,_test}.go`, `frontend/src/utils/signatureBundle.ts`, `frontend/src/components/security/SignatureBundlePanelBody.vue`, `frontend/tests/unit/signature-bundle-refresh.spec.ts` | W0 |

Wave 2 (T390–T396) runs after all six groups merge into the US3 branch: `oas/swagger.yaml` and `oas/docs.go` are regenerated once there, and T391 is the first test that needs G2, G3 and G5 together.
