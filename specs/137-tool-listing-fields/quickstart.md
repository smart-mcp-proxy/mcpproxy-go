# Quickstart: verifying the tool listing fields

1. Build: `go build -o mcpproxy ./cmd/mcpproxy` and start on a scratch data dir and a high port (`reference_isolated_dev`).
2. Add the fixture upstream from PR-1's test helpers (an mcp-go server listing tools with `title`, two icons — one `https`, one `data:image/png` — `_meta`, `defer_loading: true`, `execution.taskSupport: "required"`). Approve it.
3. `curl -s -H "X-API-Key: $KEY" localhost:$PORT/api/v1/servers/fixture/tools | jq '.data.tools[0] | {title, icons, defer_loading, task_support, meta}'` shows every field.
4. Direct surface: list tools on `/mcp/direct` (or the configured direct endpoint) with any MCP client and compare `title`, `icons`, `_meta`, `defer_loading` with the fixture; `execution` is absent.
5. Title review, `trust_mode: manual`: change the fixture title; the direct listing shows no `title`; `mcpproxy tools list --server fixture -o json` shows `pending_title`; approve via the review page; the new title appears and an activity row `tool_title_approved` exists (`mcpproxy activity list --type tool_title_approved`).
6. Scan mode: set the title to an injection phrase from `detect_corpus_v1.json`; it stays withheld with findings.
7. Upgrade: start the new binary on a `config.db` from the previous release; `mcpproxy upstream list` shows the same approval counts as before.
8. Web UI: open the server's tools; the browser network log shows no request to the `https` icon host until "Load icon" is clicked.
