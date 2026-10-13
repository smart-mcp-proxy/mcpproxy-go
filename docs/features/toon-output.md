---
id: toon-output
title: TOON Output
sidebar_label: TOON Output
description: Adaptive TOON encoding of tool-call results — token savings on tabular payloads, byte-identical passthrough everywhere else
keywords: [toon, encoding, tokens, compression, tool results, adaptive]
---

# TOON Output (Adaptive Result Encoding)

MCPProxy can re-encode tool-call results as [TOON](https://toon-format.org) — a compact, human-readable serialization that beats JSON on **large uniform tabular arrays** (database rows, list endpoints, log dumps). The feature is **adaptive**: each result is encoded only when it is actually tabular *and* the TOON emission is measurably smaller than the JSON the agent would otherwise receive. Everything else passes through byte-identically.

**Off by default.** No behavior changes until you opt in.

## Why adaptive, not blanket?

The spec-083 profiler measured TOON against compact JSON on real mcpproxy payloads:

- Tool **listings**: TOON was **+23.9% worse** — deeply nested, non-uniform structures favor JSON. Listings (`retrieve_tools`) are therefore permanently out of scope.
- Mixed **result** fixtures: only −2.2% — because most tool results are not tabular.
- TOON's winning regime — large uniform arrays of flat rows — is real (~30–60% in TOON's own benchmarks) but occurs only in a subset of results.

A blanket "TOON everything" switch would burn tokens on most responses to win on a few. Adaptive mode encodes only where TOON wins by a configurable margin, and **by construction never produces a response larger than passthrough** (the encoder compares complete emissions — marker included — before choosing).

## Configuration

```json
{
  "toon_output": "adaptive",
  "toon_min_savings_pct": 15
}
```

| Field | Default | Values |
|-------|---------|--------|
| `toon_output` | `"off"` | `off` \| `adaptive` \| `always` |
| `toon_min_savings_pct` | `15` | integer 1–90 |

- **`off`** (default): all responses byte-identical to pre-feature behavior.
- **`adaptive`** (recommended): encode a result text block only when it (a) parses as JSON, (b) classifies as tabular-uniform, and (c) the complete TOON emission (marker + decode hint + body) is at least `toon_min_savings_pct` percent smaller than the exact passthrough block. Near-ties pass through — they are not worth the agent-side decode risk.
- **`always`**: encode **every** JSON-parseable block — any JSON value, not just tabular — regardless of the size comparison. **Benchmarking/debugging only; can increase token cost.** Non-JSON text still passes through.

Changes **hot-reload**: save the config file and the next tool call uses the new mode. No restart, no client reconfiguration.

### Per-server override

```json
{
  "toon_output": "adaptive",
  "mcpServers": [
    { "name": "legacy-agent", "url": "https://...", "toon_output": "off" }
  ]
}
```

A non-empty per-server `toon_output` overrides the global for that server's tools (precedence: per-server > global > default `off`). Use it to exempt a server whose consuming agent cannot tolerate non-JSON results.

## What counts as tabular (v1 classifier)

A text block is tabular-uniform when it is:

- a JSON **array of at least 4 objects**;
- every value is a **scalar or null** (no nested objects/arrays in v1);
- the union key set derives from row keys where each key is present in at least **90% of rows**;
- an object with **exactly one key** whose value is such an array (an "envelope") also qualifies.

Empty arrays, arrays of non-objects, scalars, and nested structures do not qualify. The classifier is deliberately conservative — even when it misclassifies, the size comparison is the backstop: nothing is ever emitted larger than passthrough in `adaptive` mode.

## What the agent sees

Every encoded block is prefixed with one deterministic marker line (the decode contract with agents; also echoed in the `call_tool_*` tool descriptions so agents learn it in-session):

```
[mcpproxy:toon/v1] TOON-encoded JSON (toon-format.org); decode to JSON before reuse - tool arguments must still be sent as JSON.
users[3]{id,name,active}:
  1,Ada,true
  2,Linus,true
  3,Grace,false
```

Passthrough blocks carry **no marker** and are unchanged. Tool **arguments** always remain JSON — only result rendering is affected.

## Safety chain (ordering guarantees)

The encoder sits at exactly one seam — the text-block rendering of `call_tool_read`/`call_tool_write`/`call_tool_destructive` responses — positioned so every existing safety stage is unaffected:

1. **Output sanitisation first**: redact/block/strip runs on the raw upstream result *before* encoding; the encoder's input is the sanitised result.
2. **Sensitive-data detection scans pre-encoding text**: the activity pipeline's scanner receives the pre-encoding rendering, so enabling TOON produces identical detection findings.
3. **Truncation last**: the response-size limit applies to the final rendered payload; the marker + hint sit at the head of the block and survive truncation. When the configured limit is too small to hold marker + hint + one data row, the block passes through (in every mode).
4. **Errors never lose data**: any encoder failure falls back to passthrough, is logged, and counted — it never surfaces as a tool-call error.
5. **Structured content untouched**: output-schema validation still evaluates the original `structuredContent`; only the text rendering is re-encoded.

**Never encoded**: `retrieve_tools` and all listings (measured net-negative), code-execution paths (agent-written programs expect JSON), and direct-mode server tools (unmodified upstream passthrough).

## Observing decisions

Every tool call records its per-block encoding decision in the activity log metadata:

```bash
mcpproxy activity list --request-id <id> -o json
# metadata.toon_output.mode  → resolved mode
# metadata.toon_output.blocks[].outcome ∈
#   encoded | passthrough-not-tabular | passthrough-below-threshold | passthrough-error
# bytes_before / bytes_after present on encoded blocks
```

Byte savings approximate token savings for this payload class; the spec-083 profiler reports true token deltas per release.

## Related Documentation

- [Configuration Reference](../configuration/config-file.md)
- [Output Sanitisation](output-sanitisation.md)
- [Sensitive Data Detection](sensitive-data-detection.md)
- [Activity Log](activity-log.md)
