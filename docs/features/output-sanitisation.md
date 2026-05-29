# Output Sanitisation (Spec 054 Track B)

mcpproxy contains untrusted tool output before it reaches your agent. It builds
on the existing content-trust classification (Spec 035, derived from each tool's
`openWorldHint`) and the secret detector (Spec 026), enforcing them at the single
response chokepoint instead of only logging.

## What it does

| Behaviour | Default | Trigger |
|-----------|---------|---------|
| **Spotlight** untrusted text in source-identifying delimiters (lossless) | **off** | `spotlight_untrusted: true` |
| **Redact** detected secrets → `[REDACTED:<category>]` | off | `response_action: redact` |
| **Strip** control sequences (ANSI / C0-C1 / zero-width / bidi) on untrusted text | off | `strip_control_chars: true` |
| **Block** the whole response on a critical detection | off | `response_action: block` |

The feature is **fully opt-in**: with no `output_sanitisation` block (or all
options at their defaults) mcpproxy forwards every tool response byte-for-byte —
identical to pre-feature behaviour. Each behaviour above is enabled
independently. Non-text blocks (image / audio / embedded resource) are never
modified.

Spotlighting wraps untrusted text as:

```
«untrusted:server/tool»
<tool output, with « and » escaped so it cannot forge the delimiter>
«/untrusted:server/tool»
```

## Configuration

```json
{
  "output_sanitisation": {
    "spotlight_untrusted": true,             // opt into spotlighting untrusted output
    "response_action": "spotlight",          // "spotlight" | "redact" | "block"
    "strip_control_chars": false,
    "strip_classes": ["ansi", "c0c1", "bidi", "zero_width"],
    "max_redactions": 100
  }
}
```

Omit the block entirely (or leave `spotlight_untrusted: false`) and mcpproxy
does nothing — fully backward-compatible. Set `spotlight_untrusted: true` for the
lossless wrapper, `response_action: redact` to mask secrets, or
`response_action: block` to drop critical responses.

## Auditing

Redact / strip / block actions emit a `policy_decision` activity record with a
descriptive reason (visible via `mcpproxy activity list` and the Web UI Activity
Log). Pure spotlighting is logged at debug level only. The activity log stores
the **sanitised** response, so redacted secrets are not persisted in the audit
trail.

Redaction, stripping, and blocking run **before** truncation and the `read_cache`
write, so a large response paginated through `read_cache` carries the same
sanitisation as the agent-facing copy (a blocked response is never cached at
all). The lossless spotlight wrapper is applied after truncation and is not
cached (the cache holds raw, paginable records).

See also: [sensitive-data-detection.md](./sensitive-data-detection.md) (the
detector reused for redaction/block) and Spec 056 (output-schema validation,
Track A) which shares the same chokepoint.
