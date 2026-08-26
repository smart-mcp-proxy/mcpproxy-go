# Phase 0 Research: Versioned, Signed, Offline-First TPA Signature Database

**Feature**: `101-tpa-db` · **Spec**: [spec.md](spec.md) · **Date**: 2026-08-26

Two kinds of entry below. **D**ecisions resolve a `[NEEDS CLARIFICATION]` marker or a "MUST be specified in design" instruction from the spec. **R**esults are mechanical findings about the tree as it stands — each one is a fact a task will trip over if the plan does not state it.

---

## Mechanical findings (the tree as it is today)

### R1 — The loader is in `internal/security/scanner/`, not the package Spec 087's plan named

Spec 087's plan proposes a new package `internal/security/bundle` owning the file lifecycle. **That package does not exist.** What shipped (Spec 086) lives in `internal/security/scanner/`:

| File | Role |
|---|---|
| `tpa_bundle_source.go` | `ConfigureBundle(path, logger)`, `BundleStatus() BundleInfo`, `loadEmbeddedBundle`, `loadBundleFromFile`, `loadBundleWithInfo`, and the `storeBundle`/`snapshotBundle` global-state pair |
| `tpa_bundle.go` | `rawBundle`/`rawRule`/`rawSkip` wire types, `bundleMetadata`, `loadBundleCheck`, `checkBundleVersion`, `BundleCheck` (the `detect.Check`) |
| `registry_bundled.go` | embedded default via `go:embed` |
| `bundled/scanner-bundle.json` | the shipped corpus |

**Consequence for the plan**: this feature extends those files rather than creating 087's package. Reading 087's plan as a description of the current tree is the single most likely source of wrong file paths in tasks — treat 087's *spec* as normative for the refresh lifecycle and its *plan* as superseded on structure.

### R2 — Candidate reads are completely unbounded today

`tpa_bundle_source.go` reads a candidate with a bare `os.ReadFile(path)` carrying a `//nolint:gosec` comment that reasons "operator-configured path, same trust level as `mcp_config.json`". There is **no size check, no file-type check, no deadline, and no re-read protection**. `loadBundleCheck` (`tpa_bundle.go`) applies **no rule-count limit**. FR-011's ceilings are therefore entirely new code, not a tightening — and the existing nolint rationale is exactly the assumption FR-011 overturns, since the bundle path is in the threat model while the config path is not. That comment must be updated, not just the code beside it.

### R3 — `BundleInfo.Fingerprint` is 48 bits

`tpa_bundle_source.go` computes `Fingerprint: hex.EncodeToString(sum[:])[:12]` — the first 12 hex characters of the SHA-256. FR-010 requires the **full** digest for rollback authorization, so `BundleInfo` needs a second field rather than a widened one: the short form is load-bearing in existing operator-facing output and in `logBundle`.

### R4 — The bundle has nowhere to put per-signature metadata

Top-level keys of the shipped `scanner-bundle.json`: `bundle_version`, `generated_from`, `rules`, `schema_version`, `signature_count`, `skipped`. A `rules[]` entry is keyed `category, confidence, detector, engine, flags, id, indicators, level, pattern, severity, target, type`. **No provenance field, no license field, anywhere.** This is what FR-001a exists to fix, and it means FR-012's "each signature MUST carry provenance and a redistributable license" cannot be satisfied by any rearrangement of today's shape.

### R5 — `coverageOK` is derived from mere bundle presence

`internal/security/scanner/inprocess.go`: `coverageOK = bundlePresent && result.Coverage.ChecksFailed == 0`. Nothing consults signature state, authority, or watermark. FR-007's degradation rule and FR-009a's fallback demotion both land on this one expression, which makes it the narrowest and most safety-critical edit in the feature.

### R6 — The eval gate cannot see the bundle at all

`cmd/scan-eval/gate.go`'s `gateChecks()` returns only the four built-in `detect.Check`s; `scanner.BundleCheck` — which production **does** append in `inprocess.go` — is absent, and `scan-eval` never loads a bundle. `categoryCheck` maps a category to a check id, and `gatedCategory()` enforces a category **only** when its mapped check id is registered. So today FR-016/SC-005 would pass with the corpus entirely absent. This is FR-016a, and it is a prerequisite for the seed-corpus work rather than a parallel task: growing the corpus while the gate cannot score it is how a vacuous pass ships.

### R7 — The telemetry backstop rejects strings and booleans by construction

`internal/telemetry/anonymity.go` `scanV8TPAScanner` whitelists the `tpa_scanner` sub-object to `{scans_completed, scans_failed, scans_with_findings, findings}` and rejects anything else with `"carries a key outside the whitelist"`. A string `bundle_version`/`source` or a boolean `signature_verified` is refused **before transmit**. FR-021's typed extension is therefore a change to the backstop itself, and the cross-field invariant FR-021 names (`sequence` present while `bundle_version` is `other` → reject) is enforceable right there on the payload.

### R8 — Spec 087 is specified but not implemented

No `internal/security/bundle`, no `security.bundle` config section, no refresh ticker. `SecurityConfig` carries exactly one bundle-related field, `TPABundlePath` (`config.go:2751`), plus the `MCPPROXY_TPA_BUNDLE_PATH` env override resolved in `EffectiveTPABundlePath`. **Sequencing consequence**: FR-018 forbids this feature from inventing a second refresh lifecycle, but there is no first one to insert stages into. The spec's own Assumptions anticipate this — US1's signature/sequence verification is reachable through the existing Spec-086 `ConfigureBundle` hot-reload/file-drop path — so the plan phases the fetch-dependent parts behind 087 and delivers the file-drop path first.

### R9 — Persistence has a home but no bucket

BBolt buckets are declared in `internal/storage/models.go` (`UpstreamsBucket`, `ToolApprovalBucket`, `MetaBucket`, …). FR-011a's one-transaction requirement maps cleanly onto a single BBolt `Update` over a new bucket; nothing in the current schema needs to change.

### R10 — Ed25519 is stdlib and unused here

No `crypto/ed25519` import exists in `internal/` or `cmd/` (the only `ed25519` matches are SSH-key *path strings* in the sensitive-data detectors). Verification is stdlib — **no new dependency**, consistent with the repo's stated stance.

### R11 — The `config.db` trust boundary is a boundary, not a guarantee

BBolt is transactionally consistent and checksummed against corruption; it is not authenticated. Spec §Threat-model boundary already says so. Every persisted item this feature adds — watermark, ratchet, pin, deny-list, activation history — inherits that. The plan must never present them as surviving a prepared-database substitution (SC-012 asserts exactly this).

---

## Decisions

### D1 — Publication channel: **GitHub Releases on the corpus repo** (resolves FR-017's marker)

**Decision**: publish artifact pairs as GitHub Release assets on the tpa-db corpus repository, with **content-addressed** asset names (`scanner-bundle-<full-sha256>.json` + `.sig`) plus a **signed release index** mapping sequence → digest, and a `latest` pointer inside that index.

**"Immutable per-release asset names" would have been wrong.** GitHub Release assets are mutable: a maintainer (or a compromised token) can delete an asset and upload a different one under the same name, and releases and tags can be edited after publication. A sequence-bearing filename asserts immutability the platform does not provide. Content-addressing makes the name a checksum instead of a promise, so a substituted asset simply fails to match the digest the index — itself signed — names. Publication automation MUST additionally refuse to replace an existing asset, and historical artifacts needed for FR-010 rollback MUST be archived independently of the Release that first carried them.

**Rationale**: the alternative (a static path under mcpproxy.app) puts corpus publication on the Cloudflare Pages deploy pipeline, which is already the site's critical path and whose failure mode is silent — [the appcast feed has gone stale this way before](../../docs/). Releases give immutable asset URLs, a publication timestamp SC-006's server-side age join needs, and a signing job that lives in the same repo as the corpus it signs, so FR-016's "never signed or published without a green gate" is one workflow rather than a cross-repo handoff.

**Alternatives considered**: mcpproxy.app static path — rejected for the silent-failure coupling above and because it separates the artifact from the gate that must precede it. A dedicated CDN — rejected as unjustified operational surface for a file fetched at most daily per install.

**Consequence for the 087 handoff**: `latest` resolves through the signed index, not a fixed href — and the index must be reachable as a **cacheable object at a stable URL**, not only through the Releases API. Relying on the API alone adds an availability dependency and a rate limit that a fleet shares: unauthenticated GitHub API calls are limited per source IP, so installs behind one NAT doing a synchronized daily refresh can exhaust it and all fail together. 087 MUST therefore specify conditional requests (`ETag`/`If-None-Match`), refresh jitter so a fleet does not tick in lockstep, and explicit behavior when the index is unreachable or rate-limited (keep last-known-good, record the reason, do not treat it as a verification failure).

### D2 — `require_signed_bundle` stays **opt-in indefinitely** (resolves the Assumptions marker)

**Decision**: **OPEN — needs the maintainer's call.** The draft answer was "no flip, the ratchet does the work". Cross-model review showed that reasoning is incomplete, and the honest resolution is a back-compat judgement I should not make silently.

**Why the draft rationale fails.** It argued that a flip "would change behavior only for installs that have never seen a signed bundle". That is true — and it is the wrong conclusion, because **those installs are exactly the unprotected population**. The ratchet only arms after a first signed activation; before that, an attacker who can write the bundle path (in scope) can drop an unsigned corpus and have it activate. Saying the ratchet "does the work" describes installs that are already safe.

**The real trade is back-compat, and it is not free either way.**

| | Default OFF (draft) | Default ON with opt-out (review's proposal) |
|---|---|---|
| Fresh install, attacker writes bundle path | unsigned corpus **activates** (degraded coverage, but serving) | refused |
| Existing 086 user with an unsigned file drop, upgrading | keeps working | **breaks** until they set the flag |
| Air-gapped / self-built corpus | works out of the box | works after one explicit opt-out |

Unsigned file drops are a shipped 086 capability (`security.tpa_bundle_path`), so default-on is a **user-visible breaking change on upgrade** — loud rather than silent (the rejection reason is surfaced per FR-020), but breaking.

**Recommendation**: flip to default-on for EXTERNAL candidates with an explicit `require_signed_bundle: false` opt-out, and call it out in the release notes. The failure it prevents is silent and security-relevant; the failure it causes is loud and self-describing, and that asymmetry is the usual reason to prefer the breaking change.

**This is a spec-level decision** — it resolves an Assumptions marker in spec.md — so it is recorded here as a recommendation with its cost, not applied unilaterally. Tasks MUST NOT begin the config work until it is settled.

### D3 — Sidecar wire format (resolves FR-003's "MUST be specified in design")

**Decision**: a JSON object, one line, with exactly five required keys and no others. Full grammar and fixtures in [contracts/sidecar-format.md](contracts/sidecar-format.md).

```json
{"sidecar_version":"1","algorithm":"ed25519","key_id":"<64 lowercase hex>","signature":"<base64 std, padded>","sequence":"<decimal string>"}
```

- `sidecar_version` — exactly `"1"` in v1; any other value is a hard rejection (not a fallback).
- `algorithm` — exactly `"ed25519"`. **An unknown algorithm is a hard rejection, never a skip.**
- `key_id` — the canonical full-length fingerprint of the public key: lowercase hex SHA-256 of the 32-byte raw Ed25519 public key. Not an operator-chosen label (FR-003).
- `signature` — standard base64 with padding, decoding to exactly 64 bytes.
- `sequence` — the decimal-string form of FR-002's counter, duplicated here so a mismatch against the signed manifest is detectable **before** the bundle is parsed.

Rejection is deterministic and total: a missing key, an unknown key, a duplicate key (JSON with repeated names), a wrong type, trailing bytes after the object, a `key_id` that is not 64 lowercase hex, a `signature` that does not decode to 64 bytes, or a `sequence` failing FR-002's grammar. **No leniency anywhere** — this is the artifact an independent publisher must reproduce exactly.

**Rationale**: JSON because the bundle is JSON and the repo already parses it strictly; five fields because each one is consumed before verification and nothing else is; `sequence` duplicated because it is the one manifest field whose disagreement with the sidecar is cheap to detect early and is otherwise only visible after parsing the thing the signature protects.

**Alternatives considered**: a binary/`minisign`-style format — rejected as a second parser and a second toolchain for publishers, for no property JSON lacks here. Embedding the signature in the bundle — rejected outright: it makes the signed bytes self-referential.

### D4 — `signatures[]` section shape (resolves FR-001a)

**Decision**: an additive top-level `signatures[]` array keyed by TPA id, with rules referencing signatures many-to-one. Full schema in [contracts/bundle-signatures-section.md](contracts/bundle-signatures-section.md).

```json
{"signatures":[{"id":"TPA-2026-0001","category":"instruction_injection","gating":true,
  "provenance":{"source":"https://…","published":"2026-01-14"},
  "license":"CC-BY-4.0","rule_ids":["r-hidden-important-block"]}]}
```

Build-time referential integrity, enforced in **both** directions and rejected whole on failure: every `signatures[]` entry names ≥1 existing rule id; every rule with `gating: true` is named by exactly one signature entry; TPA ids are unique; licenses come from a fixed allowlist of redistributable SPDX ids.

**Rationale**: additive keeps contract §4 forward-compat, so a v0.1 loader ignores it (FR-001a). Many-to-one because a campaign class routinely needs several patterns — one hidden-instruction signature already implies distinct rules for `<IMPORTANT>` blocks, HTML comments and zero-width runs. Keying by TPA id rather than by rule is what makes SC-004's "at least 25 signatures spanning 8 classes" a countable property.

**Alternatives considered**: per-rule `provenance`/`license` fields — rejected: it duplicates the record across every rule implementing one campaign and gives no place to hang gating intent or the class identity SC-004 counts.

### D5 — Read budget mechanism (resolves FR-011's "design MUST name the concrete mechanism")

**Decision**: three distinct mechanisms, because one does not fit all three cases.

| Case | Mechanism |
|---|---|
| Local file open | `os.OpenFile(path, os.O_RDONLY\|syscall.O_NOFOLLOW\|syscall.O_NONBLOCK, 0)`, then `f.Stat()` on the **descriptor**, refuse unless `Mode().IsRegular()`, clear `O_NONBLOCK` on the surviving regular file, then `io.ReadFull` bounded by the ceiling read from that same `FileInfo` |
| Local file read time | a goroutine performing the read, selected against a `context.WithTimeout(5s)`; on expiry the **slot is released and a rejection reason recorded** while the goroutine is abandoned |
| Fetch body | `http.Client` timeout + request context + `io.LimitReader(body, ceiling+1)` |

**`O_NONBLOCK` is load-bearing, not defensive.** Opening a FIFO for reading with no writer attached **blocks in `open(2)` itself** — before `fstat`, before the reader goroutine, before any timeout can observe anything. Without it the whole descriptor-based design is defeated by the very fixture it exists to pass: SC-011(c)'s never-written FIFO would park the loader inside `open`, and the 5 s budget could not release the single-flight slot because nothing has started measuring yet. With `O_NONBLOCK` the open returns immediately, `fstat` sees a FIFO, and the candidate is refused `not_regular_file` without a single byte read. The flag is then cleared for the regular-file case, so an ordinary read keeps blocking semantics and `io.ReadFull` behaves normally rather than spinning on `EAGAIN`.

The abandoned-goroutine case is stated as an accepted residual, not hidden: a read blocked on a stalled network filesystem cannot be interrupted in Go, so the guarantee is *the refresh path stays available*, not *the goroutine dies*. **Windows is a weaker guarantee, stated rather than claimed away.** `O_NOFOLLOW` does not exist there, and `os.Root` is **not** a substitute: it prevents a path from escaping the root, but a symlink resolving *within* the root is still followed, and a final symlink to a regular file passes `fstat` cleanly. So the no-follow property is genuinely not delivered on Windows by that mechanism. Design MUST either open by handle with reparse-point controls (`FILE_FLAG_OPEN_REPARSE_POINT`) and inspect the opened handle, or record explicitly that the Windows candidate path trusts its containing directory and carries symlink-substitution risk that POSIX does not. Silently implying parity is the one option that is not acceptable.

**This needs build-tagged files, and the plan's file list must say so.** `syscall.O_NOFOLLOW` and `syscall.O_NONBLOCK` do not compile on Windows, so the candidate open is a platform-split helper — `candidate_open_posix.go` (`//go:build !windows`) and `candidate_open_windows.go` — each with its own test. A single `syscall`-using file would simply fail the Windows build.

**The residual must be bounded, or releasing the slot converts a stall into a leak.** Releasing the single-flight slot lets the next cycle start, which opens the same path again — so a path that stays stalled accumulates one parked goroutine and one file descriptor per refresh, indefinitely (a daily tick makes that slow, not harmless). Design MUST therefore cap concurrently-abandoned readers for a given candidate path at a small number (1 is sufficient: a second stall on the same path means the first has not returned) and, while that cap is reached, refuse the candidate immediately with the existing `read_timeout` reason instead of opening it again. That keeps the availability property — the refresh path is never *blocked* — while making the fd/goroutine cost O(1) rather than O(refreshes). Fixture: SC-011(c) extended to run a second and third refresh cycle against the same never-written FIFO and assert the process holds no more than one parked reader for it.

**Rationale**: FR-011 already rules out `SetReadDeadline` (unsupported on regular files — returns `ErrNoDeadline`). The descriptor-level check is the only form that closes the stat/open substitution window.

### D6 — Crash-consistent activation state = **one BBolt bucket, one `Update`** (resolves FR-011a)

**Decision**: a new `TPABundleStateBucket` in `internal/storage/models.go` holding one record per authority plus one activation-history list, written in a single BBolt `Update` transaction. The recoverable last-known-good bundle bytes are made durable **before** the transaction commits and before the in-memory pointer is published.

**LKG files are CONTENT-ADDRESSED, and that is what makes the ordering safe.** An earlier draft said "persist LKG bytes → commit → publish" and claimed a pre-commit crash leaves the old (bundle, watermark) pair. That is false if the new bytes land at a fixed filename: overwriting or renaming onto `last-known-good.json` before the commit means a pre-commit crash leaves **new bytes under old state** — the same split the requirement forbids, reached from the other side.

The protocol is therefore:

```text
write  <datadir>/tpa-lkg/<full-sha256>.json.tmp
fsync  that file
rename to <datadir>/tpa-lkg/<full-sha256>.json      # atomic; readers never see a partial file
fsync  <datadir>/tpa-lkg/                           # NOT optional — see below
commit the BBolt state record, which REFERENCES the digest
publish the in-memory pointer
```

Because the filename **is** the digest, writing a new LKG never destroys the old one, so a crash at any point leaves every previously-committed (state → bundle) pair intact and resolvable. The state record is the only thing that says which digest is current, and it becomes durable in one transaction.

**The directory `fsync` is a separate step and is not optional.** `fsync` on a newly created file flushes its *contents*; it does not make the *directory entry* naming it durable. Skip it and a crash can leave the state record committed — BBolt fsyncs its own file on commit — while the bundle it references has no name on disk.

**Recovery must reconcile orphans**, since content-addressing trades a lost-file failure for an unreferenced-file one: on startup, delete any `tpa-lkg/*.json` no state record references (a crash between rename and commit), and if the referenced digest is *missing*, fall back to the embedded default under FR-009a rather than starting with no corpus.

Note BBolt's own durability needs no help here: it fsyncs on commit by default, and `NoSync` must stay off on this path.

**Rationale**: BBolt gives the single-transaction property for free and is already the durable store for approvals and quarantine — the state this record sits beside. R9 confirms no schema change is needed. The deny-list and the watermark's setting-fingerprint are members of the same record precisely because FR-011a names the crash that splits them.

### D7 — Telemetry: typed whitelist entries + a payload-level cross-field invariant (resolves FR-021)

**Decision**: extend `scanV8TPAScanner` with four **typed** entries rather than relaxing it: integer `sequence`, enum-bounded `source ∈ {embedded, file, fetch}`, boolean `signature_verified`, and a bounded `bundle_version`. Enforce the complete allowed **tuple** at the backstop, not a single one-directional rule.

**`bundle_version` cannot be both verbatim and a compiled enum.** A binary shipped today cannot enumerate release lines published after it, so a static enum would classify every *new* corpus as `other` until the binary itself updates — defeating the point of an independently updatable database, and making SC-006's freshness metric blind to exactly the installs that are current. Accepting an arbitrary signed string is not bounded at all. The resolution is a **normalization rule independent of any individual release**: transmit the corpus's `schema_version` (a small, slow-moving set the binary does know) plus the `sequence`, and let the server side join sequence → publication date from what the publisher already knows. The near-unique free string never leaves the install.

**The cross-field rule must be bidirectional.** "`sequence` present while `bundle_version == "other"` is a violation" catches one direction and leaves three holes: a publisher-looking version with no sequence, a publisher-looking version from `source: file` that never verified, and `signature_verified: false` alongside publisher provenance. The backstop MUST validate the whole tuple: `(publisher schema_version, sequence)` appear **together** and **only** when `source == "embedded"`, or `source ∈ {file, fetch}` **and** `signature_verified == true`; every other combination MUST be `other` with `sequence` omitted. The reporting layer still applies the provenance gate — it is the only layer that knows which authority verified — but the backstop no longer depends on the reporting layer being correct, which is the entire reason a backstop exists.

The provenance gate lives in the reporting layer, where authority is known: verbatim version + sequence are reported only when the active bundle's provenance is the publisher authority (the embedded default, or an external bundle signature-verified against a publisher key). Every other bundle reports `other` with `sequence` omitted **regardless of the version string it claims** — a string allowlist alone is defeated by an operator corpus copying a genuine publisher version verbatim.

**Rationale**: R7 shows the backstop rejects the naive addition outright, and FR-021 explicitly forbids relaxing it to free strings. Typed entries keep the backstop's purpose (no near-unique value reaches the payload) while making the new fields expressible.

### D8 — `scan-eval` loads the bundle through the **production** loader (resolves FR-016a)

**Decision**: `cmd/scan-eval` gains a `--bundle <path>` flag defaulting to the embedded default, loads it via `scanner`'s existing loader (never a parallel parser), and registers the resulting `BundleCheck` in `gateChecks()`. The same loading path is what the Spec-087 activation self-check runs.

**Rationale**: FR-016a names the shared path explicitly, and the reason is drift: two parsers would let CI and activation disagree while SC-005 claims they are independent checks of the same artifact. Sharing the loader makes them independent *runs* of one implementation, which is the property actually wanted.

**Consequence**: `gateChecks()` stops being a pure function of the built-in set, so its "MUST mirror" doc comment needs rewriting rather than deleting.

### D9 — Control signature for the negative control (resolves FR-016b)

**Decision**: designate one control signature whose canonical malicious sample is verified to be hard-flagged by **no** built-in check and by no other bundle signature, and evaluate the control over a corpus whose gated-malicious set is **that sample alone**, so removal drives recall to 0 and `decide()` fails unambiguously.

The no-overlap property is asserted in CI as its own test, so a later built-in check gaining coverage of the control sample turns the control **red** rather than silently re-vacuating it.

**Rationale**: FR-016b spells out both vacuity paths. The "restrict to the control's category" shortcut is specifically rejected there — ten gated samples in a category land on exactly 0.90 after one miss, and `decide()` fails only on `< minRecall`.

### D10 — Structure: extend `internal/security/scanner/`, add `internal/security/bundlesig/` for verification only

**Decision**: the loader, ceilings, activation and status work extend the existing `scanner` files (R1). Signature verification and sidecar parsing go in a new leaf package `internal/security/bundlesig/` — pure functions over bytes, no I/O, no global state, no scanner imports.

**Rationale**: verification is the one piece with a genuinely independent contract (D3's format), it is where an independent implementation would be compared byte-for-byte, and a leaf package is what makes the fixture-driven conformance tests cheap. Everything else is already `scanner`'s job and moving it would be churn. Deliberately **not** 087's `internal/security/bundle` — that name describes a lifecycle that shipped elsewhere.

### D11 — Phasing against unimplemented Spec 087 (resolves the R8 sequencing problem)

**Decision**: this feature delivers the **file-drop path end to end** (format, signing, verification, ceilings, watermark, rollback, status, seed corpus, gate) and stops at the network boundary. The fetch path stays 087's, and this spec's FR-008 stages are written so 087 inserts them without a second lifecycle.

**Rationale**: FR-018 forbids a second refresh lifecycle, and R8 shows there is no first one. Building the fetch loop here would either duplicate 087 or absorb it — both worse than shipping the artifact and trust story that 087 then consumes. SC-003 (air-gapped install reaches full P1 capability) is satisfied entirely by the file-drop path, which is the strongest signal that this is the right cut.

**The loader MUST be source-neutral from day one, or 087 will fork it.** Cutting scope at the network boundary is only safe if the seam is in the right place. A file-oriented loader (path in, bundle out) forces 087 to either duplicate verification or invent temp-file semantics so a fetched body can be fed to a path-shaped API — and a duplicated verifier is the one piece of this feature that must never exist twice. Design MUST therefore define ONE candidate pipeline taking **bounded raw bytes plus source metadata**:

```text
Candidate{ BundleBytes, SidecarBytes []byte; Source (embedded|file|fetch); Origin string }
   → verify → parse → ceilings → epoch → sequence → compile → self-check → activate
```

The file-drop path becomes a thin adapter (open, fstat, bounded read → `Candidate`), and 087's fetch path becomes a second adapter (`http` + `io.LimitReader` → `Candidate`) that shares every stage after it. The ceilings then apply identically to both, which FR-011 requires anyway.

**Consequence for SC-007**: its fetch half is not demonstrable by this feature. Rather than leaving a success criterion permanently half-met — which is a spec defect, not a plan caveat, and no amount of assent makes it testable — **spec.md MUST be amended to move the fetch-cycle clause of SC-007 to Spec 087**, leaving the manual-drop clause here where it is fully demonstrable. That edit is a task, not a footnote.
