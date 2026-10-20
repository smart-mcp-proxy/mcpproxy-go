# Tasks: Spec 101 tpa-db — User Story 1 (signed format + downgrade-proof loader)

**Input**: `/specs/101-tpa-db/` (spec.md, plan.md, research.md, data-model.md, contracts/, quickstart.md)
**Scope of this file**: User Story 1 only, one PR against `main`. US2 (seed corpus, `signatures[]`, `cmd/scan-eval --bundle`, contributor docs) and US3 (publication channel, Spec 087 fetch/tick, post-activation re-scan) are listed at the end as **out of scope** so nothing is silently dropped.

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
- [ ] T082 Cross-model review (opencode Sol 6.1; ≤10 rounds).
- [x] T083 Docs: `docs/features/tpa-bundle-signing.md` (operator: verify, drop, reload, roll back, read status, air-gapped walkthrough, Windows/NFS residual), `docs/features/security-quarantine.md` + tool-scanner docs (degraded states), `docs/configuration.md` (new keys), quickstart.md corrections (Δ5, Δ8, Δ13, sequence ≥ baseline), `CLAUDE.md` Recent Changes, `roadmap.yaml` + `scripts/gen-roadmap.py`.
- [ ] T084 Open the PR; tick this file.

---

## Out of scope for this PR

- **US2 (sibling session)**: `signatures[]` section parsing and integrity (D4, `contracts/bundle-signatures-section.md`); the seed corpus in `internal/security/scanner/bundled/scanner-bundle.json` (**including stamping its `sequence`/`trust_epoch`/`generated_at`** — handed over); `cmd/scan-eval --bundle` + `BundleCheck` registration + `categoryCheck` entries + D9 negative control; `cmd/tpa-db validate` and gate-before-sign; contributor docs; `.github/workflows/eval.yml`.
- **US3 / Spec 087**: publication channel (D1), network fetch + daily tick + single-flight lifecycle, the real activation self-check, post-activation re-scan (FR-019), fleet-freshness dashboards (SC-006/SC-007 fetch clause), shipping the first publisher key in a release.
