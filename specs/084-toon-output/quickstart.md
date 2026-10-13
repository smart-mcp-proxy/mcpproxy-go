# Quickstart: Adaptive TOON Output

## For operators

### Enable adaptively (recommended)

In `~/.mcpproxy/mcp_config.json`:

```json
{
  "toon_output": "adaptive",
  "toon_min_savings_pct": 15
}
```

- `adaptive` (recommended): each tool-call result is TOON-encoded **only** when it is a large uniform
  table AND the TOON emission (marker + hint + body) is at least 15% smaller than the JSON the agent
  would otherwise receive. Everything else passes through byte-identically. No response is ever larger
  than it would be with the feature off.
- `toon_min_savings_pct` (1–90, default 15): raise it to encode only bigger wins; lower it to encode
  more aggressively. Byte savings approximate token savings for this payload class; the spec-083
  profiler reports the true token delta.

Save the file — the change **hot-reloads**; the next tool call uses the new setting. No restart.

### Disable one incompatible server

```json
{
  "toon_output": "adaptive",
  "mcpServers": [
    { "name": "legacy-agent", "url": "...", "toon_output": "off" }
  ]
}
```

Per-server `toon_output` overrides the global for that server's tools (precedence: per-server >
global > default). Omit it to inherit the global.

### Revert everything

Set `"toon_output": "off"` (or delete the key). On the next hot-reload, all responses are
byte-identical to pre-feature behavior.

### `always` mode — benchmarking only

```json
{ "toon_output": "always" }
```

Encodes every JSON-parseable result — **any** JSON value, not just tabular — **regardless of size**.
Non-JSON text still passes through untouched. Documented as potentially **cost-increasing** — use only
for measurement/debugging, never in production.

### What the agent sees

An encoded block is prefixed with one marker line:

```
[mcpproxy:toon/v1] TOON-encoded JSON (toon-format.org); decode to JSON before reuse - tool arguments must still be sent as JSON.
users[3]{id,name,active}:
  1,Ada,true
  2,Linus,true
  3,Grace,false
```

Passthrough responses carry no marker and are unchanged.

### Observing decisions

Every tool call records its encoding decision in the activity log metadata:

```bash
mcpproxy activity list --request-id <id> -o json
# → metadata.toon_output.blocks[].outcome ∈
#   encoded | passthrough-not-tabular | passthrough-below-threshold | passthrough-error
#   with bytes_before / bytes_after on encoded blocks
```

## For developers

### Where the encoder lives

`internal/toonenc` — a standalone, importable package (stdlib + `toon-go` only). Both the server and
the spec-083 bench arm call the same `toonenc.EncodeBlock`.

### The one-line seam

In `handleCallToolVariant` (`internal/server/mcp.go`), between output sanitisation and truncation:

```go
// ... applyOutputSanitisation(ctx, ..., result)  // secrets redacted/blocked on RAW result
// ... rawByteSize(result)                          // Spec 069 measurement — do NOT move
detectionText, decisions := p.encodeToonBlocks(serverName, actualToolName, contentTrust, args, result)  // NEW seam
// ... forwardContentResult(result, p.truncator, ...)  // truncates the ENCODED text
```

`p.encodeToonBlocks` resolves the mode (`toonenc.ParseMode(p.config.ResolveToonOutput(sc))` — config
stays string-only, the server parses to `toonenc.Mode` at the boundary). When `off` it returns
**`("", nil)`** — an empty `detectionText` so the detector falls back to today's `response` (zero
behavior change). Otherwise it walks the content blocks calling `toonenc.EncodeBlock`, and builds
`detectionText` as a best-effort reconstruction of what the feature-off detector would scan: the
**all-blocks** rendering (text + image/audio placeholders, matching `forwardContentResult`) of the
pre-encoding blocks, truncated with the same truncator budget, and spotlight-wrapped via
`contentTrust`. The contract is **finding parity, not byte parity** — the only residual difference is
the truncation banner's timestamped cache key, which holds no upstream data and so changes no finding.
The per-block `decisions` feed the activity record; on any `passthrough-error` decision it logs a warn
+ increments the fallback counter (FR-006). `toolName`/`args` feed the log fields and truncation cache
key; `contentTrust` drives the spotlight reconstruction.

### Run the checks

```bash
go test -race ./internal/toonenc/... ./internal/config/... ./internal/server/... -v
./scripts/test-api-e2e.sh          # hot-reload + marker + detection-parity E2E
go test ./bench/...                # profiler results arm (requires spec-083 harness merged)
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...
```

### Prove the invariants

- **Never-larger**: `internal/toonenc/encoder_test.go` property test — every fixture in `adaptive`,
  `len(out) <= len(in)`.
- **Determinism**: encode each fixture twice; identical bytes + identical `Decision`.
- **Off = byte-identical**: `internal/server` E2E — same corpus with `off` vs pre-feature, zero diff.
- **Detection parity**: feed a known-secret fixture with TOON on/off — identical detector findings.
