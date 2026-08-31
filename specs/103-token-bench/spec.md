# Feature Specification: Token-efficiency benchmark — measured savings, published results

**Feature Branch**: `103-token-bench`
**Created**: 2026-08-31
**Status**: Draft
**Input**: User description: "token-bench: measure real token cost AND task-success rate across every mcpproxy routing/serialization mode combination, on replayed real agent sessions and public agent-loop suites, so every savings number mcpproxy quotes becomes a reproducible measurement"

## Why This Exists

MCPProxy's headline promise is "massive token savings". Today that promise rests on a
measurement of the **static tool surface** — how many tokens the tool definitions cost an
agent before it does any work. That number is real, reproducible and already gated in CI.

It is also not the number a user cares about. A user cares what a *task* costs. A mode that
halves the tool surface but doubles the number of failed attempts costs more, not less.
Nothing in the repo measures that today.

Two concrete facts make this urgent:

1. **Spec 102 projected ~70% reduction and measured 29.7%** on the frozen 45-tool corpus
   (34.8% on a 527-tool snapshot). Its success criterion was restated per corpus shape
   rather than the design being changed, and its own gate concluded that names,
   descriptions and annotations — not schemas — dominate the payload, with 38.9% the
   arithmetic ceiling on that corpus even if both schema and signature were deleted.
   That conclusion is drawn from two frozen corpora and has never been checked against a
   real fleet.
2. **Retry rates are assumed, not observed.** The session-cost rows in today's report are
   computed from per-arm literature-derived defaults, and the dashboard honestly labels
   them `estimated`. Every session-level savings claim inherits that assumption.

This feature replaces assumptions with measurements, and publishes the methodology so the
numbers can be checked by someone who does not trust us.

## Scope Boundary *(read before planning)*

An extensive benchmark harness already exists and is CI-wired. **This feature extends it.
Re-implementing any of the following is out of scope and would be a defect:**

| Already exists | Do not rebuild |
|---|---|
| Six tool-definition encoding arms, incl. the spec-102 deferred rendering | arm framework, arm contracts |
| Token counting derived from the *live* tool builders (cannot drift from production) | tokenizer, catalog extraction |
| Live response-cost measurement, break-even analysis | response cost, break-even |
| Retrieval quality metrics (recall@k, nDCG) and a parity gate through the production index | retrieval scoring |
| Versioned report + self-contained dashboard with `measured`/`computed`/`estimated` provenance badges on every headline number | report schema, dashboard |
| Frozen tool corpora at 45-tool and 527-tool scale, plus an external tool-retrieval corpus and an independent third-party linter | corpus loaders |

The provenance-badge convention is load-bearing for this feature: everything it adds must
declare whether it is measured, computed or estimated, and the point of the work is to move
specific numbers from `estimated` to `measured`.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — A maintainer measures what a task actually costs (Priority: P1)

A maintainer wants to know, for a given mode combination, how many tokens it takes to
finish a real unit of work — not how many tokens the menu costs. They point the harness at
a set of previously recorded real sessions, run every mode combination over them, and get a
per-mode cost-per-completed-task figure alongside how often the agent got the call right
the first time.

**Why this priority**: This is the feature's core claim and the only part that cannot be
approximated from what exists. Without it, every session-level number stays an estimate.

**Independent Test**: Record or supply a set of real sessions, run the harness across the
mode matrix, and confirm the report contains a per-mode cost-per-completed-task and a
first-call success rate, each marked `measured` rather than `estimated`.

**Acceptance Scenarios**:

1. **Given** a set of exported real sessions, **When** a maintainer runs the replay across
   all mode combinations, **Then** the report shows, per mode, tokens per completed task,
   first-call success rate and retry count, each carrying a `measured` provenance badge.
2. **Given** a recorded session whose stored responses were truncated when captured,
   **When** the replay processes it, **Then** that session is excluded from token totals or
   explicitly annotated as under-counting, and the report states how many sessions were
   affected — it is never silently included.
3. **Given** two runs of the same replay over the same session set and the same modes,
   **When** their reports are compared, **Then** the deterministic figures are identical and
   any model-dependent figure is reported as a mean across runs with its spread.
4. **Given** a mode that reduces tool-surface tokens but lowers task completion,
   **When** the report is generated, **Then** its cost-per-completed-task rises, making the
   regression visible rather than hidden behind a favourable surface number.

---

### User Story 2 — A skeptical reader reproduces the published numbers (Priority: P2)

Someone who does not trust the project reads a published claim, follows the documented
procedure, and arrives at the same numbers within a stated tolerance — including the
comparison against not using MCPProxy at all.

**Why this priority**: An unreproducible benchmark is marketing. This is what separates
this work from the thin-methodology comparisons already circulating in this space. It
depends on US1 producing numbers worth reproducing.

**Independent Test**: A person with no prior context follows the published procedure on
their own machine and reproduces the deterministic figures exactly and the model-dependent
figures within the stated tolerance.

**Acceptance Scenarios**:

1. **Given** the published methodology, **When** an outside reader follows it, **Then** every
   input needed is either included in the repository or fetched by a documented pinned
   procedure, and no step depends on data only the maintainers hold.
2. **Given** a published comparison, **When** a reader inspects the baseline, **Then** the
   baseline is the same agent doing the same tasks with all tools loaded directly, not a
   different or weaker configuration.
3. **Given** any published percentage, **When** a reader looks it up, **Then** it is
   accompanied by the size and shape of the tool set it was measured on, because the
   figure moves with fleet size.
4. **Given** a headline figure, **When** a reader traces it, **Then** they can reach the raw
   per-run records it was computed from.

---

### User Story 3 — The 29.7% shortfall gets an answer (Priority: P2)

A maintainer wants to know whether the spec-102 conclusion — that names, descriptions and
annotations dominate the payload, capping achievable savings well below the projection —
holds on real fleets, or was an artefact of two frozen corpora.

**Why this priority**: This determines whether further serialization work is worth doing at
all, and it is the first question anyone will ask about the published numbers. It reuses
US1's machinery rather than adding its own.

**Independent Test**: Produce a payload decomposition across corpora of different sizes and
shapes and compare it against the spec-102 conclusion, yielding an explicit confirmation
or correction.

**Acceptance Scenarios**:

1. **Given** tool sets of varying size and shape, **When** the decomposition runs, **Then**
   the report attributes payload share to names, descriptions, annotations and schemas
   separately, and shows how each share moves with fleet size.
2. **Given** that decomposition, **When** it is compared with the spec-102 conclusion,
   **Then** the result explicitly states whether that conclusion is confirmed or corrected,
   and the arithmetic ceiling is recomputed per corpus rather than assumed.

---

### User Story 4 — Results reach the people deciding whether to adopt (Priority: P3)

A developer evaluating MCPProxy reads a public write-up with real numbers, an honest
account of where savings do *not* materialise, and enough method to judge it.

**Why this priority**: The measurement has no external value until it is published, but it
is strictly downstream of having trustworthy numbers.

**Independent Test**: The published write-up states the measured figures, the corpus shapes
they hold for, the known limitations, and the reproduction procedure.

**Acceptance Scenarios**:

1. **Given** the write-up, **When** a reader finishes it, **Then** they know which mode to
   choose for their own fleet size and why.
2. **Given** a case where a mode does not help, **When** the reader looks for it, **Then**
   the write-up says so plainly rather than omitting it.

---

### Edge Cases

- **A recorded session cannot be replayed** because the upstream servers it used are gone
  or have changed: the session must be reported as unreplayable rather than counted as a
  failure of the mode under test, since that would blame the mode for missing infrastructure.
- **Recorded responses were truncated at capture time**, so replaying them under-counts
  tokens. Detection is mandatory; silent inclusion is the single most dangerous failure
  mode here, because it biases every number in the project's favour.
- **Response bodies are unavailable** because the export withheld them: the harness must
  say what it cannot measure rather than substituting a guess.
- **A mode combination is not valid** (an axis that does not apply to the selected routing
  mode): the matrix must skip it explicitly and show it as skipped, not as zero.
- **A task suite performs real writes** against live third-party services: destructive
  operations must be handled deliberately, and a benchmark run must never be capable of
  damaging a real user's data.
- **A run is interrupted partway** through an expensive matrix: partial results must be
  identifiable as partial and must never be published as a complete comparison.
- **A model-dependent figure comes from a single run**: single-run agentic numbers are
  noise; the report must refuse to present one as a headline.
- **Sessions contain sensitive data** — real recorded traffic may include secrets or
  personal data. Replay inputs must be handled so that publishing results never publishes
  session contents.

## Requirements *(mandatory)*

### Functional Requirements

**Replay of real sessions**

- **FR-001**: The harness MUST accept previously recorded real agent sessions as input via
  the existing export path, without requiring a new capture mechanism.
- **FR-002**: The harness MUST detect recorded entries whose stored content was truncated at
  capture time and MUST either exclude them from token totals or annotate them as
  under-counting; it MUST NOT include them silently.
- **FR-003**: The harness MUST report how many supplied sessions were unusable and why,
  distinguishing truncation, missing bodies, and unreplayable upstreams.
- **FR-004**: The harness MUST treat recorded session content as sensitive: results and
  published artefacts MUST NOT contain session contents.

**Measured success and retries**

- **FR-005**: The harness MUST measure first-call success rate — how often the agent's first
  attempt at a tool call is accepted — per mode combination.
- **FR-006**: The harness MUST measure retry counts per mode combination, replacing the
  current assumed per-arm defaults for any mode where a measurement exists.
- **FR-007**: Any figure still derived from an assumption MUST remain marked as an estimate
  and MUST NOT be presented alongside measured figures without that distinction.
- **FR-008**: The harness MUST report a task-completion signal per replayed unit of work, so
  that cost can be expressed per *completed* task rather than per attempt.

**The mode matrix**

- **FR-009**: The harness MUST cross all three configuration axes that govern what an agent
  sees: the routing mode, the serialization mode of the discovery surface, and the
  serialization mode of the direct surface.
- **FR-010**: The matrix MUST additionally cover the batching, stored-script and
  validate-before-dispatch capabilities, since each trades one kind of cost for another.
- **FR-011**: Combinations that are not meaningful MUST be reported as skipped with a reason,
  never as a zero or a missing row.
- **FR-012**: The report MUST express headline cost as tokens per completed task, with the
  static tool-surface cost retained as a separate, clearly-labelled figure.
- **FR-013**: Token accounting MUST separate input, output and cached-read consumption.

**Honest comparison**

- **FR-014**: The baseline arm MUST be the same agent performing the same tasks with all
  tools loaded directly. A comparison against a different or weaker configuration MUST NOT
  be published.
- **FR-015**: Any model-dependent figure MUST be reported as an average over at least four
  runs together with a measure of consistency across runs; a single run MUST NOT be
  reported as a headline result.
- **FR-016**: Every published percentage MUST carry the size and shape of the tool set it was
  measured on.
- **FR-017**: The report MUST include a cost-versus-outcome view so a reader can see which
  modes are worth their savings rather than only which are cheapest.

**Payload decomposition**

- **FR-018**: The harness MUST attribute tool-definition payload share to names,
  descriptions, annotations and schemas separately, across at least two corpus shapes.
- **FR-019**: The result MUST explicitly confirm or correct the spec-102 conclusion about
  what dominates the payload, and MUST recompute the achievable ceiling per corpus rather
  than carrying a fixed figure forward.

**Public suites**

- **FR-020**: The harness MUST support running at least one public task suite that exercises
  a full agent loop, in addition to the retrieval and token corpora that already exist.
- **FR-021**: Any suite performing real writes against third-party services MUST be run in a
  way that cannot damage real user data.
- **FR-022**: Suite versions and configuration MUST be pinned so a later run is comparable to
  an earlier one.

**Reproducibility and publication**

- **FR-023**: Generated reports MUST NOT be committed to the repository; only code, fixtures,
  thresholds and methodology are versioned.
- **FR-024**: Raw per-run records MUST be retained and referenced by the report so a headline
  figure can be traced to its inputs.
- **FR-025**: Every input MUST be either included in the repository or obtainable by a
  documented, pinned procedure.
- **FR-026**: The published write-up MUST state measured figures, the corpus shapes they hold
  for, known limitations, and the reproduction procedure, and MUST state plainly where
  savings do not materialise.
- **FR-027**: A partial or interrupted run MUST be identifiable as partial and MUST NOT be
  publishable as a complete comparison.

### Key Entities

- **Replayed session**: A previously recorded unit of real agent work, with its sequence of
  tool calls and enough context to re-run it. Carries usability flags (truncated, missing
  bodies, unreplayable).
- **Mode combination**: One point in the configuration matrix — a routing mode plus the
  serialization settings and capability toggles that determine what the agent sees and how
  it calls.
- **Run record**: The raw outcome of executing one session under one mode combination:
  token consumption split by kind, attempts, first-call outcome, completion, and any error.
- **Measurement**: An aggregate over run records, always carrying its provenance
  (`measured`, `computed`, or `estimated`) and, where model-dependent, its run count and
  spread.
- **Payload decomposition**: The attribution of tool-definition cost to names, descriptions,
  annotations and schemas for a given corpus.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For every mode combination in the matrix, a maintainer can obtain a cost per
  completed task and a first-call success rate, both marked as measured rather than
  estimated.
- **SC-002**: No session-level cost figure in the report is derived from an assumed retry
  rate unless it is explicitly labelled as an estimate.
- **SC-003**: A person outside the project, following only the published procedure,
  reproduces the deterministic figures exactly and the model-dependent figures within the
  stated tolerance.
- **SC-004**: Every published percentage states the tool-set size and shape it was measured
  on; a percentage without that context does not appear.
- **SC-005**: The spec-102 payload conclusion is explicitly confirmed or corrected, with the
  achievable ceiling recomputed for each corpus measured.
- **SC-006**: A deliberately degraded mode — one that costs fewer surface tokens but
  completes fewer tasks — is visibly worse in the report's headline metric, demonstrating
  that the benchmark detects a false saving rather than rewarding it.
- **SC-007**: Recorded sessions with truncated content are never counted silently: the
  report states how many were affected and how they were handled.
- **SC-008**: At least one full agent-loop public task suite runs against MCPProxy and
  produces comparable results across at least two mode combinations.
- **SC-009**: No generated report is committed to the repository.
- **SC-010**: A reader of the published write-up can state which mode suits their fleet size
  and can name at least one case where MCPProxy's savings do not materialise.

## Assumptions

- **Existing harness is the foundation.** Everything listed under Scope Boundary is treated
  as available and correct; this feature adds to it rather than replacing it. If a
  measurement here contradicts an existing one, that is a finding to investigate, not a
  reason to fork the harness.
- **Recorded sessions are the primary corpus.** Real recorded work is more representative
  than synthetic tasks, so it drives the headline. Public suites provide external validity
  and comparability, not the headline.
- **A task-completion signal can be derived from recorded sessions.** Where it cannot be
  derived reliably, the affected sessions are reported as lacking a completion signal
  rather than being assumed complete.
- **Model-dependent measurement costs money and needs a decision.** Any full agent-loop run
  requires a pinned model and a spending decision before it can be built; the deterministic
  parts of this feature are designed to stand alone without it.
- **Reporting conventions carry over.** The provenance-badge discipline, the
  reports-are-never-committed rule, and the requirement to quote corpus size alongside any
  percentage all continue to apply.
- **Telemetry cross-validation is downstream.** Confirming these findings against the real
  user population is a later, separate step and is not required for this feature to be
  complete.
- **Publication happens outside this repository**, so the write-up is a deliverable of this
  feature but lands elsewhere; it extends the existing published comparison work rather
  than duplicating it.

## Out of Scope

- Changing any routing or serialization behaviour. This feature measures; it does not
  optimise. Findings may motivate later work.
- Real-user population validation through telemetry counters.
- Retrieval-quality evaluation, which already exists and is separately gated.
- Suites that are not MCP-native, and any suite that cannot be pointed at a single proxy
  endpoint.

## Dependencies

- The recorded-session export path must expose enough per-call detail — including response
  content when explicitly requested — for token accounting.
- The mode configuration axes must be settable per run so the matrix can be crossed.
- A pinned model and a spending decision are prerequisites for the agent-loop portions
  (US2's public-suite half and any measured retry rate that requires live inference).

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
feat: [brief description of change]

Related #[issue-number]

[Detailed description of what was changed and why]

## Changes
- [Bulleted list of key changes]
- [Each change on a new line]

## Testing
- [Test results summary]
- [Key test scenarios covered]
```
