# Security Scanner Plugin System

MCPProxy integrates external security scanners as Docker-based plugins. Scanners analyze quarantined servers before approval, detecting tool poisoning attacks, prompt injection, malware, secrets leakage, and supply chain risks.

## Overview

All scanners are plugins — no built-in scanner. MCPProxy provides a universal plugin interface that any scanner can implement. Users browse a scanner registry, install with one click, configure API keys, and start scanning.

### Key Features

- **Plugin-only architecture** — every scanner is a Docker-based plugin
- **Universal scanner interface** — source filesystem input, SARIF output
- **Parallel scanning** — multiple scanners run concurrently with independent failure handling
- **Risk scoring** — composite 0-100 risk score from aggregated findings
- **Integrity verification** — image digest checks on server restart
- **Multi-UI** — REST API, CLI, Web UI all powered by the same backend

## Quick Start

### 1. List Available Scanners

```bash
mcpproxy security scanners
```

Output:
```
ID                  NAME                VENDOR              STATUS      INPUTS
mcp-scan            MCP Scan            Invariant Labs      available   source
cisco-mcp-scanner   Cisco MCP Scanner   Cisco AI Defense    available   source, mcp_connection
semgrep-mcp         Semgrep MCP Rules   Semgrep             available   source
trivy-mcp           Trivy Scanner       Aqua Security       available   source, container_image
```

### 2. Install a Scanner

```bash
mcpproxy security install mcp-scan
```

This pulls the scanner's Docker image. Requires Docker.

### 3. Configure (if needed)

```bash
mcpproxy security configure cisco-mcp-scanner --env MCP_SCANNER_API_KEY=your-key
```

### 4. Scan a Quarantined Server

```bash
mcpproxy security scan github-server
```

Runs all installed scanners in parallel, shows progress, and displays the aggregated report.

### 5. Review and Approve/Reject

```bash
# View the report
mcpproxy security report github-server

# Approve the server (unquarantines and indexes tools)
mcpproxy security approve github-server

# Or reject (deletes server config and artifacts)
mcpproxy security reject github-server
```

## Scanner Registry

MCPProxy ships with a bundled registry of known scanners:

| Scanner | Vendor | Inputs | Description |
|---------|--------|--------|-------------|
| mcp-scan | Invariant Labs | source | Tool poisoning, prompt injection, cross-origin escalation |
| cisco-mcp-scanner | Cisco AI Defense | source, mcp_connection | YARA rules + LLM-as-judge analysis |
| semgrep-mcp | Semgrep | source | Static analysis with MCP-specific rules |
| trivy-mcp | Aqua Security | source, container_image | CVE scanning and misconfiguration detection |

### Custom Scanners

Add custom scanners via the API or CLI:

```bash
# Via API
curl -X POST http://localhost:8080/api/v1/security/scanners/install \
  -H "X-API-Key: $API_KEY" \
  -d '{"id": "custom-scanner"}'
```

## CLI Commands

```bash
# Scanner management
mcpproxy security scanners                    # List all scanners
mcpproxy security install <scanner-id>        # Install scanner
mcpproxy security remove <scanner-id>         # Remove scanner
mcpproxy security configure <id> --env K=V    # Set API keys

# Scan operations
mcpproxy security scan <server>               # Scan server (blocks)
mcpproxy security scan <server> --async       # Return job ID
mcpproxy security status <server>             # Check scan status
mcpproxy security report <server>             # View report
mcpproxy security report <server> -o json     # JSON output
mcpproxy security report <server> -o sarif    # Raw SARIF output

# Approval workflow
mcpproxy security approve <server>            # Approve
mcpproxy security approve <server> --force    # Force approve with critical findings
mcpproxy security reject <server>             # Reject and cleanup
mcpproxy security rescan <server>             # Re-run scanners

# Dashboard
mcpproxy security overview                    # Aggregate stats
mcpproxy security integrity <server>          # Check integrity
```

## REST API

### Scanner Management

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/v1/security/scanners` | List all scanners |
| POST | `/api/v1/security/scanners/install` | Install scanner |
| DELETE | `/api/v1/security/scanners/{id}` | Remove scanner |
| PUT | `/api/v1/security/scanners/{id}/config` | Configure scanner |
| GET | `/api/v1/security/scanners/{id}/status` | Scanner health |

### Scan Operations

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/v1/servers/{name}/scan` | Start scan |
| GET | `/api/v1/servers/{name}/scan/status` | Scan status |
| GET | `/api/v1/servers/{name}/scan/report` | Aggregated report |
| POST | `/api/v1/servers/{name}/scan/cancel` | Cancel scan |

### Approval Flow

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/v1/servers/{name}/security/approve` | Approve server |
| POST | `/api/v1/servers/{name}/security/reject` | Reject server |
| GET | `/api/v1/servers/{name}/integrity` | Integrity check |

### Dashboard

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/v1/security/overview` | Security dashboard stats |

### SSE Events

| Event | Payload |
|-------|---------|
| `security.scan_started` | server_name, scanners[], job_id |
| `security.scan_progress` | server_name, scanner_id, status, progress |
| `security.scan_completed` | server_name, findings_summary |
| `security.scan_failed` | server_name, scanner_id, error |
| `security.integrity_alert` | server_name, alert_type, action |

## SARIF Output

Scanners produce results in SARIF 2.1.0 format. MCPProxy normalizes findings:

| SARIF Level | MCPProxy Severity |
|-------------|-------------------|
| error | high |
| warning | medium |
| note | low |
| none | info |

Critical severity is reserved for findings explicitly marked as critical in scanner properties.

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

| Setting | Default | Description |
|---------|---------|-------------|
| auto_scan_quarantined | false | Auto-scan newly quarantined servers |
| scan_timeout_default | 60s | Default per-scanner timeout |
| integrity_check_interval | 1h | Periodic integrity check interval |
| integrity_check_on_restart | false | Check integrity on server restart |
| scanner_registry_url | (empty) | Remote registry URL (opt-in) |
| runtime_read_only | false | Run approved servers with --read-only |
| runtime_tmpfs_size | 100M | Tmpfs size for read-only containers |

## Data Storage

Scanner data is stored in BBolt database (`~/.mcpproxy/config.db`) in 4 buckets:

| Bucket | Content |
|--------|---------|
| `security_scanners` | Installed scanner configurations |
| `security_scan_jobs` | Scan job records and status |
| `security_reports` | SARIF reports and normalized findings |
| `integrity_baselines` | Per-server integrity records |

## Web UI

The Security page is available at `/security` in the Web UI. It provides:

- Dashboard stats: scanners installed, total scans, findings by severity
- Scanner marketplace: install, configure, remove scanners
- Scan trigger: select a server and start scanning
- Report viewer: findings table with severity, title, location, scanner
- Approve/reject actions: one-click server approval workflow
