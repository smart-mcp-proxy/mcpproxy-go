---
id: security-scanner-plugins
title: Security Scanner Plugin System
sidebar_label: Security Scanners
description: Docker-based scanner plugins for detecting tool poisoning, prompt injection, CVEs, and supply-chain risks in upstream MCP servers
keywords: [security, scanner, sarif, quarantine, mcp, tool-poisoning, supply-chain]
---

# Security Scanner Plugin System

MCPProxy integrates external security scanners as Docker-based plugins. Scanners analyze quarantined servers before approval, detecting tool poisoning attacks, prompt injection, malware, secrets leakage, and supply chain risks.

> **CLI reference**: for the full breakdown of every `mcpproxy security` subcommand, flags, and examples see [Security Commands](/cli/security-commands).

## Overview

Every scanner is a plugin — there is no built-in scanner. MCPProxy exposes a universal plugin interface that scanner authors implement by shipping a Docker image that reads from `/scan/source` and writes SARIF to `/scan/report/results.sarif`. Users browse a bundled scanner registry, enable scanners with one command, configure API keys if needed, and start scanning.

### Key features

- **Plugin-only architecture** — every scanner is a Docker-based plugin.
- **Universal scanner interface** — source filesystem input, SARIF output.
- **Parallel scanning** — multiple scanners run concurrently; per-scanner failures are isolated.
- **Source resolver** — detects the right thing to scan for npx/uvx/pipx/bunx package-runner commands, falls back to working dir or tool definitions for HTTP/SSE servers.
- **Risk scoring** — composite 0–100 risk score from aggregated findings.
- **Integrity verification** — image digest checks on server restart.
- **Multi-UI** — REST API, CLI, Web UI all powered by the same backend, consistent status vocabulary.
- **Failed-scanner visibility** — both CLI and Web UI surface per-scanner failures so silent crashes don't look like "clean".

### What changed recently (2026-04)

- `security approve` now actually unquarantines the server and indexes its tools (was a no-op stub).
- Source resolver tries the package cache **first** for package-runner commands, so filesystem-server-style data-dir args are no longer mistaken for source code.
- The scanner configure path no longer touches the OS keyring by default on macOS. Env values are stored in the scanner record in BBolt. See [Security Commands → configure](/cli/security-commands#security-configure) for details and the opt-in flag.
- `security report` surfaces the count of failed scanners and their names.
- `security scan --dry-run` prints a source-resolution plan without starting any containers.
- `security scan` blocking mode has a real wait loop (no more hangs) with a hard timeout.
- `scanner status` vocabulary unified between table and JSON: `available` / `pulling` / `installed` / `configured` / `error`.

## Quick start

### 1. List available scanners

```bash
mcpproxy security scanners
```

```
ID                   NAME                     VENDOR                  STATUS       INPUTS
-------------------------------------------------------------------------------------------------------
cisco-mcp-scanner    Cisco MCP Scanner        Cisco AI Defense        available    source
mcp-ai-scanner       MCP AI Scanner           MCPProxy                available    source
mcp-scan             Snyk Agent Scan          Snyk (Invariant Labs)   available    source
nova-proximity       Nova Proximity           MCPProxy                available    source
ramparts             Ramparts MCP Scanner     Javelin                 available    source
semgrep-mcp          Semgrep MCP Rules        Semgrep                 available    source
trivy-mcp            Trivy Vulnerability...   Aqua Security           available    source, container_image
```

### 2. Enable scanners

```bash
mcpproxy security enable nova-proximity
mcpproxy security enable trivy-mcp
mcpproxy security enable mcp-ai-scanner
```

(`install` is a hidden alias for `enable`, kept for backward compatibility.)

### 3. Configure API keys (if the scanner needs them)

Only a subset of scanners requires an API key. `mcp-scan` (Snyk Agent Scan) needs a free token from Snyk; `mcp-ai-scanner` can use an optional Anthropic API key or Claude Code OAuth token for richer analysis but works in pattern-only mode without one.

```bash
mcpproxy security configure mcp-scan --env SNYK_TOKEN=snyk_xxx
```

Values are stored directly in the scanner record in BBolt. The scanner container receives them via Docker `--env` flags at scan time.

### 4. Scan a quarantined server

```bash
mcpproxy security scan github-server
```

The CLI runs all enabled scanners in parallel, prints live progress, and shows a summary.

### 5. Review the findings

```bash
mcpproxy security report github-server
mcpproxy security status github-server     # per-scanner detail including stderr
mcpproxy security report github-server -o sarif > github-server.sarif
```

### 6. Approve or reject

```bash
# Approve (unquarantines and indexes tools)
mcpproxy security approve github-server

# Or reject (deletes scan artifacts, keeps server quarantined)
mcpproxy security reject github-server
```

## Scanner registry

MCPProxy ships with a bundled registry of 7 scanners. The bundled list lives in [`internal/security/scanner/registry_bundled.go`](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/internal/security/scanner/registry_bundled.go).

| Scanner | Vendor | Inputs | Required env | Notes |
|---------|--------|--------|--------------|-------|
| `cisco-mcp-scanner` | Cisco AI Defense | source | — | YARA rules + readiness analysis. Needs `tools.json` in the source dir. |
| `mcp-ai-scanner` | MCPProxy | source | — (optional `ANTHROPIC_API_KEY` / `CLAUDE_CODE_OAUTH_TOKEN`) | Agent-based AI analysis with a pattern-only fallback. Lives in a [separate repo](https://github.com/smart-mcp-proxy/mcp-scanner). |
| `mcp-scan` | Snyk (Invariant Labs) | source | `SNYK_TOKEN` | Tool poisoning, prompt injection, tool shadowing, toxic flows, secrets, rug pulls. |
| `nova-proximity` | MCPProxy (NOVA-inspired rules) | source | — | Keyword-based, fully offline. Very fast. |
| `ramparts` | Javelin | source | — | Rust-based YARA scanner. *(Known upstream issue on arm64 macOS — see [Scanner Images](/features/scanner-images).)* |
| `semgrep-mcp` | Semgrep | source | — | Static analysis with MCP-specific rules. Uses the upstream `returntocorp/semgrep:latest` image. |
| `trivy-mcp` | Aqua Security | source, container_image | — | Filesystem + CVE scan. Uses the upstream `ghcr.io/aquasecurity/trivy:latest` image. |

See [Scanner Images](/features/scanner-images) for the image sources and why vendor images are preferred over custom wrappers.

### Custom / out-of-tree scanners

Custom scanners can be added by pushing a Docker image that implements the plugin contract (see the [Plugin Interface](#plugin-interface) section below) and registering it via the REST API:

```bash
curl -X POST http://localhost:8080/api/v1/security/scanners/my-custom-scanner/enable \
  -H "X-API-Key: $API_KEY"
```

Remote scanner registries (not just the bundled one) are planned but currently opt-in via the `security.scanner_registry_url` config field.

## CLI commands

The complete CLI reference is in [docs/cli/security-commands.md](/cli/security-commands). At a glance:

```bash
# Scanner lifecycle
mcpproxy security scanners                           # list with status
mcpproxy security enable <scanner-id>                # pull image
mcpproxy security disable <scanner-id>               # remove image
mcpproxy security configure <id> --env KEY=VALUE     # set env vars (repeatable)

# Scan operations
mcpproxy security scan <server>                      # blocking, with live progress
mcpproxy security scan <server> --async              # return job id
mcpproxy security scan <server> --dry-run            # print plan, no containers
mcpproxy security scan --all                         # batch scan, progress table
mcpproxy security scan <server> --scanners a,b,c     # subset of scanners
mcpproxy security rescan <server>                    # alias for scan
mcpproxy security status <server>                    # per-scanner detail
mcpproxy security report <server> [-o json|yaml|sarif]
mcpproxy security cancel-all                         # cancel batch in progress

# Approval workflow
mcpproxy security approve <server> [--force]         # unquarantine + index
mcpproxy security reject <server>                    # delete artifacts, keep quarantined
mcpproxy security integrity <server>                 # verify against baseline

# Dashboard
mcpproxy security overview
```

All subcommands support `-o json`, `-o yaml`, and `--json` (shorthand) for scripting.

## REST API

### Scanner management

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET`    | `/api/v1/security/scanners` | List all scanners from the registry |
| `GET`    | `/api/v1/security/scanners/{id}/status` | Per-scanner detail |
| `POST`   | `/api/v1/security/scanners/{id}/enable` | Enable (pull image) |
| `POST`   | `/api/v1/security/scanners/{id}/disable` | Disable (remove image) |
| `PUT`    | `/api/v1/security/scanners/{id}/config` | Set env vars (JSON body `{"env": {"KEY": "VALUE"}}`) |
| `POST`   | `/api/v1/security/scanners/install` | Legacy install endpoint — prefer the per-scanner enable |
| `DELETE` | `/api/v1/security/scanners/{id}` | Legacy remove endpoint — prefer disable |

### Scan operations

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/api/v1/servers/{name}/scan` | Start a scan (body: `{"scanners": [...], "dry_run": false}`) |
| `GET`  | `/api/v1/servers/{name}/scan/status` | Current/latest scan job with per-scanner statuses |
| `GET`  | `/api/v1/servers/{name}/scan/report` | Aggregated report (add `?include_sarif=true` for raw SARIF) |
| `GET`  | `/api/v1/servers/{name}/scan/files` | Source-resolution preview (source_method, path, file list) |
| `POST` | `/api/v1/servers/{name}/scan/cancel` | Cancel the current scan |
| `GET`  | `/api/v1/security/scans` | Scan history across all servers |
| `GET`  | `/api/v1/security/scans/{jobId}/report` | Fetch a specific scan's aggregated report |
| `POST` | `/api/v1/security/scan-all` | Kick off a batch scan of all servers |
| `GET`  | `/api/v1/security/queue` | Batch scan progress |
| `POST` | `/api/v1/security/cancel-all` | Cancel the current batch |

### Approval workflow

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/api/v1/servers/{name}/security/approve` | Approve: saves integrity baseline + unquarantines + indexes tools. Body `{"force": true}` bypasses the critical-findings guard. |
| `POST` | `/api/v1/servers/{name}/security/reject` | Reject: deletes scan artifacts, keeps server quarantined. |
| `GET`  | `/api/v1/servers/{name}/integrity` | Integrity check against the approved baseline. |

### Dashboard

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/v1/security/overview` | Totals: scanners installed, total scans, findings by severity, Docker availability, last scan time |

### SSE events

Emitted on the `/events` SSE stream:

| Event | Payload fields |
|-------|----------------|
| `security.scan_started` | `server_name`, `scanners[]`, `job_id` |
| `security.scan_progress` | `server_name`, `scanner_id`, `status`, `progress` |
| `security.scan_completed` | `server_name`, `findings_summary` |
| `security.scan_failed` | `server_name`, `scanner_id`, `error` |
| `security.integrity_alert` | `server_name`, `alert_type`, `action` |

## Plugin interface

Every scanner plugin is a Docker image with the following contract:

| Mount / path | Direction | Description |
|--------------|-----------|-------------|
| `/scan/source` | read-only | The resolved source directory for the server under scan |
| `/scan/report` | read-write | The scanner must emit `results.sarif` here |
| `/scan/source/tools.json` | read-only | The server's exported MCP tool schemas (`[{ "name": ..., "description": ..., "inputSchema": {...} }, ...]`). Written by mcpproxy before starting the scanner container. |

Scanners communicate results via SARIF 2.1.0. Exit code `0` indicates "scan completed (with or without findings)". Non-zero exit codes are surfaced as `scanner failed` in the aggregated report.

### Source resolution order

```
1. Docker extraction (for Docker-isolated servers)
2. Package cache (npx/uvx/pipx/bunx) — PREFERRED for package runners
3. WorkingDir (from server config)
4. Arg-scan fallback — accepts only directories containing source markers
5. Tool definitions only — last resort (HTTP / SSE / unresolvable)
```

The resolved method and path are recorded on the scan job and visible via both the text and JSON report. See [Security Commands → scan](/cli/security-commands#security-scan) for more.

## SARIF normalization

Scanners produce results in SARIF 2.1.0 format. MCPProxy normalizes findings to a consistent severity vocabulary:

| SARIF level | MCPProxy severity |
|-------------|-------------------|
| `error`     | `high` |
| `warning`   | `medium` |
| `note`      | `low` |
| `none`      | `info` |

`critical` severity is reserved for findings explicitly marked critical in the scanner's SARIF `properties.severity` field or via a rule-level override. Critical findings block `security approve` unless `--force` is supplied.

MCPProxy also augments each finding with a user-facing `threat_type` (`tool_poisoning` / `prompt_injection` / `rug_pull` / `supply_chain` / `malicious_code` / `uncategorized`) and `threat_level` (`dangerous` / `warning` / `info`) so the Web UI can group findings in a way that's meaningful to MCP server trust decisions.

## Configuration

```json
{
  "security": {
    "auto_scan_quarantined": false,
    "scan_timeout_default": "60s",
    "integrity_check_interval": "1h",
    "integrity_check_on_restart": false,
    "scanner_registry_url": "",
    "runtime_read_only": false,
    "runtime_tmpfs_size": "100M"
  }
}
```

| Setting | Default | Description |
|---------|---------|-------------|
| `auto_scan_quarantined` | `false` | Auto-scan newly quarantined servers as soon as they're added. |
| `scan_timeout_default` | `60s` | Per-scanner timeout. The blocking CLI `security scan` computes a hard ceiling as `scan_timeout_default × scanner_count + 30s`, clamped between 15 and 30 minutes. |
| `integrity_check_interval` | `1h` | Periodic integrity check interval (when running as a daemon with integrity checks enabled). |
| `integrity_check_on_restart` | `false` | Re-verify integrity baseline every time an approved server is restarted. |
| `scanner_registry_url` | `""` | Remote scanner registry URL (opt-in). When empty, only the bundled registry is used. |
| `runtime_read_only` | `false` | Run approved server containers with `--read-only` and a tmpfs overlay. (P2 feature, requires Docker isolation.) |
| `runtime_tmpfs_size` | `100M` | Tmpfs size for read-only runtime containers. |

### Environment variables

| Variable | Effect |
|----------|--------|
| `MCPPROXY_KEYRING_WRITE` | Set to `1`, `true`, or `yes` to opt in to writing secrets to the OS keyring on macOS. Default (empty) routes writes to the in-config fallback. Linux and Windows default to enabled. |

## Data storage

Scanner data is stored in the BBolt database (`~/.mcpproxy/config.db`) in these buckets:

| Bucket | Content |
|--------|---------|
| `security_scanners` | Installed scanner configurations (including `configured_env`) |
| `security_scan_jobs` | Scan job records and per-scanner statuses |
| `security_reports` | Aggregated reports, per-scanner SARIF payloads, normalized findings |
| `integrity_baselines` | Per-server approval records (image digest, scan report IDs, approved-by) |

## Web UI

The Security page at `/security` in the Web UI mirrors the CLI and provides:

- **Dashboard stats** — scanners installed, total scans, findings by severity, Docker availability, Last scan timestamp.
- **Scanner table** — every scanner in the registry with its current status, vendor link, and configure button.
- **Scan All Servers** — batch scan trigger with live progress.
- **Scan report viewer** at `/security/scans/{jobId}` — risk-score badge, finding detail cards with rule/location/scanner, per-scanner execution logs including stderr from failed scanners, and scan context (source method + path + file count).
- **Approve Server / Force Approve / Reject** — scanner-gated approval dialog that requires a completed scan (or explicit force) before calling the approval endpoint.

## Known limitations

- **Ramparts on arm64 macOS** — the upstream scanner image ships a binary linked against a newer GLIBC than the image base and fails every run on arm64. Track the [scanner-ramparts image rebuild](https://github.com/smart-mcp-proxy/mcpproxy-go/issues) for a fix. Other 6 of 7 scanners work out of the box on arm64 macOS.
- **Cisco scanner output has a hardcoded `server_url`** header in its stdout (`https://mcp.deepwiki.com/mcp`). Cosmetic, does not affect findings.
- **Pass 2 (supply-chain audit)** currently requires Docker isolation to be enabled, otherwise it fails source resolution. The UI doesn't yet surface this precondition.

## Related reading

- [Security Commands](/cli/security-commands) — exhaustive CLI reference
- [Scanner Images](/features/scanner-images) — where each Docker image comes from
- [Security Quarantine](/features/security-quarantine) — the underlying quarantine mechanism that scanners gate
- [Tool Quarantine (Spec 032)](/features/tool-quarantine) — per-tool hash-based approval, a complementary layer
