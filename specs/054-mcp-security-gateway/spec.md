# Feature Specification: MCP Security Gateway Hardening (umbrella)

**Feature Branch**: `054-mcp-security-gateway`
**Created**: 2026-05-23
**Status**: Draft
**Input**: Turn mcpproxy into the *reference open-source MCP security gateway* over ~6 months via 5 independently-shippable tracks, each grounded in a gap analysis against existing features.

> This is an **umbrella spec**. Each Track (A–E) below is an independently shippable work package with its own value, tests, and (when non-trivial) its own `specs/<NNN>-<slug>/` plan. They are sequenced A→E by leverage × effort but do not hard-depend on each other except where noted.

## Context: where mcpproxy already is *(not greenfield)*

mcpproxy is already ~70% of a reference MCP security gateway. This spec **closes specific gaps** on existing systems and adds **one genuinely new axis** (output validation). Existing foundations this spec builds on:

- **Spec 032 — tool-level TOFU quarantine**: SHA-256 pins of `name|description|inputSchema`, `pending`/`changed` states, call-time *blocking* (stronger than scanners that only alert), no-silent-unquarantine invariant. (`internal/runtime/tool_quarantine.go`, `internal/storage/models.go`, `internal/server/mcp.go`)
- **Spec 026 — sensitive-data detection**: scans args + responses for 20+ secret classes; **detect/log only**, never mutates. (`internal/security/detector.go`)
- **Spec 035 — content-trust tagging**: computes `trusted`/`untrusted` per tool (openWorldHint); **log-only**, discarded before output reaches the agent. (`internal/contracts/intent.go`, `internal/server/mcp.go`)
- **Spec 028 — agent tokens**: scoped creds with per-server × per-operation (read/write/destructive) permissions; **no per-tool/per-arg granularity**. (`internal/auth/agent_token.go`, `internal/auth/context.go`)
- **Specs 016/021/024 — activity log**: tool calls, intent, detections, request-id correlation, JSONL/CSV export; **no tamper-evidence**, default retention 90d. (`internal/storage/activity.go`, `internal/runtime/activity_service.go`)
- **`forwardContentResult`** (`internal/server/content_forward.go`): the single response chokepoint — today only truncates. The natural hook for output validation + sanitisation.

## User Scenarios & Testing *(mandatory)*

### User Story A — Validated tool output (Track A · Priority: P1)

As an operator running AI agents through mcpproxy, when an upstream tool declares an `outputSchema`, I want mcpproxy to verify that the tool's structured response actually conforms to that schema before it reaches my agent, so a buggy or compromised server cannot inject malformed/oversized/unexpected data into the agent's context.

**Why this priority**: This is the emptiest axis (mcpproxy does zero output validation today) and the highest-leverage new capability — it completes the "validated data out" half of the security story. Mostly additive, safe defaults.

**Independent Test**: Configure a stub upstream tool with an `outputSchema`; return a conforming response (passes through) and a non-conforming response (blocked in strict mode / tagged in warn mode, with an activity record emitted). Verify `structuredContent` is preserved verbatim on the pass path (no strip-then-validate).

**Acceptance Scenarios**:

1. **Given** a tool with a declared output schema and `output_validation.mode=strict`, **When** it returns `structuredContent` violating the schema, **Then** the call is blocked with a clear error and a `policy_decision` activity record is written.
2. **Given** the same tool in `mode=warn`, **When** it returns a violating response, **Then** the response is forwarded but tagged as schema-violating and an activity record is written.
3. **Given** a tool with NO declared output schema, **When** it returns any response, **Then** validation is a no-op and behaviour is unchanged (backward compatible).
4. **Given** a valid structured response, **When** it is validated, **Then** the original `structuredContent` reaches the agent unmodified.

---

### User Story B — Untrusted output is contained, not blindly injected (Track B · Priority: P2)

As an operator, I want output from untrusted tools to be clearly demarcated (and optionally have secrets redacted / control sequences stripped) before it enters the agent's context, so that indirect prompt-injection, data smuggling, and secret leakage via tool responses are mitigated.

**Why this priority**: mcpproxy already *computes* the signals (Spec 035 trust level, Spec 026 detections) but discards them into the log. Promoting them into the forward path is mostly wiring of existing data. Default posture is **annotate, don't mutate** (lossless), with mutation opt-in.

**Independent Test**: Mark a tool as untrusted; verify its text output is wrapped in explicit delimiters by default. Enable redaction; verify a planted secret in a response is masked. Enable control-char stripping; verify ANSI/zero-width/bidi sequences are neutralised. Verify non-text blocks (image/audio/embedded) are untouched.

**Acceptance Scenarios**:

1. **Given** a tool classified `untrusted` and default config, **When** it returns text, **Then** each text block is wrapped in explicit untrusted-content delimiters identifying the source server/tool.
2. **Given** `response_action=redact`, **When** a response contains a detectable secret, **Then** the matched span is replaced with a `[REDACTED:<category>]` placeholder before forwarding, and the detection is logged.
3. **Given** control-sequence stripping enabled, **When** a response contains ANSI/C0-C1/zero-width/bidi codepoints, **Then** those are neutralised in untrusted text blocks.
4. **Given** default config (no opt-in mutation), **When** any tool returns output, **Then** content is annotated/spotlighted but never silently altered.

---

### User Story C — Least-privilege per-tool / per-argument access (Track C · Priority: P3)

As an operator issuing agent tokens, I want to grant a token access to only specific tools (and optionally constrain their arguments), not just whole servers, so each agent runs with true least privilege.

**Why this priority**: Closes the access-control granularity gap (today: per-server × per-operation only). Backward compatible (empty allow-list = current behaviour).

**Independent Test**: Create a token scoped to `github:create_issue` only; verify it can call that tool but is denied `github:delete_repo` and tools on other servers, with a `policy_decision` audit record on denial. Add an argument constraint; verify a call with a disallowed argument value is denied.

**Acceptance Scenarios**:

1. **Given** a token with `AllowedTools=[github:create_issue]`, **When** it calls `github:create_issue`, **Then** the call is allowed; **When** it calls any other tool, **Then** it is denied with an audit record.
2. **Given** a token with an empty tool allow-list, **When** it calls any tool on its allowed servers, **Then** behaviour is unchanged (backward compatible).
3. **Given** a per-argument constraint (e.g. `repo` must match `myorg/*`), **When** a call supplies a non-matching value, **Then** the call is denied before forwarding.

---

### User Story D — The full tool signature is pinned, with provenance and a readable diff (Track D · Priority: P4)

As a security-conscious operator, I want tool-description pinning (TOFU) to also cover output schemas and safety annotations, to be bound to the server's identity, and to show me a readable, security-classified diff when something changes, so rug-pulls cannot slip through gaps in what is pinned.

**Why this priority**: Hardens an already-strong system (Spec 032). Gaps are specific: output schema + annotations not pinned, no provenance binding, raw diff only.

**Independent Test**: Flip a tool's `destructiveHint` and verify the change is now detected (currently it is not). Re-point a server name at a new endpoint and verify the pin is invalidated. View a `/diff` and verify it shows a structured diff plus a risk verdict from the TPA scanner.

**Acceptance Scenarios**:

1. **Given** an approved tool, **When** its `outputSchema` or a security-relevant annotation (`destructiveHint`/`openWorldHint`) changes, **Then** it transitions to `changed` and is blocked pending re-approval.
2. **Given** an approved server, **When** the server's identity (URL/transport for HTTP, command for stdio) changes, **Then** its tool pins are invalidated and treated as a new origin.
3. **Given** a `changed` tool, **When** I view its diff, **Then** I see a token/line-level diff and a risk classification (cosmetic vs. semantic; suspicious patterns from the TPA scanner).
4. **Given** cosmetic-only changes across many tools, **When** I bulk-approve cosmetic changes, **Then** semantically-changed tools still require explicit per-tool approval.

---

### User Story E — Tamper-evident, retention-correct audit trail (Track E · Priority: P5)

As an operator preparing for EU AI Act obligations (high-risk logging applicable 2 Aug 2026), I want an automatic, tamper-evident, retention-correct record of every agent tool call that I can export for an auditor, so I can demonstrate what my agents did.

**Why this priority**: Builds on a strong activity log. The headline gap is tamper-evidence; retention default is below the legal floor. Framed as **alignment/support, not compliance certification**.

**Independent Test**: Enable compliance mode; verify each activity record carries a hash chained to its predecessor and a periodic signed checkpoint is produced. Tamper with a record and verify `mcpproxy activity verify` reports the break at the right entry. Verify retention floor prevents eviction of records younger than the configured minimum even under the count cap. Produce a sealed export and verify it bundles records + checkpoint signature + an Article-12 mapping.

**Acceptance Scenarios**:

1. **Given** compliance mode enabled, **When** activity records are written, **Then** each carries `EntryHash = hash(canonical(record) ‖ PrevHash)` and periodic signed checkpoints are produced.
2. **Given** a tampered or deleted record, **When** `mcpproxy activity verify` runs, **Then** it reports the first chain break and the affected entry.
3. **Given** `min_retention ≥ 180 days` and a full record count cap, **When** pruning runs, **Then** no record younger than `min_retention` is evicted (time floor wins over count cap).
4. **Given** a sealed export request, **When** it completes, **Then** the bundle contains the JSONL records, the checkpoint signature, and an Article-12-mapping document, with an explicit "alignment, not certification" disclaimer.

---

### Edge Cases

- **A**: tool declares an output schema but returns only legacy text `content` and no `structuredContent` → must NOT hard-fail in warn mode (the ContextForge #4042 trap); treat as "no structured output to validate".
- **A**: extremely large / deeply-nested structured payloads → bounded by size/depth guards before validation, consistent with existing payload caps.
- **B**: a tool legitimately returns content that looks like a secret the user explicitly wants (e.g. a key to rotate) → redaction is opt-in and critical-category-scoped; default is detect+spotlight, not redact.
- **B**: an untrusted tool emits text containing the spotlight delimiter itself → delimiters must be escaped/neutralised to prevent boundary spoofing.
- **C**: a token's allowed tool is renamed/rug-pulled upstream → must interact with Track D pinning so a renamed tool is re-quarantined, not silently matched.
- **D**: server URL legitimately changes (e.g. port) → provenance mismatch forces re-approval; provide an admin "migrate pins to new origin" action and document the behaviour.
- **D**: reconnect instability re-ordering annotations → security hash must normalise annotation defaults (the original reason annotations were excluded) to avoid false-positive churn.
- **E**: async activity writes vs. a strictly-ordered hash chain → chain must be written under a serialized path; post-hoc metadata updates (Spec 026 detections) must not mutate an already-hashed canonical record (hash a stable subset or move detections to a side-record).
- **E**: time-floor retention + high volume → unbounded growth risk; needs disk-pressure surfacing.

## Requirements *(mandatory)*

### Functional Requirements — Track A (output-schema validation)

- **FR-A1**: System MUST capture each upstream tool's declared output schema during tool discovery/indexing and persist it alongside the existing input schema.
- **FR-A2**: System MUST, on every proxied tool-call return, validate the structured portion of the response against the tool's captured output schema when one exists.
- **FR-A3**: System MUST preserve the original structured response unchanged on the success path (validate a copy; never strip-then-validate).
- **FR-A4**: System MUST support `strict` (block on violation) and `warn` (forward + tag) modes, configurable and defaulting to a backward-compatible setting.
- **FR-A5**: System MUST emit an audit (policy-decision) record on every validation failure, including the tool, mode, and the violation.
- **FR-A6**: System MUST enforce configurable size/structure (byte size, nesting depth) guards on structured output.
- **FR-A7**: When a tool declares no output schema, validation MUST be a no-op (unchanged behaviour).

### Functional Requirements — Track B (output sanitisation enforcement)

- **FR-B1**: System MUST, by default, wrap text output from tools classified `untrusted` in explicit, source-identifying delimiters before forwarding (lossless spotlighting).
- **FR-B2**: System MUST escape/neutralise any delimiter-spoofing content in untrusted output.
- **FR-B3**: System MUST support an opt-in mode that masks detected secret spans in responses with category-labelled placeholders, reusing the existing detector.
- **FR-B4**: System MUST support opt-in neutralisation of control sequences (ANSI/C0-C1/zero-width/bidi) in untrusted text blocks, with per-class toggles.
- **FR-B5**: System MUST leave non-text content blocks (image/audio/embedded) unmodified.
- **FR-B6**: Default configuration MUST annotate without mutating; all content-mutating behaviours MUST be opt-in.
- **FR-B7**: System MUST support a `block` action that replaces the payload with a remediation error on critical detections, with an audit record.

### Functional Requirements — Track C (per-tool / per-arg ACLs)

- **FR-C1**: Agent tokens MUST support per-tool allow/deny lists using `server:tool` patterns (incl. `server:*` and `*`).
- **FR-C2**: The proxy MUST enforce per-tool access at call time, after the existing per-server check, denying with an audit record when not permitted.
- **FR-C3**: An empty tool allow-list MUST mean "all tools on allowed servers" (backward compatible).
- **FR-C4**: System MUST support optional per-argument constraints (equals/in/regex/prefix) evaluated before forwarding.
- **FR-C5**: The token-creation interface MUST allow specifying per-tool scope (e.g. `--tools server:tool,...`).
- **FR-C6** *(server edition, optional)*: System MAY support named roles mapping to capability sets for OAuth users; personal edition MUST be unaffected.

### Functional Requirements — Track D (TOFU pinning hardening)

- **FR-D1**: The pinned tool signature MUST include the output schema and security-relevant annotations (`destructiveHint`, `openWorldHint`), normalised to avoid reconnect false-positives.
- **FR-D2**: Tool pins MUST be bound to a stable server-identity; a change in server identity (URL/transport, or command for stdio) MUST invalidate the affected pins.
- **FR-D3**: The diff view MUST present a structured (token/line-level) diff and a risk classification, including re-running the new description through the existing TPA scanner.
- **FR-D4**: System MUST distinguish cosmetic from semantic changes and support approving cosmetic-only changes in bulk while requiring explicit approval for semantic changes.
- **FR-D5**: All Track D changes MUST preserve the existing no-silent-unquarantine invariant.

### Functional Requirements — Track E (AI Act-aligned audit logging)

- **FR-E1**: System MUST support a tamper-evident hash chain over activity records (`EntryHash = hash(canonical(record) ‖ PrevHash)`).
- **FR-E2**: System MUST periodically produce a cryptographically signed checkpoint of the chain head, with the signing key held outside the log store.
- **FR-E3**: System MUST provide a verification command that walks the chain and reports the first integrity break and affected entry.
- **FR-E4**: System MUST support a compliance mode raising default retention to ≥180 days and making the time floor win over the record-count cap.
- **FR-E5**: System MUST support a sealed export bundling the records, the checkpoint signature, and an Article-12 field-mapping document.
- **FR-E6**: Canonical hashed records MUST NOT be mutated by post-hoc metadata updates (detections etc.); such data MUST be stored without breaking the chain.
- **FR-E7**: All compliance framing MUST be "alignment/support", never "certified compliant".

### Cross-cutting requirements

- **FR-X1**: Every track MUST ship with backward-compatible defaults; existing personal-edition behaviour MUST be unchanged unless an operator opts in.
- **FR-X2**: Server-only functionality MUST be behind the existing server build tag and MUST NOT affect the personal edition.
- **FR-X3**: Each track MUST follow TDD (a failing `_test.go` before implementation) per repo conventions.

### Key Entities

- **Tool signature pin**: the persisted, hashed representation of a tool's trusted shape — now `name | description | inputSchema | outputSchema | security-annotations`, bound to a server-identity hash.
- **Output validation result**: pass/fail verdict + violations for a tool response against its declared output schema.
- **Capability grant**: per-token allow/deny of tools (and optional argument constraints), layered on existing server + operation scopes.
- **Audit chain entry**: an activity record extended with `PrevHash` + `EntryHash`; plus periodic signed checkpoints.
- **Sealed audit export**: records + checkpoint signature + Article-12 mapping document.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of upstream tools that declare an output schema have it captured and enforced (configurable strict/warn); responses with no schema are unaffected.
- **SC-002**: A non-conforming tool response is caught (blocked or tagged) in 100% of test cases, and a valid response is delivered byte-identical to the agent (zero corruption).
- **SC-003**: Untrusted tool output is, by default, demarcated such that an evaluator (human or model) can distinguish tool data from instructions in 100% of cases; no content is silently mutated under default config.
- **SC-004**: An agent token can be scoped to an explicit tool set such that any out-of-scope tool call is denied and audited; existing whole-server tokens continue to work unchanged.
- **SC-005**: A flipped safety annotation or changed output schema is detected as a change in 100% of cases (currently 0%); a server re-point invalidates pins.
- **SC-006**: Any single-record tampering or deletion in the audit log is detected by the verification command, identifying the affected entry; records younger than the configured retention floor are never evicted.
- **SC-007**: The personal edition's default behaviour is unchanged across all five tracks (verified by the existing personal-edition test suite passing without modification).
- **SC-008** *(positioning)*: mcpproxy can credibly document, with working code, end-to-end "validated identity in / validated data out" plus tamper-evident, Article-12-aligned logging — a combination no current open-source MCP gateway ships.

## Assumptions

- The upstream MCP client library exposes tool output schemas on `tools/list` (verify the pinned version surfaces `RawOutputSchema`); where absent, Track A degrades to no-op for that tool.
- "Compliance" language throughout is alignment/support only; legal certification is explicitly out of scope.
- Tracks are sequenced A→E by leverage × effort but are independently shippable; D interacts with C (renamed tools) and reuses A's output-schema capture.
- Signing keys for Track E are operator-managed; key distribution/HSM integration is out of scope for the initial track.

## Non-Goals

- No legal compliance certification (EU AI Act or otherwise) — alignment/support only.
- No change to the MCP protocol itself; mcpproxy proposes/implements conventions (e.g. signed manifests) additively.
- No mandatory content mutation — destructive sanitisation is always opt-in.
- No GoReleaser/tooling migrations; build on existing pipelines.
- This umbrella spec does not include the docs Diátaxis restructure (separate spec).

## Track Sequencing & Decomposition

| Track | Title | Builds on | Effort | Edition |
|-------|-------|-----------|--------|---------|
| A (P1) | Output-schema validation | new; `content_forward.go` hook | S–M | both |
| B (P2) | Output sanitisation enforcement | Specs 026, 035; same hook | S–M | both |
| C (P3) | Per-tool / per-arg capability ACLs | Spec 028 | M–L | both (RBAC = server) |
| D (P4) | TOFU pinning hardening | Spec 032; reuses A | M | both |
| E (P5) | AI Act-aligned audit logging | Specs 016/021/024 | M | both |

When a track begins implementation it SHOULD get its own `specs/<NNN>-<slug>/plan.md` via `/speckit.plan`.

## Commit Message Conventions *(mandatory)*

- Use `Related #[issue]` (never `Fixes/Closes/Resolves`).
- Do **not** include `Co-Authored-By: Claude` or "Generated with Claude Code" (repo policy).
- Conventional Commits; scope by track, e.g. `feat(054-A): validate structured tool output against outputSchema`.
