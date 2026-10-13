# MCPMark ↔ mcpproxy integration (spec 103 US2)

This directory is the **integration guide**, not a copy of the suite. It holds three
things and nothing else:

| File | What it is |
|------|------------|
| `mcpproxy_arm.patch` | A reviewable diff against a SHA-pinned MCPMark checkout. One seam, mirrored twice. |
| `mcp_config.example.json` | An **example** mcpproxy config standing the whole suite fleet up at once. The real one is never committed. |
| `README.md` | This file: how to apply the patch, what it does and does not do, and the seams still open before a run can be published. |

**Why a patch and not a vendored suite.** Vendoring MCPMark would put ~140 task
definitions, their fixtures and their verifiers into this repository, where they
would drift from upstream silently and where "which version produced this number"
becomes unanswerable. A patch against a pinned SHA keeps the comparison honest:
the suite is exactly upstream plus a diff you can read in one sitting.

---

## 1. The pin (FR-028)

| | |
|---|---|
| Repository | <https://github.com/eval-sys/mcpmark> |
| Commit | `cd45b7f57923b9b3985467f5139927575f83141c` |
| Commit date | 2026-06-12 |
| License | Apache-2.0 |
| Task set | **MCPMark Verified** — the default at this commit |
| Python | `requires-python >= 3.11` |

**Report every figure as "MCPMark Verified @ cd45b7f5".** Upstream made Verified
the default in this very commit and states that results from earlier task
versions are deprecated and *not directly comparable*. A number quoted without
the task-set name is therefore not comparable to anything, including our own
earlier runs.

Task counts at this commit, counted as `description.md` files under `tasks/`:

| Service | Tasks | Credentials needed | Real writes |
|---------|------:|--------------------|-------------|
| filesystem | 40 | none | local only |
| postgres | 31 | local server | local only |
| github | 33 | PAT | **yes, remote** |
| notion | 38 | integration token | **yes, remote** |
| playwright | 4 | none | browser only |
| playwright_webarena | 31 | WebArena host | depends on host |

These differ from the counts in `specs/103-token-bench/research.md`
(filesystem 30, postgres 21, github 23, notion 28). Research counted the
pre-Verified task set; this table counts the pinned commit. **Use this table**,
and re-count after any re-pin — the suite grows.

---

## 2. Applying the patch

```bash
# 1. Clone and pin. Do this OUTSIDE this repository.
git clone https://github.com/eval-sys/mcpmark ~/bench/mcpmark
cd ~/bench/mcpmark
git checkout --detach cd45b7f57923b9b3985467f5139927575f83141c

# 2. Dry-run first. If this fails the pin has moved; re-pin deliberately,
#    do not force the patch.
git apply --check /path/to/mcpproxy-go/bench/mcpmark/mcpproxy_arm.patch

# 3. Apply.
git apply /path/to/mcpproxy-go/bench/mcpmark/mcpproxy_arm.patch

# 4. Confirm it is the only difference from the pin.
git diff --stat        # expect: 2 files changed, 132 insertions(+)

# To revert: git checkout -- src/agents/
```

Install the suite per its own README (it uses `uv`); this patch adds no
dependency — `MCPHttpServer` and the official MCP Python SDK streamable-HTTP
transport it wraps are already in the suite.

### What the patch changes

MCPMark builds its MCP server in exactly one place, and defines that place
twice: `_create_mcp_server()` in `src/agents/mcpmark_agent.py` and its mirror in
`src/agents/base_agent.py` (which `src/agents/react_agent.py` inherits). The
patch adds, to both:

* `_mcpproxy_arm_server()` — returns an `MCPHttpServer` pointed at mcpproxy, or
  `None`;
* two lines at the top of `_create_mcp_server()` that return it when it is not
  `None`.

`list_tools()` and `call_tool()` are the only calls the agent loop makes against
the server object, and `MCPHttpServer` satisfies both unchanged. Nothing in the
loop, the task set, the state managers or the verifiers is touched.

### Why the gate is an env var and not a fork

The FR-020 baseline arm is *the same agent running the same tasks with every
upstream tool loaded directly*. If the baseline ran unpatched code and the proxy
arms ran patched code, the patch would be a variable inside its own comparison.
With `MCPPROXY_MCP_URL` unset, `_mcpproxy_arm_server()` returns `None` and the
stock factory runs byte-for-byte as upstream — **both arms execute the same
file, and only the environment differs.**

The gate is the URL rather than a boolean because the URL is *also the cell
selector*. mcpproxy mounts one endpoint per routing mode permanently, at
startup, regardless of config (`internal/server/server.go`, "Routing mode
dedicated endpoints"), so the whole matrix crosses on one long-lived instance:

| Matrix cell | `MCPPROXY_MCP_URL` path | Config needed |
|---|---|---|
| `retrieve_full` | `/mcp/call` | `tool_response_mode: "full"` |
| `retrieve_compact` | `/mcp/call` | `tool_response_mode: "compact"` |
| `direct_full` | `/mcp/all` | `direct_tool_response_mode: "full"` |
| `direct_deferred` | `/mcp/all` | `direct_tool_response_mode: "deferred"` |
| `code_exec` | `/mcp/code` | `enable_code_execution: true` |
| `baseline` (FR-020) | *(unset)* | — the suite spawns the servers itself |

Both serialization settings hot-reload, so a cell change is a config apply, not
a restart. See `bench/modematrix.go` for the frozen cell ids and the seven
redundant combinations that are reported as skips.

### Environment

| Variable | Meaning |
|---|---|
| `MCPPROXY_MCP_URL` | **The gate.** e.g. `http://127.0.0.1:18080/mcp/call`. Unset ⇒ stock suite behaviour. |
| `MCPPROXY_API_KEY` | Sent as the `X-API-Key` **header**. Required when `require_mcp_auth: true`. |
| `MCPPROXY_MCP_TIMEOUT` | Seconds; defaults to the suite's own `DEFAULT_TIMEOUT` (600). |

Two details that are easy to get wrong:

* **Header, never `?apikey=`.** The query parameter is a Web-UI-only convenience
  on mcpproxy; using it writes the key into every access log and every recorded
  activity URL.
* **The suite's `MCPHttpServer` default timeout is 30 s and covers `initialize`,
  `list_tools` *and* every `call_tool`.** Behind a proxy, first contact also
  brings up N upstream servers (npx / pipx / docker cold starts), which
  routinely exceeds 30 s. A timeout there surfaces as a transport error that a
  naive classifier reads as a corrective retry — biasing the exact rates this
  benchmark exists to measure. The patch raises the default for that reason;
  do not lower it back.

---

## 3. The mcpproxy side — `mcp_config.example.json`

**This file is an EXAMPLE.** Copy it outside the repository, fill the
`REPLACE_ME…` placeholders, and point mcpproxy at the copy. The real file
carries credentials and machine-specific absolute paths and is **never
committed** — the same rule that keeps generated reports out of the tree
(SC-011) applies to its inputs.

```bash
cp bench/mcpmark/mcp_config.example.json ~/bench/mcpmark-proxy.json
$EDITOR ~/bench/mcpmark-proxy.json          # fill every REPLACE_ME
mcpproxy serve --config ~/bench/mcpmark-proxy.json --data-dir ~/bench/mcpmark-datadir
```

### Why all five services at once (T048)

Stock MCPMark connects to **one** server per run, so the per-task fleet is a
single service's toolset — far too small for any proxy mode to differ
measurably from loading everything inline. Configuring the whole fleet while
running one service's tasks is what puts the measurement in the regime the
proxy exists for, and it is *also the honest baseline*: the same agent, the same
tasks, all those tools loaded directly.

The upstream versions in the example are pinned to the exact ones the suite
would spawn itself (`@modelcontextprotocol/server-filesystem@2025.12.18`,
`postgres-mcp==0.3.0`, `ghcr.io/github/github-mcp-server:v0.15.0`,
`@notionhq/notion-mcp-server@1.9.1`, `@playwright/mcp@0.0.68`). Pinning them to
anything else would compare two different fleets.

### Config details that decide whether the run is valid

* **`quarantine_enabled: false` and `"quarantined": false` per server.** A
  quarantined server contributes **no callable tools**, so an unnoticed
  quarantine silently shrinks the fleet and inflates every saving. Check the
  tool count before believing a number.
* **Unknown keys are silently ignored.** mcpproxy's loader rejects malformed
  JSON but accepts unknown fields, so `tools_limitt: 15` costs you a run and no
  error. Copy the example; do not retype it.
* **`tools_limit` and `tool_response_limit` are pinned in the example** (15 /
  20000, the product defaults). They cap what `retrieve_tools` returns, so they
  are part of the measured configuration, not incidental.
* **`require_mcp_auth: true`** makes `/mcp` reject unauthenticated requests. It
  is off by default in the product for client compatibility; turn it on here so
  a stray local client cannot join the benchmark instance mid-run.
* **`telemetry.enabled: false`** — benchmark traffic is not product usage.
* **Record the fleet shape actually measured**, not the one configured. If
  GitHub or Notion fails to connect (bad token, no Docker), their tools are
  absent and the fleet is smaller. Every published percentage carries the fleet
  shape it was measured on (IC-004, SC-005).

### Credential-free core

`filesystem` + `postgres` + `playwright` need no third-party credentials. That is
the FR-027-clean starting point, and a smaller fleet — say so when reporting it.
GitHub and Notion perform **real writes against real accounts**: run them only
against a throwaway account with a narrowly scoped token, or not at all.

---

## 4. Open seam: per-task service state (read before scheduling a run)

**The transport patch does not close this, and it is not an oversight — it is
the piece that has to be decided before filesystem or postgres tasks can run
through the proxy at all.**

MCPMark rebinds its service configuration **per task**, and both credential-free
services do it:

* **filesystem** — `src/mcp_services/filesystem/filesystem_state_manager.py`
  copies the test environment into
  `<clone>/.mcpmark_backups/backup_<service>_<category>_<task>_<pid>` for each
  task, hands that path to the agent as `test_directory`, and deletes it
  afterwards. Stock MCPMark passes it straight into the filesystem server's
  argv, so the server's root *is* the per-task directory. Task instructions then
  speak in terms of "the test environment root".
* **postgres** — `src/mcp_services/postgres/postgres_state_manager.py`
  `get_service_config_for_agent()` returns a `current_database` created for that
  task, which stock MCPMark bakes into `DATABASE_URI`.

A statically configured mcpproxy fleet cannot follow either. Rooting the
proxied filesystem server at `.mcpmark_backups` (as the example config does, for
want of anything better) makes the *parent of every task directory* the root:
the agent no longer sees the task's own root at the top level, and it can see
other tasks' directories. That is fine for standing the fleet up and counting
tools; it is **not** a valid task run.

Three ways out, none of them free:

1. **Re-point per task over the REST API.** `PATCH /api/v1/servers/{id}`
   deep-merges (omitted keys preserved) and the config hot-reloads, so the
   patched factory — which runs once per task, after state setup, with
   `self.service_config` already refreshed — is the right seam to call it from:

   ```bash
   curl -sS -X PATCH http://127.0.0.1:18080/api/v1/servers/mcpmark_filesystem \
     -H "X-API-Key: $MCPPROXY_API_KEY" -H 'Content-Type: application/json' \
     -d '{"args":["-y","@modelcontextprotocol/server-filesystem@2025.12.18","<per-task dir>"]}'
   ```

   Cost: the upstream restarts and re-indexes between tasks, and the run has to
   wait for that. **Not yet exercised — treat the curl as the shape, not as a
   verified recipe.**
2. **Run the services that do not rebind.** GitHub and Notion carry static
   credentials, so their tasks need no per-task rebinding — but they perform
   real remote writes (FR-027) and need credentials, which is exactly what the
   credential-free core avoided.
3. **Measure the fleet, not the tasks, through the proxy.** Keep the full fleet
   configured for menu-cost measurement and run the *tasks* stock. This is
   coherent but it is no longer an agent loop through mcpproxy, so it cannot
   produce tokens-per-completed-task, and it is not what US2 asks for.

Whichever is chosen, say which one produced the numbers.

---

## 5. What this integration cannot give you

* **Cache-read accounting (FR-014).** The suite's per-task `meta.json` carries
  `execution_result.success`, `token_usage{input,output,total,reasoning}` and
  `turn_count` — and **no cache-read field**. That axis must come from the
  driver reading the provider's own `usage`, or be declared out of reach for
  suite-driven runs. The patch does not establish it.
* **A cross-source total.** Deterministic figures come from the tiktoken
  tokenizer; live figures come from provider-reported usage. They are never
  summed; a cross-source aggregate is *withheld with a stated reason*, the same
  rule `bench/live_report.go` already applies to withheld headlines.
* **A headline from one run.** Every model-dependent figure is an average over
  k ≥ 4 runs with its spread (FR-021).
* **A saving from a mode that completes less.** Lower cost with a lower
  completion rate is a regression, not a saving (FR-019, SC-007).

## 6. Status

The patch, the example config and this guide are complete and verified offline.
**No suite run has been executed and no model spend has been incurred** — T049
(run k ≥ 4 across ≥ 2 cells plus the baseline) is a separate task and is gated
on the pinned-model and spend-ceiling decision, and on §4 above.

### Verifying this directory without spending anything

```bash
SHA=cd45b7f57923b9b3985467f5139927575f83141c
git clone https://github.com/eval-sys/mcpmark /tmp/mcpmark && cd /tmp/mcpmark
git checkout --detach $SHA
git apply --check /path/to/bench/mcpmark/mcpproxy_arm.patch   # applies clean
git apply         /path/to/bench/mcpmark/mcpproxy_arm.patch
python3 -m py_compile src/agents/mcpmark_agent.py src/agents/base_agent.py

# the example config parses under the production loader
jq -e . /path/to/bench/mcpmark/mcp_config.example.json
mcpproxy doctor --config /path/to/bench/mcpmark/mcp_config.example.json
#   ⇒ "doctor requires running daemon" means the CONFIG LOADED;
#     "failed to load config file" means it did not.
```
