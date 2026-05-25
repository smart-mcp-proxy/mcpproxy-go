# Feature Specification: Output-Schema Validation for Proxied Tool Calls

**Feature Branch**: `056-output-schema-validation`
**Created**: 2026-05-25
**Status**: Draft
**Input**: Spec 054 Track A (carved into its own feature). When an upstream tool declares an `outputSchema`, mcpproxy verifies the tool's structured response conforms to that schema before it reaches the agent, so a buggy or compromised server cannot inject malformed/oversized/unexpected data into the agent's context.

> Scope note: this feature is **Track A only** of the Spec 054 umbrella ("MCP Security Gateway Hardening"). It deliberately excludes Track B (output sanitisation), Track C (per-tool ACLs), Track D (TOFU pinning hardening), and Track E (audit hash chain). Those ship separately.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Structured output is validated against its declared schema (Priority: P1)

As an operator running AI agents through mcpproxy, when an upstream tool declares an `outputSchema`, I want mcpproxy to verify that the tool's structured response actually conforms to that schema before it reaches my agent, so that a buggy or compromised server cannot inject malformed, oversized, or unexpected data into the agent's context.

**Why this priority**: This is the emptiest axis — mcpproxy does zero output validation today — and the highest-leverage new capability, completing the "validated data out" half of the security story. It is mostly additive with safe defaults, so it can ship as a standalone MVP.

**Independent Test**: Configure a stub upstream tool with an `outputSchema`. Return a conforming response (must pass through unchanged) and a non-conforming response (blocked in strict mode / forwarded-and-tagged in warn mode, with an activity record emitted). Verify `structuredContent` is preserved verbatim on the pass path (no strip-then-validate).

**Acceptance Scenarios**:

1. **Given** a tool with a declared output schema and `output_validation.mode=strict`, **When** it returns `structuredContent` violating the schema, **Then** the call is blocked with a clear error and a `policy_decision` activity record is written.
2. **Given** the same tool in `mode=warn`, **When** it returns a violating response, **Then** the response is forwarded but tagged as schema-violating and a `policy_decision` activity record is written.
3. **Given** a tool with NO declared output schema, **When** it returns any response, **Then** validation is a no-op and behaviour is unchanged (backward compatible).
4. **Given** a valid structured response, **When** it is validated, **Then** the original `structuredContent` reaches the agent unmodified (byte-for-byte).

---

### User Story 2 - Oversized / pathological output is bounded before validation (Priority: P2)

As an operator, I want extremely large or deeply-nested structured payloads from a tool to be bounded by configurable guards before validation runs, so a single response cannot exhaust memory, blow the agent's context window, or DoS the proxy through schema-validation cost.

**Why this priority**: A guard is cheap, protects the proxy itself, and is a prerequisite for safely running schema validation (which can be expensive on adversarial nested input). It builds on the same response chokepoint as Story 1.

**Independent Test**: Configure a byte-size guard and a nesting-depth guard. Return a structured payload exceeding the byte size, and one exceeding the depth. Verify each is treated as a validation failure (blocked in strict, tagged in warn) with an activity record, and that the guard check happens before full schema validation.

**Acceptance Scenarios**:

1. **Given** a configured max structured-output byte size, **When** a response's structured payload exceeds it, **Then** the call is treated as a guard violation (blocked in strict / tagged in warn) and a `policy_decision` activity record is written.
2. **Given** a configured max nesting depth, **When** a response's structured payload exceeds it, **Then** the same guard-violation handling applies.
3. **Given** a payload within both guards, **When** it is validated, **Then** guards add negligible overhead and schema validation proceeds.

---

### User Story 3 - Operator can observe and tune validation behaviour (Priority: P3)

As an operator, I want to configure validation mode and guard limits, and to see validation failures in the activity log alongside other policy decisions, so I can roll the feature out safely (warn first, then strict) and audit what was caught.

**Why this priority**: Configurability + observability turn a binary feature into one operators trust enough to enable. It reuses the existing activity-log and config plumbing.

**Independent Test**: Set `output_validation.mode` to `off`, `warn`, and `strict` and confirm behaviour matches each. Confirm `mcpproxy activity list` surfaces the validation `policy_decision` records and that `mcpproxy activity show <id>` reveals the tool, mode, and violation detail.

**Acceptance Scenarios**:

1. **Given** `output_validation.mode=off`, **When** any tool returns structured output, **Then** no validation runs and no validation activity records are written.
2. **Given** a validation failure was recorded, **When** the operator runs `mcpproxy activity list`, **Then** the failure appears as a `policy_decision` record filterable by status.
3. **Given** a validation failure record, **When** the operator inspects it, **Then** it includes the server, tool, mode, and a human-readable description of the violation.

---

### Edge Cases

- **Legacy text-only response (ContextForge #4042 trap)**: a tool declares an `outputSchema` but returns only legacy text `content` with no `structuredContent`. This MUST NOT hard-fail in warn mode — it is treated as "no structured output to validate" (no-op). In strict mode the configured `missing_structured_content` posture decides (default: allow, to preserve backward compatibility with tools that under-declare).
- **Oversized / deeply-nested payload**: bounded by the size/depth guards (Story 2) *before* schema validation, consistent with existing payload caps; the guard verdict short-circuits expensive validation.
- **Malformed / unparseable schema on the tool**: if the captured `outputSchema` is itself invalid JSON Schema, validation cannot run; treat as no-op + emit a one-time warning per tool (do not block traffic on the proxy's inability to compile a schema).
- **`IsError` upstream result**: when the upstream tool returns an error result, output validation is skipped (there is no successful structured payload to validate).
- **Streaming / multiple content blocks**: validation targets the single `structuredContent` field of the result; text/image/audio/embedded blocks are out of scope for Track A and pass through untouched.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-A1**: System MUST capture each upstream tool's declared output schema during tool discovery/indexing and persist it alongside the existing input schema, so the schema is available at call time without re-querying the upstream.
- **FR-A2**: System MUST, on every proxied tool-call return, validate the structured portion of the response against the tool's captured output schema when one exists.
- **FR-A3**: System MUST preserve the original structured response unchanged on the success path — validation operates on a copy/read-only view; it MUST never strip-then-validate or otherwise mutate the forwarded payload on success.
- **FR-A4**: System MUST support `strict` (block on violation), `warn` (forward + tag), and `off` (disabled) modes, configurable globally and defaulting to a backward-compatible setting (`warn`).
- **FR-A5**: System MUST emit a `policy_decision` activity record on every validation failure, including the server, tool, mode, and a description of the violation.
- **FR-A6**: System MUST enforce configurable byte-size and nesting-depth guards on structured output, evaluated before full schema validation; a guard breach is handled as a validation failure under the active mode.
- **FR-A7**: When a tool declares no output schema, validation MUST be a no-op (behaviour unchanged, no activity records).
- **FR-A8**: When a tool declares an output schema but the response carries no `structuredContent` (legacy text-only), the system MUST NOT hard-fail in `warn` mode (treat as nothing-to-validate); behaviour in `strict` mode is governed by a configurable `missing_structured_content` posture defaulting to allow.
- **FR-A9**: When the captured output schema is itself not a compilable JSON Schema, the system MUST treat validation as a no-op for that tool and surface a single diagnostic warning rather than blocking traffic.
- **FR-A10**: Validation MUST be skipped when the upstream result is an error result (`IsError`), since there is no successful structured payload to validate.
- **FR-A11**: In `warn` mode, a forwarded-but-violating response MUST be tagged in a way observable to the operator (activity record) without altering the payload delivered to the agent.
- **FR-A12**: Behaviour MUST be identical across personal and server editions (no build-tag-specific logic).

### Key Entities *(include if feature involves data)*

- **Captured Output Schema**: the JSON Schema a tool declares for its structured output, captured at discovery and persisted with the tool's existing metadata (alongside input schema). Absent for most tools today.
- **Output Validation Config**: operator-facing settings — `mode` (off/warn/strict), `max_bytes`, `max_depth`, `missing_structured_content` posture — with backward-compatible defaults.
- **Validation Failure Record**: a `policy_decision` activity entry capturing server, tool, mode, and violation description; reuses the existing activity-log entity.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For a tool with a declared output schema returning a non-conforming structured response, mcpproxy blocks the call in strict mode and forwards-with-an-audit-record in warn mode — 100% of the time in tests.
- **SC-002**: For a conforming response, the `structuredContent` delivered to the agent is byte-for-byte identical to what the upstream returned (zero mutation on the success path).
- **SC-003**: For a tool with no declared output schema, end-to-end behaviour (latency, payload, content blocks) is indistinguishable from the pre-feature baseline (no observable change).
- **SC-004**: A structured payload exceeding the configured byte-size or nesting-depth guard is caught before full schema validation, in 100% of guard-breach tests.
- **SC-005**: Every validation failure produces exactly one `policy_decision` activity record discoverable via `mcpproxy activity list`, with the tool, mode, and violation present in `activity show`.
- **SC-006**: With `mode=off` (or a tool without a schema), added per-call overhead is negligible (no measurable regression in the E2E tool-call latency baseline).

## Assumptions

- **Default mode is `warn`** (not `strict`), so enabling the feature never breaks an existing working agent on day one; operators opt into `strict`.
- **`missing_structured_content` defaults to allow**, because many real-world tools declare an output schema yet still return only text content; hard-failing those would break compatibility (the ContextForge #4042 lesson).
- **Validation targets `structuredContent` only.** Text/image/audio/embedded content blocks are out of scope for Track A; their handling (sanitisation/spotlighting) is Track B.
- **Schema compilation is cached per tool** so repeated calls don't recompile; an uncompilable schema degrades to no-op (FR-A9).
- **The existing `forwardContentResult` chokepoint** (`internal/server/content_forward.go`) is where validation hooks in, since it is the single response path for proxied calls; `emitActivityPolicyDecision` is reused for FR-A5/FR-A11 records.
- **Default guard limits** are chosen to be generous enough never to trip on legitimate tool output (e.g. multi-MB byte cap, depth in the tens) while still bounding pathological adversarial input; exact defaults finalised in planning.

## Out of Scope

- Output **sanitisation / redaction / spotlighting** of untrusted content (Track B).
- Per-tool / per-argument **access control** (Track C).
- TOFU **pinning** of output schemas / annotations and provenance binding (Track D) — note Track A only *captures* the schema for validation; pinning its changes is Track D.
- Tamper-evident **audit hash chain** and retention floors (Track E).
- Validation of **input** arguments (already covered by upstream tools / existing intent flow).

## Commit Message Conventions *(mandatory)*

- Use `Related #<issue>` (never `Fixes/Closes/Resolves`).
- Do NOT include `Co-Authored-By: Claude` or "Generated with Claude Code" (per repo policy / memory `feedback_no_claude_git_attribution`).
- Conventional Commit prefixes enforced by commitlint (Spec 053 WP-C5): `feat(056): ...`, `test(056): ...`, etc.
