# Quickstart: Compact Router

Walkthrough for building, running, toggling, and measuring the compact router. Assumes the
`085-compact-router` worktree and a few upstream servers configured in
`~/.mcpproxy/mcp_config.json`.

## 1. Build

```bash
cd /Users/user/repos/mcpproxy-worktrees/085
go build -o mcpproxy ./cmd/mcpproxy          # personal edition
go build -tags server -o mcpproxy-server ./cmd/mcpproxy   # server edition (flag/env path)
```

## 2. Default behavior — full mode (Phase 1, FR-006)

Unset `tool_response_mode` ⇒ `full`. Responses are byte-identical to pre-feature behavior.

```bash
# Kill any instance holding the DB, then run on an isolated dev instance/port.
./mcpproxy serve --listen 127.0.0.1:18085 --data-dir /tmp/mcpproxy-085 --log-level=debug
```

Search and observe today's shape (full `inputSchema` per entry):
```bash
mcpproxy tools retrieve --query "create a cdn resource" -o json   # or via /mcp retrieve_tools
```
The tools/list surface differs from pre-feature by **exactly**: `describe_tool` added, the
`detail` param added to `retrieve_tools`, and updated `call_tool_*`/`retrieve_tools`
descriptions (FR-014). Everything else is unchanged.

## 3. Flip to compact — hot-reload, no restart (FR-015 / SC-007)

Edit `~/.mcpproxy/mcp_config.json` (or the dev `--data-dir` config):
```json
{ "tool_response_mode": "compact", "mcpServers": [ … ] }
```
Save. The existing config watcher reloads; the **next** `retrieve_tools` call returns compact
entries:
```json
{ "query": "create a cdn resource",
  "tools": [
    {"id":"digitalocean:cdn_create","score":0.94,"lossy":false,
     "sig":"(origin*:str, certificate_id:str, custom_domain:str, ttl:int=3600)",
     "desc":"Create a CDN for a Spaces bucket"} ],
  "hint":"Call via call_tool_write. If a sig contains '~', call describe_tool({tool_ids:[id]}) first." }
```
Env/flag equivalents (server edition):
```bash
MCPPROXY_TOOL_RESPONSE_MODE=compact ./mcpproxy-server serve …
./mcpproxy-server serve --tool-response-mode compact …
```

## 4. Per-call override (FR-005)

Force full schemas for one call regardless of configured mode:
```jsonc
// retrieve_tools arguments
{ "query": "create a cdn resource", "detail": "full" }   // → full entries this call only
{ "query": "create a cdn resource", "detail": "compact" } // → compact even if config=full
```
Unset `detail` ⇒ configured `tool_response_mode` applies.

## 5. Full schemas on demand — describe_tool (US2)

```jsonc
{ "tool_ids": ["cloudflare:zone_create"] }
// → { "definitions": [ {full inputSchema + long description} ], "errors": [] }
```
Batch ≤5; unknown/invisible ids come back in `errors` without failing the batch; >5 ids ⇒ a
single limit error. A quarantined/disabled/out-of-scope id returns a per-id error, never a
definition (parity with retrieve_tools visibility).

## 6. Self-healing failed call (US3)

Call a tool omitting a required param:
```jsonc
// call_tool_write
{ "name": "github:create_issue", "args": { "body": "no title" } }
```
The error embeds the tool's full `input_schema` + a `hint`; upstream is not even hit
(pre-dispatch validation). Retry with `{ "title": "…", "body": "…" }` succeeds. Non-argument
failures (auth/timeout/5xx) return the normal error with no schema.

## 7. Verify the invariants

```bash
go test -race ./internal/toolsig/...            # grammar, determinism, never-elide, lossy-legible
go test -race ./internal/server/... -run 'RetrieveTools|DescribeTool|SelfHeal|EntryBuilder'
./scripts/test-api-e2e.sh                        # API E2E incl. mode toggle + self-heal
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...
```
Key assertions: full-mode byte-identity (SC-003); ranked-ID identity full vs compact (SC-002);
required params present in 100% of signatures (SC-004); mode toggle within one reload (SC-007).

## 8. Measure the gates (US5, spec-083 profiler)

After `internal/toolsig` lands and the offline bench arm imports it:
```bash
make bench-discovery                      # offline corpus arm (compact_sig via internal/toolsig)
# live arm against a running compact-mode proxy — see bench/README.md live mode
```
Read from the report: per-query ranked-ID identity (gate 100% / SC-002), discovery-token
p50/p95/max (gate median ≤1,000, max ≤1,500), lossy-signature rate on the 45-tool corpus (gate
<20% / SC-005), describe_tool calls per completed task (<0.3, informational). These authorize
the separate Phase-2 default flip (FR-016) — **not** part of this release.

## Reference fixtures (spec Assumptions)
- Golden set: `specs/065-evaluation-foundation/datasets/retrieval_golden_v1.json` (47 queries)
- Frozen corpus: `specs/083-discovery-profiler/datasets/corpus_v2.tools.json` (45 tools)
- Self-healing E2E: one deterministic invalid-params fixture against a reference server.
