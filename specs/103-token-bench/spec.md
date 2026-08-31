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

## Replay Boundary *(read before planning — this bounds US1)*

A recorded session is a trace of what an agent **did** under the mode it was running. It
carries the tool calls, their arguments, the responses, status, timing and the grouping that
ties calls into one unit of user work. It does **not** carry the user's prompt, the
conversation, the model's state, or any oracle for whether the user's goal was met.

Therefore replay CAN answer: what this real workload, at this real fleet size, would have
cost under a different serialization or routing mode. That is arithmetic over recorded
traffic and is fully deterministic.

Replay CANNOT answer: how the agent would have *behaved* differently — which calls it would
have attempted, whether its first attempt would have been right, whether it would have
finished. Those are new decisions by a model that is not present in the recording.

Any requirement about success, retries or completion therefore belongs to a live agent loop
(US2), not to replay (US1). A design that infers agent behaviour from a recording is wrong,
and this boundary is the single most important thing for planning to respect.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Recompute real workload cost under every mode (Priority: P1)

A maintainer takes a set of previously recorded real sessions — the actual sequence of tool
calls a real agent made, at real fleet size — and asks what that same workload would have
cost under every other mode combination. The answer is arithmetic over recorded traffic: no
model is invoked and no agent decision is predicted.

**Why this priority**: It is the cheapest way to replace synthetic corpora with real workload
shapes, it needs no model spend, and it is fully deterministic. It is also strictly bounded:
see the Replay Boundary below for what it cannot answer.

**Independent Test**: Supply exported real sessions, run the recomputation across the mode
matrix, and confirm a per-mode cost for that workload, each figure marked `measured` or
`computed` and never `estimated`.

**Acceptance Scenarios**:

1. **Given** a set of exported real sessions, **When** a maintainer runs the recomputation,
   **Then** the report shows per-mode token cost for that workload, broken into tool-surface
   cost and response cost, on real fleet sizes rather than a frozen corpus.
2. **Given** a recorded session whose responses were truncated at capture, **When** it is
   processed, **Then** it is excluded from token totals or explicitly annotated as
   under-counting, and the report states how many sessions were affected — never silently
   included.
3. **Given** two runs over the same sessions and modes, **When** their reports are compared,
   **Then** the figures are byte-identical, since no model is involved.
4. **Given** any recomputed figure, **When** a reader inspects it, **Then** it is labelled as
   a counterfactual over recorded traffic, not as observed agent behaviour.

---

### User Story 2 — Measure whether a mode helps the agent succeed (Priority: P1)

A maintainer wants the number that actually matters: tokens per *completed* task. This
requires a live agent loop under a pinned model, because it depends on decisions the agent
makes differently under each mode — which calls it attempts, how often the first attempt is
right, and whether it finishes.

**Why this priority**: This is the feature's core claim. Without it every savings figure
remains a statement about menu size rather than about work done. It is separated from US1
because it has a fundamentally different cost, determinism and data requirement.

**Independent Test**: Run a fixed task set through a live agent under at least two mode
combinations and confirm the report gives, per mode, tokens per completed task, task
completion rate, first-attempt success rate and retry count, each `measured`.

**Acceptance Scenarios**:

1. **Given** a fixed task set, **When** it is run under each mode combination, **Then** the
   report gives per mode: task completion rate, tokens per completed task, first-attempt
   success rate and retry count, each `measured`.
2. **Given** a mode that lowers token use but completes fewer tasks, **When** the report is
   read, **Then** the completion drop is displayed with equal prominence to the token
   saving, and the mode is not presented as a saving.
3. **Given** any model-dependent figure, **When** it is published, **Then** it is an average
   over at least four runs with its spread, never a single run.

---

### User Story 3 — A skeptical reader reproduces the published numbers (Priority: P2)

Someone who does not trust the project reads a published claim, follows the documented
procedure, and arrives at the same numbers within a stated tolerance — including the
comparison against not using MCPProxy at all.

**Why this priority**: An unreproducible benchmark is marketing. This is what separates
this work from the thin-methodology comparisons already circulating in this space. It
depends on US1 and US2 producing numbers worth reproducing.

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

### User Story 4 — The 29.7% shortfall gets an answer (Priority: P2)

A maintainer wants to know whether the spec-102 conclusion — that names, descriptions and
annotations dominate the payload, capping achievable savings well below the projection —
holds on real fleets, or was an artefact of two frozen corpora.

**Why this priority**: This determines whether further serialization work is worth doing at
all, and it is the first question anyone will ask about the published numbers. It reuses
the existing tool-surface measurement machinery rather than adding its own.

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

### User Story 5 — Results reach the people deciding whether to adopt (Priority: P3)

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

### Definitions *(binding — these terms are used with exactly these meanings)*

- **Unit of work**: one task from a fixed task set (US2), or one recorded work-session
  grouping (US1). Never an individual tool call.
- **Attempt**: one tool call issued by the agent toward a given intent.
- **First-attempt success**: the first attempt for an intent returned a non-error result AND
  was not followed by a corrective retry for the same intent. A schema-valid call that the
  agent immediately re-issues differently is NOT a success.
- **Retry**: a subsequent attempt for the same intent after a failed or corrected one.
  Transport-level and infrastructure retries are counted and reported SEPARATELY, because
  they measure the network, not the mode.
- **Task completion**: the task suite's own pass verdict. Where a suite has no verdict, the
  unit of work is reported as having no completion signal and is excluded from
  completion-dependent figures rather than assumed complete.
- **Token accounting**: provider-reported usage where the run is model-backed; the existing
  deterministic tokenizer where it is not. Which one produced a figure MUST be stated; the
  two MUST NOT be summed into one number.
- **Fleet shape**: the tool count plus the distribution of definition sizes for the tool set
  a figure was measured on.

### Inherited constraints *(already provided by the existing harness — satisfy, do not rebuild)*

- **IC-001**: Every figure carries a `measured` / `computed` / `estimated` provenance badge.
- **IC-002**: Static tool-surface cost is measured from the live tool builders for every
  routing mode and MUST NOT be re-derived.
- **IC-003**: Generated reports are never committed; only code, fixtures, thresholds and
  methodology are versioned.
- **IC-004**: Any quoted percentage carries the fleet shape it was measured on.

### Functional Requirements

**Replay of real sessions (deterministic; see Replay Boundary)**

- **FR-001**: The harness MUST accept previously recorded real agent sessions as input via
  the existing export path, without requiring a new capture mechanism.
- **FR-002**: The harness MUST detect recorded entries whose stored content was truncated at
  capture time and MUST either exclude them from token totals or annotate them as
  under-counting; it MUST NOT include them silently.
- **FR-003**: The harness MUST report how many supplied sessions were unusable and why,
  distinguishing truncation, missing bodies, and unreplayable upstreams.
- **FR-004**: Replay output MUST be labelled as a counterfactual cost recomputation over
  recorded traffic. It MUST NOT be presented as observed agent behaviour, and the harness
  MUST NOT infer success, retry or completion figures from a recording.
- **FR-005**: Replay MUST report cost split into tool-surface cost and response cost, so the
  two can be reasoned about separately at real fleet shapes.

**Handling of recorded session data**

Recorded sessions are real user traffic. The export path that includes bodies deliberately
returns full unmasked values — it is the compliance/incident-response surface — so replay
inputs can contain secrets and personal data that the browsing surfaces mask.

- **FR-006**: Replay inputs MUST be treated as sensitive by default. Neither they nor any
  excerpt MUST appear in a report, dashboard or published artefact.
- **FR-007**: The harness MUST NOT transmit recorded session content to any third-party
  service, including model providers. Replay is local arithmetic; a design requiring such
  transmission is outside this feature.
- **FR-008**: Records flagged as containing sensitive data MUST be excluded, or reduced to
  the non-sensitive measurements needed for token accounting, before use; the count of
  affected records MUST be reported.
- **FR-009**: Replay inputs MUST live outside the repository, MUST NOT be committed, and the
  documented procedure MUST tell an operator how to delete them when finished.

**Measured success, retries and completion (live agent loop)**

- **FR-010**: The harness MUST measure first-attempt success rate per mode combination, as
  defined above.
- **FR-011**: The harness MUST measure retry counts per mode combination, reporting
  corrective retries separately from infrastructure retries.
- **FR-012**: The harness MUST record a task-completion verdict per unit of work, taken from
  the task suite rather than inferred.
- **FR-013**: Measured success and retry figures MUST replace the assumed per-arm defaults
  for any mode where a measurement exists; any figure still derived from an assumption MUST
  remain badged `estimated` and MUST NOT be presented without that distinction.
- **FR-014**: Token accounting MUST separate input, output and cached-read consumption.

**The mode matrix**

- **FR-015**: The matrix MUST cross the three configuration axes that determine what an agent
  sees: routing mode, discovery-surface serialization, and direct-surface serialization.
- **FR-016**: Batching, stored scripts and validate-before-dispatch MUST each be covered as a
  binary condition applied to the routing modes where they are available, and the report MUST
  enumerate which rows each applies to.
- **FR-017**: Combinations that are not meaningful MUST be reported as skipped with a reason,
  never as a zero or a missing row.
- **FR-018**: The report MUST express headline cost as tokens per completed task, with
  completion rate displayed alongside it at equal prominence.
- **FR-019**: A mode whose completion rate falls below the baseline's by more than a stated
  threshold MUST be marked as a regression regardless of its token cost, and MUST NOT be
  described as a saving.

**Honest comparison**

- **FR-020**: The baseline arm MUST be the same agent performing the same tasks with all
  tools loaded directly. A comparison against a different or weaker configuration MUST NOT
  be published.
- **FR-021**: Any model-dependent figure MUST be reported as an average over at least four
  runs together with a measure of consistency across runs; a single run MUST NOT be reported
  as a headline result.
- **FR-022**: The reproduction tolerance for model-dependent figures MUST be stated
  numerically in the published methodology.
- **FR-023**: The report MUST include a cost-versus-outcome view so a reader can see which
  modes are worth their savings rather than only which are cheapest.

**Payload decomposition**

- **FR-024**: The harness MUST attribute tool-definition payload share to names,
  descriptions, annotations and schemas separately, across at least two fleet shapes.
- **FR-025**: The result MUST explicitly confirm or correct the spec-102 conclusion about
  what dominates the payload, and MUST recompute the achievable ceiling per corpus rather
  than carrying a fixed figure forward.

**Public suites**

- **FR-026**: The harness MUST support at least one public task suite that exercises a full
  agent loop and emits a per-task pass verdict.
- **FR-027**: Any suite performing real writes against third-party services MUST be run in a
  way that cannot damage real user data.
- **FR-028**: Suite versions and configuration MUST be pinned so a later run is comparable to
  an earlier one.

**Reproducibility and publication**

- **FR-029**: Raw per-run records MUST be retained and referenced by the report so a headline
  figure can be traced to its inputs, without embedding session contents (FR-006).
- **FR-030**: Every input MUST be either included in the repository or obtainable by a
  documented, pinned procedure. Figures that depend on private recorded sessions MUST be
  marked as not independently reproducible, and MUST NOT be the sole support for a published
  claim.
- **FR-031**: The published write-up MUST state measured figures, the fleet shapes they hold
  for, known limitations, and the reproduction procedure, and MUST state plainly where
  savings do not materialise.
- **FR-032**: A partial or interrupted run MUST be identifiable as partial and MUST NOT be
  publishable as a complete comparison.

### Key Entities

- **Replayed session**: A previously recorded unit of real agent work — its tool calls,
  arguments and responses — with usability flags (truncated, missing bodies, unreplayable,
  sensitive).
- **Mode combination**: One point in the matrix — routing mode plus serialization settings
  and capability toggles determining what the agent sees and how it calls.
- **Run record**: The raw outcome of one unit of work under one mode: token consumption by
  kind, attempts, first-attempt outcome, retries by class, completion verdict, errors.
- **Measurement**: An aggregate over run records, carrying provenance and, where
  model-dependent, run count and spread.
- **Payload decomposition**: Attribution of tool-definition cost to names, descriptions,
  annotations and schemas for a given fleet shape.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For every VALID mode combination exercised by the live task set, a maintainer
  can obtain tokens per completed task, completion rate and first-attempt success rate, each
  badged `measured`.
- **SC-002**: For every valid mode combination, a maintainer can obtain the counterfactual
  cost of a real recorded workload, reproducible byte-for-byte across runs.
- **SC-003**: No session-level cost figure is derived from an assumed retry rate unless it is
  explicitly badged `estimated`.
- **SC-004**: A person outside the project, following only the published procedure,
  reproduces the deterministic figures exactly and the model-dependent figures within the
  stated numeric tolerance.
- **SC-005**: Every published percentage states the fleet shape it was measured on; a
  percentage without that context does not appear.
- **SC-006**: The spec-102 payload conclusion is explicitly confirmed or corrected, with the
  achievable ceiling recomputed for each fleet shape measured.
- **SC-007**: A mode that lowers token cost while completing fewer tasks is flagged as a
  regression by the completion threshold and is not reported as a saving — demonstrated by
  running a deliberately degraded mode.
- **SC-008**: Recorded sessions with truncated content are never counted silently: the report
  states how many were affected and how they were handled.
- **SC-009**: No recorded session content, and no excerpt of it, appears in any report,
  dashboard or published artefact, and no such content is transmitted to a third-party
  service.
- **SC-010**: At least one full agent-loop public task suite runs against MCPProxy and
  produces comparable results across at least two mode combinations.
- **SC-011**: No generated report is committed to the repository.
- **SC-012**: A reader of the published write-up can state which mode suits their fleet shape
  and can name at least one case where MCPProxy's savings do not materialise.

## Assumptions

- **Existing harness is the foundation.** Everything listed under Scope Boundary is treated
  as available and correct; this feature adds to it rather than replacing it. If a
  measurement here contradicts an existing one, that is a finding to investigate, not a
  reason to fork the harness.
- **Recorded sessions supply real workload SHAPE, not agent behaviour.** They drive the
  cost recomputation (US1) because real fleets beat synthetic corpora. They cannot drive the
  success/completion headline, because a recording contains no prompt, conversation, model
  state or completion oracle — see Replay Boundary. The live task set (US2) owns that.
- **Task completion comes from the task suite's own verdict**, never inferred from a call
  trace. Units of work with no verdict are reported as lacking a completion signal and
  excluded from completion-dependent figures rather than assumed complete.
- **Model-dependent measurement costs money and needs a decision.** Any full agent-loop run
  requires a pinned model and a spending decision before it can be built. US1 is designed to
  deliver value alone without it, so the feature is not blocked on that decision.
- **Private recorded sessions cannot be published**, so figures derived from them are not
  independently reproducible by an outsider. Public suites and frozen corpora therefore carry
  the reproducible claims; recorded-session figures corroborate them at real fleet shape.
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
- Inferring agent behaviour, success or completion from recorded traces. This is not merely
  out of scope: it is forbidden by FR-004, because it would fabricate the feature's headline.
- Any new activity capture mechanism. If the existing export proves insufficient for cost
  recomputation, that is a finding to report, not scope to absorb here.

## Dependencies

- The recorded-session export path must expose enough per-call detail — including response
  content when explicitly requested — for token accounting. It is already known NOT to carry
  prompts, conversation or completion verdicts, which is why US2 exists separately.
- A task suite with per-task pass verdicts is required for every completion-dependent
  figure; without one, US2 cannot be satisfied by any amount of recorded data.
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
