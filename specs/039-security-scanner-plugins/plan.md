# Implementation Plan: Security Scanner Plugin System

**Branch**: `feat/039-security-scanner-plugins` | **Date**: 2026-04-03 | **Spec**: [spec.md](spec.md)
**Input**: Feature specification from `/specs/039-security-scanner-plugins/spec.md`

## Summary

MCPProxy gains a pluggable security scanner system that runs Docker-based scanners against quarantined servers before approval. The system provides scanner registry management, parallel scan execution with SARIF output normalization, an approve/reject/rescan workflow with integrity baselines, and runtime integrity verification. All operations are exposed via REST API + SSE events and consumed by CLI, Web UI, and macOS tray.

## Technical Context

**Language/Version**: Go 1.24 (toolchain go1.24.10)
**Primary Dependencies**: Cobra (CLI), Chi router (HTTP), BBolt (storage), Zap (logging), os/exec (Docker CLI)
**Storage**: BBolt database (`~/.mcpproxy/config.db`) — 4 new buckets
**Testing**: go test with -race, E2E via scripts/test-api-e2e.sh
**Target Platform**: macOS (primary), Linux, Windows
**Project Type**: Single Go binary + Vue 3 frontend + Swift tray app
**Performance Goals**: Scan orchestration overhead <5s; SARIF parsing <1s for 10MB files
**Constraints**: No new heavy dependencies (use os/exec for Docker, not Docker Go SDK); follow existing codebase patterns
**Scale/Scope**: 5-20 scanners in registry, 1-5 concurrent scans, reports up to 10MB SARIF

## Design Decisions

### D1: Docker CLI via os/exec (not Docker Go SDK)

The existing codebase uses `os/exec` to shell out to `docker` in `internal/upstream/core/isolation.go`. Adding `github.com/docker/docker/client` would bring a large dependency tree. Scanner operations (pull, run, cp, rm) are well-served by CLI commands. This keeps the implementation consistent and lightweight.

### D2: SARIF as primary output + custom JSON adapter

Real scanners (mcp-scan, Cisco) output custom JSON, not SARIF natively. The scanner engine will:
1. First try to read `/scan/report/results.sarif` (standard SARIF path)
2. If not found, read stdout as JSON and check if it's SARIF
3. If neither, read stdout as scanner-specific JSON and normalize via a per-scanner adapter function

### D3: Volume mount for results (not docker cp)

Mount a host temp directory at `/scan/report` inside the scanner container. This avoids `docker cp` TAR extraction complexity and works naturally with `--rm` containers.

### D4: Scanner registry as embedded Go + user-extensible JSON

Bundle a default registry as Go constants (no external file needed for fresh install). Users can override/extend via `~/.mcpproxy/scanner-registry.json`. The registry is merged: user entries override bundled ones by scanner ID.

### D5: Integration with existing quarantine at Runtime level

Hook into `Runtime.checkToolApprovals()` flow. When `auto_scan_quarantined` is enabled, the Runtime triggers a scan job when a server enters quarantine. The scan engine runs as a background service registered in the Runtime.

## Project Structure

### Source Code (new files)

```text
internal/security/
├── scanner/
│   ├── types.go              # ScannerPlugin, ScanJob, ScanReport, ScanFinding, IntegrityBaseline
│   ├── registry.go           # Scanner registry (bundled + user JSON)
│   ├── registry_test.go
│   ├── engine.go             # Scan orchestration engine (run scanners, collect results)
│   ├── engine_test.go
│   ├── docker.go             # Docker operations (pull, run, cp, rm via os/exec)
│   ├── docker_test.go
│   ├── sarif.go              # SARIF 2.1.0 parser + normalization
│   ├── sarif_test.go
│   ├── integrity.go          # Integrity baseline + verification
│   ├── integrity_test.go
│   ├── service.go            # Security service (business logic, state management)
│   └── service_test.go
├── detector.go               # (existing) sensitive data detector
├── types.go                  # (existing)
├── ...                       # (existing pattern files)

internal/storage/
├── scanner.go                # BBolt CRUD for 4 new buckets
├── scanner_test.go
├── bbolt.go                  # (existing — add bucket init)
├── manager.go                # (existing — add scanner methods)
├── models.go                 # (existing — add scanner models)

internal/httpapi/
├── security.go               # REST API endpoints for scanner + scan + approve
├── security_test.go
├── server.go                 # (existing — add route registration)

internal/runtime/
├── runtime.go                # (existing — add security service registration)
├── lifecycle.go              # (existing — hook auto-scan on quarantine)

cmd/mcpproxy/
├── security_cmd.go           # CLI commands: security scanners/install/scan/approve/etc.

internal/config/
├── config.go                 # (existing — add SecurityConfig section)

frontend/src/
├── views/SecurityView.vue    # Web UI security dashboard
├── components/
│   ├── ScanReportModal.vue   # Scan report detail view
│   └── ScannerCard.vue       # Scanner install/config card

native/macos/MCPProxy/MCPProxy/
├── Views/SecurityView.swift  # macOS tray security sidebar item

data/
├── scanner-registry.json     # Bundled default scanner registry
```

## Implementation Phases

### Phase A: Data Layer (Storage + Types + Config)
**Estimated scope**: ~400 LOC Go + tests

1. Define Go types in `internal/security/scanner/types.go`
2. Add 4 new BBolt buckets to `internal/storage/bbolt.go`
3. Implement CRUD operations in `internal/storage/scanner.go`
4. Add `SecurityConfig` section to `internal/config/config.go`
5. Write tests for all storage operations

### Phase B: Scanner Registry
**Estimated scope**: ~300 LOC Go + tests

1. Create bundled default registry with real scanner entries
2. Implement registry loader (merge bundled + user JSON)
3. Add custom scanner validation + registration
4. Write tests for registry operations

### Phase C: Docker Operations
**Estimated scope**: ~400 LOC Go + tests

1. Implement Docker image pull with progress
2. Implement scanner container creation with security constraints
3. Implement container lifecycle (start, wait, timeout, kill, cleanup)
4. Implement result collection from volume mount
5. Write tests with mock Docker CLI

### Phase D: SARIF Parser + Scan Engine
**Estimated scope**: ~500 LOC Go + tests

1. Implement SARIF 2.1.0 parser
2. Implement finding normalization (SARIF level → severity mapping)
3. Implement risk score calculation
4. Build scan orchestration engine (parallel scanners, timeout, aggregation)
5. Write tests with sample SARIF files

### Phase E: Security Service + Quarantine Integration
**Estimated scope**: ~400 LOC Go + tests

1. Implement SecurityService (business logic layer)
2. Implement approve/reject/rescan workflow
3. Implement integrity baseline creation + verification
4. Hook into Runtime for auto-scan on quarantine
5. Add SSE events for scan lifecycle
6. Write integration tests

### Phase F: REST API
**Estimated scope**: ~400 LOC Go + tests

1. Implement scanner management endpoints (CRUD)
2. Implement scan operation endpoints (trigger, status, report, cancel)
3. Implement approval flow endpoints
4. Implement security overview endpoint
5. Update OpenAPI spec
6. Write HTTP handler tests

### Phase G: CLI Commands
**Estimated scope**: ~350 LOC Go

1. Implement `security scanners` (list)
2. Implement `security install/remove/configure`
3. Implement `security scan/status/report`
4. Implement `security approve/reject/rescan`
5. Implement `security overview/integrity`
6. Support output formats (table/json/yaml/sarif)

### Phase H: Web UI
**Estimated scope**: ~500 LOC Vue/TS

1. Create SecurityView.vue with dashboard stats
2. Create ScannerCard.vue for install/configure
3. Create ScanReportModal.vue for findings
4. Add Security route and sidebar entry
5. Wire SSE events for real-time updates

### Phase I: macOS Tray Integration
**Estimated scope**: ~200 LOC Swift

1. Add Security section to tray menu
2. Add SecurityView.swift to main window sidebar
3. Add security notifications

### Phase J: Documentation + Verification
1. Generate feature documentation
2. Run full test suite
3. Verify with real scanners (mcp-scan)
4. Create autonomous summary
