# Output-Schema Validation (Spec 056 / Security Gateway Track A)

When an upstream MCP tool declares an `outputSchema`, MCPProxy can verify that
the tool's **structured response** conforms to that schema *before* it reaches
your agent. This protects the agent's context from a buggy or compromised
server injecting malformed, oversized, or unexpected data.

This is **Track A** of the MCP security-gateway hardening effort (Spec 054). It
validates `structuredContent` only; sanitisation/redaction of untrusted text
(Track B) and access control (Track C) are separate features.

## Configuration

Add an `output_validation` block to `~/.mcpproxy/mcp_config.json`:

```json
{
  "output_validation": {
    "mode": "warn",
    "max_bytes": 5242880,
    "max_depth": 64,
    "missing_structured_content": "allow"
  }
}
```

| Field | Values | Default | Meaning |
|-------|--------|---------|---------|
| `mode` | `off` \| `warn` \| `strict` | `warn` | `off` disables validation; `warn` forwards violations but logs them; `strict` blocks violations. |
| `max_bytes` | integer | `5242880` (5 MiB) | Max serialized size of the structured payload; larger payloads are a guard violation. |
| `max_depth` | integer | `64` | Max nesting depth of the structured payload; deeper payloads are a guard violation. |
| `missing_structured_content` | `allow` \| `block` | `allow` | In **strict** mode only: what to do when a tool declares a schema but returns no `structuredContent`. `allow` forwards it (recommended); `block` rejects it. |

If the block is **absent**, validation runs in `warn` mode with the defaults
above — safe to leave on, since it never blocks a working agent; it only adds
audit signal for tools that declare an output schema. Set `mode: "strict"` once
you've reviewed the warnings.

Config is hot-reloaded; changing `mode` does not require a restart.

## Behaviour

| Tool declares `outputSchema`? | Response has `structuredContent`? | Conforms? | `warn` | `strict` |
|---|---|---|---|---|
| No | — | — | forward (no-op) | forward (no-op) |
| Yes | No (text only) | — | forward (no-op) | forward, unless `missing_structured_content=block` |
| Yes | Yes | Yes | **forward unchanged** | **forward unchanged** |
| Yes | Yes | No | forward + audit | **block** + audit |
| Yes | Yes (oversized / too deep) | — | forward + audit (guard) | **block** + audit (guard) |
| Yes | upstream error result | — | forward (skip) | forward (skip) |

Key guarantees:

- **Lossless on success**: a conforming `structuredContent` is forwarded
  byte-for-byte unchanged (validation runs on a read-only view).
- **Never blocks on a bad schema**: if a tool's declared schema is itself not
  compilable, validation degrades to a no-op (logged once) — it never blocks
  traffic on the proxy's inability to compile a schema.
- **Strict blocks return a clear error** to the agent:
  `output schema validation failed: <keyword> at <path>: <detail>`.

## Auditing

Every violation (block or warn) is recorded as a `policy_decision` activity
record with `decision = "blocked"` or `"warning"` and the violation detail:

```bash
mcpproxy activity list --type policy_decision    # validation warnings + blocks
mcpproxy activity list --status blocked          # strict blocks only
mcpproxy activity show <id>                       # tool, decision, reason
```

Or over the REST API:

```bash
curl -s -H "X-API-Key: $KEY" \
  "http://127.0.0.1:8080/api/v1/activity?type=policy_decision&limit=5" | jq .
```

## Editions

Identical behaviour in the personal and server editions (no build-tag-specific
logic).
