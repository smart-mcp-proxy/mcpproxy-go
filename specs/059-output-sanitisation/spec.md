# Feature Specification: Output Sanitisation Enforcement (Spec 054 Track B)

**Feature Branch**: `059-output-sanitisation`
**Created**: 2026-05-29
**Status**: Draft
**Input**: Spec 054 (MCP security gateway) Track B — "Untrusted output is contained, not blindly injected"

## Context

mcpproxy already classifies every tool's output as `trusted`/`untrusted` (Spec 035, derived from `openWorldHint`) but that classification is **log-only** — it is computed and discarded before the response reaches the agent. mcpproxy also already detects secrets in responses (Spec 026) but only records the finding; it never alters the payload. Track A (Spec 056) added output-*schema* validation at the single response chokepoint `forwardContentResult`. Track B closes the remaining gap: making the trust classification and secret detection **actually contain** untrusted output before it enters the agent's context, while preserving mcpproxy's "never silently mutate by default" promise.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Untrusted output is demarcated by default (Priority: P1)

As an operator running AI agents through mcpproxy, when an untrusted (open-world) tool returns text, I want that text wrapped in explicit, source-identifying delimiters before it reaches my agent, so the agent (or a downstream evaluator) can tell tool *data* apart from *instructions* and indirect prompt-injection is mitigated — without any of my tool output being silently altered.

**Why this priority**: This is the default, always-on, non-mutating behaviour that delivers the core containment value with zero configuration and zero risk of data loss. It is the MVP.

**Independent Test**: Configure a stub upstream tool whose annotations make it untrusted (openWorldHint true / unset). Call it and verify the returned text content is wrapped in source-identifying delimiters, and that content which itself mimics the delimiter is escaped/neutralised so it cannot break out of the wrapper. Verify a trusted (closed-world) tool's output is untouched.

**Acceptance Scenarios**:

1. **Given** an untrusted tool and default config, **When** it returns text content, **Then** the forwarded text is wrapped in explicit source-identifying delimiters naming the originating server/tool.
2. **Given** untrusted output containing text that mimics the wrapper delimiter, **When** it is spotlighted, **Then** the spoofing content is escaped/neutralised so it cannot be mistaken for a real delimiter.
3. **Given** a trusted (closed-world) tool **or** default config, **When** it returns output, **Then** the output is forwarded byte-for-byte unchanged (backward compatible).
4. **Given** any tool returning non-text blocks (image/audio/embedded resource), **When** output is processed, **Then** those blocks are forwarded unmodified.

### User Story 2 - Opt-in secret redaction and control-sequence neutralisation (Priority: P2)

As a security-conscious operator, I want to optionally mask detected secrets in tool responses and strip dangerous control sequences from untrusted text, so credentials cannot leak into the agent's context and terminal/unicode trickery (ANSI, zero-width, bidi) cannot smuggle hidden instructions.

**Why this priority**: High-value hardening, but content-mutating, so it must be explicitly enabled. Builds directly on the P1 chokepoint and the existing detector.

**Independent Test**: Enable redaction; plant a secret (e.g. an AWS key) in a tool response and verify the matched span is replaced with a `[REDACTED:<category>]` placeholder and the detection is logged. Enable control-sequence stripping with per-class toggles; verify ANSI/C0-C1/zero-width/bidi sequences are neutralised in untrusted text while ordinary text survives.

**Acceptance Scenarios**:

1. **Given** `response_action=redact`, **When** a response contains a detectable secret, **Then** the matched span is replaced with `[REDACTED:<category>]` before forwarding and the detection is logged via a policy_decision activity record.
2. **Given** control-sequence stripping enabled with the bidi class on and zero-width off, **When** untrusted text contains both, **Then** only the bidi sequences are neutralised.
3. **Given** redaction/stripping disabled (default), **When** a response contains secrets or control sequences, **Then** content is forwarded unchanged (only spotlighting from Story 1 applies).

### User Story 3 - Block on critical detection (Priority: P3)

As an operator in a high-assurance environment, I want the option to block a tool response entirely when a critical secret is detected, replacing the payload with a remediation error and an audit record, so the most dangerous leaks never reach the agent at all.

**Why this priority**: Strongest enforcement, lowest-frequency use; opt-in and built on the same detector + activity plumbing.

**Acceptance Scenarios**:

1. **Given** `response_action=block`, **When** a response contains a critical-severity detection, **Then** the payload is replaced with a remediation error and a `blocked` policy_decision activity record is written.
2. **Given** `response_action=block` and a non-critical or no detection, **When** a response is returned, **Then** it is forwarded normally (subject to Story 1 spotlighting).

### Edge Cases

- A tool legitimately returns content that *looks* like a secret the user wants (e.g. a key to rotate): redaction/block are opt-in and critical-category-scoped; the default path only spotlights, never redacts.
- An untrusted tool returns only non-text blocks: spotlighting is a no-op; blocks are forwarded unmodified.
- Spotlighting interacts with Track A's truncation and caching: spotlighting wraps the (possibly truncated) text; the cached full payload is the original unspotlighted content.
- Server edition vs personal edition: behaviour is identical and config-gated; no server-only divergence.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-B1**: System MUST support wrapping text output from tools classified `untrusted` in explicit, source-identifying delimiters before forwarding (lossless spotlighting). *(Implementation note: shipped fully opt-in — `spotlight_untrusted` defaults to `false` per the product decision to keep Track B entirely inert until an operator opts in; see FR-B6.)*
- **FR-B2**: System MUST escape/neutralise any delimiter-spoofing content in untrusted output so it cannot be mistaken for a real wrapper boundary.
- **FR-B3**: System MUST support an opt-in mode that masks detected secret spans in responses with category-labelled `[REDACTED:<category>]` placeholders, reusing the existing Spec 026 detector, and MUST log each redaction.
- **FR-B4**: System MUST support opt-in neutralisation of control sequences (ANSI escape, C0/C1 control chars, zero-width, bidi override) in untrusted text blocks, with per-class toggles.
- **FR-B5**: System MUST leave non-text content blocks (image/audio/embedded resource) unmodified.
- **FR-B6**: Default configuration MUST annotate without mutating; all content-mutating behaviours (redact, strip, block) MUST be opt-in.
- **FR-B7**: System MUST support a `block` action that replaces the payload with a remediation error on critical detections, with a policy_decision audit record.
- **FR-X1**: Existing personal-edition behaviour MUST be unchanged unless an operator opts in; the default config MUST be safe and non-mutating beyond spotlighting of untrusted output.
- **FR-X3**: Implementation MUST follow TDD — a failing `_test.go` before implementation — per repo conventions.

### Key Entities

- **Output sanitisation config**: operator-facing settings controlling spotlighting (on/off), the response action (`spotlight` default / `redact` / `block`), and per-class control-sequence toggles. Mirrors the existing output-validation config shape (mode with safe defaults).
- **Sanitisation decision**: the per-response verdict — what was applied (spotlighted / redacted N spans / stripped / blocked) — surfaced as a policy_decision activity record for audit.
- **Content trust classification**: existing `trusted`/`untrusted` tag (Spec 035) that gates whether spotlighting and stripping apply.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Under default config, 100% of untrusted tool text output is demarcated such that an evaluator can distinguish tool data from instructions; no content is silently mutated beyond the lossless wrapper.
- **SC-002**: With redaction enabled, 100% of secrets the existing detector finds in a response are masked with a category-labelled placeholder before the response reaches the agent, and each is logged.
- **SC-003**: With block enabled, 100% of responses carrying a critical detection are replaced with a remediation error and an audit record; non-critical responses pass through.
- **SC-004**: With control-sequence stripping enabled, ANSI/C0-C1/zero-width/bidi sequences are neutralised in untrusted text per the enabled per-class toggles, with ordinary text preserved.
- **SC-005**: Non-text blocks are forwarded byte-identical in 100% of cases.
- **SC-006**: With sanitisation off and trusted tools, existing behaviour is byte-identical to pre-feature output (verified against the existing forward/truncation test suite).

## Commit Message Conventions *(mandatory)*

### Issue References

Relates to the Spec 054 umbrella (#521) security-gateway work; reference the umbrella spec in commit bodies.

### Co-Authorship

Per repo policy: no Claude/Co-Authored-By attribution in mcpproxy-go commits.

### Example Commit Message

```
feat(054-B): spotlight untrusted tool output at the response chokepoint

Wrap untrusted (open-world) tool text in source-identifying delimiters
by default; add opt-in redact/strip/block actions reusing the Spec 026
detector. Emits policy_decision activity records. Default config is
non-mutating beyond the lossless wrapper.

Relates to Spec 054 Track B (#521).
```

## Testing

- Unit: pure sanitiser functions (spotlight + escape, control-sequence stripping per class, redaction span replacement) and a pure decision core mirroring Track A's `evaluateOutputValidation`.
- Integration: the `forwardContentResult` chokepoint applies spotlighting/redaction/block based on trust + config; non-text blocks preserved; policy_decision emitted.
- Verification (mandatory, post-implementation): curl-driven REST/MCP roundtrip against a stub untrusted upstream; the `scripts/test-api-e2e.sh` suite; and the Web UI activity view inspected via the chrome extension to confirm policy_decision records render.
