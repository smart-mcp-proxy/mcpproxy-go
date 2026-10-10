---
id: security-quarantine
title: Security Quarantine
sidebar_label: Security Quarantine
sidebar_position: 4
description: Protect against Tool Poisoning Attacks with automatic server quarantine
keywords: [security, quarantine, tpa, tool poisoning]
---

# Security Quarantine

MCPProxy includes an automatic quarantine system to protect against Tool Poisoning Attacks (TPA).

## What is a Tool Poisoning Attack?

Tool Poisoning Attacks occur when malicious MCP servers:

1. **Hidden Instructions**: Embed malicious instructions in tool descriptions that AI agents might follow
2. **Data Exfiltration**: Trick AI agents into sending sensitive data to external servers
3. **Credential Theft**: Attempt to extract API keys or tokens
4. **System Manipulation**: Try to execute unauthorized commands

## How Quarantine Works

### Automatic Quarantine

When a new server is added via an AI client (using the `upstream_servers` tool):

1. Server is automatically placed in **quarantine status**
2. Tool calls to quarantined servers return a **security analysis** instead of executing
3. Server remains quarantined until **manually approved**

### Servers added by hand-editing `mcp_config.json`

Since issue #937, the same admission gate applies to servers written directly
into the configuration file — hand-editing is a normal workflow, and config
files get shared, templated and copied between machines, so an entry from a
config edit is admitted on exactly the same terms as one from
`mcpproxy upstream add`.

A config-file server is held for review when **both** of the following are true:

- the entry does **not** contain a `quarantined` key (writing `"quarantined": false`
  is an explicit operator statement and is obeyed), **and**
- the server is not yet recorded in `config.db`.

Quarantine must also be enabled (`quarantine_enabled`, on by default), and the
server's [trust mode](#trust-modes-auto--scan--manual) must not be `auto`. A
server with no `trust_mode` is `manual` unless a legacy setting resolves to
`auto` (`"auto_approve_tool_changes": true`, or the older
`"skip_quarantine": true`), so by default a first-seen config-file server is
held; a server whose trust mode is `auto` is admitted.

The second condition is what makes upgrading safe: every server you are already
running has a `config.db` record, so **upgrading never re-quarantines a server
you have already vetted**. "Vetted" means the record is backed by an approval
baseline — at least one of the server's tools was approved at some point, which
happens automatically when a trusted server first connects. A server recorded
as unquarantined whose tools were never approved was never admitted, and the
gate holds it for review (see the restart note below).

Because the rule cannot tell such a server apart from a vetted one that has no
tool records, a few vetted servers are also held once on upgrade: servers that
expose no tools (prompts or resources only), servers that never connected while
live (for example, disabled ever since they were vetted), and servers last
vetted before v0.21. Approve them from the quarantine review; the decision is
recorded and they are not held again. To skip the review, add
`"quarantined": false` to the server's entry before upgrading. The boundary is that a server present in a
hand-written config but absent from `config.db` — after a wiped data directory,
or on a machine that has never seen that config before — is treated as
first-seen and held for review.

The decision is durable: once recorded, a quarantine is not reverted by a config
file that never mentions the key. Un-quarantining stays a user action, and
writes both the config file and `config.db`.

No TPA scan runs at config-load admission; quarantine-by-default already keeps
the tools away from agents, and the scan is what the human review step performs.

If mcpproxy writes the configuration file itself (any API apply, a quarantine
toggle, an `upstream add`), it does **not** stamp `"quarantined": false` onto
servers that never stated it — a value mcpproxy invented would otherwise read
back as an operator statement and disable the gate for that server.

#### Upgrading from a release affected by #937 — action required

The gate treats "present in `config.db`" as "has already been through
admission". That is what keeps upgrades safe, but it also means an install that
was **already** hit by #937 is not remediated by upgrading: the buggy admission
left exactly the `config.db` record the gate now reads as vetted, so a server
that was admitted unquarantined stays live.

At startup mcpproxy logs a warning naming any server that looks like this —
configured, running unquarantined, never explicitly reviewed, and with a
`trust_mode` that would have held it:

```
Configured servers predate the config-load admission gate and have never been
explicitly reviewed  servers=["suspicious-server"]
```

For each server named, either:

- review it and record the decision — quarantining and then releasing it from
  the quarantine UI writes an explicit `"quarantined"` value to
  `mcp_config.json`, which silences the warning; or
- write the decision by hand: add `"quarantined": true` (hold it) or
  `"quarantined": false` (you have vetted it) to that server's entry.

Adding the key by hand is enough — the gate obeys an explicit value either way.

**Restarts also cleared the quarantine.** In the same affected releases,
restarting a server that the gate had quarantined silently cleared its
quarantine: a restart from the REST API, the CLI, the tray or an MCP client,
"restart all", a secret change that restarts the servers using it, and a security
scan (including the baseline scan that runs shortly after startup) all re-read the
server from `mcp_config.json` and wrote the un-gated entry over the recorded
quarantine. Upgrading now catches this case: such a server has no approved tool,
so the gate quarantines it again at startup and logs `Quarantining known server
with no approved tool baseline`; approve it from the quarantine review as usual.
A server that ran unquarantined (and so has approved tools) still only shows up
in the "predate the config-load admission gate" warning. From this fix on, a restart
runs the file entry through the admission gate, and `config.db` refuses to lower a
recorded quarantine unless the operator states `"quarantined": false` or the
server is released from the quarantine review.

### Tool Discovery and Search Isolation

**Quarantined servers are completely isolated from the tool discovery and search system:**

| Feature | Quarantined Server | Approved Server |
|---------|-------------------|-----------------|
| Tools indexed | ❌ No | ✅ Yes |
| Tools searchable via `retrieve_tools` | ❌ No | ✅ Yes |
| Tools appear in HTTP API search | ❌ No | ✅ Yes |
| Tool calls allowed | ❌ No (returns security analysis) | ✅ Yes |

This isolation prevents Tool Poisoning Attacks from:
- **Injecting malicious descriptions** into search results that AI agents might read and follow
- **Appearing in tool recommendations** where they could be mistakenly selected
- **Influencing AI agent behavior** through carefully crafted tool metadata

When a server is quarantined:
1. Its tools are **immediately removed** from the search index
2. `retrieve_tools` queries will **never return** tools from that server
3. The server remains visible in the server list (marked as quarantined) for management

When a server is unquarantined (approved):
1. The server connects to discover its tools
2. Tools are **indexed and become searchable**
3. Tool calls are allowed to execute normally
4. Any **pending** (newly-discovered, never-reviewed) tool-approval records for
   the server are **auto-promoted to approved** — approving a server means you
   trust its current tool snapshot (baseline trust). Tools whose description or
   schema later **changes** (`changed`, i.e. rug-pull) are *not* affected and
   stay blocked until you re-approve them explicitly.

### Security Analysis

When a tool from a quarantined server is called, MCPProxy **blocks the call** and
returns a structured security response instead of invoking it — so the tool's
description can be reviewed before it ever runs:

```json
{
  "status": "QUARANTINED_SERVER_BLOCKED",
  "serverName": "suspicious-server",
  "toolName": "fetch_data",
  "message": "🔒 SECURITY BLOCK: Server 'suspicious-server' is currently in quarantine for security review. Tool calls are blocked to prevent potential Tool Poisoning Attacks (TPAs).",
  "instructions": "To use tools from this server, please: 1) Review the server and its tools for malicious content, 2) Use the 'upstream_servers' tool with operation 'list_quarantined' to inspect tools, 3) remove from quarantine if verified safe",
  "toolAnalysis": {
    "name": "fetch_data",
    "description": "…",
    "inputSchema": { "…": "…" },
    "serverName": "suspicious-server",
    "analysis": "SECURITY ANALYSIS: This tool is from a quarantined server. Please carefully review the description and input schema for potential hidden instructions, embedded prompts, or suspicious behavior patterns."
  }
}
```

The actual pattern **detection** — hidden-Unicode smuggling, cross-server
shadowing, decoded shell payloads, injection/exfiltration phrases, and embedded
secrets — is performed by the deterministic offline detect engine that backs the
built-in `tpa-descriptions` scanner. Its findings appear in the scan report
(`mcpproxy security report <server>`), each carrying a `rule_id`, `severity`,
`threat_level`, `confidence`, and the contributing check `signals`. See
[Tool Scanner](/features/tool-scanner) for the full rule reference.

## Prompt rug-pull baseline (spec 100)

When upstream **prompt aggregation** is enabled (`aggregate_upstream_prompts: true`, off by default), mcpproxy keeps a per-prompt approval baseline that mirrors the tool rug-pull machinery. A trusted server that ships a benign prompt and later mutates its **advertised metadata** (name, description, or argument descriptions) has that changed prompt **withheld from `prompts/list`** until it is approved — closing the gap where a subtler injection that passes the poison scanner could still slip in via a later edit.

- **Scope — metadata only.** The baseline hashes the prompt's advertised metadata, not its `prompts/get` message content (which is materialised fresh per call and has no list-time artifact to baseline). Content is defended separately by output sanitisation and size caps. This is an inherent limit, not a shortcut.
- **Enforcement is by withholding.** A held prompt is simply not registered, so it is absent from `prompts/list` and `prompts/get` on it fails natively. There is no separate runtime gate.
- **Trust parity.** A server with `trust_mode: auto` (or `auto_approve_tool_changes: true`) auto-approves its prompt changes; `manual` holds them. Disabling quarantine globally (`quarantine_enabled: false`) auto-approves.

Manage held prompts with the `quarantine_security` MCP tool:

```jsonc
// see what is held (all servers, or one via "name")
{ "operation": "inspect_prompts", "name": "github" }
// approve one held prompt, or all for a server
{ "operation": "approve_prompt", "name": "github", "prompt_name": "summarize_pr" }
{ "operation": "approve_all_prompts", "name": "github" }
```

## Managing Quarantine

## Review verbs and where to find them {#review-verbs}

Every surface offers the same four decisions. Only the scan-gated approval can release a quarantined server; none of the first-party surfaces uses the legacy `POST /unquarantine` endpoint.

| Verb | REST |
|------|------|
| Approve server | `POST /api/v1/servers/{id}/security/approve`. Scan-gated; `force` only after the dangerous-verdict confirmation; optional `block: [tools]` keeps tools disabled |
| Reject server | `POST /api/v1/servers/{id}/security/reject` |
| Approve tool | `POST /api/v1/servers/{id}/tools/approve` |
| Reject tool | `POST /api/v1/servers/{id}/tools/block` |

| Surface | Where |
|---------|-------|
| Web UI | The **Review queue** (`/review`) and one server's review screen (`/review/<server>`, also the **Review** tab of the server detail page). The Tools page links each new or changed tool to it, and its "Needs review" count opens the queue |
| macOS app | Sidebar **Review Queue** and its review sheet. The tray item "Review Queue… (N)" and the Needs Attention rows open the sheet |
| CLI | `mcpproxy review list`, `show`, `approve`, `reject` ([Review Commands](/cli/review-commands)) |
| Tray on Windows and Linux (Go tray) | A server in the "Security Quarantine" submenu opens the Web UI at `/review/<name>` |
| MCP | `quarantine_security` with `list_quarantined`, `inspect_quarantined`, `inspect_tools`, `approve_tool`, `approve_all_tools`, `block_tool`, `block_all_tools` (admin only). There is no server-level approve over MCP by design: an agent cannot release a quarantined server |

### Scan coverage on the review screen

The scan line of the review screen says whether the baseline scan describes the definitions you are looking at:

| Coverage | What the screen shows |
|----------|-----------------------|
| current | `Baseline scan: clean · risk 0/100 · covers all 5 tools`, in the colour of the verdict. The risk score appears only here |
| stale | A warning: a tool definition changed or was added after the last scan. It names the tools and the last result, and offers **Rescan**. A rug pull after the scan therefore never reads as clean |
| not captured | A warning that the scan was not checked against tool definitions, with **Fetch tool definitions** (CLI: `mcpproxy review fetch <server>`) |
| no tools scanned | A warning that the last scan did not analyse tool definitions, with **Rescan** |
| scanning | `Scan in progress…` |
| none | `Not scanned yet.` with **Scan now** |

Each tool's scan verdict follows the same rule: `clean` only when the scan covered that tool's current definition. After a baseline scan has listed a quarantined server's tools, MCPProxy captures the definitions itself, so the review list is not empty until someone clicks **Fetch tool definitions** or runs `mcpproxy review fetch <server>`. With `security.auto_baseline_scan: false` and no manual scan nothing is started automatically.

Coverage is bound to the definition itself, not only to timing. When a scan exports a tool's definition it records a SHA-256 digest of the description and input schema (`tool_hashes` in the scan context). A tool counts as covered only if that digest equals the digest of its current definition, so a benign definition that was scanned and then swapped for a poisoned one inside the same timing window reads as stale. Scans stored before this field existed carry no digests and keep the earlier name-and-timing rules. Approval still re-checks the definition independently; this only keeps the review display honest.

On a server that is not quarantined, the review tab shows approved state: approved tools read **Approved** or **Blocked** (no Approve or Reject), the heading says the server is approved, and **Manage tools** and **Quarantine to review again…** are offered. The second is the existing quarantine action behind a confirmation. Only a new or changed tool shows Approve and Reject.

### Scan a Server for TPAs (MCP)

The `quarantine_security` tool can also run and read the TPA scan, so an agent
reviewing a held server does not have to leave for the CLI or web UI:

```jsonc
// run the offline baseline scan (in-process, no Docker required)
{ "operation": "scan_server", "name": "github" }
// read the latest verdict + findings
{ "operation": "get_scan_report", "name": "github" }
```

`scan_server` answers with the verdict when the scan settles quickly, otherwise
with the job id and `"status": "scan started"` — poll `get_scan_report` for the
result. Every `list_quarantined`, `inspect_quarantined` and `inspect_tools`
response also carries a one-line `scan_status`, so a server nobody ever scanned
reads as `never scanned — run scan_server first` instead of looking clean.

The optional Docker-based deep scanners are a separate layer: they run only when
[deep scan](/features/security-scanner-plugins) is enabled, and when they are
unavailable they are skipped without changing the baseline verdict.

### View Quarantined Servers

**Web UI:**
1. Open **Review queue** in the Protect section.
2. Select the server that is waiting for review.
3. Read the server-provided tool definitions, scan result, tier counts, and
   changed-definition diff before making a decision. Definitions are displayed
   as plain text because their content is not trusted.

The queue also retains recent scan history. Old `/security` links redirect to
the queue; a specific report remains available at `/security/scans/:jobId`.
Optional scanner setup lives at **Settings → Security → Scanners**.

**CLI:**
```bash
mcpproxy review list
# One row per server that needs review: quarantined servers and trusted
# servers with new or changed tools

mcpproxy review show github [--full]
# Captured tool definitions, tiers and scan verdicts for one server
```

### Approve a Server

**Web UI:**
1. Select the quarantined server from **Review queue**.
2. Choose the tools to allow. Only read-only tools whose scan is clean start
   checked; write, destructive, unannotated, not-scanned and held tools start
   unchecked. Unselected tools are submitted as explicit blocks with the
   approval decision and stay blocked until you enable them on the Tools tab.
3. Choose **Approve server**; the button names the exact count (for example
   "Approve server (3 of 9 tools)"). **Approve all** is a separate action that
   allows every pending or changed tool (tools you blocked earlier on a
   re-quarantined server stay blocked). If no tool definitions have been captured, the button
   reads "Approve without seeing tools" and the UI asks for a separate
   confirmation before a blind approval can proceed.

The review controls are deliberate: **Fetch tool definitions** (CLI: `mcpproxy review fetch`) uses the
inspection-only `discover-tools` capture to store current upstream metadata
without indexing it, **Allow this tool** selects an individual tool for
the server decision, **Approve server** releases only the selected tools, and
**Reject server** keeps the server quarantined. Existing trusted servers use
the same screen to approve or reject a changed tool definition.

**API:**
```bash
curl -X POST \
  -H "X-API-Key: your-key" \
  http://127.0.0.1:8080/api/v1/servers/server-name/discover-tools

# The capture action reports success only. Read the stored, redacted review
# payload before deciding which tools to block.
curl -H "X-API-Key: your-key" \
  http://127.0.0.1:8080/api/v1/servers/server-name/review

# Submit the scan-gated approval. Omit block[] or list only the tools to keep
# disabled; direct unquarantine is legacy-only.
curl -X POST -H "X-API-Key: your-key" -H "Content-Type: application/json" \
  -d '{"block":["tool-to-keep-disabled"]}' \
  http://127.0.0.1:8080/api/v1/servers/server-name/security/approve
```

**CLI:**
```bash
# Quarantined server: scan-gated approval. By default only read-only tools with
# a clean scan are allowed; --all allows every tool, --tools a,b exactly those,
# --except keeps more tools disabled
mcpproxy review approve github [--all | --tools a,b] [--except a,b] [--force] [--yes]

# Trusted server: approve only the listed new or changed tools
mcpproxy review approve github --tools create_issue
```

See [Review Commands](/cli/review-commands) for every flag.

**Configuration:**

Leave a new server quarantined until the review flow has captured its tool
definitions and the scan-gated approval is complete. Do not edit
`quarantined: false` into the configuration file as an approval shortcut:
that bypasses the informed-review record and its baseline check.

### Re-quarantine a Server

If you need to quarantine a previously approved server:

```bash
curl -X POST \
  -H "X-API-Key: your-key" \
  http://127.0.0.1:8080/api/v1/servers/server-name/quarantine
```

## Security Checklist

Before approving a server, verify:

- [ ] **Source**: Is the server from a trusted source?
- [ ] **Code Review**: Have you reviewed the server's code?
- [ ] **Tool Descriptions**: Do tool descriptions look legitimate?
- [ ] **Network Access**: Does the server need network access?
- [ ] **Permissions**: Are requested permissions appropriate?

## Detection Patterns

Tool-description analysis is performed by the deterministic, fully-offline
**detect engine** that backs the built-in `tpa-descriptions` scanner. It runs
**seven checks across two tiers** — four **hard** checks that auto-quarantine and
block approval, and three **soft** checks that raise a human-review item:

| Check | Tier | Catches |
|-------|------|---------|
| `unicode.hidden` | hard | Zero-width / bidi / TAG-block / PUA character smuggling |
| `shadowing.cross_server` | hard | Impersonation clone (same name + near-duplicate description on another server) or exclusive cross-server reference |
| `payload.decoded` | hard | base64/hex blob that decodes to a shell/exfil command |
| `phrase.injection` | hard | Curated instruction-override / exfiltration directives |
| `directive.imperative` | soft | Injection directives, secrecy imperatives, instruction overrides |
| `capability.mismatch` | soft | Compute/string tool touching `~/.ssh` etc.; unexplained data-sink param |
| `secret.embedded` | soft | Hardcoded live credential (confidence-scored, placeholders dropped) |

Each check is deterministic and reliability is enforced by a CI eval gate. See
[Tool Scanner](/features/tool-scanner) for the full rule reference, the two-tier
model, normalization, and the eval gate.

## Best Practices

1. **Review All Servers**: Never auto-approve servers added by AI agents
2. **Source Verification**: Only approve servers from known, trusted sources
3. **Minimal Permissions**: Prefer servers with limited, specific capabilities
4. **Regular Audits**: Periodically review approved servers
5. **Network Isolation**: Use Docker isolation with `network_mode: "none"` for untrusted servers

## Tool-Level Quarantine

In addition to server-level quarantine, MCPProxy provides **tool-level quarantine** that detects changes to individual tool descriptions and schemas using SHA256 hashing. This protects against "rug pull" attacks where a previously trusted server silently modifies tool behavior.

See [Tool Quarantine](./tool-quarantine.md) for complete documentation on:
- SHA256 hash-based tool approval
- CLI commands: `mcpproxy upstream inspect` and `mcpproxy upstream approve`
- Configuration: `quarantine_enabled` (global) and `auto_approve_tool_changes` (per-server; deprecates `skip_quarantine`)
- REST API endpoints for tool approval management

### Trust modes (auto | scan | manual)

Since spec 086, each server carries a **trust mode** that governs both
new-server admission and tool-change approval (superseding the binary
`auto_approve_tool_changes` flag, which is migrated onto it automatically):

| Mode | Add time | Tool changes |
|------|----------|--------------|
| `auto` | Admitted without quarantine or scanning | Trusted without scanning (rug-pull risk) |
| `scan` | Quarantined, then a fail-closed automatic TPA scan admits it on a clean verdict | Auto-approved only when the offline scan verdict is clean; otherwise held for review |
| `manual` (default) | Quarantined for human review | Every change held for review |

Config field: per-server `trust_mode`; REST: `trust_mode` on
`POST/PATCH/GET /api/v1/servers`; CLI: `mcpproxy upstream add --trust-mode`.

**Unrecognized values are rejected, not guessed.** The values are
case-sensitive (`Scan` is not `scan`). A bogus value is refused with a `400`
naming the accepted vocabulary on `POST`/`PATCH /api/v1/servers`, by the
`upstream_servers` MCP tool, and by `--trust-mode` / `upstream add-json` —
every seam where a value is being *written*.

**Loading is different: it normalizes instead of failing.** A bogus
`trust_mode` already sitting in `mcp_config.json` (older releases persisted
whatever the API was handed) is rewritten to `manual` at load time with a
`WARN` on stderr naming the server and the offending value. This is the tier
the runtime already applied to it, so nothing about the decision changes — but
the daemon still starts and hot-reloads instead of refusing to boot on a file
it cannot fix itself. `POST /api/v1/config/apply` normalizes the same way, so
a full-config apply is never blocked by a legacy value it did not introduce.
Should an unvalidated value ever reach the runtime anyway, resolution still
fails closed to `manual`.

### Automatic informational baseline scan

Independently of `trust_mode`, MCPProxy runs the free in-process Pass-1 TPA scan
so every server ends up with a security verdict instead of an empty badge:

- **On admission** — a newly added, enabled server gets one baseline scan. Servers
  with `trust_mode: "scan"` are skipped entirely: that mode's own admission gate
  scans them, and routing an informational verdict into its settle-driven
  auto-approval could change quarantine state.
- **Once per installation** — a background sweep at startup scans enabled servers
  that have never been scanned (for installs that predate this behaviour). It is
  serialized, never delays startup, is cancelled on shutdown, and a persisted
  marker keeps it one-shot.

These scans are **informational**: the verdict fills in the scan summary and the
UI badge and never quarantines, approves, or blocks anything. Disable with
`security.auto_baseline_scan: false` (env: `MCPPROXY_AUTO_BASELINE_SCAN`).
The `trust_mode: "scan"` gate above is a separate path and is unaffected.

### Signature bundle (offline TPA corpus)

The `scan` mode runs an offline TPA signature corpus (the tpa-db
`scanner-bundle.json`). By default it is the corpus embedded in the build; set
`security.tpa_bundle_path` in `mcp_config.json` (env override:
`MCPPROXY_TPA_BUNDLE_PATH`, which wins over the file value on every path —
loader, hot-reload, `/api/v1/config/apply`) to run a corpus from disk instead.
The path is re-read on config hot-reload, so refreshing signatures needs no
restart, and it is honoured in **every transport**, stdio included.

A configured bundle that cannot be read, parsed, version-checked, or compiled
is **refused**: the previously active corpus stays live (fail-closed, never
fail-empty) and the reason is logged and reported as `load_error` below.
A bundle that parses but contributes **zero runnable rules** — an empty
`rules` array, or rules that are all non-runnable offline — counts as a load
failure for exactly the same reason: an empty corpus would leave the `scan`
gate reporting full coverage while nothing at all was being matched.

Which corpus is live is visible in:

- `mcpproxy security overview` — source, version, freshness stamp, fingerprint,
  and the runnable / skipped / declared-skipped rule split;
- `GET /api/v1/security/overview` → `signature_bundle`;
- the Web UI **Security** tab ("Signatures (runnable)" stat).

The "Add time" column applies to every admission path — the `upstream_servers`
tool, the REST API, a registry add, and (since issue #937) a first-seen server
in `mcp_config.json`. See
[Servers added by hand-editing `mcp_config.json`](#servers-added-by-hand-editing-mcp_configjson).

**Web UI (spec 088)**: the server's Configuration tab has a tri-mode
selector (choosing `auto` asks for confirmation and explains the risk); the
add-server form chooses the mode instead of a raw quarantine checkbox
(initial quarantine is derived from the mode); server tiles show a
trust-mode badge.

### Hold evidence in the Web UI (spec 088)

When the `scan` gate holds a tool, the approval record carries evidence —
`held_reason` (`scan_findings` = the scan found threats vs `scan_coverage`
= the scan could not complete, held as a precaution), `held_verdict`, and
`held_signals` (matched check ids, with known-attack `TPA-YYYY-NNNN`
signature ids listed first). The Web UI renders this on the tool-quarantine
panel, the change-diff dialog, and the global Tools page, with a best-effort
link into the server's latest scan report highlighting matching findings.
The quarantine banner distinguishes four states: scan running, scan verdict
blocked automatic approval, scan could not complete (retry offered), and
awaiting manual review.

### Block (approve + disable)

When reviewing a pending or changed tool you may want to **acknowledge it but
keep it hidden** from MCP clients — for example, dismissing a noisy "changed"
flag for a tool you never intend to use. The **block** operation does this
atomically: it approves the tool (clearing the quarantine flag) **and** disables
it in a single, all-or-nothing server-side write, so a tool is never left in the
approved+enabled state.

- **REST**: `POST /api/v1/servers/{id}/tools/block` with `{"tools":[...]}` or
  `{"block_all": true}`.
- **MCP**: `quarantine_security` operations `block_tool` (with `name` +
  `tool_name`) and `block_all_tools` (with `name`).

A blocked tool can be re-exposed later with the normal enable operation
(`POST /api/v1/servers/{id}/tools/{tool}/enabled` with `{"enabled": true}`).

### Namespaced tool names

A tool's identity is the exact name its server reports in `tools/list`, colons
included. `erase` and `ns:erase` on the same server are two tools, and each has
its own approval record, index entry and callability — a record for `erase`
never approves `ns:erase`, and vice versa. Every gate (dispatch through
`call_tool_*`, direct-name dispatch on `/mcp/all`, `call_tool()` inside code
execution, preflight and `describe_tool`) looks the record up under the exact
name it will dispatch.

Releases before this rule filed a colon-named tool under the text after its
first colon, so `ns:erase` shared the record `erase`. On the first discovery
after upgrading, a server that has a baseline (any approved tool) and serves
colon-named tools therefore sees those tools **become pending once, under
their own names** on `manual` and `scan` trust — review them with
`mcpproxy upstream inspect <server>` and approve them with
`mcpproxy upstream approve <server> <tool>`, the `quarantine_security` MCP
tool, or the Web UI. `trust_mode: auto` servers (and installs with
`quarantine_enabled: false`) auto-approve them. Nothing is deleted: the old
collapsed record stays with the bare name it stores.

Two things about the old record carry over so an upgrade never silently
widens access:

- A **user block** (a `Disabled` toggle, `disable_all`, `block_all`) on the
  collapsed record is copied onto the namespaced tool's new record, and the
  log says so at `WARN` naming both keys. The namespaced tool stays hidden
  until you enable it under its own name — a tool you had blocked before the
  upgrade is still blocked after it. A **quarantine lock** (`pending` /
  `changed`) on the collapsed record is adopted onto the new record *with its
  evidence* (the before/after definition of a rug pull, the scan verdict of
  a held tool) on `manual` and `scan` trust — including the first discovery
  of a server that has no baseline yet, where a brand-new tool would be
  auto-baselined: a tool the old release held for review stays held until
  you approve it by its own name, and the review shows what the old release
  had flagged rather than "new tool". The lock is never turned into a block:
  approving the tool by its name is all it takes (no second enable toggle).
  Under `trust_mode: auto` or `quarantine_enabled: false` the old lock never
  bound and the tool auto-approves as before.
- A namespaced tool you had **toggled** in the UI before the upgrade already
  has a record under its exact name — one that the toggle created without an
  approved contract hash. Such a record carries no approval decision of its
  own, so on the first discovery after upgrade it takes its decision from the
  collapsed record: a `pending` / `changed` lock there (with its before/after
  evidence) is adopted and the tool stays held for review under its own name;
  an approval there counts only if the definition it approved is the one the
  server reports now — then the exact record is baselined and change
  detection works from then on — while a differing definition is held as
  `changed` with the approved one as the before-evidence, exactly as the
  collapsed record itself would have been (a tool that changed while it had
  no live baseline is a potential rug pull, not a new baseline). A user
  block on the collapsed record rides along on every one of those outcomes.
  With no collapsed record at all the tool is pending under its own name on
  `manual` and `scan` trust (a green scan approves it on `scan`), and
  baselined on `trust_mode: auto` or with `quarantine_enabled: false`.
  Nothing you toggled is ever approved for a definition nobody reviewed.

The same rule now applies to the toggle itself: disabling or re-enabling a
tool that has **no approval record yet** (a tool discovery listed but never
filed) creates a `pending` record while the quarantine gate is active for its
server — the toggle records visibility, it does not approve — so a disable →
enable round trip leaves the tool pending until you approve it by name.
Under `trust_mode: auto` or `quarantine_enabled: false` the record is
approved and baselined to the definition the server currently reports.

Only records written by an older release are consulted this way. Every record
this release writes is stamped as identity-keyed, and the first discovery pass
after upgrade stamps every remaining record the server holds — including
collapsed records for tools the server no longer lists — so the migration
runs exactly once per server: a genuine sibling `erase` you disable later
never affects a new `v2:erase`. The one exception is an old record that still
*restricts* — user-disabled, `pending` or `changed` — for a tool the pass did
not list (the tool was not served on the first pass after upgrade, or an
authoritative empty refresh dropped it). Such a record is a decision you or
the old release made about a tool that is merely absent, so it is left
unstamped and waits: when the tool reappears, the pass that files its exact
record consults the old record once — the block or lock carries over exactly
as above — and stamps it then.

## Disabling Quarantine

**Not recommended**, but you can opt out of quarantine globally by setting a
single top-level flag in `~/.mcpproxy/mcp_config.json`:

```json
{
  "quarantine_enabled": false
}
```

When `quarantine_enabled` is `false`:

- Servers added dynamically via the `upstream_servers` MCP tool or the
  `POST /api/v1/servers` REST endpoint default to **not quarantined**.
- Tool-level quarantine (per-tool SHA-256 approval of descriptions and
  schemas, see [Tool Quarantine](./tool-quarantine.md)) is skipped.

An explicit `quarantined` field in an add-server request still wins over
the default, so client code can always override on a per-server basis.
Per-server `auto_approve_tool_changes: true` auto-approves all post-baseline tool changes and additions for that server (the deprecated `skip_quarantine: true` is migrated onto it automatically).

Warning: Disabling quarantine exposes your system to Tool Poisoning
Attacks. Only do this on machines where every MCP server you connect to
is already trusted.
