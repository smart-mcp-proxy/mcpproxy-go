# Contract: Marker Format

**Package**: `internal/toonenc`
**Constant**: `Marker`

The marker is the deterministic one-line header that precedes every TOON-encoded block (FR-005). It
is part of the response contract with agents and is asserted byte-for-byte in tests.

## Exact bytes

```
[mcpproxy:toon/v1] TOON-encoded JSON (toon-format.org); decode to JSON before reuse - tool arguments must still be sent as JSON.
```

- Single line, **strict ASCII** (no em dash — the separator is ` - `, a hyphen with surrounding
  spaces), no trailing whitespace, no trailing newline in the constant itself.
- Go: `const Marker = "[mcpproxy:toon/v1] TOON-encoded JSON (toon-format.org); decode to JSON before reuse - tool arguments must still be sent as JSON."`
- **Mode-agnostic wording**: the marker says "TOON-encoded JSON", NOT "tabular JSON". `adaptive` only
  ever encodes tabular payloads, but `always` mode (FR-009) encodes *any* JSON value (nested objects,
  scalars), so the marker must be truthful for both. A single marker serves both modes; there is no
  per-mode variant.

## Emission assembly

The emitted text block is exactly:

```
<Marker>\n<toonBody>
```

- Exactly one `\n` between marker and body.
- `<toonBody>` is the output of `toon.MarshalString(v)` with no added leading/trailing whitespace.
- Passthrough blocks carry **no marker** (FR-005, US1-AC2, SC-006).

## Field-by-field intent (why each token is present)

| Fragment | Purpose | FR |
|----------|---------|-----|
| `[mcpproxy:toon/v1]` | Machine-detectable sentinel + version. Lets agents/the profiler recognize the encoding and lets a future classifier v2 bump the version. | FR-005, FR-011 |
| `TOON-encoded JSON (toon-format.org)` | Names the encoding and points to the spec so an unfamiliar agent can look it up. Says "JSON" not "tabular JSON" so it stays truthful in `always` mode, which encodes arbitrary (non-tabular) JSON. | FR-005, FR-009 |
| `decode to JSON before reuse` | The decode hint — the payload is TOON, not JSON. | FR-005 |
| `tool arguments must still be sent as JSON` | Guards the spec's "agents echo results into args" edge case: input parsing is out of scope, args stay JSON. | FR-005, Spec edge cases |

## Guarantees

- **Deterministic**: a compile-time constant — no interpolation, timestamps, counts, or locale. Every
  encoded block across every call is prefixed by identical bytes (FR-011).
- **Counted in the size comparison**: `len(Marker)+1` is included in `EncodedEmissionBytes`, so the
  never-larger and threshold checks account for the marker's own cost (FR-003c, FR-004).
- **Truncation-safe**: because the marker is at the head of the block and truncation runs *after*
  encoding (D-SEAM), the marker + hint are never truncated away (FR-008); a mid-row truncation still
  leaves the marker intact at the top.

## Cross-surface documentation

The same wording is echoed in the `call_tool_read|write|destructive` tool descriptions
(`buildCallToolVariantTool`, `internal/server/mcp.go:615`) so agents learn the marker's meaning
in-session (spec Assumptions). That copy is documentation of this contract, not a second source of
truth — `toonenc.Marker` is authoritative.

## Test obligations (TDD)

- Assert `Marker` equals the exact string above, byte-for-byte, and that it is pure ASCII
  (`utf8.RuneCountInString(Marker) == len(Marker)`) — guards against an em dash creeping back in.
- Assert an encoded emission starts with `Marker + "\n"` and that the remainder round-trips:
  `toon.DecodeString(body)` reconstructs the classified value (structural equality, `json.Number`
  aware).
- Assert passthrough emissions never contain `Marker`.
