# Extension Payload Specification: `app.mcpproxy/required-tools`

**Status**: Draft (design document — not yet a SEP)
**Created**: 2026-08-19
**Related**: spec 098 (REST/CLI preflight, shipped v0.58.0) · spec 099 (`describe_tool` check mode, shipped v0.58.0) · issue #969 item 3 · SEP-1862 (tools/resolve, draft) · SEP-2133 (Extensions framework, Final)
**Companion schema**: `required-tools-extension.schema.json` (JSON Schema draft 2020-12: settings object, request rider, response facet, per-tool verdict)

## Purpose

mcpproxy ships a deterministic, side-effect-free required-tools preflight: given a list of tool IDs, answer per-ID `ready | unavailable` with a machine-readable reason from a closed enum, evaluated purely from local proxy state. Today that verdict is reachable over REST (`POST /api/v1/preflight`), the CLI (`mcpproxy tools preflight`, exit codes 0/10/11/12), and in-band via `describe_tool` check mode — all three consuming one shared evaluator (`internal/preflight`).

This document specifies the same verdict payload as an MCP **extension**, so that a generic MCP client — not just one that knows mcpproxy's tool surface — can preflight its required toolset. It defines:

- **Shape A (standalone extension)**: the payload carried in `_meta` on an existing MCP method — negotiated per SEP-2133 on the 2026-07-28 revision, or via the legacy `experimental` capability on 2025-11-25 (an mcpproxy-defined compatibility convention built from protocol-legal fields, not SEP-2133 negotiation), which is the carriage that works today.
- **Shape B (SEP-1862 facet)**: the identical per-tool verdict as an availability facet of a `tools/resolve` response. This is the strategic goal — availability is a preflight concern, and SEP-1862 is the protocol's consolidated preflight exchange.

**Why preflight at all — token economy.** An unavailable required tool discovered at call time costs an agent turn or more: the model retrieves the tool, plans around it, calls it, receives an error, and re-plans — and in the worst case burns a whole discovery loop first. Discovered at preflight, the same fact costs one local check — and when the client enforces it before the session starts, no model tokens at all. This is the reporter-confirmed primary motivation of the shipped feature (#969: fail deterministically *before* the agent session spends tokens on discovery) and the economic argument for carrying availability in the protocol's consolidated preflight exchange: `tools/resolve` exists to spend one cheap exchange to avoid expensive wrong turns, and availability is a cheap fact with an expensive failure mode.

This is a payload spec, not a full SEP. Implementation phasing, conformance scenarios, and SDK work are out of scope here.

## Identifier and Versioning

- **Extension identifier**: `app.mcpproxy/required-tools`.
  - Grammar per SEP-2133: `{vendor-prefix}/{extension-name}`, following `_meta` key rules with a mandatory prefix. Prefix `app.mcpproxy` is the reverse-DNS of `mcpproxy.app`; second label `mcpproxy` is not a reserved label (`modelcontextprotocol`/`mcp` are); the name `required-tools` begins and ends alphanumeric with one interior hyphen; the identifier contains exactly one slash.
- **Versioning**: SEP-2133 recommends (SHOULD) that extensions be versioned but deliberately does not prescribe a mechanism; **this extension's convention** is that the version lives in the settings object, never in the identifier: `{ "version": 1 }`. The one SEP-2133 mandate is that breaking changes mint a new identifier via a name suffix (`app.mcpproxy/required-tools-v2`; a `/v1` path segment would be grammatically invalid — two slashes).
- **Payload evolution rule** (inherited from spec 098 FR-003): the reason enum is **closed for a given settings version** — v1 emits exactly the codes below and nothing else. New codes ship with a settings-version bump; that is a compatible change, because consumers MUST treat unknown reason codes as non-retryable and schema validators pin the version they know. Codes are never repurposed; a semantic change to an existing code is a breaking change and mints a new identifier per SEP-2133.

## Verdict Payload (normative, both shapes)

### Per-tool result

```json
{
  "id": "gh-ops:sync_issues",
  "status": "unavailable",
  "reason": "tool_changed",
  "retryable": false,
  "action": "approve",
  "detail": "tool definition changed after approval (rug-pull guard)",
  "remediation": "Review and re-approve: mcpproxy tools approve gh-ops:sync_issues",
  "did_you_mean": ["gh-ops:sync_issue"]
}
```

- `id` (required in Shape A; omitted in Shape B, where the resolved tool names itself): the requested tool ID, echoed verbatim.
- `status` (required): `ready | unavailable`. `ready` is the success status, not a reason code; a ready result carries `id` and `status` only in Shape A, and `status` only in Shape B.
- `reason` (unavailable only): exactly one code from the closed enum below.
- `retryable` (unavailable only): boolean per the table.
- `action` (unavailable only, optional): existing health-action vocabulary — `login` / `restart` / `enable` / `approve` / `view_logs` / `set_secret` / `configure`. "No action" is represented by **omitting** the field.
- `detail`, `remediation` (unavailable only, optional): human-readable strings.
- `did_you_mean` (optional, `not_found` only, ≤3): nearest-name suggestions computed over the caller-visible scope only; never suggests quarantined-tool names.
- The payload never carries `inputSchema`, descriptions, `call_with`, or tool-definition fields — verdict-only, matching spec 099 FR-004.
- The payload never carries schema hashes at any tier (deliberate divergence from the REST operator-tier `hash` disclosure; spec 099 FR-009).

### Reason enum

Identical to the shipped closed enum (spec 098 FR-003; 15 codes, `server_saturated` reserved and not implemented):

| Reason | Class | `retryable` | Default `action` | Set verdict | CLI exit |
|---|---|---|---|---|---|
| `server_initializing` | retryable | true | — (omitted) | degraded_retryable | 10 |
| `server_unhealthy` | retryable | true | best-effort from diagnostics (`restart`/`login`/`view_logs`; default `view_logs`) | degraded_retryable | 10 |
| `server_disabled` | fix-state-first | false | enable | blocked | 11 |
| `server_quarantined` | fix-state-first | false | approve | blocked | 11 |
| `tool_pending_approval` | fix-state-first | false | approve | blocked | 11 |
| `tool_changed` | fix-state-first | false | approve | blocked | 11 |
| `tool_blocked_by_user` | fix-state-first | false | enable | blocked | 11 |
| `oauth_required` | fix-state-first | false | login | blocked | 11 |
| `hash_mismatch` | fix-state-first | false | configure | blocked | 11 |
| `server_not_in_scope` (operator tier only) | permanent-config | false | configure | blocked | 11 |
| `tool_denied_by_config` | permanent-config | false | configure | blocked | 11 |
| `missing_annotation` | permanent-config | false | configure | blocked | 11 |
| `policy_filtered` | permanent-config | false | — (omitted) | blocked | 11 |
| `not_found` | permanent-config | false | configure | unknown_ids | 12 |
| `server_not_configured` | permanent-config | false | configure | unknown_ids | 12 |

**Wire subset for this extension**: the extension is an in-band surface and always evaluates at the agent-token disclosure tier (spec 099 FR-009). Consequently `server_not_in_scope` and `server_not_configured` never appear on the extension wire — both collapse to a byte-indistinguishable `not_found`. `hash_mismatch` cannot fire in v1 (no pin field; see Reserved). The full table remains normative as the enum registry so REST/CLI and the extension share one taxonomy.

### Set-level verdict

`verdict ∈ { ready, degraded_retryable, blocked, unknown_ids }` — the worst class present, ordered `unknown_ids` > `blocked` > `degraded_retryable` > `ready`. Carried only where the exchange is batch-shaped (Shape A). Under Shape B (single-tool resolve) aggregation is the client's job.

## Shape A — Standalone Extension (fallback carriage)

The check rides an existing method's `_meta`; no new protocol method (method-proliferation is an explicit SEP-1862 anti-goal, and a rider degrades to core behavior for free). The carrier is **`tools/list`**: it is already the availability surface, already read-only, and a server that does not support the extension simply ignores the unknown `_meta` key — the client detects support by the presence of the response facet.

### Request (2026-07-28 revision)

```json
{
  "jsonrpc": "2.0",
  "id": 7,
  "method": "tools/list",
  "params": {
    "_meta": {
      "io.modelcontextprotocol/protocolVersion": "2026-07-28",
      "io.modelcontextprotocol/clientCapabilities": {
        "extensions": { "app.mcpproxy/required-tools": { "version": 1 } }
      },
      "app.mcpproxy/required-tools": {
        "tools": [
          { "id": "gh-ops:sync_issues" },
          { "id": "slack:post_message" }
        ],
        "policy": { "read_only_only": true }
      }
    }
  }
}
```

- `tools` (required): 1–50 entries `{ id }` (raw-array cap before dedup, matching spec 099 FR-005; duplicates deduplicated, one result per unique ID, ordered by first occurrence).
- `policy` (optional): the three annotation booleans — `read_only_only`, `exclude_destructive`, `exclude_open_world` — with spec-094/098 semantics (fixed evaluation order; `missing_annotation` when the hint is absent, `policy_filtered` when explicitly unsafe).
- No `profile` parameter (scope is the session's — agent-token `allowed_servers` ∩ token profile pin ∩ session active profile, spec 099 FR-009a), no wait budget, no pin field (Reserved below).

### Response

```json
{
  "jsonrpc": "2.0",
  "id": 7,
  "result": {
    "resultType": "complete",
    "tools": [
      { "name": "gh-ops:sync_issues", "inputSchema": { "type": "object" } },
      { "name": "slack:post_message", "inputSchema": { "type": "object" } }
    ],
    "_meta": {
      "app.mcpproxy/required-tools": {
        "verdict": "blocked",
        "checked_at": "2026-08-19T09:00:00Z",
        "request_id": "req_01J…",
        "tools": [
          { "id": "gh-ops:sync_issues", "status": "ready" },
          { "id": "slack:post_message", "status": "unavailable",
            "reason": "tool_pending_approval", "retryable": false,
            "action": "approve",
            "detail": "tool discovered after baseline; awaiting review",
            "remediation": "Approve in Web UI or: mcpproxy tools approve slack:post_message" }
        ]
      }
    }
  }
}
```

- The carrier result's `tools` array is exactly what `tools/list` would have returned without the rider — elided above to two minimal entries (the `resultType` field is required by the 2026-07-28 revision on every result, rider or not; under the 2025-11-25 legacy carriage it is absent).
- On served tool names: mcpproxy's aggregated surface uses the `server:tool` form. The base spec's recommended tool-name character set discourages `:`; that tension is an aggregation artifact that predates this extension and applies to mcpproxy's whole tool surface, not just the rider — it would be raised explicitly during any standardization discussion.
- `request_id` is the same correlation ID written to the local activity record (spec 099 FR-004), so an operator can trace the check via `mcpproxy activity list --request-id`.
- The availability verdict is always in the payload, never a JSON-RPC error. Errors are reserved for malformed requests: if the extension was negotiated and the rider is malformed (missing `tools`, empty list, >50 entries, `policy` of wrong type), the carrier call fails with `-32602` naming the fault — no partial evaluation (mirrors spec 099 FR-005/FR-012a). If the extension was not negotiated, unknown `_meta` keys are ignored per core spec and the carrier proceeds normally.

### Reserved fields (v1)

`pin_hash` / `expect_hashes` (per-entry hash pinning), `wait_ms`, and `profile` are **reserved**: senders MUST NOT include them; a v1 server treats their presence as a malformed-rider error. This matches the spec 099 decision (2026-08-16) that trimmed `expect_hashes` from the in-band surface. Hash pinning remains available on the REST surface at the operator tier.

### Legacy carriage (2025-11-25 revision)

The 2025-11-25 revision has no `extensions` capability field, but the full `_meta` prefix grammar exists and both sides carry `experimental?: { [key: string]: object }`. This is an **mcpproxy-defined compatibility convention** built from protocol-legal fields — not SEP-2133 negotiation, which does not exist on this revision:

- Server advertises at initialize: `capabilities.experimental["app.mcpproxy/required-tools"] = { "version": 1 }`.
- Client mirrors in its own `capabilities.experimental` and sends the identical `_meta` rider on `tools/list`.
- Migration to 2026-07-28 changes only the advertisement location, not the payload.

## Shape B — SEP-1862 `tools/resolve` Facet (strategic goal)

SEP-1862 defines `tools/resolve`: request `{ name, arguments }`, response `{ tool }` — the complete Tool with refined annotations; servers MUST be side-effect-free, deterministic per `(name, arguments)` within a session, and SHOULD complete in milliseconds. Its Future Extensibility section already establishes `_meta` on the resolved Tool as the extension point (the SEP-1913 sensitivity tie-in rides exactly there). The availability facet is the same per-tool verdict attached the same way:

### Request

```json
{
  "jsonrpc": "2.0",
  "id": 12,
  "method": "tools/resolve",
  "params": { "name": "gh-ops:sync_issues", "arguments": { "repo": "acme/site" } }
}
```

### Response

```json
{
  "jsonrpc": "2.0",
  "id": 12,
  "result": {
    "tool": {
      "name": "gh-ops:sync_issues",
      "inputSchema": { "type": "object", "properties": { "repo": { "type": "string" } } },
      "annotations": { "readOnlyHint": true },
      "_meta": {
        "app.mcpproxy/required-tools": {
          "status": "unavailable",
          "reason": "oauth_required",
          "retryable": false,
          "action": "login",
          "detail": "upstream connection is in PendingAuth",
          "remediation": "Complete login: mcpproxy auth login gh-ops"
        }
      }
    }
  }
}
```

Differences from Shape A, all forced by the resolve exchange shape (note: the request/response pair above follows SEP-1862's envelope exactly as written in the SEP draft, which predates the 2026-07-28 revision; on a 2026-07-28 connection the request would additionally carry the per-request `_meta` protocol fields and the result would carry `resultType`):

- **Per-tool, not batch**: no `verdict`, no `checked_at` set wrapper — one facet per resolved tool. Set aggregation (worst-class-wins) becomes the client's responsibility, or a future batch resolve's.
- **`id` is redundant** (the resolved tool names itself) and is omitted.
- **A `not_found` verdict is not expected to ride a resolve result**: SEP-1862 specifies (SHOULD-level) `-32602` for an unknown tool name, so non-existence stays an error, not a facet verdict; `not_found`/`server_not_configured` stay on the batch surfaces.
- **Reachability is narrower than Shape A's, and this is a real design decision.** `tools/resolve` operates on a *served* tool, but mcpproxy does not serve blocked tools on its listing surface by default: the runtime de-indexes blocked/pending/changed tools, and disabled tools are visible only via the spec-049 `include_disabled` opt-in. Resolving such a tool would therefore hit the unknown-tool error before the facet could carry `server_quarantined` / `tool_pending_approval` / `tool_changed` / `server_disabled`. The v1 position is to **concede this**: those codes are reachable on Shape A/REST/CLI (batch surfaces that take requested IDs, not served tools), while the Shape B facet's reachable codes are the ones that can hold for a served tool — `server_initializing`, `server_unhealthy`, `oauth_required`, and the policy-filter codes. The alternative — listing-but-flagging ineligible tools so resolve can carry their verdicts — would change the listing contract and is out of scope here; if SEP-1862 adopts an availability facet, this listing-vs-resolve tension should be raised in the thread.
- **The SEP-1862 server requirements match on three of four axes — and honestly diverge on the fourth.** Side-effect-free, millisecond-fast, local-state-only are exactly the shipped evaluator's invariants (spec 098 FR-006, SC-002: zero upstream calls hard-asserted by instrumented transport; 10-tool preflight <50ms p95). But SEP-1862 requires resolve results to be deterministic per `(name, arguments)` **within a session**, and an availability verdict is inherently point-in-time — OAuth expires, servers reconnect, approvals land mid-session. The evaluator is deterministic for a given local-state snapshot, not for a session. Any availability facet therefore needs a point-in-time carve-out from the SEP's session-stability rule; this is raised explicitly in the contribution comment rather than papered over.

## Relationship to SEP-1862

findleyr's consolidation principle in the SEP-1862 thread (2025-11-24): "we probably want to avoid a future where there are a bunch of separate preflight checks for a single tool call — it would be better to consolidate them into a single exchange." No participant in the thread and nothing in the SEP text raises availability/readiness as a resolve facet, yet it is the concern this project has the most production evidence for: a required tool silently absent or ineligible surfaces only as downstream agent confusion, and the shipped preflight exists because an operator measured that cost.

**What we would contribute upstream**:

- The facet shape: `{ status, reason, retryable, action?, detail?, remediation? }` with worst-class aggregation semantics — a shipped taxonomy validated by an initial production operator, with a conformance-grade sabotage matrix (48 cells) behind it — plus the extensibility rule (namespaced codes, unknown-is-non-retryable) that lets the gateway class participate without forking the mechanism.
- The evaluator invariants as facet requirements: verdicts from local state only, point-in-time, no side effects. SEP-1862 already mandates the side-effect and local-state parts for the whole exchange; the point-in-time part is a deliberate carve-out from its session-determinism rule that the facet must negotiate (see Shape B above).
- Implementation-report evidence from a proxy/aggregator — the class of server the SEP author points to in the thread as a beneficiary (mcp-fusion's "conservative annotation aggregation", cited by SamMorrowDrums in-thread, 2026-02-12) — and an aggregator-seat answer to the per-tool-vs-capability question raised by connor4312 and answered in-thread by gyrgy from the server-author seat.

**What rides as namespaced codes (gateway-class, not universal core)**:

- Gateway-policy codes: `server_quarantined`, `tool_pending_approval`, `tool_changed`, `hash_mismatch`, `server_disabled`, `server_not_configured`, `server_not_in_scope`. These are not mcpproxy quirks to be hidden away: gateways and aggregators are an established MCP server class (SEP-1862's own thread cites an aggregator as a beneficiary), and gateways with approval, pinning, or scoping models may need codes of this kind. They stay out of the *universal* core because they presuppose a policy layer many servers don't have — but the facet design MUST keep the door open to them: namespaced codes under the existing `_meta` prefix grammar (e.g. `app.mcpproxy/tool_changed`), made safe for every client by the unknown-code-is-non-retryable rule. Where a core concept exists, vendor codes map onto it (`server_disabled` → core disabled-by-policy); quarantine/approval codes have no core counterpart today and ride namespaced — unless the working group prefers to bless a gateway-common set directly, which this design equally supports.
- The `server:tool` ID form (an aggregation artifact — on mcpproxy's own MCP surface these are simply the served tool names), disclosure tiers, profiles, hash pinning, exit-code mapping, and the activity-log correlation contract.
- The REST/CLI surfaces themselves.

**Sequencing**: SEP-1862 is a draft that has not yet entered review. Shape A does not depend on SEP-1862; Shape B is a comment-plus-prototype contribution to the SEP thread, and remains useful design evidence however the SEP evolves. The two shapes deliberately share the same core verdict fields (`status`, `reason`, `retryable`, `action`, `detail`, `remediation`) so the fold is a carriage change, not a redesign; Shape B drops only `id` and the batch set-wrapper.

**Protocol-revision dependency**: mcpproxy speaks the 2025-11-25 revision today; the primary negotiation path in this document (`server/discover`, `capabilities.extensions`) presumes the separately-scoped 2026-07-28 protocol upgrade (its own roadmap epic, currently blocked on mcp-go library support). If implemented before that upgrade lands, v1 ships on the legacy `experimental`-capability carriage alone; the 2026-07-28 negotiation activates with the revision bump, with no payload change.

## Inherited Invariants (normative for any carriage)

1. **Side-effect-free / zero upstream I/O** (spec 098 FR-006): evaluating the extension payload performs zero upstream server calls and mutates no proxy runtime state — no connects, reconnects, re-indexing, config or approval changes. The local activity-log write is the one permitted write. If the runtime is unavailable, the server refuses the rider (Shape A: `-32603` on the carrier if the carrier itself cannot be served honestly; never reduced-fidelity verdicts).
2. **Point-in-time eligibility, not call-success guarantee** (spec 098 FR-002 carve-out): a `ready` verdict means the proxy's local state poses no known obstacle at `checked_at`. Connection-time failures that only manifest during a live call (network races, per-user OAuth state in the server edition) are outside the guarantee.
3. **No-skew, with directionality** (spec 098 FR-002): for the shared policy gates (quarantine, approval, user/config disablement) the verdict is exactly equivalent to call-time dispatch gating — two-way. For fail-open gates the guarantee is one-way: dispatch refusal ⇒ non-ready verdict; a non-ready verdict does not imply dispatch would refuse. Dispatch is ground truth; divergences resolve in its favor.
4. **Disclosure tier** (spec 099 FR-009, locked 2026-08-16): the in-band surface always evaluates at the agent-token tier regardless of session auth context — `AuthContext.IsAdmin()` is never consulted (the MCP middleware injects a full admin context for unauthenticated requests when `require_mcp_auth=false`, so it proves nothing). Out-of-scope and unconfigured collapse to byte-indistinguishable `not_found`; no hashes; `did_you_mean` never crosses the caller-visible scope. Relaxation can only ever be additive.
5. **Parity**: the extension surface MUST call the same shared evaluator through the same glue seam as REST and check mode (spec 099 FR-003/FR-017 pattern), extending the existing parity test: identical `{status, reason, retryable, action}` per ID across surfaces at the agent-token tier.
6. **Transparency**: every evaluated check writes a local activity record (request ID, requested-ID count, set verdict, per-tool reasons) before responding; tool names never leave the local activity log for telemetry.

## Negotiation and Advertisement

**2026-07-28 (current stable revision)**:

- Server side: `app.mcpproxy/required-tools` appears in the `capabilities.extensions` map of the mandatory `server/discover` result, keyed to its settings object: `{ "version": 1, "max_tools": 50 }` (an empty object would mean support with defaults; we always emit `version`). In v1 `max_tools` is a fixed constant (50) carried for readability, not a negotiable value — a negotiated cap would need its own request-validation semantics and is deferred.
- Client side: the identifier appears in the `extensions` map inside `_meta["io.modelcontextprotocol/clientCapabilities"]` on each request that carries the rider.
- Mismatch handling: per core spec, when one party supports an extension and the other does not, the supporting party MUST either revert to core protocol behavior or reject the request. **This extension chooses the revert branch**, which is automatic: an unsupporting server ignores the `_meta` rider and returns a plain `tools/list` result; the client detects absence of the response facet and falls back to `describe_tool` check mode or REST. Documented fallback behavior, as extensions SHOULD provide.

**2025-11-25 (legacy, what mcpproxy speaks today)**: advertisement via the `experimental` capability map at initialize, as specified under Legacy carriage above. The payload and `_meta` keys are identical across revisions; only the advertisement location differs.

**Third-party `_meta` keys are the sanctioned pattern**: the 2026-07-28 spec states normatively that third-party extensions define additional `_meta` keys under their own vendor prefix, specified in the extension's documentation. This document is that specification.

## Security Considerations

- **Probe oracle**: a batch availability check is a name-probing oracle if reason codes differ by whether a server exists, is configured, or is merely out of the caller's scope. Mitigation is the tier collapse (invariant 4): at the agent-token tier — the only tier this extension serves — `server_not_in_scope` and `server_not_configured` are byte-indistinguishable from `not_found`, including `detail`/`remediation` wording, and `did_you_mean` never suggests names outside the caller-visible scope or quarantined-tool names. The spec-099 sabotage matrix already asserts this per cell; the extension surface joins the same matrix.
- **Quarantine disclosure**: `server_quarantined` / `tool_pending_approval` / `tool_changed` reveal that the security layer intervened. This is deliberate for in-scope servers (the caller could observe the same by attempting the call — dispatch refuses with equivalent gating, no-skew invariant), and the codes carry no tool definitions, no diffs, and no hashes. What the caller learns is exactly what a failed call would teach, minus the wasted call.
- **No new attack surface on the carrier**: the rider adds no capability the caller lacks — every verdict is derivable from information the caller could obtain via `describe_tool` check mode today. The 50-ID cap bounds evaluation cost; evaluation is local-state-only, so a flood of riders degrades no upstream and triggers no I/O amplification.
- **Trust model** (echoing gyrgy in the SEP-1862 thread): the extension improves accuracy for honest servers; it does not make verdicts enforceable. A proxy that lies in `tools/list` can lie in the rider. Clients gating automation on the verdict are trusting the proxy exactly as much as they already do by routing calls through it.

## Risks

- **Upstream standardizes a different shape.** If SEP-1862 (or a successor) adopts an availability facet with a different taxonomy or envelope — a competing core enum, resolve losing per-Tool `_meta`, a batch resolve with its own aggregation — Shape A consumers are insulated by the vendor identifier: `app.mcpproxy/required-tools` stays versioned under our namespace, a settings-version bump maps our codes onto the standard's core enum (vendor codes stay namespaced), and mcpproxy would emit both facets during a transition window. The fold-in is an aspiration, not a compatibility dependency.
- **SEP-1862 never progresses.** Shape A and the REST/CLI surfaces are independent of the SEP; the contribution comment and this document remain the design record for a future standalone readiness SEP if that route is chosen.
- **Client adoption is zero.** Shape A is inert until a client emits the rider (see First Consumers); the REST/CLI surfaces already serve the confirmed use case regardless.

## First Consumers

Shape A needs both ends of the wire. The planned first consumers, doubling as client-side implementation evidence for any SEP:

1. **mcpproxy's own CLI in client mode**: `mcpproxy tools preflight --in-band` exercising the rider against the proxy's own MCP surface (self-hosted conformance).
2. **A copy-paste agent snippet** in the docs: raw JSON-RPC (the Shape A request above verbatim) plus one SDK example, so any harness that can speak MCP can gate on the rider.
3. **A real agent runtime**: to be approached only after upstream reaction to the SEP comment — candidates are the harnesses already consuming the REST preflight (the #969 operator's cron flow) graduating to in-band.

## Non-Goals

- Refresh/liveness operations — the verdict describes proxy state only; refresh remains a separate explicit operation (reporter-confirmed boundary, #969).
- Per-user verdicts in the server edition (`as_user` reserved).
- Operator-tier disclosure in-band (hashes, `server_not_in_scope`) — REST-only, by design.
- Standardizing mcpproxy's policy codes into the universal core — what is proposed for SEP-1862 is the facet shape, a small universal core, and an extensibility rule under which gateway-class codes (ours included) ride as namespaced values; whether a gateway-common set is later blessed directly is the working group's call.
- A new JSON-RPC method. Both shapes ride existing exchanges.

## Open Questions

1. **Carrier method for Shape A**: `tools/list` is chosen because it is the availability surface and degrades for free, but it forces the client to pay for a full tool listing to run a check. Riding `ping` (whose result is otherwise empty) would decouple them at the cost of a semantically odd carrier. Decide before implementation.
2. **Malformed-rider severity**: v1 fails the carrier call with `-32602` (mirrors spec 099's request-error stance). The alternative — answer the carrier normally and return an error facet — is friendlier to mixed-version fleets. Revisit if field reports show riders breaking listings.
3. **In-band hash pinning**: reserved in v1 (follows the spec-099 `expect_hashes` trim). If cron-style contracts move in-band, pinning wants to come with them — but the no-hash-disclosure tier rule means mismatch reporting must not confirm the current hash. Needs its own design note.
4. **Shape B batch**: single-tool `tools/resolve` pushes set aggregation client-side. Whether to propose a batch resolve upstream, or keep batch checks on Shape A / REST permanently, is a SEP-1862 thread question — it intersects the per-tool-vs-capability discussion (raised by connor4312, answered in-thread by gyrgy from the server-author seat).
5. **Core-code mapping for standardization**: the exact partition of the 15 codes into a standardizable core enum vs vendor-namespaced codes (sketched above) needs a concrete proposal before any SEP-1862 comment.
6. ~~**`max_tools` in settings**~~ — resolved for v1: the cap is a fixed constant (50, matching spec 099's in-band batch cap); `max_tools` in settings is informational only. A negotiable cap would need request-validation semantics and waits for a real need.
