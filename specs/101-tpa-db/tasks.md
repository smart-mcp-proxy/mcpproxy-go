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
- **US3 / Spec 087**: publication channel (D1), network fetch + daily tick + single-flight lifecycle, the real activation self-check, post-activation re-scan (FR-019), fleet-freshness dashboards (SC-006/SC-007 fetch clause), shipping the first publisher key in a release.

---

<!-- US2 section added from branch claude/101-tpa-db-corpus-* (PR #1702). Numbering is disjoint: US1 uses T001-T199, US2 uses T200 and up. -->

## User Story 2 — Seed corpus + eval gate (P2)

**Goal**
- The embedded bundle carries at least 25 gating signatures across at least 8 classes. Each signature has provenance, a redistributable licence and an eval pair.
- The bundle is built deterministically in-repo from per-signature sources.
- `scan-eval --gate` scores it through the production loader, and a deterministic negative control proves the gate is not vacuous (SC-004, SC-005 CI half, FR-001a, FR-012 to FR-016b).

**Independent test**
- `go run ./cmd/tpa-corpus verify` exits 0.
- `go run ./cmd/scan-eval --corpus specs/065-evaluation-foundation/datasets/detect_corpus_v1.json --gate --min-recall 0.90 --max-fp 0.05` passes, with `tpa.bundle` in `checks`.
- `go test ./cmd/scan-eval/ -run TestBundleControl` shows that removing the control signature exits `6`.

### Decisions (US2)

**D-US2-1 — How rules are addressed.**
- `rules[].id` stays equal to the owning `TPA-YYYY-NNNN` id. Each `signatures[].rule_ids` entry is the string `"<TPA-id>/<detector>"`.
- The loader enforces all of the following:
  - `(id, detector)` is unique.
  - Every `rule_ids` entry resolves.
  - The id part of every `rule_ids` entry equals the owning signature's `id`.
  - Every `rules[]` entry is owned by exactly one signature.
- *Rationale:* the CheckID `tpa.<TPA-id>.<detector>` is left unchanged. The CLI HELD column (`cmd/mcpproxy/tools_cmd.go:557`), the Web UI parser (`frontend/src/utils/holdEvidence.ts:71`) and the runtime prefix tests all depend on that shape. `<id>/<detector>` is already the loader's error format (`tpa_bundle.go`, `compile rule %s/%s`). The contract example `r-hidden-important-block` would have broken all of them.

**D-US2-2 — How "gating" is derived.**
- A rule is gating if and only if it is **runnable**, meaning `engine=="regex"` and `target=="tool_description"`.
- A signature must declare `gating: true` exactly when it owns at least one runnable rule. A mismatch in either direction rejects the bundle.
- A gating signature may also own non-runnable companion rules, such as the `structural_diff` rules of 0001 and 0006.
- `level` remains a severity label only.
- *Rationale:* `BundleCheck.Inspect` hard-codes `detect.TierHard` for every runnable rule (`tpa_bundle.go:170`). Deriving gating from `level`, as contract §4 rule 2 says, would therefore mean nothing. Out of Scope forbids tier changes, so the derivation has to follow what the loader really does. Today's 0002 (`resource_content` only) becomes the one non-gating signature.

**D-US2-3 — Where integrity checks live (one implementation, two layers).**
- **Load layer.** File: `internal/security/scanner/tpa_bundle_signatures.go`.
  - `validateSignatures` is called from `loadBundleCheck` after the zero-runnable check. A failure rejects the **whole** candidate, as FR-001a requires. `ConfigureBundle` then keeps the last-known-good bundle and records `LoadError`.
  - It runs only when `signatures[]` is present, so v0.1 drops and every existing fixture keep loading (additive).
  - It enforces:
    - id grammar and uniqueness;
    - D-US2-1 and D-US2-2;
    - `rules[]` sorted by `(id, detector)`;
    - `signature_count == len(signatures)`;
    - non-empty `provenance.source`, `provenance.published` (format `YYYY-MM-DD`) and `license`;
    - every `skipped[].id` resolves to a signature.
- **Policy layer, build and CI only.** File: `internal/security/tpacorpus/policy.go`. It enforces:
  - the licence allowlist;
  - the class vocabulary;
  - the eval pair (contract rule 5);
  - category registration (rule 6, closed by a scan-eval test).
- *Rationale:* rule 6 depends on `categoryCheck`, which lives in `package main`. Enforcing the SPDX allowlist at runtime would also refuse FR-004a operators who build private corpora.
- **Shared seam.** File: `internal/security/scanner/tpa_bundle_eval.go`. It exports `LoadBundleCheck(data []byte) (*BundleCheck, BundleInfo, error)`, which is pure and touches no `ConfigureBundle` state, plus `EmbeddedBundle() []byte` and `ParseBundleSignatures(data) ([]Signature, error)`.
  - The builder, `scan-eval --bundle` and (US1/087) the activation self-check all call it.
  - It goes in a new file so it does not collide with US1's edits to `tpa_bundle_source.go`.

**D-US2-4 — Sources and the deterministic build.**
- **Sources.** One JSON file per signature in `internal/security/tpacorpus/signatures/TPA-YYYY-NNNN.json`. Each file holds the signature metadata, its rules and its skipped entries.
- **Manifest.** `internal/security/tpacorpus/signatures/manifest.json` holds:
  - `bundle_version`;
  - `schema_version`;
  - `source_date_epoch`;
  - a `header` object of extra top-level keys copied through verbatim. US1 puts `sequence`, `trust_epoch` and similar keys there without touching the builder.
- **Builder.** The library is `internal/security/tpacorpus`. The CLI is `cmd/tpa-corpus`, with the subcommands `build`, `verify` (rebuild and byte-compare) and `validate --bundle --dataset`.
  - It sorts `rules[]` by `(id, detector)` and `signatures[]` by id.
  - `generated_at` comes from `SOURCE_DATE_EPOCH` when that is set, and otherwise from the manifest pin. Wall-clock time is never used (FR-002).
  - It encodes with stdlib `encoding/json`, `SetIndent("", "  ")`, `SetEscapeHTML(false)` and a trailing LF. This matches today's file byte-for-byte.
  - It adds no new dependencies and does not sign; signing is US1's job.
- *Rationale:* `cmd/tpa-db` (keygen and sign) belongs to US1, so a separate command avoids a `main.go` conflict. `verify` in CI makes a hand-edited `scanner-bundle.json` fail. The `spec.md` Assumption that "the pipeline is outside the repo" is amended in T206.

**D-US2-5 — Class vocabulary.**
- `signatures[].category` is a snake_case slug with the prefix `tpa_`. Each class gets a **static** entry `categoryCheck["tpa_<class>"] = "tpa.bundle"`.
- The prefix avoids collisions with the built-in categories `shadowing` and `phrase_injection`.
- The vocabulary lives once, in `tpacorpus.Classes`. The scan-eval test and the dataset validator both assert against it.
- Final classes (16, from the Phase 1 research; the assignment of TPA ids to verified catalog entries is in `specs/101-tpa-db/corpus-research/assignment.json`): `tpa_hidden_instruction_block`, `tpa_comment_directive`, `tpa_tool_shadowing`, `tpa_cross_server_override`, `tpa_exfil_redirect`, `tpa_sensitive_file_coaxing`, `tpa_credential_harvesting`, `tpa_persistence_implant` (holds the control), `tpa_rug_pull_phrasing`, `tpa_instruction_override`, `tpa_conversation_exfil`, `tpa_preference_manipulation`, `tpa_concealment_directive`, `tpa_invisible_text`, `tpa_payload_execution`, `tpa_schema_injection`. The planned `tpa_markdown_image_beacon` was dropped: no verifiable MCP-specific primary source.
- `rules[].category` keeps its v0.1 meaning, which is the threat-type mapping. `bundleCategoryToThreatType` gains one case, `"exfiltration"` → `detect.ThreatExfiltration`, so exfil classes do not fall into `uncategorized`.
- No class name may be a prefix of another class name.

**D-US2-6 — Where eval samples go.**
- The samples extend `specs/065-evaluation-foundation/datasets/detect_corpus_v1.json` **in place**.
- *Rationale:* this follows the Spec 077 T016 precedent. No test pins the file's hash or count, and the edit keeps `eval.yml`, `release-qa-gate.yml`, `gate_test.go:240`, `coverage_corpus_test.go:13`, the docs and the spec's own Independent Test valid without repointing them. The README's immutability note is amended to say so.
- Bundle-class malicious entries gain an optional `tpa_id` field. Exactly one entry per class also carries `"canonical": true`. Unknown keys are ignored by every decoder.
- Hard negatives use the id prefix `hn_<class>_`, for example `hn_tpa_exfil_redirect_01`, with `resembles: "<class>"`. `hardNegPrefix` is derived from `tpacorpus.Classes`. FR-013 and AC-4 are reworded to "the prefix the validator registers for the category".
- New hard negatives must stay at **zero** hard findings under the production engine, not merely 5%. `TestCoverage_BenignNotHardBlocked` (`coverage_corpus_test.go:126`) hard-fails on any benign entry the bundle blocks.

**D-US2-7 — The D9 control.**
- The control signature is the new TPA-2026-0044 in class `tpa_persistence_implant` (agent-config persistence). Its sample is an instruction to append to `CLAUDE.md`/`.cursor/rules`.
  - It contains no override phrase, exfil verb, secret noun, URL, credential file, hidden rune or blob.
  - It matches none of the existing rules.
- The control corpus is `cmd/scan-eval/testdata/bundle_control_corpus.json`. It contains:
  - 1 malicious entry (`tpa_id` = control, `peers: []`, plain schema, ASCII only);
  - at least 1 `hn_tpa_persistence_implant_*` hard negative;
  - 1 benign entry.
  - With this corpus, `GatedMalicious == 1`: recall is 1.0 with the control signature and 0.0 without it. The run exits `6`, never `4`.
- The reduced bundle is built by `tpacorpus.Build(..., Options{Exclude: []string{controlID}})`. That drops the signature and its rules and recomputes `signature_count`, so the reduced bundle still passes the load layer.
- No-overlap is asserted twice:
  - against the gate built-ins (`builtinGateChecks()`);
  - against the production `baselineEngineChecks()`. With the full bundle, the only `tpa.` signal must be `tpa.<controlID>.*`.
- Identical patterns cannot serve as a control. `0004.shadow_directive` and `0005.cross_shadow` share one regex, and `0001` co-fires `0003`.

**D-US2-8 — `eval.yml`.**
- The gate command stays as it is, because `--bundle` defaults to the embedded bundle and the existing step now scores it.
- Add the path filter `cmd/tpa-corpus/**`.
- Add a blocking step that runs `go run ./cmd/tpa-corpus verify` and `go test -count=1 -run 'TestBundleControl|TestCategoryCheck|TestSC004|TestEmbeddedCorpus' ./cmd/scan-eval/ ./internal/security/scanner/ ./internal/security/tpacorpus/`.
- Update the job comments. `release-qa-gate.yml` needs no edit, because it inherits the embedded default.

**D-US2-9 — SC-004 test.**
- File: `internal/security/scanner/tpa_corpus_sc004_test.go`.
- It checks the embedded bundle has at least 25 **gating** signatures and at least 8 classes, each class with at least 1 gating signature. A non-gating signature such as 0002 cannot pad the count.
- It checks that every signature has provenance and a licence on the allowlist.
- For each class, it loads the `canonical: true` dataset entry and runs `inProcessToolScan`, which is the production path. It asserts a hard finding whose `Signals` contain the prefix `tpa.<that signature's id>.`. A hard tier reached only through `phrase.injection` does not count.

**D-US2-10 — Hunks that conflict with US1** (rebase onto whichever side lands first)
- **`tpa_bundle.go`:**
  - `rawBundle`: add the field `Signatures` (US1 adds manifest fields).
  - The tail of `loadBundleCheck`: one `validateSignatures` call just before `return` (US1 inserts ceilings and the epoch check between parse and version check).
  - `bundleCategoryToThreatType`: one new case.
  - US2 does **not** touch `bundleMeta`/`bundleMetadata`.
- **`scanner-bundle.json`:** the whole file is regenerated. If US1 lands header keys first, port them into the manifest `header`, then run `build`.
- **`tpa_bundle_test.go` / `tpa_bundle_source_test.go`:** both stories edit the pinned assertions.
- **`tpa_bundle_source.go` and `inprocess.go` (`coverageOK`):** US2 makes **no** edits.

**Licence allowlist (policy layer):** `CC0-1.0`, `CC-BY-4.0`, `MIT`, `Apache-2.0`.
- `CC-BY-SA-4.0` is dropped, because its share-alike terms could attach to the MIT-licensed binary that embeds it. The maintainer can re-add it in the contract.
- Patterns written by the project are `MIT` (the repo `LICENSE`).
- `provenance.source` is the factual public disclosure. Its licence is not the signature's licence.
- Dataset `provenance.license` stays on its own allowlist, where `self-authored` remains valid.

**Version:** the embedded bundle moves to `bundle_version` `0.1.1`. This is a patch bump for the additive `signatures[]`, so the loader's `0.1` gate still accepts it.

### Foundations

- [x] T200 [P] Write a failing table test in `internal/security/scanner/tpa_bundle_signatures_test.go`.
  - **Reject cases.** One case per rule in D-US2-1, D-US2-2 and D-US2-3. Each asserts its own error substring.
  - **Fail-closed through `ConfigureBundle`.** The last-known-good bundle keeps serving and `LoadError` is set.
  - **Accept cases.**
    - A valid bundle with `signatures[]` loads.
    - A v0.1 bundle without `signatures[]` loads unchanged. Reuse the fixtures in `tpa_bundle_source_test.go` and `tpa_bundle_empty_test.go`.
- [x] T201 Implement `internal/security/scanner/tpa_bundle_signatures.go` (`rawSignature`, `rawProvenance`, `validateSignatures`).
  - In `internal/security/scanner/tpa_bundle.go`, add the `Signatures []rawSignature` field to `rawBundle`.
  - Also in `tpa_bundle.go`, add one `validateSignatures` call before the final `return` of `loadBundleCheck`.
  - Makes T200 pass.
- [x] T202 [P] Write a failing test, `internal/security/scanner/tpa_bundle_eval_test.go`.
  - `LoadBundleCheck(EmbeddedBundle())` returns the same runnable count and fingerprint as `BundleStatus()`.
  - Calling it on bad bytes leaves `BundleStatus()` unchanged.
  - `EmbeddedBundle()` returns a copy, so mutating the result does not change the embed.
  - `ParseBundleSignatures` round-trips `rule_ids`.
- [x] T203 Implement `internal/security/scanner/tpa_bundle_eval.go` (`LoadBundleCheck`, `EmbeddedBundle`, `Signature`, `ParseBundleSignatures`), wrapping `loadBundleWithInfo` without editing it. Coordinate the exported name with US1's `tpa_candidate.go` (D11).
- [x] T204 [P] Add a failing case to `internal/security/scanner/tpa_bundle_test.go`: a rule with category `exfiltration` maps to `detect.ThreatExfiltration`. Then add the case to `bundleCategoryToThreatType` in `tpa_bundle.go`.
- [x] T205 Amend `specs/101-tpa-db/contracts/bundle-signatures-section.md`:
  - the `rule_ids` format (D-US2-1);
  - the gating derivation (D-US2-2);
  - the split between the load layer and the policy layer (D-US2-3);
  - the licence allowlist;
  - the `hn_<class>_` naming;
  - §6: `signature_count` is `len(signatures)`, which equals distinct TPA ids. It is not the rule count; today's file holds 6 and 10.
- [x] T206 [P] Amend the remaining spec docs:
  - `specs/101-tpa-db/spec.md`: the Assumption that the pipeline is outside the repo becomes "in-repo deterministic builder, signing external/US1". Reword FR-013 and AC-4 to the registered prefix, and FR-013's "frozen dataset" to "extended in place".
  - `specs/101-tpa-db/data-model.md` §7.
  - `specs/101-tpa-db/quickstart.md` §8: `go run ./cmd/tpa-corpus validate`.
  - The source tree in `specs/101-tpa-db/plan.md`: `internal/security/tpacorpus/`, `cmd/tpa-corpus/`.

### Corpus

- [x] T207 Research. Append section "R12 — Seed corpus classes and sources" to `specs/101-tpa-db/research.md`. It must contain:
  - The final class list (at least 8) and the planned signatures 0007 onward (at least 25 gating in total). Each needs a **verified** public disclosure URL and date. Do not use root or blog-index URLs, and do not invent URLs; unverifiable sources are dropped.
  - The licence for each signature.
  - The control designation (D-US2-7).
  - A pre-check of every planned regex against the hard families in `phrase_injection.go`. The aim is to label each signature as built-in-overlapped or bundle-only, not to avoid overlap except for the control.
  - The note that IoC-literal patterns (`attkr@pwnd.com`, `+13241234123`) are narrow, campaign-specific signatures. New classes use general phrasing.
  - Source material: `tests/tpa-attack-lab/catalog.go` (V01–V19, M01–M06). RT-01..07 are mostly not description classes. Do not mine `docs/research/.../redteam-findings.md` for coverage claims, because it predates `a857dbafe`.
- [x] T208 [P] Write a failing test, `internal/security/tpacorpus/build_test.go`, using an `fstest.MapFS`. It must cover:
  - two builds are byte-equal;
  - `SOURCE_DATE_EPOCH` overrides the manifest pin, and `generated_at` is RFC 3339 UTC;
  - `<IMPORTANT>` is not escaped;
  - `rules[]` and `signatures[]` come out sorted;
  - the manifest `header` keys pass through;
  - `Options.Exclude` drops the signature and its rules and fixes `signature_count`;
  - the output passes `scanner.LoadBundleCheck`;
  - the build refuses a source that is missing provenance or a licence, has a licence outside the allowlist, or has an unknown class.
- [x] T209 [P] Write a failing test, `internal/security/tpacorpus/policy_test.go`, for `ValidatePolicy(bundle, dataset []byte)`:
  - each gating signature has at least 1 malicious entry with a matching `tpa_id` and category;
  - each gating signature has at least 1 hard negative with `resembles == category` and prefix `hn_<class>_`;
  - exactly one `canonical` entry per class;
  - every `tpa_id` resolves;
  - no class name is a prefix of another.
- [x] T210 Implement `internal/security/tpacorpus/` (`classes.go`: `Classes`, `LicenceAllowlist`; `build.go`: `Build(fs.FS, Options)`; `policy.go`: `ValidatePolicy`). Use stdlib only and add no dependencies. Makes T208 and T209 pass.
- [x] T211 Implement `cmd/tpa-corpus/main.go`, with subcommands `build` (writes `internal/security/scanner/bundled/scanner-bundle.json`), `verify` (rebuild and compare, exit non-zero on drift) and `validate --bundle <path> --dataset <path>` (load layer plus policy layer). `cmd/tpa-corpus/main_test.go` covers the exit codes, test first.
- [x] T212 Migrate the six existing signatures:
  - Create `internal/security/tpacorpus/signatures/manifest.json` and `TPA-2026-0001.json` to `TPA-2026-0006.json`.
  - 0002 is `gating:false`.
  - 0003 is re-classed to `tpa_sensitive_file_coaxing` in `signatures[]` only.
  - Existing `rules[]` and `skipped[]` stay byte-identical.
- [x] T213 Write a failing test, `internal/security/tpacorpus/embedded_test.go` (`TestEmbeddedCorpus_*`):
  - rebuilding from `signatures/` with the manifest pin equals `scanner.EmbeddedBundle()` byte for byte;
  - the embedded bundle passes `ValidatePolicy` against `detect_corpus_v1.json`;
  - the 10 v0.1 rules are present unchanged, so the CheckIDs that attack-lab and the runtime tests pin stay stable.
- [x] T214 Author the new signature sources `internal/security/tpacorpus/signatures/TPA-2026-0007.json` and onward, per T207, including the control.
  - Every regex is inline `(?is)`, `regex` × `tool_description`, and RE2-safe.
  - Before committing each regex, run `go test ./internal/security/scanner/ -run 'TestSEC01_RealWorldBenignCorpora_NoNewHolds|TestSEC01_VariantCorpus|TestCoverage_BenignNotHardBlocked'`.
  - If a regex causes a regression, narrow it. **Never re-baseline** `testdata/sec01_realworld_baseline.json`.
- [x] T215 [P] Write a failing test by extending `specs/065-evaluation-foundation/datasets/detect_corpus_test.go`:
  - derive the bundle classes in `validDetectCategory`, `gatedDetectCategories` and `hardNegPrefix` from `tpacorpus.Classes`;
  - schema checks for `tpa_id` and `canonical`;
  - no `hardNegPrefix` value is a prefix of another.
- [x] T216 Add the eval pairs to `specs/065-evaluation-foundation/datasets/detect_corpus_v1.json`:
  - at least 1 malicious entry per gating signature (`tpa_id`, class category, `self-authored`);
  - at least 2 `hn_<class>_*` hard negatives per class (security tooling, docs that quote attacks);
  - one `canonical` per class.
  - Update `specs/065-evaluation-foundation/datasets/README.md`: the composition table, which is stale (it says 32 and the file has 57), and the in-place-extension note.
- [x] T217 Regenerate the bundle with `SOURCE_DATE_EPOCH=<pin> go run ./cmd/tpa-corpus build`, setting `bundle_version` to `0.1.1`. T213 and T215 now pass, and `go run ./cmd/tpa-corpus verify` exits 0.
- [x] T218 Write `internal/security/scanner/tpa_corpus_sc004_test.go` (`TestSC004_*`), as specified in D-US2-9.

### Gate

- [x] T219 [P] Write a failing test, `cmd/scan-eval/bundle_gate_test.go`. It must cover:
  - with no `--bundle`, the metrics `checks` contain `tpa.bundle` and a new `bundle` block carries the fingerprint, runnable rules and signature count;
  - `--bundle <missing>` exits `4`;
  - `--bundle <file that fails validation>` exits `4` and never falls back to the embedded bundle;
  - `TestCategoryCheck_CoversCorpusClasses`: every entry of `tpacorpus.Classes`, and every gating signature category in the embedded bundle, maps to `"tpa.bundle"` in `categoryCheck`.
- [x] T220 Implement the gate changes:
  - `cmd/scan-eval/main.go`: the `--bundle` flag, with empty meaning the embedded bundle loaded through `scanner.LoadBundleCheck`.
  - `cmd/scan-eval/gate.go`:
    - `builtinGateChecks()` plus `gateChecks(bundle)`;
    - `runGate(c, checks, …)`;
    - static `categoryCheck` entries for every class;
    - a `bundle` block in `gateMetrics`;
    - a rewritten "MUST mirror" doc comment (the production surface is the hard built-ins plus `BundleCheck`, and the soft checks are unscored by design).
  - Update the five `evaluateGateCorpus`/`runGate` call sites in `cmd/scan-eval/gate_test.go`.
- [x] T221 Write the control corpus `cmd/scan-eval/testdata/bundle_control_corpus.json` (D-US2-7). Its `description` names the control id and why it is the control.
- [x] T222 [P] Write `cmd/scan-eval/bundle_control_test.go` with two tests:
  - `TestBundleControl_NoOverlap`: with `builtinGateChecks()` only, `Detected == 0`. With the full bundle, the finding's `Signals` hold exactly one `tpa.` id, the control's, and no hard negative is flagged.
  - `TestBundleControl_RemovalBreachesGate`: `run()` with `--bundle <full>` exits `exitOK`. `run()` with `--bundle <tpacorpus.Build Exclude control>` exits exactly `exitGateBreach`, and the test asserts the two recall values, 1.0 and 0.0.
- [x] T223 [P] Write `internal/security/scanner/tpa_control_overlap_test.go` (`TestBundleControl_NoProductionBuiltinHardFinding`). It reads `../../../cmd/scan-eval/testdata/bundle_control_corpus.json` and asserts that `baselineEngineChecks()` alone yields no `ThreatLevelDangerous` finding for the control sample.
- [x] T224 Run `go run ./cmd/scan-eval --corpus specs/065-evaluation-foundation/datasets/detect_corpus_v1.json --gate --min-recall 0.90 --max-fp 0.05`. It must pass, with every `tpa_*` category listed as `gated: true`. Record the numbers in the PR body.

### CI

- [x] T225 Edit `.github/workflows/eval.yml` (job `security-d2`):
  - add the path filter `cmd/tpa-corpus/**`;
  - add a blocking step after the gate step that runs `go run ./cmd/tpa-corpus verify` and the `go test` line from D-US2-8;
  - update the comment blocks (lines 4–9 and 57–63) to say the gate scores the embedded bundle.
  - No edit to `release-qa-gate.yml`.

### Docs

- [x] T226 [P] New contributor doc `docs/features/tpa-signatures.md`. It covers:
  - the class vocabulary;
  - the source file format;
  - provenance, licence allowlist and FR-012 refusal;
  - the eval-pair and `hn_<class>_` rules and the four registration points (source, `tpacorpus.Classes`, `categoryCheck`, dataset);
  - the `build`/`verify`/`validate` and `scan-eval --gate --bundle` loop;
  - how the control stops a vacuous gate.
  - Add it to `website/sidebars.js` under the Security category.
- [x] T227 [P] Update `docs/features/tool-scanner.md`:
  - add `BundleCheck` to the checks table;
  - add the `--bundle` flag next to the gate command (around line 236);
  - rewrite the forward-compat paragraph (lines 256–271) so that "unmapped is unscored" reads as a hazard closed by `TestCategoryCheck_*`;
  - fix the claim that `inprocess.go` invokes "exclusively" the built-ins.
  - Also update `docs/features/security-quarantine.md:617-645` with the signature and class counts and a link to the new doc.

### Fixups (existing tests and goldens affected by bundle growth)

- [x] T228 Update the tests that pin the bundle's size or version:
  - `internal/security/scanner/tpa_bundle_test.go:27-38` (`TestLoadEmbeddedBundle`): replace the pins Runnable 7, Skipped 3, Declared 3 with values derived from `ParseBundleSignatures`, or with the new literals, and keep `ID()=="tpa.bundle"`.
  - `internal/security/scanner/tpa_bundle_source_test.go:57`: `BundleVersion` is now `"0.1.1"`.
- [x] T229 Verify that these tests stay green **without editing their expectations**. If one fails, narrow the new regex, not the test:
  - `internal/security/scanner/tpa_bundle_test.go:61-98` (0001 signal, `ThreatRugPull`, confidence 0.9, clean weather).
  - `internal/security/scanner/tpa_bundle_span_test.go:44,80,95-118`.
  - `internal/security/scanner/inprocess_span_test.go:75`.
  - `internal/security/scanner/scan_verdict_test.go:14-27`.
  - `internal/security/scanner/sec01_variant_corpus_test.go:83,201`.
  - `internal/security/scanner/coverage_corpus_test.go:97,126`.
  - `internal/security/scanner/bundle_overview_test.go:50-51`.
  - `internal/security/scanner/tpa_bundle_empty_test.go:62,68`.
  - `tests/tpa-attack-lab/lab_test.go:89,176,201,219,228,247,287,302,327`.
  - `internal/security/scanner/e2e_tpa_smoke_test.go:199`.
  - `internal/runtime/qapilot_conformance_test.go:319` (the benign contracts).
  - `internal/runtime/tool_quarantine_scan_test.go:36,120,179,325,401,424,428`.
  - `cmd/scan-eval/gate_test.go:239` (`TestGate_CommittedCorpusPasses`).
- [x] T230 Run the suites:
  - `go test -race ./internal/security/... ./cmd/scan-eval/ ./cmd/tpa-corpus/ ./specs/065-evaluation-foundation/datasets/ ./tests/tpa-attack-lab/ ./internal/runtime/ -run 'Quarantine|Scan'`.
  - `go test -tags server -run Bundle ./internal/server/`, which exercises `tpa_bundle_transport_test.go` (own one-rule bundle, no `signatures[]`).
  - `./scripts/test-api-e2e.sh`.
  - These need no change and are not tied to the embedded bundle: `cmd/mcpproxy/hold_visibility_test.go`, `frontend/tests/unit/signature-bundle.spec.ts` and `internal/telemetry/tpa_scanner_test.go`.

### Ship

- [x] T231 Run lint both ways:
  - `/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...`
  - the same command with `--build-tags server`.
- [x] T232 In `roadmap.yaml`, set `tpa-db` → `tpa-db-corpus` to `status: in_review` with the `pr:` link (`done` once merged). Regenerate `ROADMAP.md` with `python3 scripts/gen-roadmap.py`.
- [x] T233 Add a Recent Changes line to `CLAUDE.md` (`101-tpa-db US2`): Go 1.26, `internal/security/tpacorpus` + `cmd/tpa-corpus`, `signatures[]` load-layer integrity, `scan-eval --bundle`, the control, and **No new dependencies**.
- [ ] T234 Run an internal review workflow (provenance accuracy, licence compliance, sample safety, gate non-vacuity, determinism; 3-refuter verification), then the cross-model round with opencode `github-copilot/gpt-6.1-sol` (zcode's Z.AI login is expired), split into small file-named chunks, with a cap of 10 rounds. The fallback if Sol 6.1 is down is the app-bundled codex `gpt-5.6-sol`, read-only.
- [x] T235 Open the PR against `main`:
  - title `feat(security): Spec 101 US2 TPA seed corpus and eval gate`;
  - body with `Related #938`, the gate numbers from T224, the class and signature table, the recorded assumptions, and the D-US2-10 conflict hunks with US1;
  - no closing keyword and no QA report.

### Out of scope for US2 (owned by US1/US3)

- Ed25519 signing, the sidecar, `internal/security/bundlesig/`, `cmd/tpa-db keygen|sign`, and the CI signing job (US1).
- Manifest `sequence`, `trust_epoch` and `key_id` semantics, the watermark, the FR-009b baseline seed, and the FR-011 ceilings (US1 adds header keys through the manifest `header` passthrough of D-US2-4).
- `ConfigureBundle`, `tpa_bundle_source.go`, `BundleInfo` fields, the `inprocess.go` `coverageOK`/degraded state, status, rollback, CLI and Web UI surfaces (US1).
- The activation self-check calling `LoadBundleCheck` and the gate metric function (US1/087 for FR-016a(c) and SC-005's activation half). US2 only ships the reusable seam.
- Fetch and refresh, re-scanning approved tools after activation, and telemetry (US3/087).
- Any change to detect check semantics, tiers, gate thresholds or scoring, or `ClassifyPosition`.
