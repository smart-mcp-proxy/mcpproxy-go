# Contract: Deferred Direct Surface

Normative wire-level contract for Spec 102. Where this file and
[spec.md](../spec.md) disagree, the spec wins. Reuses two Spec-085 contracts
verbatim: `specs/085-compact-router/contracts/signature-grammar.md` (signature
rendering) and `.../invalid-params-error.md` (self-healing error body).

## 1. Deferred `tools/list` entry

With `direct_tool_response_mode: "deferred"`, each upstream tool entry on any
direct-serving route (`/mcp/all`; `/mcp`, `/v1/tool_code`, `/v1/tool-code` when
`routing_mode:"direct"`):

```json
{
  "name": "github__create_issue",
  "description": "[github] Create a new issue in a repository.\ncreate_issue(owner*:str, repo*:str, title*:str, labels?:[str], milestone~)",
  "inputSchema": { "type": "object" },
  "annotations": { "...unchanged from full mode..." }
}
```

Rules:
- `name`, annotations, membership, count, and ordering source: identical to full
  mode for the same session (FR-008/FR-016).
- `description` = full-mode description (`[server] …`, untruncated) + newline +
  the **bare tool name** + the compact signature per the 085 grammar (`*`
  required — never elided; `~` lossy collapse). The name prefix is normative and
  must be prepended by this renderer: `toolsig.Signature.Sig` is the parenthesised
  parameter list only (`"(origin*:str, ttl:int=3600, account~:obj)"`,
  `internal/toolsig/signature.go:18`) and carries no name.
  `toolsig.Signature.Desc` — the first-sentence prefix Spec 085's compact entries
  use — is deliberately NOT used here, because deferred entries keep the full
  description. On a signature-cache miss the whole suffix (name + signature) is
  absent and the entry is otherwise unchanged (never dropped, never delayed —
  FR-005).
- `inputSchema` is exactly `{"type":"object"}` — never literal `{}`, never absent,
  never carrying upstream properties/required (strict-client safety, FR-004).
  Note the mechanism is normative too: `mcp.NewTool` marshals
  `{"properties":{},"required":[],"type":"object"}`, which fails this rule and
  re-opens the arg-pruning hazard, so deferred entries use
  `mcp.NewToolWithRawSchema` with annotations copied on explicitly (research.md
  R11).
- `outputSchema` is absent (research.md R2).
- Full mode (`"full"`, the default) is byte-identical to pre-feature output modulo
  §3's built-in addition (FR-015).

## 2. In-band convention (FR-007)

The direct server's `initialize` result carries `instructions` (today absent on
this server instance), static across both serialization modes, conditionally
phrased. The value is `resolveInstructions(cfg.Instructions)` (the operator's
configured `instructions`, or the built-in default) followed by a blank line and
the deferral legend — so enabling this feature never hides an operator's
configured instructions on the direct surface. Reference legend text (final bytes
pinned by the direct built-in golden, captured with the default
`instructions`):

> Some tool descriptions end with a compact signature `(param*:type, …)`:
> `*` = required, `~` = collapsed/lossy details. When a signature is present the
> listed inputSchema is a placeholder — flat signatures are directly callable;
> for `~`-marked tools call `describe_tool` with the listed tool name to get the
> full schema.

## 3. `describe_tool` on the direct surface (FR-009/FR-011)

- **Definition**: the existing single `buildDescribeToolTool` definition
  (surface-neutral prose per research.md R5), listed in BOTH serialization modes,
  registered via direct tool-set construction so it survives every refresh
  (FR-018).
- **Request**: unchanged shape — `tool_ids` (≤5, or ≤50 with `check:true`),
  `check`, `filters`. Accepted id forms on this surface: canonical
  `"<server>:<tool>"` AND direct `"<server>__<tool>"`; direct ids resolve through
  the registration mapping (first-`__` re-parsing is forbidden).
- **Response**: existing shape (`definitions` + per-id `errors`); definitions
  additively carry `"output_schema": {…}` when the tool declares one (all
  surfaces).
- **Per-id outcomes** (parity with THIS session's direct `tools/list`):

| Id state for this session | definition mode | `check: true` |
|---|---|---|
| Listed, ready | full definition | `ready` |
| Listed, but pending / changed / tool-level-disabled (non-agent sessions retain **these** — see the note below) | full snapshot-backed definition — a listed tool is never undescribable | informative verdict (`pending_approval` / `changed` / `disabled`) |
| On a **server-level** quarantined or disabled server | never listed on this surface (see note) → `not_found` | `not_found` |
| Omitted from this session's listing — any reason: token server scope, operation-permission tier, profile scope, agent callability, or nonexistence | `not_found` + standard remediation | `not_found` |
| Removed between list and describe | per-id `not_found`, batch not failed | `not_found` |
| Malformed id | per-id `not_found` + format remediation | same |

**Note — server-level vs tool-level states.** The spec's Edge Cases say a
non-agent direct listing "retains tools that are pending, changed, quarantined or
disabled". On this surface that is true only of the **tool-level** states:
`DiscoverTools` drops a server before its tools are ever projected — `if
!snapshot.enabled { continue }` and the quarantined-server skip
(`internal/upstream/manager.go:1128-1138`) — so a tool on a quarantined or
disabled *server* is never in the direct catalog and therefore answers
`not_found` here in both modes, exactly like any other unlisted id. Only
pending / changed / tool-level-disabled entries reach the listing and get the
informative verdict. This narrowing does not weaken FR-011; it is the same
parity rule applied to a state the listing never had.

No reason code, remediation variant, **suggestion**, or timing may confirm the
existence of an id this session's listing omitted. That explicitly covers both
suggestion channels: `did_you_mean` (check mode) and the definition-mode
case-correction hint. Both must be drawn from this session's direct catalog under
the listing-parity gates, or omitted on this surface — the shared preflight
suggestion corpus is server-level-filtered only and is NOT parity by itself
(research.md R10). Remediation strings on this surface must not instruct the agent
to "re-run retrieve_tools": the shared not-found and malformed-id remediations are
rewritten surface-neutral along with the tool prose (research.md R5).

Membership on both sides of this table is decided by the same catalog: the direct
listing filters resolve display names through the catalog's registration mapping,
never by re-parsing on the first `__`, so listing and describe cannot disagree for
a server name containing `__`.

Retrieve_tools-surface semantics are unchanged.

## 4. Pre-dispatch validation (FR-012/FR-013)

Direct-mode calls validate against the catalog entry's stored `paramsJSON` (never
the advertised placeholder), in both serialization modes, after the
profile/token/callability gates and before upstream dispatch. Failure → the
Spec-085 `invalid-params-error.md` body (error, `error_type:"invalid_params"`,
tool, one-line hint, full `input_schema`), with the hint's describe reference
using an id valid on this surface. Fail-open for uncompilable schemas (counted,
logged); non-argument failures keep their existing shapes with no schema attached.

## 5. Rollout (FR-014)

A `direct_tool_response_mode` change via config hot-reload rebuilds the direct
tool set and (via mcp-go `SetTools`) emits `notifications/tools/list_changed` to
connected direct-surface sessions; the tool set (names, count) is identical across
the flip. A config edit that does not change the effective serialization triggers
no rebuild and no notification.
