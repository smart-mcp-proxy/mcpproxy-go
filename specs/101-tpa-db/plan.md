# Implementation Plan: Versioned, Signed, Offline-First TPA Signature Database

**Branch**: `101-tpa-db` | **Date**: 2026-08-26 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/101-tpa-db/spec.md` (roadmap P1 epic `tpa-db`)

## Summary

Make the TPA signature database a first-class security artifact: a **signed, sequence-versioned publishable pair** (the existing `scanner-bundle.json` + a detached Ed25519 sidecar), a **verify-before-parse loader** with real ceilings and per-authority anti-downgrade state, an **explicit fingerprint-authorized rollback**, a **seed corpus of ≥25 cataloged campaign signatures** with provenance and licences, and the **observability** to tell "current and verified" from "stuck on last-known-good, and why".

Technical approach, grounded in the code:

- **Verification is a new leaf package, everything else extends what shipped.** `internal/security/bundlesig/` holds sidecar parsing and Ed25519 verification as pure functions over bytes — no I/O, no globals, no `scanner` import — because that is the one piece with an independent wire contract two implementations must agree on byte-for-byte (D3/D10). The loader, ceilings, activation and status work extend `internal/security/scanner/tpa_bundle_source.go` and `tpa_bundle.go`, where Spec 086 actually shipped them. **Spec 087's plan names a package `internal/security/bundle` that does not exist** (research.md R1); reading it as a description of the tree is the fastest way to write tasks against wrong paths.
- **The load pipeline gains a first stage and grows ceilings throughout.** FR-008's order is not a preamble to the existing pipeline but interleaved with it, because the checks become knowable at different points: sidecar cap → parse sidecar → bundle byte ceiling → **signature over the raw bytes** → parse manifest → rule-count ceiling → epoch match → sequence/fingerprint → version compat → compile-all-or-reject → runnable>0 → self-check → activate. Today's `loadBundleFromFile` does `os.ReadFile` with no cap, no file-type check and no deadline (R2), so FR-011's numbers — bundle ≤ 8 MiB, rules ≤ 2000, sidecar ≤ 4 KiB, 5 s read budget — are new code at three different points, not one guard.
- **Candidate opening is descriptor-based, non-blocking, and platform-split.** `os.OpenFile(..., O_NOFOLLOW|O_NONBLOCK)` → `fstat` the descriptor → refuse non-regular → clear `O_NONBLOCK` → read the ceiling from that same `FileInfo`, never re-resolving the path (D5). `O_NONBLOCK` is not defensive: opening a writer-less FIFO **blocks inside `open(2)`**, before `fstat` and before any timeout exists to observe it, so without it SC-011(c) cannot pass by construction. The time bound is a context-cancelled reader goroutine, **not** `SetReadDeadline` (`ErrNoDeadline` on regular files), and abandoned readers are capped at one per path so releasing the slot cannot leak an fd per refresh. Windows gets its own build-tagged implementation and an explicitly **weaker** guarantee — `os.Root` blocks path escape but still follows symlinks resolving inside the root.
- **Anti-downgrade state is per signing authority, one BBolt transaction, and content-addressed on disk.** A new `TPABundleStateBucket` (`internal/storage/models.go`) holds, per authority: watermark, its epoch, the fingerprint that set it, the signature ratchet, the FR-010 pin and deny-list — plus an append-only activation history, all in a single `Update`. Last-known-good bundles are stored as `tpa-lkg/<full-sha256>.json` and referenced by digest from that record, so writing a new one never destroys the old and no crash can pair new bytes with old state — the failure a fixed `last-known-good.json` filename would reintroduce. Write-temp → fsync file → rename → **fsync the directory** (a file fsync does not make its directory entry durable) → commit → publish; startup reconciles unreferenced files (D6).
- **`BundleInfo` gains a full digest rather than a widened one.** Today's `Fingerprint` is `hex(sha256)[:12]` — 48 bits (R3), fine as a human label and load-bearing in existing output, but not as authorization for the one path that bypasses the watermark. Rollback accepts only the full digest, and status surfaces both.
- **`coverageOK` is where the trust story meets the approval gates.** `inprocess.go` currently computes it from mere bundle presence (R5). FR-007's unsigned-external degradation and FR-009a's fallback demotion both land on that one expression — the narrowest and most safety-critical edit in the feature, and the one whose regression is silent (a quietly suspended `scan`-mode auto-approval looks exactly like a quiet install, which is why FR-020 requires surfacing it).
- **The gate must be taught to see the database before the corpus grows.** `cmd/scan-eval` never loads a bundle and `gateChecks()` omits `BundleCheck` entirely (R6), so FR-016/SC-005 would pass with the corpus absent. `--bundle` + registration through the **production** loader (D8), `categoryCheck` entries for every new class, and a deterministic negative control whose gated-malicious set is one control sample alone (D9).
- **Telemetry needs typed backstop entries, not a relaxation.** `scanV8TPAScanner` whitelists four integer keys and rejects anything else before transmit (R7). Four typed entries plus one payload-level cross-field rule (`sequence` present while `bundle_version == "other"` is a violation), with the provenance gate in the reporting layer where authority is known (D7).
- **Scope stops at the network boundary, behind a source-neutral seam.** Spec 087 is specified but not implemented (R8) and FR-018 forbids inventing a second refresh lifecycle, so this feature delivers the file-drop path end to end and leaves fetch to 087 (D11). SC-003's air-gapped install reaches full P1 capability on that path alone. The cut is only safe because the pipeline takes **bounded bytes + source metadata**, not a path: file-drop and 087's fetch are both thin adapters over one verifier, so the one component that must never exist twice cannot be forked. SC-007's fetch clause moves to 087 **in spec.md** rather than shipping permanently half-met.

## Technical Context

**Language/Version**: Go 1.25.5 module toolchain (`go.mod`). Backend-only; no frontend, tray, or Swift changes beyond status fields already rendered generically.
**Primary Dependencies**: **stdlib only for the new work** — `crypto/ed25519`, `crypto/sha256`, `encoding/json`, `syscall`. No `ed25519` usage exists in the tree today (R10). Existing: `go.etcd.io/bbolt` (state), `go.uber.org/zap`, Cobra (CLI). **No new dependencies.**
**Storage**: one new BBolt bucket (`TPABundleStateBucket`) in the existing database; no schema migration, no Bleve change.
**Testing**: `go test -race ./internal/...` (locally `internal/server` needs the CI skip regex); fixture-driven conformance tests for the sidecar format; `cmd/scan-eval --gate` in CI (`.github/workflows/eval.yml`).
**Target Platform**: Linux/macOS/Windows core binary, personal + server editions — pure `internal/`, edition-agnostic. `O_NOFOLLOW` is POSIX-only; Windows gets the same descriptor-level guarantee via `os.Root`-scoped opens (D5).
**Project Type**: Single Go project (core server) + one new leaf package.
**Performance Goals**: SC-008 — a database at the ceilings (≥500 RE2 rules) moves p95 scan latency <10% versus the 6-signature baseline, and load-plus-verify completes under one second. Verification is one Ed25519 check over ≤8 MiB; the cost is compilation, which is bounded by the rule ceiling.
**Constraints**: verify-before-parse (FR-006) is an ordering constraint on every candidate path; all patterns stay RE2-only; the embedded default is exempt from watermark and ratchet (FR-009) so scanning never stops.
**Scale/Scope**: ~5 packages — `internal/security/bundlesig` (new), `internal/security/scanner`, `internal/storage`, `internal/config`, `internal/telemetry` — plus `cmd/scan-eval`, `cmd/mcpproxy` (CLI status/rollback), the corpus, docs, and the CI signing job.

## Constitution Check

*GATE: must pass before Phase 0 and be re-checked after Phase 1.* — Constitution v1.1.0.

| Principle | Assessment |
|-----------|------------|
| **I. Performance at Scale** | PASS. BM25 search and indexing untouched. Verification is one signature check per candidate load — not per scan. The rule ceiling (2000) is what bounds compile cost, and SC-008 measures the hot path directly rather than asserting it. |
| **II. Actor-Based Concurrency** | PASS. The activation swap is Spec 087 FR-010's existing atomic in-memory swap; this feature adds durable state written under a single BBolt transaction on the same path. No new goroutine except D5's bounded reader, which is scoped to one candidate read and abandoned rather than joined — stated as a residual, not hidden. |
| **III. Configuration-Driven Architecture** | PASS. New config: operator keys, operator baseline, operator epoch, active-authority declaration, `require_signed_bundle`. All config-resident **by design, not convenience** — FR-004b, FR-009b and FR-005a each turn on the config-write attacker being out of the threat model while the bundle-path attacker is in it. |
| **IV. Security by Default** | PASS, and this is the feature's whole subject. Verify-before-parse; ceilings before verification's own inputs; per-authority watermarks so neither authority can lock the other out; a config-resident active-authority declaration so file-drop cannot choose the authority; full-digest rollback authorization; a signature ratchet; degraded coverage rather than silent full trust whenever the trust basis is weaker than it looks. The one place the design accepts less than the spec's literal text is recorded in Complexity Tracking. |
| **V. Test-Driven Development** | PASS. Every SC has a named fixture; the sidecar format ships byte-level accept/reject fixtures (D3) so conformance is testable by construction, and D9's control asserts the gate itself is not vacuous. |
| **VI. Documentation Hygiene** | PASS. FR-022 is explicit and in scope: operator lifecycle (verify, drop, roll back, read status) and contributor rules (author a signature, provenance/licence, eval samples, how the gate blocks a bad corpus). |

**Result**: PASS. One new leaf package, one new BBolt bucket, no new dependency. Re-checked after Phase 1 design: unchanged.

## Key Decisions (normative for tasks)

Resolved in [research.md](research.md); recorded here as the plan of record.

| # | Decision | Where resolved |
|---|----------|----------------|
| D1 | Publication channel = **GitHub Releases on the corpus repo**, immutable per-sequence asset names, `latest` resolved via the Releases API (not a mutable file) | R-D1 |
| D2 | **OPEN — maintainer's call.** The ratchet only arms *after* a first signed activation, so "no flip" leaves fresh installs accepting an attacker's unsigned drop. Recommendation: default-on for external candidates with an explicit opt-out, accepting a loud breaking change for existing 086 unsigned droppers | R-D2 |
| D3 | Sidecar = one-line JSON, exactly five keys (`sidecar_version`, `algorithm`, `key_id`, `signature`, `sequence`), total deterministic rejection, Ed25519-only in v1 | R-D3, contracts/sidecar-format.md |
| D4 | `signatures[]` additive top-level section keyed by TPA id, many-to-one to `rules[]`, bidirectional referential integrity enforced at build, licence from an SPDX allowlist | R-D4, contracts/bundle-signatures-section.md |
| D5 | Read budget = `O_NOFOLLOW\|O_NONBLOCK` open + descriptor `fstat` + ceiling from that `FileInfo`; context-cancelled reader goroutine, never `SetReadDeadline`; abandoned readers capped at one per path; Windows build-tagged and explicitly weaker | R-D5 |
| D6 | Activation state = one new `TPABundleStateBucket`, one BBolt `Update`; LKG bundles **content-addressed** (`tpa-lkg/<digest>.json`) and referenced by digest; fsync file **and directory**, commit, publish; startup reconciles orphans | R-D6 |
| D7 | Telemetry = four **typed** backstop entries + **whole-tuple** validation (not one direction); `schema_version` + sequence instead of a per-release enum the binary cannot know; provenance gate still in the reporting layer, but the backstop no longer depends on it | R-D7 |
| D8 | `scan-eval --bundle` loads through the **production** loader and registers `BundleCheck`; same path as the activation self-check | R-D8 |
| D9 | Negative control = one signature whose sample no built-in check flags, evaluated over a corpus whose gated-malicious set is that sample alone; no-overlap asserted in CI | R-D9 |
| D10 | Structure = new leaf `internal/security/bundlesig/` for verification only; everything else extends `internal/security/scanner/` | R-D10 |
| D11 | Scope = **file-drop path end to end** behind a source-neutral `Candidate{bytes, source}` pipeline so 087 cannot fork the verifier; SC-007's fetch clause moves to 087 in spec.md | R-D11 |

## Project Structure

### Documentation (this feature)

```text
specs/101-tpa-db/
├── spec.md                              # Feature spec (normative)
├── plan.md                              # This file
├── research.md                          # Phase 0: R1–R11 findings + D1–D11 decisions
├── data-model.md                        # Phase 1: state records, manifest additions, entity relations
├── quickstart.md                        # Phase 1: sign, drop, verify, roll back, read status
├── contracts/
│   ├── sidecar-format.md                # D3 wire format + accept/reject fixtures
│   ├── bundle-signatures-section.md     # D4 signatures[] schema + integrity rules
│   └── bundle-status-surface.md         # FR-020 fields across CLI / REST / Web UI
└── tasks.md                             # Phase 2 (/speckit.tasks)
```

### Source Code (repository root) — REAL paths

```text
internal/
├── security/
│   ├── bundlesig/                       # NEW leaf package (D10) — pure, no I/O, no globals
│   │   ├── sidecar.go                   #   D3 parse + total rejection grammar
│   │   ├── verify.go                    #   Ed25519 verify over raw bundle bytes; key-id agreement (FR-003)
│   │   └── testdata/                    #   byte-level valid + invalid sidecar fixtures
│   └── scanner/
│       ├── candidate_open_posix.go      # NEW (//go:build !windows): O_NOFOLLOW|O_NONBLOCK + fstat (D5)
│       ├── candidate_open_windows.go     # NEW: handle-based open; weaker guarantee, documented (D5)
│       ├── tpa_candidate.go              # NEW: source-neutral Candidate{bytes, source} pipeline (D11)
│       ├── tpa_bundle_source.go         # file-drop ADAPTER over that pipeline; sidecar cap;
│       │                                #   verify-before-parse (FR-006, read once, never re-read);
│       │                                #   BundleInfo gains full digest, sequence, authority, epoch,
│       │                                #   signature_verified, staleness, last rejection reason (FR-020)
│       ├── tpa_bundle.go                # rule-count ceiling; signatures[] parse + integrity (D4);
│       │                                #   sequence grammar (FR-002 decimal string, ≤ 2^64-1)
│       ├── tpa_bundle_state.go          # NEW: per-authority watermark/ratchet/pin/deny-list/history
│       │                                #   read+write through storage (D6); epoch scoping (FR-005a)
│       ├── tpa_bundle_rollback.go       # NEW: FR-010 full-digest rollback, pin + deny-list
│       ├── inprocess.go                 # coverageOK: degrade on unsigned-external and on the two
│       │                                #   FR-009a fallback conditions ONLY (R5) — the narrowest edit
│       └── bundled/scanner-bundle.json  # seed corpus grows to ≥25 signatures / ≥8 classes (SC-004)
├── storage/
│   ├── models.go                        # TPABundleStateBucket const
│   └── tpa_bundle_state.go              # NEW: single-Update transaction over that bucket (D6)
├── config/
│   └── config.go                        # SecurityConfig: operator keys, operator baseline, operator
│                                        #   epoch, active-authority declaration, require_signed_bundle
└── telemetry/
    └── anonymity.go                     # scanV8TPAScanner: four typed entries + cross-field rule (D7)
cmd/
├── scan-eval/
│   ├── main.go                          # --bundle flag (D8)
│   └── gate.go                          # register BundleCheck; categoryCheck entries per new class;
│                                        #   D9 control + its no-overlap assertion
└── mcpproxy/                            # security status output (FR-020) + rollback command (FR-010)
.github/workflows/
├── eval.yml                             # D2 job now scores the bundle (D8)
└── (corpus repo)                        # gate-then-sign-then-publish (D1) — not this repo
docs/                                    # FR-022 operator + contributor lifecycle
```

**Structure Decision**: one new leaf package for verification, everything else in place. `bundlesig` earns its own package on the Spec-085 `toolsig` argument — a pure, fixture-testable contract with an independent consumer (any publisher tool) — which does **not** apply to the loader, the state, or the status surface.

## Test Strategy

**Fixture-first, because almost every requirement here is a rejection boundary.** A rejection you cannot drive one unit past is not tested.

- **Sidecar conformance** (D3): byte-level accept and reject fixtures in `bundlesig/testdata/` — missing key, unknown key, duplicate key, wrong type, trailing bytes, non-hex `key_id`, wrong-length signature, unknown algorithm, `sidecar_version != "1"`, malformed `sequence`. Each asserts a *specific* rejection reason, not merely failure, so two implementations can be compared on refusal as well as acceptance.
- **Verify-before-parse** (FR-006/SC-001): a candidate whose bytes are altered after the digest is computed must be refused **before** any pattern compiles — asserted by a compile counter, not by output alone. Plus the four SC-001 tamper classes (bundle byte, signature value, sidecar identity field, mismatched pair) and the explicit non-requirement (sidecar whitespace/key-order changes are NOT required to be rejected).
- **Ceilings** (SC-010a): both sides of every boundary — 8 MiB and 8 MiB+1, 2000 rules and 2001, 4 KiB and 4 KiB+1, and a read exceeding 5 s — with last-known-good serving throughout.
- **Race-free opening** (SC-011): FIFO swapped in between path resolution and open; symlink-to-FIFO; a never-written FIFO whose slot is released within the budget so a later legitimate update still activates.
- **Anti-downgrade** (SC-002/SC-002a): same-authority same-epoch comparisons; the max-sequence lockout and its epoch-bump recovery; the post-bump stale-epoch replay refusal (a2); the two-authority isolation (b); the authority-not-active refusal (b2); and the baseline-less operator authority activating **degraded** rather than fully trusted (c).
- **Rollback** (SC-010): the 12-hex-prefix collision fixture — an attacker-substituted, publisher-signed, current-epoch bundle sharing the abbreviated fingerprint but differing in the full digest — refused as a target mismatch; plus N-activation history replay across a process restart.
- **Crash consistency** (FR-011a): a fault injected between LKG persist, state commit and pointer publish, asserting every recovery lands on a consistent (bundle, watermark) pair, and specifically that a preserved rollback never loses its deny-list.
- **State substitution** (SC-012): a prepared `config.db` with a lowered watermark, a cleared ratchet and a forged pin — in each case the outside-the-data-dir anchors still hold and the install reports degraded. **No fixture may assert the ratchet/watermark/pin survive**; they are best-effort against this attacker by construction (R11).
- **Coverage degradation** (FR-007/FR-009a): the four states that must degrade, and — equally important — the two that must **not**: an ordinary fresh install on the embedded default, and an embedded default at or above the publisher watermark after an external file was removed. A false degrade silently disables `scan`-mode auto-approval fleet-wide.
- **Gate non-vacuity** (SC-005/D9): the control signature's removal turns the gate red with exit `exitGateBreach`, plus the standalone no-overlap assertion that keeps the control honest as built-in checks grow.
- **Telemetry backstop** (D7): each typed entry accepted; a free-string version rejected; the cross-field violation rejected; and an operator-built corpus reporting `other` with `sequence` omitted **even when it claims a genuine publisher version string**.
- **Air-gapped parity** (SC-003): the file-drop path with the network stack unavailable produces byte-identical behavior to a connected install's file-drop path.

## Documentation & wiring checklist (Constitution VI)

- `docs/features/security-quarantine.md` + the tool-scanner docs: database lifecycle, degraded-coverage states and what each means.
- New operator doc: verify, drop, roll back, read status; the air-gapped walkthrough.
- New contributor doc: author a signature, provenance/licence rules, eval-sample requirements, how the gate blocks a bad corpus.
- `docs/configuration.md`: operator keys, baseline, epoch, active authority, `require_signed_bundle`.
- `make swagger` after the status-surface change.
- `CLAUDE.md`: security-model line + Recent Changes entry.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| **Persisted anti-downgrade state is best-effort against an attacker who can substitute `config.db`** (spec §Threat-model boundary; SC-012) | The bundle path may be configured inside the data directory, so the in-scope bundle-path attacker may also reach the state store. BBolt is transactional and checksummed, **not** authenticated (R11). | Authenticating the state with a key outside the writable data directory means a second key-management story — key location, rotation, and its own compromise path — to protect state whose loss already degrades safely to the FR-009b seeded baseline and the config-resident anchors. The spec accepts this explicitly and SC-012 tests the boundary rather than a guarantee that does not exist. **The plan's obligation is to never present the ratchet, watermark, pin or deny-list as surviving that attacker** — and to keep the anchors that do survive genuinely outside the data dir. |
| **A read blocked on a stalled filesystem leaves a goroutine parked** (D5) | Go cannot interrupt a blocked read on a regular file; `SetReadDeadline` returns `ErrNoDeadline`, which is why FR-011 forbids specifying the bound that way. | Every alternative that truly kills the read is a subprocess or an OS-specific `aio` path — a large, platform-divergent surface to reclaim one goroutine. The property that matters is *the single-flight refresh slot is released and a rejection reason recorded*, so a legitimate update still activates; that is delivered, tested (SC-011c), and stated rather than implied. |
| **SC-007's fetch clause moves to Spec 087** (D11) | SC-007 measures publish → active → fixture fires "within one daily refresh cycle on a fetch-enabled install". There is no refresh cycle: Spec 087 is unimplemented (R8), and FR-018 forbids building a second one here. | Implementing the fetch loop here would either duplicate 087 or absorb it, and absorbing it makes this feature the whole epic. But leaving the criterion in place and half-met is not the alternative: a success criterion no fixture can exercise is a **spec defect**, and maintainer assent does not make it testable. So this is an amendment task against spec.md, not an accepted residual — the fetch clause moves to 087, the manual-drop clause ("and immediately on manual drop") stays here where it is demonstrated end to end. Recorded in this table only because it changes what the spec promises. |

| **The Windows candidate open gives a weaker guarantee than POSIX** (D5) | `O_NOFOLLOW` does not exist on Windows, and `os.Root` — the obvious substitute — blocks path *escape* but still follows a symlink resolving inside the root, so a final symlink to a regular file passes `fstat` cleanly. | Reaching parity means a handle-based open with reparse-point controls (`FILE_FLAG_OPEN_REPARSE_POINT`), which is a real amount of platform-specific code for a path the operator already controls. Design may take that route; what is NOT acceptable is implying parity. If the weaker guarantee is accepted, it is written down: on Windows the candidate path trusts its containing directory and carries symlink-substitution risk POSIX does not. |

No other deviations: no new dependency, one new leaf package the verification contract justifies, one new BBolt bucket the crash-consistency requirement names.
