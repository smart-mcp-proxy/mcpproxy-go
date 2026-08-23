# Feature Specification: Versioned, Signed, Offline-First TPA Signature Database (tpa-db)

**Feature Branch**: `101-tpa-db`
**Created**: 2026-08-23
**Status**: Draft
**Input**: Roadmap P1 epic `tpa-db` — "Build a versioned, offline-first signature/pattern database (known TPA campaigns, malicious phrase corpora, IoC hashes) that the engine consumes — bundled with the binary, refreshable out-of-band, community-contributable, and guarded by the existing scan-eval recall/FP CI gate." Tasks: `tpa-db-format` (signature DB format + loader: versioned, signed, bundled default), `tpa-db-corpus` (seed corpus of known public TPA campaigns/patterns), `tpa-db-refresh` (out-of-band refresh, offline-friendly, eval-gated).

## Positioning *(context)*

<!--
  What already exists, and what this spec adds. Spec 086 (shipped) made the
  detect engine consume a compiled `scanner-bundle.json` (contract v0.1) as one
  hard-tier detect.Check: embedded default via go:embed, operator file-drop
  override (`security.tpa_bundle_path` / `MCPPROXY_TPA_BUNDLE_PATH`, hot-reload),
  fail-closed loader (version major/minor gate, compile-all-or-reject-whole-
  candidate, no-runnable-rules rejection, keep-last-known-good), and the
  BundleInfo status surface (source/version/fingerprint/generated_at/load_error).
  Spec 087 (spec'd, not yet implemented) defined the refresh LIFECYCLE: daily
  best-effort tick, single-flight validate-before-activate with a recall/FP
  activation self-check mirroring the CI gate, optional opt-in signed network
  fetch, fail-safe to last-known-good.

  This spec completes the tpa-db epic by making the DATABASE ITSELF a first-class
  security artifact: (1) a signed, sequence-versioned publishable format with
  downgrade protection and a key-rotation story; (2) a real seed corpus that
  catalogs known public TPA campaigns (the TPA-2026-0001 hidden-instruction class
  and its siblings) with provenance and eval samples; (3) a publication channel
  those signed artifacts flow through, riding Spec 087's lifecycle unchanged; and
  (4) an adoption loop — freshness observability, telemetry, and a post-activation
  informational re-scan — so a refreshed DB actually protects existing installs.
  Today's embedded corpus is 6 signatures / 10 rules; that is a proof of plumbing,
  not a database.
-->

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Signed database format + trust-anchored, downgrade-proof loader (Priority: P1)

An operator receives a TPA signature database out of band — a `scanner-bundle.json` plus its detached signature file — and drops both into the configured bundle location (or lets the opt-in Spec 087 fetch retrieve them). Before anything else touches the bytes, mcpproxy verifies the signature against its built-in publisher trust anchors (or an operator-configured key), checks that the candidate's publish sequence is not older than what is already active, and only then runs the existing validate-before-activate pipeline (version compatibility, pattern compilation, activation self-check). A tampered bundle, a bundle signed by an unknown key, or a replayed older bundle never activates: the last-known-good database keeps serving and the rejection reason is recorded and surfaced. When the operator genuinely needs to roll back (a bad signature pushed by the publisher), an explicit, loudly-logged rollback action reverts to the embedded default or a named prior bundle.

**Why this priority**: The database is a code-adjacent security input: whoever controls its bytes controls what the scanner ignores. Without signing and anti-rollback, the "refreshable signature DB" pillar creates a new supply-chain surface (swap the file → blind the scanner; replay an old bundle → resurrect a fixed blind spot). Signing + sequence + fail-closed loading is what makes every other story safe to ship, so it goes first.

**Independent Test**: Fully offline, no live upstream. Sign a fixture bundle with a test key configured as a trust anchor; drop bundle+signature and assert activation with `signature_verified=true` and the new sequence recorded. Then assert each of the following is rejected with the active bundle unchanged and a machine-readable reason: (a) one flipped byte in the bundle, (b) a signature from a non-trusted key, (c) a missing sidecar with `require_signed_bundle=true`, (d) a validly-signed bundle whose sequence is lower than the active one, (e) a validly-signed bundle failing the activation self-check. Finally, invoke the explicit rollback action and assert the embedded default is restored and the override is logged.

**Acceptance Scenarios**:

1. **Given** a candidate bundle whose detached signature verifies against a configured trust anchor and whose sequence is greater than the active bundle's, **When** refresh runs, **Then** the candidate proceeds through the existing validation pipeline and, on success, becomes active with its sequence persisted and `signature_verified=true` surfaced in bundle status.
2. **Given** a candidate whose bytes were altered after signing (signature mismatch), **When** refresh runs, **Then** the candidate is rejected BEFORE its JSON is parsed or any pattern compiled, the active bundle is unchanged, and a "signature verification failed" reason is recorded.
3. **Given** a validly-signed candidate whose sequence is lower than the highest sequence ever activated on this install, **When** refresh runs, **Then** it is refused as a downgrade, the active bundle is unchanged, and a "downgrade refused" reason names both sequences.
4. **Given** `require_signed_bundle` is enabled and a file-drop candidate has no signature sidecar, **When** refresh runs, **Then** the candidate is refused (fail-closed) with a "signature required" reason; with the setting disabled (default), an unsigned drop is accepted through the existing Spec 086 validation path but surfaced as `signature_verified=false`.
5. **Given** a network-fetched candidate (Spec 087 opt-in path), **When** it arrives, **Then** signature verification is ALWAYS mandatory regardless of `require_signed_bundle` — an unsigned or wrongly-signed fetched bundle is never activated (parity with Spec 087 FR-013/FR-014).
6. **Given** an operator invokes the explicit rollback action naming the embedded default (or a prior bundle file), **When** it runs, **Then** the target is re-validated, activated even though its sequence is lower, the anti-downgrade floor is reset to the rolled-back sequence, and the override is loudly logged and visible in bundle status.
7. **Given** an air-gapped host with no network at any point, **When** any of the above scenarios run via file drop, **Then** behavior is identical — signature verification, sequence checks, and rollback require no network.

---

### User Story 2 - Seed corpus: catalog known public TPA campaigns, eval-gated (Priority: P2)

A security-conscious developer installs mcpproxy and gets, out of the box, a signature database that actually catalogs the publicly known Tool Poisoning Attack landscape — not 6 demo signatures. Hidden-instruction blocks (the `TPA-2026-0001` `<IMPORTANT>…read ~/.ssh/id_rsa…</IMPORTANT>` class), hidden HTML/comment directives, tool shadowing and cross-server override instructions, exfiltration redirects ("also send the result to…"), sensitive-file coaxing, and rug-pull phrasings each have cataloged signatures with a stable `TPA-YYYY-NNNN` identity, a provenance record pointing at the public disclosure that motivated them, and a redistributable license. Every signature ships with labeled eval samples, and no corpus change — addition, tightening, or removal — can land unless the existing scan-eval CI gate stays green.

**Why this priority**: The format (US1) is worthless empty, and the refresh channel (US3) has nothing to carry without a corpus. This is the "database" in tpa-db. It is P2 only because the P1 trust machinery must exist before a larger corpus becomes an attractive distribution target.

**Independent Test**: Run the corpus build and assert every signature carries id, category, detector(s), provenance source, and license; assert every gated signature has ≥1 labeled malicious eval sample and ≥1 category-matched hard-negative in the eval dataset; run `go run ./cmd/scan-eval --corpus specs/065-evaluation-foundation/datasets/detect_corpus_v1.json --gate --min-recall 0.90 --max-fp 0.05` with the new embedded default and assert PASS; feed each campaign class's canonical fixture through the offline scanner and assert a hard-tier finding naming the TPA id.

**Acceptance Scenarios**:

1. **Given** the seed corpus is built into the embedded default bundle, **When** the offline scanner inspects a tool description carrying any cataloged campaign class's canonical payload (e.g. the TPA-2026-0001 hidden-instruction string), **Then** a hard-tier finding fires naming the matched `TPA-YYYY-NNNN` id, and a `scan`-mode gate holds the tool/server.
2. **Given** any signature in the corpus, **When** its metadata is inspected, **Then** it carries provenance (public source reference) and a license permitting redistribution; a contribution without a redistributable license is refused at corpus build time (Spec 087 FR-021 parity).
3. **Given** a proposed corpus change that would drop gated-category recall below 0.90 or push hard-negative false positives above 0.05 on the frozen eval dataset, **When** the CI gate runs, **Then** the change fails CI and cannot merge; the same regressing bundle, if force-published, is additionally rejected at activation by the Spec 087 self-check (two independent layers).
4. **Given** a new signature intended to gate approvals, **When** it is authored, **Then** it emits hard-tier signals (the eval gate scores hard tier only) and its eval samples follow the dataset validator's conventions, including `hn_<attack_category>_*` naming for hard-negatives.
5. **Given** benign-but-spicy tool descriptions (security tooling that legitimately mentions credentials, docs that quote attack examples), **When** scanned with the full seed corpus, **Then** they do not fire hard-tier findings — each campaign class is paired with hard-negatives that pin this down.

---

### User Story 3 - Publication channel + adoption loop (freshness you can see, refresh that re-protects) (Priority: P3)

The publisher cuts a new database release (new signature for a fresh campaign), signs it, and publishes it to a stable public location. A connected install with the Spec 087 opt-in fetch enabled picks it up within one daily cycle; an air-gapped operator downloads the same two files from any machine and drops them in. In both cases the operator can see — in `mcpproxy security` CLI output, the REST status, and the Web UI — which database version/sequence is active, whether its signature verified, and how stale it is. After a new database activates, mcpproxy re-evaluates the already-approved toolsets it has cached against the new signatures off the hot path and surfaces any new hits as review findings — without auto-revoking approvals — so a signature published today protects servers approved last month. Anonymous telemetry gains the active bundle version/sequence and source so the funnel "published → fetched → active → detected" is finally measurable.

**Why this priority**: This closes the loop that makes the DB worth updating. Verified adoption analysis (2026-08-22) showed TPA adoption is structurally gated: refresh as spec'd in 087 only swaps the bundle and never re-scans, so a refreshed DB would protect only future admissions. P3 because it strictly builds on US1's artifacts and 087's lifecycle, and the product is already safer with US1+US2 alone.

**Independent Test**: Serve a signed release from a local test endpoint; enable the 087 fetch against it; assert activation within one refresh cycle and that bundle status (CLI, REST, Web UI) reports the new version, sequence, source=fetch, signature_verified=true, and generated_at. Separately: with a server already approved whose cached tool description matches ONLY a signature added in the new bundle, activate the new bundle and assert a review finding naming the TPA id appears for that tool without the approval being revoked or the server quarantined. Assert heartbeat telemetry (when enabled) carries the active bundle version/sequence/source, and carries nothing when telemetry is opted out.

**Acceptance Scenarios**:

1. **Given** a signed database release published at the channel's stable location, **When** an install with the opt-in fetch enabled completes its next daily cycle, **Then** the new database is verified, gated, and active, and the same artifact pair downloaded manually and file-dropped on an air-gapped install activates identically.
2. **Given** an active database, **When** the operator inspects bundle status on any surface (CLI, REST, Web UI), **Then** they see bundle version, sequence, fingerprint, source (embedded/file/fetch), signature-verified state, generated_at, and last refresh outcome incl. the last rejection reason.
3. **Given** a newly-activated database containing a signature absent from the previous one, **When** the post-activation re-scan runs over cached, already-approved tool metadata, **Then** tools matching the new signature surface as review findings naming the TPA id — approvals are NOT auto-revoked and servers are NOT auto-quarantined by refresh alone.
4. **Given** telemetry is enabled, **When** the heartbeat fires, **Then** it includes active bundle version, sequence, and source alongside the existing anonymous TPA-scanner stats; **Given** telemetry is disabled, **Then** nothing bundle-related is sent (existing opt-out respected).

---

### Edge Cases

- **Air-gapped host, forever**: every P1 behavior (verification, sequence check, rollback, activation self-check) is network-free; the trust anchors ship inside the binary; staleness is surfaced but never blocks scanning — an old database is degraded coverage, not an outage.
- **Signature key rotation**: the trust anchor is a SET of publisher keys embedded per binary release. Rotation is a binary-release event: a release ships old+new keys, bundles are signed with the new key, the old key is removed in a later release. Keys are NEVER delivered over the network, and rotation is not done casually — every shipped binary pins its key set, so a hasty rotation strands older installs on unsigned-refresh only (same philosophy as the app-update signing keys). Key compromise → remove the key in the next release AND publish a higher-sequence bundle signed by the surviving key.
- **Bundle signed by a formerly-trusted, now-removed key**: verification fails on binaries that dropped the key (fail-closed to last-known-good); older binaries still trusting it keep working — the downgrade floor still blocks sequence replay.
- **Tampered bundle / tampered sidecar / swapped pair** (valid signature belonging to a different bundle's bytes): all reduce to signature-verification failure before parse; never a crash, never partial load.
- **Downgrade attack via the embedded default**: a fresh binary whose embedded bundle sequence is LOWER than a previously-activated external bundle's persisted floor does not silently downgrade — the external candidate re-validates and wins by precedence; if only the embedded bundle is available, it serves (scanning must never stop) with the floor retained and the situation surfaced.
- **Anti-downgrade state lost** (data directory wiped/reset): the floor resets; this is accepted — the floor is best-effort hardening on top of signing, not the sole defense, and a wiped data dir already resets approvals.
- **Equal sequence, different bytes** (two distinct bundles claiming the same sequence): refused as suspicious unless it is the currently-active fingerprint (idempotent re-drop stays a no-op per Spec 087 FR-011).
- **Clock skew**: no validity windows or expiry in v1 — sequence ordering, not wall-clock time, is the trust signal; `generated_at` is advisory freshness only.
- **Huge or adversarial database** (thousands of rules, pathological regex): size and rule-count ceilings are enforced at load; every pattern remains RE2 (linear-time, no catastrophic backtracking by construction); a candidate exceeding ceilings is rejected whole, keeping last-known-good.
- **Post-activation re-scan storms**: the re-scan is off the hot path, rate-limited, runs once per activation over cached metadata only (no upstream reconnects), and produces at most one review finding per tool per bundle activation — no notification storm.
- **Signature removed from a newer bundle** (false-positive retired): tools previously held by it are NOT auto-approved by refresh; the held state persists for human review (state changes only flow toward review, never silently toward approval).

## Requirements *(mandatory)*

### Functional Requirements

**Database format & signing**

- **FR-001**: The publishable database artifact MUST be the existing compiled bundle (`scanner-bundle.json`, Scanner Bundle Contract) plus a detached signature sidecar over the exact bundle bytes. Bundle bytes MUST remain deterministic for a given corpus (byte-identical rebuilds), so the signature and the fingerprint are stable.
- **FR-002**: The bundle manifest MUST gain additive metadata within the supported schema line: `generated_at` (RFC3339 build stamp — the loader already surfaces it), `sequence` (a strictly-increasing integer publish counter), and a publisher key identifier. These keys MUST be additive so existing v0.1 loaders ignore them (contract §4 forward-compat preserved).
- **FR-003**: The signature scheme MUST be Ed25519 detached signatures (or an equivalent modern EdDSA scheme selected in design); the sidecar MUST carry the algorithm identifier and signing key id so the scheme can evolve without breaking verification of existing artifacts.
- **FR-004**: The set of trusted verification keys MUST be the union of (a) publisher public keys embedded in the binary at build time and (b) operator-configured keys (the Spec 087 FR-013 key). Keys MUST NOT be retrieved over the network, and verification MUST work fully offline.
- **FR-005**: Key rotation MUST be supported by trusting multiple keys simultaneously: a binary release MAY add a new key while retaining the old for at least one release cycle, and key removal is also a binary-release event. The design MUST document the compromise procedure (drop key next release + publish higher-sequence bundle under surviving key) and the "do not rotate casually" operational stance.

**Loader & activation**

- **FR-006**: For any candidate carrying a signature, verification MUST run BEFORE JSON parsing or pattern compilation; a candidate failing verification MUST be rejected without further processing, keeping last-known-good (extends the Spec 086 fail-closed pipeline with a new first stage).
- **FR-007**: Signing policy MUST be: network-fetched candidates ALWAYS require a valid signature (Spec 087 FR-013/FR-014 unchanged); file-drop candidates are verified whenever a sidecar is present; a new `require_signed_bundle` setting (default off for back-compat with existing unsigned drops) makes the sidecar mandatory for file drops too; the embedded default needs no sidecar (its integrity rides on binary distribution integrity). Bundle status MUST always report whether the active bundle was signature-verified.
- **FR-008**: The full validate-before-activate order MUST be: signature (per policy) → sequence/anti-downgrade → version compatibility → parse → compile-all-or-reject → runnable-rules > 0 → activation self-check (Spec 087 FR-008) → atomic activation. Any failure at any stage MUST keep last-known-good and record a machine-readable reason and timestamp (surfaced via the existing bundle status), never an empty or partial rule set.
- **FR-009**: The system MUST persist the highest sequence ever activated (per data directory) and refuse any candidate with a lower sequence — and any candidate with an equal sequence but different content fingerprint — as a downgrade/replay, unless an explicit operator rollback (FR-010) is in effect. The embedded default competes under the same rule; when no higher-sequence valid candidate exists, the embedded default still serves (scanning never stops) with the retained floor surfaced.
- **FR-010**: An explicit rollback action MUST let an operator revert to the embedded default or a named bundle file: the target is re-validated (signature policy and self-check still apply), activated despite a lower sequence, the persisted floor is reset to the rolled-back sequence, and the override is loudly logged and visible in bundle status. Rollback MUST be the ONLY path that lowers the floor.
- **FR-011**: The loader MUST enforce load ceilings (maximum bundle byte size and maximum rule count, fixed in design) and reject a candidate exceeding them as a whole; all patterns remain RE2-only. A database at the ceilings MUST NOT measurably regress the scan hot path (see SC-008).

**Seed corpus**

- **FR-012**: The seed corpus MUST catalog known public TPA campaign/technique classes as signatures with stable `TPA-YYYY-NNNN` identities, covering at minimum: hidden-instruction blocks (the TPA-2026-0001 class), hidden HTML/comment directives, tool shadowing and cross-server override, exfiltration redirects, sensitive-file coaxing (SSH keys, cloud credentials), and rug-pull phrasing patterns. Each signature MUST carry category, detector(s), level, confidence, provenance (public source reference), and a redistributable license; corpus build MUST refuse contributions lacking either (Spec 087 FR-021 parity).
- **FR-013**: Every signature intended to gate approvals MUST ship with eval evidence in the frozen eval dataset: at least one labeled gated-malicious sample and at least one category-matched hard-negative following the dataset validator's conventions (including `hn_<attack_category>_*` naming), so the gate can measure both its recall contribution and its false-positive risk.
- **FR-014**: Gating signatures MUST emit hard-tier signals (the eval gate and the `scan`-mode approval gates score hard tier only); any soft-tier-only signature MUST be explicitly marked non-gating in its metadata and MUST NOT be counted toward gate coverage.
- **FR-015**: Signatures MUST be authored in the corpus source-of-truth pipeline (signature sources compiled to the bundle); mcpproxy MUST continue to consume only the compiled bundle and never parse signature sources (Spec 087 FR-002 parity). The contribution pipeline MUST run the same validation the loader runs (compile, schema, license, eval gate) before a bundle can be signed.
- **FR-016**: The CI eval gate (`cmd/scan-eval --gate`, recall ≥ 0.90 over gated categories, hard-negative FP rate ≤ 0.05, existing vacuity guard) MUST pass for: every change to the embedded default corpus (merge-blocking, existing `eval.yml` D2 job) and every published bundle BEFORE it is signed — a bundle that fails the gate is never signed or published. Thresholds are reused verbatim, not redefined.

**Publication & refresh channel**

- **FR-017**: Published databases MUST be versioned, signed artifact pairs (bundle + sidecar) at a stable public location with a discoverable "latest" reference; the Spec 087 opt-in fetch consumes this channel unchanged, and manual download + file drop MUST remain a first-class, documented, equally-capable path. [NEEDS CLARIFICATION: hosting location for the published artifacts — GitHub Releases on the tpa-db corpus repo vs. a mcpproxy.app static path (affects URL stability, availability SLO, and bandwidth, not behavior)]
- **FR-018**: This feature MUST NOT introduce a second refresh lifecycle: cadence, single-flight, on-demand trigger, activation self-check, and fail-safe behavior are Spec 087's; this spec only inserts the signature and sequence stages (FR-008) and defines the artifacts flowing through it.
- **FR-019**: After a new database activates, the system MUST run a post-activation informational re-scan over cached tool metadata of already-approved toolsets, off the hot path, without connecting to upstreams: new hard-tier hits surface as review findings naming the TPA id. Refresh alone MUST NOT auto-revoke approvals, auto-quarantine servers, or auto-approve previously-held tools; all state changes from re-scan flow toward human review only.

**Observability, telemetry & docs**

- **FR-020**: Bundle status on all existing surfaces (CLI security commands, REST security overview, Web UI) MUST additionally report sequence, signature-verified state, and staleness (age since `generated_at`), alongside the existing version/fingerprint/source/load-error fields, and MUST show the last rejection reason (tamper, downgrade, gate failure) so an operator can tell "current and verified" from "stuck on last-known-good and why".
- **FR-021**: The existing anonymous TPA-scanner telemetry MUST gain the active bundle version, sequence, and source (and signature-verified state) so database adoption and freshness are measurable fleet-wide; the existing telemetry opt-out MUST fully cover these fields, and no new identifying data is introduced.
- **FR-022**: Documentation MUST cover the database lifecycle end to end for operators (verify, drop, fetch, roll back, read status) and for contributors (author a signature, provenance/license rules, eval-sample requirements, how the gate blocks a bad corpus), updating the existing security-quarantine/tool-scanner docs.

### Key Entities *(include if feature involves data)*

- **Signature Database (bundle + sidecar)**: the publishable unit — the deterministic compiled `scanner-bundle.json` plus a detached signature over its exact bytes. Identified by (bundle_version, sequence, fingerprint).
- **Bundle Manifest metadata**: `bundle_version`/`schema_version` (existing), plus additive `generated_at`, `sequence`, publisher key id.
- **Publisher Trust Anchor**: the set of publisher public keys embedded in a binary release, unioned with operator-configured keys; the only roots of trust for verification, never network-delivered.
- **Signature (TPA record)**: one cataloged campaign/technique — `TPA-YYYY-NNNN` id, detectors, category, level, confidence, tier intent (gating vs non-gating), provenance reference, license.
- **Eval Sample Pair**: the labeled gated-malicious sample(s) plus category-matched hard-negative(s) a gating signature must contribute to the frozen eval dataset.
- **Sequence Floor**: the persisted highest-activated sequence per install; the anti-downgrade/anti-replay state, lowered only by explicit rollback.
- **Publication Channel**: the stable public location of versioned signed artifact pairs with a "latest" reference; consumed by the Spec 087 fetch or by manual download.
- **Post-Activation Re-scan**: the one-shot, off-hot-path evaluation of cached approved tool metadata against a newly-activated database, emitting review findings only.
- **Active Database / Last-Known-Good**: unchanged from Spec 086/087 — the serving bundle and the fail-closed fallback target.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of tampered candidates (any byte of bundle or sidecar altered, or mismatched pair) are rejected before parsing, with the last-known-good database still serving and the reason surfaced — demonstrated by fixture in CI.
- **SC-002**: A validly-signed but lower-sequence bundle is never activated by any non-rollback path (drop, fetch, restart, embedded fallback race), and an explicit rollback both succeeds and leaves an audit trail — demonstrated by fixture in CI.
- **SC-003**: An air-gapped install (zero network, ever) reaches full P1 capability: verified activation of a file-dropped signed database, downgrade refusal, and rollback, with behavior byte-identical to a connected install's file-drop path.
- **SC-004**: The shipped seed corpus contains at least 25 signatures spanning at least 8 distinct public campaign/technique classes, every one carrying provenance + redistributable license, and every gating signature backed by its eval sample pair; the canonical fixture of each class produces a hard-tier finding naming its TPA id.
- **SC-005**: The scan-eval CI gate (recall ≥ 0.90 gated categories, hard-negative FP ≤ 0.05) passes with the full seed corpus embedded, and a deliberately-regressing corpus change is blocked twice independently: at CI (cannot merge) and at activation (self-check rejects it) — both demonstrated by fixture.
- **SC-006**: Fleet freshness is answerable from telemetry: for opted-in installs, the distribution of active database version/sequence/source is reportable, enabling the metric "% of active installs on a database ≤ 30 days old" — a metric that is impossible to compute today.
- **SC-007**: A newly published signature reaches detection within one daily refresh cycle on a fetch-enabled install (and immediately on manual drop), measured end to end: publish → active → the new signature's fixture fires.
- **SC-008**: With a database at the design load ceilings (≥ 500 regex rules), p95 scan latency over a 100-tool server changes by less than 10% versus the 6-signature baseline, and bundle load-plus-verify completes under one second on commodity hardware.
- **SC-009**: After activating a database containing a new signature, an already-approved tool whose cached description matches it is surfaced for review within the same refresh cycle, with zero approvals auto-revoked and zero servers auto-quarantined by the refresh itself.

## Assumptions

- Spec 087's refresh lifecycle (daily tick, single-flight, activation self-check, opt-in fetch, fail-safe) is implemented before or together with this feature's refresh story; this spec inserts stages into that pipeline rather than duplicating it. US1's signature/sequence verification is also exercised by the existing hot-reload/file-drop path (Spec 086 `ConfigureBundle`), so US1 does not hard-depend on 087 landing first.
- The Scanner Bundle Contract v0.1 line remains the wire format; signing metadata is additive and the sidecar is a separate file, so existing loaders and the byte-determinism guarantee are unaffected.
- The corpus source-of-truth pipeline (signature sources → compiled bundle) lives outside this repo's runtime (the tpa-db authoring pipeline); this repo consumes compiled bundles only and embeds one at build time (a release build with no embeddable bundle fails, per Spec 087 FR-001).
- The eval gate's thresholds, dataset conventions (including hard-negative naming), vacuity guard, and hard-tier-only scoring are reused verbatim; growing the corpus means growing the dataset alongside it.
- The offline tier's runnable surface remains `engine: regex` × `target: tool_description` for v1; `structural_diff`, `resource_content`, and `server_manifest` rules stay declared-not-runnable (skipped, never clean coverage) exactly as today.
- Cached tool metadata already held by mcpproxy (approval baselines, index) is sufficient for the post-activation re-scan; no upstream connection is initiated by refresh.
- [NEEDS CLARIFICATION: back-compat window for unsigned file drops — should `require_signed_bundle` flip to default-on after one or two release cycles once signed publishing is live, or remain opt-in indefinitely for air-gapped/self-built-corpus operators?]

## Out of Scope

- Any change to the detect engine's check semantics, tiers, thresholds, or the shared position classifier (a corpus change must never require touching `ClassifyPosition`; recall fixes belong in signatures + eval samples, not classifier cues).
- Running `structural_diff`/stateful rules, or adding `resource_content`/`server_manifest` scan surfaces.
- LLM-assisted or networked detection tiers; this database feeds the deterministic offline tier only.
- Remote/networked trust-anchor distribution, certificate hierarchies, or transparency logs (key set ships with the binary; revisit only if the publisher set ever grows beyond the project).
- Auto-revoking approvals, auto-quarantining, or auto-approving anything as a side effect of a database refresh (re-scan is informational; state moves toward review only).
- IoC hash feeds and package/registry reputation data (the roadmap note's "IoC hashes" are deferred: v1 targets are description-borne patterns; the format's additive versioning leaves room for later rule engines).
- mcpproxy binary self-update or release-awareness changes (Spec 087 US3 owns that surface).
- Changing the scan-eval gate's thresholds or scoring.

## Constitution Check *(note)*

Principle IV (Security by Default) governs this spec: the database is treated as a hostile input until proven otherwise (verify-before-parse, fail-closed to last-known-good, anti-downgrade floor, mandatory signatures on the network path), and refresh can never silently widen approvals. Principle III (Configuration-Driven Architecture): `require_signed_bundle`, operator keys, and the existing bundle path/fetch settings live in `mcp_config.json` with env override and hot-reload; no hardcoded URLs or paths. Principle V (TDD): every rejection class (tamper, downgrade, unsigned, gate regression, ceiling breach) is built against failing fixtures first, and the corpus itself is test-gated by scan-eval. Principle I (Performance at Scale): verification, activation, and the post-activation re-scan stay off the scan hot path with atomic swaps (SC-008).

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat(security): [brief description of change]

Related #[issue-number]

[Detailed description of what was changed and why]

## Changes
- [Bulleted list of key changes]
- [Each change on a new line]

## Testing
- [Test results summary]
- [Key test scenarios covered]
```
