# MCPX_STDIO_HANDSHAKE_INVALID

**Severity**: see `internal/diagnostics/registry.go` for the authoritative severity.
**Registered in**: [`internal/diagnostics/registry.go`](../../internal/diagnostics/registry.go)

## What happened

mcpproxy classified a terminal failure as `MCPX_STDIO_HANDSHAKE_INVALID`. This page is a stub
and will be expanded with cause, symptoms, and remediation guidance.

## How to fix

See the fix steps emitted by the CLI and web UI:

```bash
mcpproxy doctor --server <name>
mcpproxy doctor fix MCPX_STDIO_HANDSHAKE_INVALID --server <name>    # dry-run by default for destructive fixes
```

## Related

- [Spec 044 — Diagnostics & Error Taxonomy](../../specs/044-diagnostics-taxonomy/spec.md)
- [Design doc](../superpowers/specs/2026-04-24-diagnostics-error-taxonomy-design.md)
