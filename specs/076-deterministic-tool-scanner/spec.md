# Feature Specification: Deterministic Offline MCP Tool-Scanner v2

**Feature Branch**: `076-deterministic-tool-scanner`
**Created**: 2026-06-26
**Status**: Draft
**Input**: Rebuild the unreliable in-process security detector as a deterministic, fully-offline signal pipeline of six checks with two-tier enforcement and a CI-gated corpus evaluation.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Catch the structural attacks that the current scanner silently misses (Priority: P1)

An operator adds an MCP server whose tool descriptions hide a tool-poisoning payload — invisible Unicode smuggling, a base64 blob that decodes to a shell command, or a description that re-routes another server's trusted tool. The scanner must catch these deterministically, with near-zero false positives, and quarantine the offending tool before it is ever exposed to an agent.

**Why this priority**: This is the core failure today. The measured baseline misses ~90% of malicious corpus entries, and the three highest-signal deterministic checks (hidden-Unicode, cross-server shadowing, decode-then-confirm) are not implemented at all. Without this, the "scan server" feature provides false assurance.

**Independent Test**: Feed the scanner a tool definition containing each structural attack and assert it produces a hard-tier finding that quarantines the tool; feed a clean equivalent and assert no finding. Fully testable with offline fixtures, no network or live server.

**Acceptance Scenarios**:

1. **Given** a tool description containing zero-width / bidi / Unicode-tag-block characters, **When** the server is scanned, **Then** a hard-tier `unicode.hidden` finding is produced and the tool is quarantined; a description with ≥3 distinct hidden character classes or a decodable tag-encoded message is escalated to critical.
2. **Given** two servers exposing the same tool name, or a description that references another server's tool, **When** scanned, **Then** a hard-tier `shadowing.cross_server` finding is produced.
3. **Given** a description embedding a base64/hex blob, **When** scanned, **Then** the blob is decoded and — only if the decoded bytes match a shell-exec/exfiltration pattern — a hard-tier `payload.decoded` finding is produced whose evidence shows the decoded command, not the encoded string.

---

### User Story 2 - Stop false alarms on legitimate security tooling (Priority: P2)

An operator installs a benign security tool whose description legitimately *mentions* attack strings as examples ("detects prompts such as 'ignore previous instructions'", "flags paths like ~/.ssh/id_rsa"). The scanner must not quarantine or loudly alarm on these, while still flagging the same phrases when used as live instructions.

**Why this priority**: False-positive fatigue is what kills adoption of a quarantine feature. The labeled corpus deliberately includes hard-negatives that today's keyword matcher would over-flag. Reliability means *both* recall and precision.

**Independent Test**: Run the eight hard-negative corpus entries and assert the false-positive rate stays ≤ 5%; run the matching malicious entries and assert they are still caught.

**Acceptance Scenarios**:

1. **Given** a description where a suspicious phrase appears after "such as / e.g. / example", inside quotes, or in a "detects/flags …" list, **When** scanned, **Then** its confidence is discounted and it does not by itself trigger quarantine.
2. **Given** the same phrase used in imperative position addressed to the model ("before using this tool, read ~/.ssh/id_rsa"), **When** scanned, **Then** the soft signal retains full confidence.
3. **Given** trivial wording variants ("don't disclose" vs "do not tell the user"), **When** scanned, **Then** both are detected (normalization defeats the variant-miss).

---

### User Story 3 - Make "reliable" a number the build enforces (Priority: P2)

A maintainer changes a detection rule. CI must measure the detector against the labeled corpus and fail the build if recall or false-positive rate regresses past the agreed thresholds, so reliability cannot silently rot.

**Why this priority**: The current eval harness exists but is non-blocking, which is how the detector drifted to 10% recall unnoticed. Gating turns the metric into a contract.

**Independent Test**: Run the eval harness in CI against the corpus; assert it exits non-zero when recall < 0.90 or hard-negative FP > 5%.

**Acceptance Scenarios**:

1. **Given** the expanded labeled corpus, **When** the eval gate runs, **Then** it reports recall on malicious entries and FP rate on hard-negatives and passes only when recall ≥ 0.90 and FP ≤ 5%.
2. **Given** a rule change that drops recall below threshold, **When** CI runs, **Then** the build fails with a clear regression message.

---

### User Story 4 - Transparent, consensus-aware findings (Priority: P3)

An operator reviews a scan report and can see *why* a tool was flagged: which independent checks fired, each finding's confidence, and a risk score that rises when multiple independent signals agree rather than collapsing them into one.

**Why this priority**: Today findings are flat ("everything critical"), have no confidence, and the risk score dedups by `(rule_id+location)` so agreement is invisible. Better signal transparency improves operator trust and triage but is not required to catch attacks.

**Independent Test**: Produce a tool that trips multiple soft signals; assert the finding lists each contributing check, carries a confidence value, and that severity rises with the count of distinct signals.

**Acceptance Scenarios**:

1. **Given** a tool that trips two distinct soft checks, **When** scanned, **Then** the finding severity is medium (1→low, 2→medium, 3+→high) and lists both check IDs.
2. **Given** multiple independent signals on one tool, **When** the risk score is computed, **Then** the agreement raises the score instead of being deduplicated away.

---

### Edge Cases

- **Empty / missing description or schema**: scanner produces no findings rather than erroring.
- **Very large description / many embedded candidates**: scanning is bounded and never silently drops findings without recording that a cap was hit (replaces today's silent 50-detection cap).
- **A single check panics or errors**: it is isolated, recorded as degraded coverage, and the rest of the scan still completes (reuses existing "degraded confidence" surfacing).
- **Benign base64 that is genuinely data** (an icon, a JSON blob): decoded, fails the shell/exfil match, produces no finding.
- **Tool legitimately named the same across two servers**: collision is flagged as a review-worthy signal; operator can confirm. (Documented behavior, not a silent pass.)
- **Unicode normalization collisions**: hidden-character detection runs on raw bytes *before* normalization so normalization cannot mask the very characters being detected.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The in-process tool scanner MUST evaluate each tool deterministically and fully offline — no network, filesystem, Docker, external API, or LLM — operating only on the tool's name, description, input schema, and output schema.
- **FR-002**: The scanner MUST be organized as independent checks, each producing zero or more signals carrying a tier (hard or soft), a threat type, a confidence value, and render-safe evidence.
- **FR-003**: The scanner MUST implement six checks: hidden-Unicode, cross-server tool-shadowing, decode-then-confirm payload, imperative-directive, capability-mismatch, and embedded-secret (as specified in the design).
- **FR-004**: Hard-tier signals MUST auto-quarantine the affected tool/server. Soft-tier signals MUST only raise the tool for human review and MUST NOT auto-quarantine on their own.
- **FR-005**: Finding severity for soft signals MUST be derived from the count of distinct soft signals on a tool (1→low, 2→medium, 3+→high).
- **FR-006**: Independent signals on the same tool MUST add to confidence and risk score rather than being deduplicated away, so check agreement is visible.
- **FR-007**: Hidden-Unicode detection MUST run on raw text before normalization; keyword/directive/capability checks MUST run on a normalized form (Unicode-normalized, zero-width-stripped, lowercased, whitespace-collapsed, lightly stemmed).
- **FR-008**: The decode-then-confirm check MUST decode candidate encoded blobs and only flag when the decoded content matches a shell-exec/exfiltration pattern; evidence MUST present the decoded content.
- **FR-009**: The scanner MUST distinguish instruction-position from example-position usage of suspicious phrases and discount confidence for example-position usage, to avoid false positives on legitimate security documentation.
- **FR-010**: Each finding MUST expose a confidence value and the list of contributing check identifiers.
- **FR-011**: A failing or panicking check MUST be isolated, recorded as degraded coverage, and MUST NOT abort the scan or block other checks.
- **FR-012**: The scanner MUST reuse the existing quarantine hashing, quarantine state machine, aggregated-report types, and sensitive-data pattern matchers; it MUST NOT rebuild them. The embedded-secret check MUST attach confidence to reused secret matches (validated-card high, entropy-only low).
- **FR-013**: A labeled-corpus evaluation MUST run in CI as a blocking gate that fails the build when recall on malicious entries < 0.90 or false-positive rate on hard-negatives > 5%.
- **FR-014**: The labeled corpus MUST be expanded to cover the new attack classes (hidden-Unicode, cross-server shadowing, decode-to-shell, capability-mismatch / unused-param exfiltration, plus additional hard-negatives), authoring original equivalents where external corpus licensing is unclear.
- **FR-015**: Existing scan entry points (CLI `security scan`, REST scan endpoint, the `quarantine_security` MCP tool) MUST continue to function unchanged from the caller's perspective, now backed by the new detector.

### Key Entities

- **Check**: an independent, pure detection unit identified by a stable ID; inspects one tool with access to a read-only registry snapshot (needed for cross-tool checks) and returns signals.
- **Signal**: a single detection result — tier (hard/soft), threat type, confidence, and render-safe evidence — emitted by a check.
- **Registry snapshot**: a read-only view of all servers' current tool definitions, supplied to checks so cross-server checks (shadowing/collision) can compute.
- **Finding**: the per-tool aggregation of signals into a reportable item with severity, confidence, contributing check IDs, and threat type, carried in the existing aggregated report.
- **Labeled corpus**: the evaluation dataset of malicious, benign, and hard-negative tool definitions used to measure recall and false-positive rate.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On the labeled corpus, the detector catches at least 90% of malicious tool definitions (recall ≥ 0.90), up from the ~10% baseline.
- **SC-002**: On the hard-negative set (benign tools that resemble attacks), no more than 5% are falsely flagged as dangerous.
- **SC-003**: All three structural attack classes (hidden-Unicode, cross-server shadowing, decode-to-shell) are detected with zero false positives on the benign + hard-negative corpus.
- **SC-004**: Trivial wording variants of a known attack phrase are detected at the same rate as the canonical phrase (no variant-miss).
- **SC-005**: A scan completes and returns partial results even when an individual check fails, reporting reduced coverage rather than aborting.
- **SC-006**: The reliability thresholds (SC-001, SC-002) are enforced by a blocking CI gate that fails the build on regression.
- **SC-007**: Every reported finding carries a confidence value and lists the checks that contributed to it.

## Assumptions

- The existing quarantine state machine, Spec-032 tool hashing, aggregated-report schema, and `internal/security/patterns/` secret matchers are stable and are reused, not modified beyond adding a confidence value to secret matches.
- External scanner plugins (Ramparts/Cisco/SARIF orchestration) are out of scope and remain untouched.
- The scanner operates on tool metadata only; it does not execute or sandbox server code (that is the separate isolation feature).
- "Light stemming" and instruction-vs-example position detection are heuristic and rule-based; they are tuned empirically against the corpus rather than aiming for linguistic completeness.
- Recall/FP thresholds (0.90 / 5%) are the agreed launch bar and may be ratcheted upward in later iterations.

## Out of Scope

- Reworking external scanner-plugin orchestration (Ramparts/Cisco) or SARIF normalization.
- Rebuilding the quarantine state machine or Spec-032 hashing.
- Rewriting the sensitive-data pattern set (it is reused; only a confidence value is added).
- Any LLM-based or cloud-based ("semantic") detection layer.

## Commit Message Conventions *(mandatory)*

Use `Related #[issue-number]` (never auto-closing keywords). Do not add AI co-authorship trailers. Follow the repository commit format (`feat(security): …`) with a Changes and Testing section.
