# Spec 039: Security Scanner Plugin System

## Overview

MCPProxy becomes the universal MCP security gateway by integrating external security scanners as plugins. Scanners analyze quarantined servers before approval, detecting tool poisoning attacks, prompt injection, malware, secrets leakage, and supply chain risks.

All scanners are plugins — no built-in scanner. MCPProxy provides a universal plugin interface that any scanner can implement. Users browse a scanner registry, install with one click (Docker image pull), configure API keys, and start scanning.

## Goals

1. **Plugin-only architecture** — every scanner is a plugin with a standard interface
2. **Universal scanner interface** — three input types (source, mcp_connection, container_image), SARIF output
3. **Best UX + strong security** — one-click install, automatic scanning of quarantined servers, clear reports
4. **Container integrity** — frozen snapshots, read-only runtime, hash verification on restart
5. **Multi-UI** — Web UI, CLI, macOS tray app all use the same REST API + SSE events

## Non-Goals

- Building a proprietary scanner (we integrate existing ones)
- Replacing the existing tool-level quarantine (this complements it)
- Supporting non-Docker scanner execution in v1 (subprocess fallback is v2)

## Architecture

### Scanner Plugin Interface

Each scanner declares a manifest:

```json
{
  "id": "cisco-mcp-scanner",
  "name": "Cisco MCP Scanner",
  "vendor": "Cisco AI Defense",
  "description": "YARA rules + LLM-as-judge. Detects TPA, prompt injection, malware.",
  "license": "Apache-2.0",
  "homepage": "https://github.com/cisco-ai-defense/mcp-scanner",
  "docker_image": "ghcr.io/cisco-ai-defense/mcp-scanner:latest",
  "inputs": ["mcp_connection", "source"],
  "outputs": ["sarif"],
  "required_env": [
    {"key": "MCP_SCANNER_API_KEY", "label": "Cisco API Key", "secret": true}
  ],
  "optional_env": [
    {"key": "VIRUSTOTAL_API_KEY", "label": "VirusTotal Key", "secret": true}
  ],
  "command": ["mcp-scanner", "scan"],
  "timeout": "60s",
  "network_required": true
}
```

### Three Input Types

| Input | How MCPProxy Provides It | Scanner Use Case |
|-------|--------------------------|------------------|
| `source` | Mount snapshot filesystem read-only at `/scan/source` | Static analysis: YARA, Semgrep, secrets, source review |
| `mcp_connection` | Expose temporary MCP endpoint via isolated Docker network | Behavioral analysis: probe tools, test responses, detect TPA |
| `container_image` | Pass snapshot image name as `SCAN_IMAGE` env var | Deep inspection: layer analysis, binary scanning, dependency audit |

### SARIF Output

Scanners write results as SARIF JSON to `/scan/report/results.sarif`. MCPProxy reads this file and normalizes findings into its internal model.

If a scanner doesn't support SARIF natively, an adapter shim (thin Docker wrapper) translates its output. Adapter shims are part of the scanner registry entry.

### Scanner Registry

A JSON file listing all known scanners. Ships bundled with MCPProxy and can be updated remotely (optional, user-controlled).

```
~/.mcpproxy/scanner-registry.json
```

Initial registry includes: Cisco MCP Scanner, Snyk Agent Scan, mcp-scan (rodolfboctor), Ramparts (Highflame), MCPScan (Ant Group).

Users can add custom scanners via `+ Custom` in the UI or config file.

## Container Security Lifecycle

### Phase 1: Install (quarantined, network temporarily enabled)

1. Pull base image (python:3.11, node:20, etc.)
2. Start container with `network=bridge` (temporary, for downloads)
3. Run install commands: pip install, npm ci, download ML weights, compile .pyc
4. `docker commit` → create frozen snapshot image `mcpproxy-snapshot-<server>:<hash>`
5. Record baseline: image digest, source hash (dirhash excluding __pycache__/node_modules), lockfile hash, `docker diff` manifest
6. Kill install container
7. Server enters QUARANTINED state

### Phase 2: Scan (parallel scanners, network restricted)

For each enabled scanner, MCPProxy:

1. Creates scanner container from scanner's Docker image
2. Mounts snapshot filesystem read-only at `/scan/source` (if scanner needs `source` input)
3. Creates tmpfs at `/scan/report` for scanner to write results
4. If scanner needs `mcp_connection`: starts the server from snapshot on an isolated Docker bridge network (`network: none` to external, shared only between server and scanner containers)
5. If scanner needs `container_image`: passes `SCAN_IMAGE=mcpproxy-snapshot-<server>:<hash>` and mounts Docker socket read-only
6. Scanner container network: `none` unless `network_required: true` (for cloud API calls like Cisco/VirusTotal)
7. Enforces timeout. Kills container on expiry.
8. Reads `/scan/report/results.sarif`, normalizes to internal model
9. Stores report in BBolt database

Scanners run in parallel. All must complete (pass or fail) before the aggregated report is presented to the user.

### Phase 3: Review + Approve

User sees in any UI (Web, CLI, Tray):
- Aggregated findings by severity (critical/high/medium/low)
- Per-scanner breakdown
- Tool descriptions (for manual TPA review)
- Filesystem diff (what was installed during Phase 1)
- Dependency list from lockfile
- Composite risk score (0-100)

Actions:
- **Approve**: Store integrity baseline, unquarantine server, index tools
- **Reject**: Delete server config + snapshot image + scan reports
- **Rescan**: Re-run all scanners (e.g., after scanner update)

### Phase 4: Runtime (approved, integrity-verified)

Server starts from snapshot image with hardened flags:

```
docker run --read-only \
  --tmpfs /tmp:noexec,nosuid,size=100M \
  --tmpfs /root/.cache:noexec,size=50M \
  --security-opt no-new-privileges \
  --env PYTHONPYCACHEPREFIX=/tmp/pycache \
  --network <per-config> \
  mcpproxy-snapshot-<server>:<hash>
```

On each restart:
- Verify snapshot image digest matches approved baseline
- If mismatch → auto re-quarantine + notification
- Log integrity check to activity log

Periodic (configurable, default 1h):
- `docker diff` against allowlist
- Alert on unexpected filesystem changes

### Integrity Hashing Strategy

**Source hash**: Go `dirhash`-style — walk source directory, SHA256 each file, sort by path, SHA256 the combined output. Exclude: `__pycache__`, `*.pyc`, `node_modules`, `.npm`, `.git`, `*.log`, `.venv`.

**Lockfile hash**: SHA256 of `requirements.txt`, `package-lock.json`, `uv.lock`, `go.sum`, or `Cargo.lock` (whichever exists).

**Image digest**: Docker content-addressable digest from `docker inspect --format='{{.Id}}'`.

**Tool hashes**: Existing Spec 032 SHA256 hash of tool name + description + schema.

All four stored in `IntegrityBaseline` record on approval.

## REST API

### Scanner Management

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/v1/security/scanners` | List registry (available + installed + configured status) |
| POST | `/api/v1/security/scanners/install` | Install scanner (pull Docker image). Body: `{"id": "cisco-mcp-scanner"}` |
| DELETE | `/api/v1/security/scanners/{id}` | Remove scanner (delete image + config) |
| PUT | `/api/v1/security/scanners/{id}/config` | Set scanner env vars (API keys). Body: `{"env": {"MCP_SCANNER_API_KEY": "..."}}` |
| GET | `/api/v1/security/scanners/{id}/status` | Scanner health: image pulled, configured, last used |

### Scan Operations

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/v1/servers/{name}/scan` | Trigger scan (async). Returns `{"job_id": "..."}` |
| GET | `/api/v1/servers/{name}/scan/status` | Scan status: pending/running/completed/failed per scanner |
| GET | `/api/v1/servers/{name}/scan/report` | Latest aggregated report (findings + risk score) |
| POST | `/api/v1/servers/{name}/scan/cancel` | Cancel running scan |

### Approval Flow

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/v1/servers/{name}/approve` | Approve after scan. Stores baseline, unquarantines. |
| POST | `/api/v1/servers/{name}/reject` | Reject. Deletes server + snapshot + reports. |
| GET | `/api/v1/servers/{name}/integrity` | Runtime integrity check result |

### Aggregate

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/v1/security/overview` | Dashboard: total scans, findings by severity, risk distribution |

### SSE Events

```
security.scan_started      — server, scanners[], job_id
security.scan_progress     — server, scanner, progress%, status
security.scan_completed    — server, findings count by severity
security.scan_failed       — server, scanner, error
security.integrity_alert   — server, type (digest_mismatch/diff_violation), action taken
```

## CLI Commands

```bash
# Scanner management
mcpproxy security scanners                      # List available + installed
mcpproxy security install <scanner-id>          # Pull Docker image
mcpproxy security configure <scanner-id>        # Set API keys (interactive prompt)
mcpproxy security remove <scanner-id>           # Remove scanner

# Scan operations
mcpproxy security scan <server>                 # Scan server (blocks until done)
mcpproxy security scan <server> --async         # Returns job_id immediately
mcpproxy security scan --all-quarantined        # Scan all quarantined servers
mcpproxy security status <server>               # Current scan status
mcpproxy security report <server>               # View latest report
mcpproxy security report <server> -o json       # JSON for scripting
mcpproxy security report <server> -o sarif      # Raw SARIF output

# Approval
mcpproxy security approve <server>              # Approve (requires clean scan or --force)
mcpproxy security reject <server>               # Delete server + artifacts
mcpproxy security rescan <server>               # Re-run scanners

# Overview
mcpproxy security overview                      # Dashboard summary
mcpproxy security integrity <server>            # Check runtime integrity
```

## macOS Tray App Integration

### Tray Menu

New "Security" section appears when findings exist or scans are running:

```
⚠ Security (2 servers need review)
  🔍 github-mcp — 1 High finding
  🔍 new-server — Scanning...
```

Clicking a finding opens the Web UI security report.

### Main Window — Security Sidebar Item

New "Security" item in sidebar (shield icon) between "Activity Log" and "Secrets". Shows:

- Stats cards: Critical/High/Medium findings count, scans today
- Installed scanners table with health status, configure/remove buttons
- Recent scans table: server, scanner, findings, time
- Click row → detailed report view

### Notifications

- "New server quarantined. Security scan started..." (on auto-scan)
- "Scan complete: 1 High vulnerability found in github-mcp" (on completion)
- "Integrity alert: server image digest mismatch, re-quarantined" (on integrity failure)

## Data Model

### BBolt Buckets

```
security_scanners     — installed scanner configs (ScannerPlugin)
security_scan_jobs    — scan job records (ScanJob)
security_reports      — SARIF reports (ScanReport)
integrity_baselines   — per-server integrity records (IntegrityBaseline)
```

### Go Types

```go
type ScannerPlugin struct {
    ID            string            `json:"id"`
    Name          string            `json:"name"`
    Vendor        string            `json:"vendor"`
    Description   string            `json:"description"`
    License       string            `json:"license"`
    Homepage      string            `json:"homepage"`
    DockerImage   string            `json:"docker_image"`
    Inputs        []string          `json:"inputs"`
    Outputs       []string          `json:"outputs"`
    RequiredEnv   []EnvRequirement  `json:"required_env"`
    OptionalEnv   []EnvRequirement  `json:"optional_env"`
    Command       []string          `json:"command"`
    Timeout       string            `json:"timeout"`
    NetworkReq    bool              `json:"network_required"`
    Installed     bool              `json:"installed"`
    InstalledAt   time.Time         `json:"installed_at"`
}

type EnvRequirement struct {
    Key    string `json:"key"`
    Label  string `json:"label"`
    Secret bool   `json:"secret"`
}

type ScanJob struct {
    ID          string    `json:"id"`
    ServerName  string    `json:"server_name"`
    ScannerID   string    `json:"scanner_id"`
    Status      string    `json:"status"`
    StartedAt   time.Time `json:"started_at"`
    CompletedAt time.Time `json:"completed_at"`
    Error       string    `json:"error,omitempty"`
}

type ScanReport struct {
    ID          string          `json:"id"`
    ServerName  string          `json:"server_name"`
    ScannerID   string          `json:"scanner_id"`
    Findings    []ScanFinding   `json:"findings"`
    RiskScore   int             `json:"risk_score"`
    SarifRaw    json.RawMessage `json:"sarif_raw"`
    ScannedAt   time.Time       `json:"scanned_at"`
}

type ScanFinding struct {
    Severity    string `json:"severity"`
    Category    string `json:"category"`
    Title       string `json:"title"`
    Description string `json:"description"`
    Location    string `json:"location"`
    Scanner     string `json:"scanner"`
}

type IntegrityBaseline struct {
    ServerName    string            `json:"server_name"`
    ImageDigest   string            `json:"image_digest"`
    SourceHash    string            `json:"source_hash"`
    LockfileHash  string            `json:"lockfile_hash"`
    DiffManifest  []string          `json:"diff_manifest"`
    ToolHashes    map[string]string `json:"tool_hashes"`
    ScanReportIDs []string          `json:"scan_report_ids"`
    ApprovedAt    time.Time         `json:"approved_at"`
    ApprovedBy    string            `json:"approved_by"`
}
```

## Configuration

```json
{
  "security": {
    "auto_scan_quarantined": true,
    "scan_timeout_default": "60s",
    "integrity_check_interval": "1h",
    "integrity_check_on_restart": true,
    "scanner_registry_url": "",
    "runtime_read_only": true,
    "runtime_tmpfs_size": "100M"
  }
}
```

## Assumptions

- Docker is available on the host (required for scanner execution and container isolation)
- Scanners are distributed as Docker images (no host installation needed)
- SARIF is the universal output format; non-SARIF scanners use adapter shims
- Scanner API keys are stored encrypted in MCPProxy's BBolt database (same as existing secrets)
- The scanner registry JSON ships bundled; remote updates are opt-in
- v1 targets stdio servers in Docker; HTTP/SSE servers are scanned via URL (no container needed)
- Pre-configured servers (in JSON config) are not auto-scanned; users can trigger manual scans

## Security Properties

1. **Install phase**: Temporary network access for downloads, frozen via `docker commit`
2. **Scan phase**: Scanner and server isolated; shared only via read-only volume or isolated bridge
3. **Runtime**: `--read-only --tmpfs` prevents persistent writes; no new code can be downloaded
4. **Integrity**: Image digest + source hash + lockfile hash verified on every restart
5. **Quarantine cascade**: Tool hash change OR image digest mismatch → automatic re-quarantine
6. **Scanner isolation**: Scanners run in their own containers; cannot affect the host or other servers
7. **Secret protection**: Scanner API keys stored encrypted; passed only to scanner containers as env vars

## Open Questions

1. Should the scanner registry be a static JSON file or a lightweight API (like a package registry)?
2. Should MCPProxy support a "dry-run" scan mode that doesn't quarantine, just reports?
3. How to handle scanners that need Docker socket access (for `container_image` input) — is Docker-in-Docker acceptable or too risky?
4. Should scan reports be exportable for compliance (PDF/HTML report generation)?
