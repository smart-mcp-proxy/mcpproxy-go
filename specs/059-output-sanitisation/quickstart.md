# Quickstart: Output Sanitisation (Track B)

## Enable

Add to `~/.mcpproxy/mcp_config.json` (all optional — defaults already spotlight untrusted output):

```json
{
  "output_sanitisation": {
    "spotlight_untrusted": true,
    "response_action": "redact",
    "strip_control_chars": true,
    "strip_classes": ["ansi", "c0c1", "bidi", "zero_width"],
    "max_redactions": 100
  }
}
```

- `response_action`: `"spotlight"` (non-mutating) | `"redact"` (mask secrets) | `"block"` (replace payload on critical detection).
- **Fully opt-in**: omit the block (or leave `spotlight_untrusted: false`) → mcpproxy forwards everything unchanged. Set `spotlight_untrusted: true` to enable the wrapper.

## Verify (the mandatory three)

1. **curl / MCP roundtrip**: stand up mcpproxy with a stub untrusted upstream that returns a secret + ANSI; call the tool via `/mcp` and `call_tool_read`; assert the response is wrapped (`«untrusted:…»`) and, with `redact`, the secret is `[REDACTED:cloud_credentials]`.
2. **e2e script**: `./scripts/test-api-e2e.sh` stays green (no regression).
3. **Web UI / chrome ext**: open the activity view; confirm a `policy_decision` row appears for the redact/block call with the reason.

## Behaviour summary

| Config | Untrusted tool text | Trusted tool text | Non-text blocks |
|--------|--------------------|-------------------|-----------------|
| default | wrapped in delimiters | unchanged | unchanged |
| `redact` | wrapped + secrets masked | secrets masked | unchanged |
| `block` + critical | payload → remediation error + audit | payload → remediation error + audit | n/a |
| `strip_control_chars` | wrapped + control seqs stripped | unchanged | unchanged |
