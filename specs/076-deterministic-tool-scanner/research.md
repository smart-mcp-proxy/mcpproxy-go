# Phase 0 Research: Deterministic Offline MCP Tool-Scanner v2

All major unknowns were resolved during the brainstorming session (five locked decisions) and the external-scanner research sweep. This file records the decisions and their rationale; there are no open `NEEDS CLARIFICATION` items.

## Decision 1 — Detection engine boundary: deterministic-only, fully offline

- **Decision**: No LLM, no external API, no network/FS/Docker. Every check is a pure function over tool metadata.
- **Rationale**: mcpproxy is local-first and privacy-conscious; mcp-scan's default ships tool descriptions to a cloud API, which we reject. The external-scanner survey (Invariant mcp-scan, Snyk agent-scan, Mondoo, Trail of Bits) converged on the finding that **deterministic structural checks alone capture ~90% of the value** at near-zero FP. Offline also eliminates the availability/arch failure modes that already plague the Ramparts/Cisco plugins.
- **Alternatives considered**: (a) deterministic core + optional local-LLM intent layer — deferred, can be added later behind a flag without disturbing the deterministic core; (b) cloud-LLM/external — rejected (data egress + reliability regression).

## Decision 2 — Two-tier enforcement

- **Decision**: HARD signals auto-quarantine; SOFT signals raise for review with severity = count of distinct soft signals.
- **Rationale**: Matches the existing quarantine model and the field consensus (Snyk's threshold model). Keeps FP-driven breakage out of the auto-block path while still surfacing weaker evidence. False-positive fatigue is the documented killer of quarantine adoption.
- **Alternatives**: report-only (no auto-protection) and quarantine-on-any-finding (FP-fatigue trap) — both rejected.

## Decision 3 — Scope: rebuild the in-process detector only

- **Decision**: Replace the in-process TPA/keyword engine; reuse quarantine hashing + state machine + report types + `patterns/`. Leave external plugins and SARIF normalization untouched.
- **Rationale**: Smallest, highest-leverage change that directly fixes the measured 10% recall. The quarantine state machine is already well-designed; the external-plugin Docker/arch issues are a separate reliability problem.

## Decision 4 — Reliability is a CI-gated number

- **Decision**: Target recall ≥ 0.90 on malicious, FP ≤ 5% on hard-negatives; expand the corpus; make `cmd/scan-eval` a blocking CI gate.
- **Rationale**: The detector drifted to 10% recall *because* the existing eval was non-blocking. Gating converts "reliable" from opinion into a build contract (constitution principle V, TDD).
- **Baseline reference**: `specs/065-evaluation-foundation/datasets/baseline_v1.json` records the current `sensitive-data` detector at precision 0.67 / recall 0.10 / F1 0.17.

## Decision 5 — Engine architecture: signal pipeline (new `detect` package)

- **Decision**: Independent per-check `Signal` producers + two-tier aggregator (Approach A).
- **Rationale**: Only structure where reliability is structurally enforced — each check is unit-tested in isolation against the corpus, and the aggregator's distinct-signal-count scoring fixes the consensus-masking dedup bug by design. Reuses all existing plumbing.
- **Alternatives**: extend `inprocess.go` in place (retrofits two-tier awkwardly, carries the shape that produced 10% recall); YAML rule engine (over-engineered; the high-signal checks are code, not declarative patterns).

## Technique research — the six checks (sources)

From the external-scanner sweep (Invariant Labs tool-poisoning disclosure + mcp-scan; Snyk `agent-scan` issue-code taxonomy; Mondoo 6-layer pipeline; Trail of Bits "line jumping"; Simon Willison on prompt injection):

| Check | Tier | Basis in the field |
|-------|------|--------------------|
| `unicode.hidden` | hard | Snyk W021 (hidden Unicode; escalate on ≥3 classes / decoded tag-message), Mondoo layer 4 |
| `shadowing.cross_server` | hard | Snyk E002 (cross-server tool reference), Invariant shadowing PoC |
| `payload.decoded` | hard | Snyk E005/E006 decode-then-confirm, Mondoo YARA layer — flag *decoded → shell*, not "looks base64" |
| `directive.imperative` | soft | Snyk W001 + Invariant `<IMPORTANT>` direct-poisoning payloads; regex families over normalized text |
| `capability.mismatch` | soft | Mondoo `description_mismatch` / Snyk capability heuristics; Invariant "sidenote" unused-param exfil |
| `secret.embedded` | soft | reuse existing `patterns/`, add confidence (Luhn-validated card high, entropy-only low) |

**Rug-pull / changed-hash** stays in the Spec-032 quarantine layer (TOFU pinning — the single most reliable deterministic check, already built); the detector surfaces it as a report signal for consensus but does not re-implement it.

## Reliability technique decisions

- **Normalization order**: raw-text checks (unicode, decode) run on original bytes *before* NFKC normalization, so normalization cannot mask the very hidden characters being detected. Keyword/directive/capability checks run on the normalized form — this defeats the "don't disclose" vs "do not tell" variant-miss that fixed-phrase matching suffers.
- **Hard-negative discrimination**: instruction-position vs example-position classifier. Phrases after "such as / e.g. / example", inside quotes, or in a "detects/flags …" list get confidence discounted; imperative phrases addressed to the model keep full confidence. This is the empirically-tuned discriminator that lifts recall without FP blowup on the 8 hard-negatives.
- **Evidence safety**: rendered evidence is truncated and control-char/zero-width escaped so the scan report cannot itself carry a live payload into a viewer.

## Corpus expansion sources

- Invariant `mcp-injection-experiments` (direct poisoning, shadowing, rug-pull, exfil-obfuscation) — author original equivalents where redistribution licensing is unclear (flagged in the research).
- MCPTox benchmark (arXiv 2508.14925) — reference for additional real-world variants.
- New original fixtures: Unicode smuggling (each hidden class + combinations), base64/hex→shell, "sidenote" capability-mismatch, plus expanded hard-negatives (security tools that legitimately mention attack strings).
