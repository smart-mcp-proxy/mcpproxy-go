---
id: security-commands
title: Security Scanner Commands
sidebar_label: Security Commands
sidebar_position: 6
description: CLI commands for managing security scanners, scanning upstream MCP servers, and reviewing findings
keywords: [security, scanner, scan, approve, quarantine, sarif, cli]
---

# Security Scanner Commands

The `mcpproxy security` command group manages the Security Scanner Plugin System (Spec 039).
It covers three responsibilities:

1. **Scanner lifecycle** — list, enable, disable, and configure Docker-based scanner plugins.
2. **Scan operations** — run scanners against quarantined upstream servers, track progress, and view reports.
3. **Approval workflow** — approve a server (unquarantine + index its tools) or reject and clean up, backed by an integrity baseline.

For background on the architecture, scanner images, and storage see:

- [Security Scanner Plugin System](/features/security-scanner-plugins)
- [Scanner Images](/features/scanner-images)
- [Quarantine System](/features/security-quarantine)

## Overview

```
mcpproxy security
├── scanners                    List scanners from the registry and their status
├── enable <scanner-id>         Enable a scanner (pulls its Docker image)
├── disable <scanner-id>        Disable a scanner (cleans up the image)
├── configure <scanner-id>      Set scanner env vars (e.g. API tokens)
├── scan <server>               Run scanners on a server; --all for every server
├── scan --dry-run <server>     Print a plan without running containers
├── rescan <server>             Re-run scanners on a server
├── status <server>             Show current scan state + per-scanner stderr
├── report <server>             Aggregated findings report (table / json / yaml / sarif)
├── approve <server>            Unquarantine + index tools + save integrity baseline
├── reject <server>             Delete scan artifacts + keep server quarantined
├── integrity <server>          Verify server against its approved baseline
├── overview                    Dashboard totals (scanners installed, last scan, etc.)
└── cancel-all                  Cancel an in-progress batch scan
```

:::tip Global flags apply
Every `mcpproxy security …` subcommand honors the global flags `--config` / `-c`, `--data-dir` / `-d`, `--output` / `-o`, `--json`, `--log-level`, `--log-to-file`, and `--log-dir`.
In particular, **`-o json` (or `--json`)** switches the table output to a structured JSON payload that's safe to feed into `jq`.
:::

## Prerequisites

- **Docker** must be installed and reachable. Every scanner runs as a Docker container.
  Run `mcpproxy doctor` if you're unsure.
- **Network access** to pull scanner images from `ghcr.io/smart-mcp-proxy/*` and vendor registries (see [Scanner Images](/features/scanner-images)).
- For some scanners, a **third-party API token** — surfaced via `required_env` on the scanner record. The only scanner that currently refuses to run without an explicit token is `mcp-scan` (Snyk), which needs `SNYK_TOKEN`.

## Output Formats

All subcommands that return data support the standard formats:

| Flag | Description |
|------|-------------|
| `-o table` | Human-readable text (default) |
| `-o json`  | Canonical JSON shape, stable across releases |
| `-o yaml`  | Same data as JSON, rendered as YAML |
| `-o sarif` | Raw SARIF 2.1.0 (only `security report`) |

You can also set `MCPPROXY_OUTPUT=json` in the environment to change the default for the whole shell session.

---

## security scanners

List every scanner in the bundled registry along with its current runtime status.

### Usage

```bash
mcpproxy security scanners [flags]
```

### Flags

(none beyond global flags)

### Status vocabulary

The status column is consistent between table and JSON output:

| Status | Meaning |
|--------|---------|
| `available`  | Known to the registry, Docker image not pulled yet |
| `pulling`    | Background pull in progress after `enable` |
| `installed`  | Docker image cached locally, no extra env needed |
| `configured` | Installed AND the user has set one or more env vars via `configure` |
| `error`      | The last operation on this scanner failed — see the error message |

### Examples

```bash
# Human-readable table
mcpproxy security scanners

# JSON for scripting
mcpproxy security scanners -o json | jq '.[] | {id, status, required_env}'
```

**Sample table output:**

```
ID                   NAME                     VENDOR                  STATUS       INPUTS
-------------------------------------------------------------------------------------------------------
cisco-mcp-scanner    Cisco MCP Scanner        Cisco AI Defense        installed    source
mcp-ai-scanner       MCP AI Scanner           MCPProxy                installed    source
mcp-scan             Snyk Agent Scan          Snyk (Invariant Labs)   configured   source
nova-proximity       Nova Proximity           MCPProxy                installed    source
ramparts             Ramparts MCP Scanner     Javelin                 installed    source
semgrep-mcp          Semgrep MCP Rules        Semgrep                 installed    source
trivy-mcp            Trivy Vulnerability...   Aqua Security           installed    source, container_image
```

**Sample JSON fields:**

```jsonc
{
  "id": "mcp-scan",
  "name": "Snyk Agent Scan",
  "vendor": "Snyk (Invariant Labs)",
  "docker_image": "ghcr.io/smart-mcp-proxy/scanner-snyk:latest",
  "status": "configured",
  "inputs": ["source"],
  "outputs": ["sarif"],
  "timeout": "120s",
  "network_required": true,
  "required_env": [
    {"key": "SNYK_TOKEN", "label": "Snyk API Token", "secret": true}
  ],
  "optional_env": null,
  "installed_at": "2026-04-10T10:09:25+03:00",
  "last_used_at": "2026-04-10T10:51:48+03:00"
}
```

---

## security enable

Enable a scanner by pulling its Docker image in the background. Returns immediately once the pull is kicked off; watch the status transition via `security scanners` or the SSE event stream.

### Usage

```bash
mcpproxy security enable <scanner-id>
```

`install` is a hidden alias preserved for backward compatibility.

### Examples

```bash
# Start pulling the image
mcpproxy security enable mcp-scan
#  → "Enabling scanner "mcp-scan"..."
#  → "Scanner "mcp-scan" enabled successfully."

# Follow the pull via repeated listings
watch -n 2 'mcpproxy security scanners | grep mcp-scan'
```

### Common failures

- **`docker: daemon not running`** — start Docker Desktop / `systemctl start docker`.
- **`manifest unknown`** — the scanner's image tag changed upstream. Update mcpproxy to get a new bundled registry.
- **`no space left on device`** — prune unused images with `docker system prune -a`.

---

## security disable

Disable a scanner and remove its Docker image. The scanner's configuration (env vars) is kept in BBolt, so re-enabling restores the previous configuration.

### Usage

```bash
mcpproxy security disable <scanner-id>
```

`remove` is a hidden alias.

### Example

```bash
mcpproxy security disable ramparts
#  → "Scanner "ramparts" disabled successfully."
```

---

## security configure

Set environment variables for a scanner (typically API tokens). Values are stored in the scanner's `configured_env` map in the BBolt database.

### Usage

```bash
mcpproxy security configure <scanner-id> --env KEY=VALUE [--env KEY2=VALUE2 ...]
```

### Flags

| Flag | Description |
|------|-------------|
| `--env KEY=VALUE` | Environment variable in `KEY=VALUE` format. Repeatable. |

### Storage model

Scanner env values are stored **directly in the scanner record** in BBolt, NOT in the OS keyring.
Scanner containers receive these values at scan time via Docker `--env` flags. They would end up in `/proc/environ` inside the scanner container either way, so storing them in the keyring added no real confidentiality.

If you *do* want a value in the OS keyring, you can reference it via a `${keyring:name}` placeholder — the resolver expands it via a read-only keyring Get at scan time. That path is safe on all platforms because Get never triggers the macOS "Keychain Not Found" modal.

To enable writes to the OS keyring (needed if you want `mcpproxy secrets set` to back scanner values via `${keyring:…}` references):

```bash
export MCPPROXY_KEYRING_WRITE=1       # opt-in, macOS only
mcpproxy secrets set my-snyk-token
# Then configure the scanner to reference it
mcpproxy security configure mcp-scan --env SNYK_TOKEN='${keyring:my-snyk-token}'
```

On Linux and Windows the keyring write path is enabled by default.

### Examples

```bash
# Set a single API token (stored in BBolt)
mcpproxy security configure mcp-scan --env SNYK_TOKEN=snyk_xxx

# Multiple env vars in one call
mcpproxy security configure mcp-ai-scanner \
  --env ANTHROPIC_API_KEY=sk-ant-xxx \
  --env SCANNER_MODEL=claude-sonnet-4-6

# Reference a value from the OS keyring
mcpproxy security configure cisco-mcp-scanner \
  --env CISCO_API_KEY='${keyring:cisco-ai-defense-key}'
```

### Scanner status changes

After a successful `configure`, the scanner's status transitions from `installed` to `configured`. The scanner is marked ready to scan, and the UI surfaces a green checkmark.

### Common failures

- **`scanner "<id>" not found`** — typo, or the scanner isn't in the bundled registry. Run `mcpproxy security scanners` to see valid IDs.
- **`context deadline exceeded`** — the CLI socket timeout (60s) fired. On a healthy system `configure` returns in well under a second; if you see this, file a bug.
- **Timeout with macOS Keychain dialog** — should not happen in current versions. If it does, run `unset MCPPROXY_KEYRING_WRITE` and retry; see [issue #372](https://github.com/smart-mcp-proxy/mcpproxy-go/pull/372).

---

## security scan

Run security scanners against an upstream MCP server. In the default (blocking) mode, the CLI prints live progress every 750 ms and exits when the job reaches a terminal state.

### Usage

```bash
mcpproxy security scan <server> [flags]
mcpproxy security scan --all [flags]
```

### Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--all` | Scan every configured upstream server in a batch job | `false` |
| `--async` | Start the scan and return immediately with a job ID | `false` |
| `--dry-run` | Print a plan (source resolution + scanner list) without running any containers | `false` |
| `--scanners <list>` | Comma-separated subset of scanner IDs (default: all installed) | all |

The blocking mode has a hard timeout computed as `scan_timeout_default * number_of_scanners + 30s`, clamped to a minimum of 15 minutes and a maximum of 30 minutes. When the scan runs longer than this bound the CLI bails out with an error (the server-side job continues and can be inspected with `security status`).

### Live progress (blocking mode)

On a TTY the CLI prints one progress line per tick:

```
  [2s] 2 run, 4 running, 1 failed of 7 (running: cisco-mcp-scanner, mcp-ai-scanner, mcp-scan, semgrep-mcp)
  [6s] 5 run, 1 running, 1 failed of 7 (running: semgrep-mcp)
  [8s] 6 run, 0 running, 1 failed of 7
  Scan completed in 8s
Scan completed for "everything".

Security Report: everything
Scan ID:     scan-everything-1775804891180898000
Risk Score:  0/100
Scanned:     2026-04-10 10:08:19
Scanners:    6 run, 1 failed (ramparts) of 7

WARNING: Scan coverage incomplete: 1 of 7 scanners did not run

=== Security Scan (Pass 1) ===
  0 findings

=== Supply Chain Audit (Pass 2) ===
  Not started
```

:::tip Piped output is safe
When stdout is not a TTY (e.g. `mcpproxy security scan foo | tee log.txt`), the CLI falls back to emitting one plain line per state transition instead of redrawing the block in place. This keeps your log files readable.
:::

### Batch mode (`--all`)

Batch mode queues every configured server and streams a shared progress table:

```
Scanning all servers (1/3 completed, 2 running)...
SERVER                   STATUS       FINDINGS   ERROR
----------------------------------------------------------------------
everything               completed    0
filesystem-test          running      -
fetch-test               running      -
```

Cancel a running batch at any time with `mcpproxy security cancel-all`.

### Dry-run

`--dry-run` fetches the source-resolution preview and the scanner list, then prints a plan and exits `0` without starting any Docker container. Use this to verify which directory a scanner would examine (see F-02 below).

```bash
$ mcpproxy security scan everything --dry-run
Dry-run plan for "everything"
------------------------------------------------------------
Source: npx_cache · /tmp/.npm/_npx/…/@modelcontextprotocol/server-everything · 46 files · 164 KB

Scanners that would run (7):
  - cisco-mcp-scanner (Cisco MCP Scanner) [installed]
      image:   ghcr.io/smart-mcp-proxy/scanner-cisco:latest
      timeout: 120s
      command: --analyzers yara,readiness --format raw static --tools /scan/source/tools.json
      inputs:  source
  - mcp-ai-scanner (MCP AI Scanner) [installed]
      image:   ghcr.io/smart-mcp-proxy/mcp-scanner:latest
      timeout: 900s
      inputs:  source
  …
  - trivy-mcp (Trivy Vulnerability Scanner) [installed]
      image:   ghcr.io/aquasecurity/trivy:latest
      timeout: 300s
      command: fs --format sarif /scan/source
      inputs:  source, container_image

Dry-run only — no scanners executed. Re-run without --dry-run to scan.
```

### Async mode

```bash
$ mcpproxy security scan github-server --async
Scan started for "github-server" (job: scan-github-server-1775807459327309000)
Use 'mcpproxy security status github-server' to check progress.
```

### Scanner subset

```bash
# Only run the two fastest scanners while iterating on a new server
mcpproxy security scan my-new-server --scanners nova-proximity,trivy-mcp
```

### Source resolution (important)

Before running any scanner, mcpproxy determines *what to scan*. The resolver order is:

1. **Docker-isolated servers** → extract `/app` (or the server's `WorkingDir`) from the running container.
2. **Package-runner commands** (`npx`, `uvx`, `pipx`, `bunx`) → resolve from the local package cache (`~/.npm/_npx/…`, `~/.cache/uv/…`, etc.). This path is tried **first** for package runners.
3. **Working directory** from the server config.
4. **Arg-scan fallback** — iterate positional command args, accept the first one that exists as a directory AND contains a source marker (`package.json`, `pyproject.toml`, `setup.py`, `Cargo.toml`, `go.mod`, etc.).
5. **Tool definitions only** — export the server's tool schemas to a temp dir and scan those (used for HTTP/SSE servers and as a last-resort fallback).

The `source_method` and `source_path` are recorded on the scan job and shown in both the text and JSON report. This is how you verify a scanner is examining the right directory.

:::warning Don't confuse a config path with source code
Prior to v0.23.x the resolver would pick *any* positional arg that happened to be a directory — including the data directory passed to `@modelcontextprotocol/server-filesystem`. That led to false positives on unrelated user files. The modern resolver tries the package cache first and only falls back to arg-based source if the arg directory contains recognizable source markers.
:::

---

## security rescan

Identical to `security scan <server>`, kept as a named subcommand for clarity. Accepts the same flags.

```bash
mcpproxy security rescan github-server
mcpproxy security rescan github-server --async
mcpproxy security rescan github-server --scanners trivy-mcp
```

---

## security status

Show the current (or most recent) scan job for a server, including per-scanner stderr and exit codes.

### Usage

```bash
mcpproxy security status <server> [flags]
```

### Example

```bash
$ mcpproxy security status everything
Scan Status: everything
  Job ID:   scan-everything-1775799677404855000
  Status:   completed
  Started:  2026-04-10 08:41:17
  Finished: 2026-04-10 08:42:09

  SCANNER              STATUS       FINDINGS ERROR
  -----------------------------------------------------------------
  cisco-mcp-scanner    completed    0
  mcp-ai-scanner       completed    0
  mcp-scan             failed       0        scanner mcp-scan produ...
  nova-proximity       completed    0
  ramparts             failed       0        scanner ramparts produ...
  semgrep-mcp          completed    0
  trivy-mcp            completed    0
```

:::tip Use status for diagnostics
If `security report` shows "0 findings" but you think a scanner should have flagged something, open `status` — failed scanners appear here with their truncated stderr. The full stderr is available via `security status <server> -o json`.
:::

### Common states

| State | Meaning |
|-------|---------|
| `pending` | Job accepted but not yet started |
| `running` | At least one scanner is executing |
| `completed` | All scanners reached a terminal state (some may have failed) |
| `failed` | The job itself failed (not individual scanners) |
| `cancelled` | Cancelled via `cancel-all` |

---

## security report

Display the aggregated scan report for a server. This is the primary decision-support view.

### Usage

```bash
mcpproxy security report <server> [flags]
```

Use `-o json`, `-o yaml`, or `-o sarif` for machine-readable output.

### Table output

```
Security Report: everything
Scan ID:     scan-everything-1775804891180898000
Risk Score:  0/100
Scanned:     2026-04-10 10:08:19
Scanners:    6 run, 1 failed (ramparts) of 7

WARNING: Scan coverage incomplete: 1 of 7 scanners did not run

=== Security Scan (Pass 1) ===
  0 findings

=== Supply Chain Audit (Pass 2) ===
  Not started
```

The `Scanners: X run, Y failed (names) of Z` line surfaces per-scanner failures that used to be invisible in the report. When Y > 0 the CLI also prints a yellow **"Scan coverage incomplete"** warning so a human reviewer can't mistake "0 findings" for "clean" when scanners actually crashed.

### JSON output

`-o json` returns the full aggregated report including:

- `risk_score` — composite 0-100 score
- `summary` — severity counts (`critical`, `high`, `medium`, `low`, `info`, `dangerous`, `warnings`, `info_level`, `total`)
- `findings` — normalized findings across all scanners
- `reports` — per-scanner raw results (also includes SARIF when `?include_sarif=true` is passed to the REST endpoint)
- `scan_context` — source method, source path, scanned file list
- `scanners_run`, `scanners_failed`, `scanners_total`
- `pass1_complete`, `pass2_complete`, `pass2_running`

```bash
# Extract just the severity summary
mcpproxy security report github-server -o json | jq '.data.summary'

# Get the full list of findings with file:line locations
mcpproxy security report github-server -o json \
  | jq '.data.findings[] | {severity, rule_id, scanner, location: .location}'
```

### SARIF output

`-o sarif` emits the raw per-scanner SARIF 2.1.0 documents as a JSON array, one per scanner that produced output. Useful for piping into SARIF viewers (VS Code's SARIF Viewer extension, `reviewdog`, etc.).

```bash
mcpproxy security report github-server -o sarif > github-server.sarif
```

---

## security approve

Approve a server after reviewing its scan report. This is the primary "commit the trust decision" action.

### Usage

```bash
mcpproxy security approve <server> [flags]
```

### Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--force` | Approve even if the most recent scan has critical findings | `false` |

### What approve does

1. Loads the most recent aggregated scan report for the server.
2. Checks `summary.critical == 0` — if there are critical findings, refuses unless `--force` is set.
3. Saves an `IntegrityBaseline` record (image digest, scan report IDs, approved-by, approved-at).
4. **Unquarantines the server** via the core server manager (removes it from the quarantine bucket, marks `quarantined: false`, and triggers a fresh tool index on its next connection).
5. Emits a `servers.changed` event and an activity log entry.

:::info Approve is the only path to unquarantine after a scan
Prior to v0.23.x, `security approve` only wrote the integrity baseline — it did not unquarantine the server. That was a bug (see QA report §F-01). Modern `approve` calls the unquarantine logic directly, so there is no need to run a separate `upstream unquarantine` afterwards.
:::

### Examples

```bash
# Happy path: server has 0 critical findings, approve immediately
mcpproxy security approve github-server
#  → Server "github-server" approved.
#  → (server becomes connected, tools start indexing)

# Scan reported critical findings — explicit override
mcpproxy security approve github-server --force
```

### Common failures

- **`server has N critical findings; resolve them or use --force`** — default guard. Review the findings via `security report` before overriding.
- **`no scan results found; run a scan first or use --force`** — the server has never been scanned. You can still `--force` approve but the CLI will warn you loudly.
- **`failed to unquarantine server: …`** — the unquarantine path errored AFTER the baseline was saved. The baseline is kept (it's a valid provenance record) and you can retry with a direct unquarantine call.

---

## security reject

Reject a server after reviewing its scan report. Deletes scan artifacts but keeps the server in quarantine.

### Usage

```bash
mcpproxy security reject <server>
```

### What reject does

1. Deletes all scan reports for the server from BBolt.
2. Deletes all scan job records.
3. Deletes the integrity baseline (if any).
4. Best-effort removes the `mcpproxy-snapshot-<server>` Docker image.
5. Leaves the server **still quarantined** — its config is untouched so you can run a fresh scan later.

:::note Reject is not "delete"
`security reject` never removes the server config or the server from the BBolt `servers` bucket. To fully remove a server, use `mcpproxy upstream delete <server>`.
:::

### Example

```bash
mcpproxy security reject github-server
#  → Server "github-server" rejected and quarantined.
```

---

## security integrity

Verify that a previously-approved server still matches its integrity baseline.

### Usage

```bash
mcpproxy security integrity <server>
```

### What integrity checks

| Check | What it verifies |
|-------|------------------|
| Image digest | Current `mcpproxy-snapshot-<server>` image digest matches the value recorded at approval time. Catches rebuilds of the server's own Docker image. |
| Scan report IDs | The scan reports referenced by the baseline still exist in BBolt. |
| Approval timestamp | Exposed for observability (not a gate). |

### Example

```bash
$ mcpproxy security integrity everything
Integrity Check: everything
  Status:  PASSED
  Checked: 2026-04-10 08:51:07
```

### Common failures

- **`no integrity baseline found for server "X"`** — the server has never been approved. Approve it first.
- **`digest_mismatch`** — the server's Docker image was rebuilt and no longer matches the approved version. Re-scan and re-approve if you trust the new version.

### Automated integrity checks

When `security.integrity_check_on_restart = true`, mcpproxy runs an integrity check every time it restarts an upstream server. A `digest_mismatch` on restart re-quarantines the server automatically and emits a `security.integrity_alert` SSE event. See [Security Scanner Plugin System — Configuration](/features/security-scanner-plugins#configuration) for the full list of integrity settings.

---

## security overview

Print a dashboard summary of scanner and scan totals.

### Usage

```bash
mcpproxy security overview [flags]
```

### Example

```bash
$ mcpproxy security overview
Security Overview
  Scanners installed: 7
  Servers scanned:    3
  Total scans:        12
  Active scans:       0
  Last scan:          2026-04-10 09:59:53

  Findings:
    Critical: 0
    High:     2
    Medium:   0
    Low:      0
    Info:     0
```

When no scans have been run yet, `Last scan` shows `never` (table) or `null` (JSON `last_scan_at`). Use the JSON output to drive dashboards:

```bash
mcpproxy security overview -o json \
  | jq '{installed: .scanners_installed, last: .last_scan_at, critical: .findings_by_severity.critical}'
```

---

## security cancel-all

Cancel an in-progress batch scan (started with `security scan --all`). Individual per-scanner containers may still complete, but pending servers are skipped and the batch transitions to `cancelled`.

### Usage

```bash
mcpproxy security cancel-all
```

### Example

```bash
$ mcpproxy security cancel-all
Batch scan cancelled.

$ mcpproxy security overview -o json | jq '.batch_status // "none"'
"cancelled"
```

---

## Typical workflows

### New server — full review

```bash
# 1. Add the server (lands in quarantine by default)
mcpproxy upstream add my-server --command=npx --args="-y,@my-org/mcp-server"

# 2. Make sure scanners are ready
mcpproxy security scanners
mcpproxy security enable mcp-scan nova-proximity trivy-mcp  # if needed

# 3. Run the scan (blocking, live progress)
mcpproxy security scan my-server

# 4. Review
mcpproxy security report my-server
mcpproxy security status my-server   # check for silently-failed scanners

# 5. Approve (or reject)
mcpproxy security approve my-server
#   OR
mcpproxy security reject my-server
```

### CI / scripting — async + poll

```bash
JOB=$(mcpproxy security scan my-server --async --json | jq -r '.data.job_id')
echo "scan job: $JOB"

# Poll until done
while true; do
  S=$(mcpproxy security status my-server --json | jq -r '.data.status')
  case "$S" in
    completed|failed|cancelled) break ;;
  esac
  sleep 2
done

# Fail the build if there are critical findings
CRITICAL=$(mcpproxy security report my-server --json | jq -r '.data.summary.critical')
if [ "$CRITICAL" -gt 0 ]; then
  echo "::error::Server has $CRITICAL critical findings"
  exit 1
fi

mcpproxy security approve my-server
```

### Triaging false positives

```bash
# See exactly what was scanned (most important when a scanner cries wolf)
mcpproxy security scan my-server --dry-run
# Check `Source: …` — is it the actual server package?

# Pull the full per-scanner stderr to verify the tool is reading the right files
mcpproxy security status my-server -o json | jq '.data.scanner_statuses[] | {scanner_id, exit_code, stderr}'

# Export the full SARIF for a second opinion
mcpproxy security report my-server -o sarif > my-server.sarif
```

---

## See also

- [Security Scanner Plugin System](/features/security-scanner-plugins) — architecture and feature overview
- [Scanner Images](/features/scanner-images) — which image each scanner uses and why
- [Security Quarantine](/features/security-quarantine) — the underlying quarantine mechanism
- [Activity Commands](/cli/activity-commands) — correlate scans with the activity log
- [Command Reference](/cli/command-reference) — top-level CLI index
