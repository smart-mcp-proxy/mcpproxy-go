# UNIX Socket Communication Rollout Plan

This document captures the phased plan to introduce a socket-based control channel between the tray app and the mcpproxy core while preserving existing TCP access for browsers and remote clients.

**Primary goals**
- Provide a more reliable, low-latency transport for tray ↔ core coordination.
- Deliver a seamless UX: users should not need to adjust OS permissions or configure ports.
- Support macOS, Linux, and Windows without CGO.
- Ensure the socket/pipe surface remains private to the current desktop user and does not weaken security posture.

---

## Phase 1 – Discovery & Design Readiness
**Scope**
- Catalogue current tray → core startup flow (core launch flags, env vars, API key generation).
- Map all core HTTP entry points and the authentication middleware flow.
- Decide on socket location conventions per OS and lifecycle expectations (creation, cleanup).
- Validate cross-platform feasibility (Go stdlib Unix sockets; Windows named pipes via `go-winio`).

**Deliverables**
- Design notes capturing endpoint naming, security model, and fallback behavior.
- Decision on configuration surface (`TrayEndpoint` flag/env) and migration strategy.

**Verification**
- Design walkthrough reviewed by maintainers with sign-off on location + security approach.
- POC snippets that open and clean up a socket/pipe on each target OS.

---

## Phase 2 – Core Listener Abstraction
**Scope**
- Refactor `internal/server` bootstrap to accept an injected `net.Listener` (or listener factory) so multiple transports can reuse the same mux/handlers.
- Implement Unix-domain listener helpers (macOS/Linux): directory preparation, stale socket removal, `chmod 0700` on parent dir, `chmod 0600` on socket.
- Implement Windows named-pipe listener helpers using `github.com/Microsoft/go-winio` with a per-user security descriptor.
- Tag connections accepted on the local channel (context value) so downstream middleware can identify tray-origin requests.

**Deliverables**
- New listener management utilities (`internal/server/listener_unix.go`, `listener_windows.go`, etc.).
- Updated server startup/shutdown paths that start/stop both TCP and socket listeners when configured.

**Verification**
- Unit tests validating permission bits / ACL checks and cleanup routines.
- Server starts with both listeners active and shuts down removing the socket/pipe (captured via integration test).

---

## Phase 3 – Authentication & Middleware Adjustments
**Scope**
- Update HTTP API middleware to trust requests originating from the socket/pipe while retaining API-key enforcement for TCP/HTTPS.
- Add runtime checks that verify socket ownership (UID/GID on Unix, SID on Windows) before trusting the connection.
- Preserve API key generation for the browser/TCP path; skip injecting it into tray traffic.

**Deliverables**
- Context-aware auth middleware updates.
- Diagnostic logs when a connection fails security validation.

**Verification**
- Automated tests covering both trusted (socket) and untrusted (TCP) entry paths to ensure the correct auth policy is applied.
- Manual test: tray connects via socket without API key; remote HTTP request without API key still rejected.

---

## Phase 4 – Tray Integration & Transport Client
**Scope**
- Add endpoint negotiation between tray and core (env var `MCPPROXY_TRAY_ENDPOINT`, CLI flag fallback).
- Update tray launcher to provision the socket/pipe path before spawning the core and to clean it up on exit.
- Extend the tray HTTP client (REST + SSE + health monitor) with a custom dialer that targets the socket/pipe, falling back to TCP if unavailable.
- Stop injecting the auto-generated API key into tray-bound requests when socket transport is active.

**Deliverables**
- Tray-side transport selector with per-OS behavior.
- Enhanced process launcher environment setup including new endpoint hints.
- User-facing logs confirming which transport is in use.

**Verification**
- Tray functional tests on macOS and Windows verifying launch, health checks, SSE, and API operations via the socket/pipe.
- Regression test ensuring legacy TCP-only mode still works when socket support is disabled.

---

## Phase 5 – Reliability & Security Hardening
**Scope**
- Implement runtime self-healing: remove stale socket files on startup, recreate directories with correct perms, and refuse to run if perms cannot be enforced.
- Add telemetry/logging for unexpected UID/SID or failed permission checks.
- Stress-test rapid tray/core restarts to confirm consistent cleanup.
- Document the feature and provide troubleshooting guidance.

**Deliverables**
- Additional guardrails in listener creation.
- Updated documentation (README, docs/tray-debug.md, CHANGELOG).

**Verification**
- Automated tests simulating stale socket scenarios and ensuring automatic recovery.
- Pen-test style validation confirming other local users cannot connect.
- Documentation PR reviewed and approved.

---

## Phase 6 – Final QA & Rollout Criteria
**Scope**
- Cross-platform manual QA pass (macOS, Windows, Linux) covering install, first run, restarts, and fallback scenarios.
- Ensure metrics/logging dashboards highlight socket transport usage and failures.
- Coordinate release notes and version gating with tray/Core updates.

**Verification**
- QA checklist completed with screenshots/log excerpts.
- Release approval from product/security stakeholders.
- Feature flag audit ensuring safe rollout toggles exist if needed.

---

## Go Library Dependencies
- **Standard library**: `net`, `net/http`, `os`, `syscall`, `context`, `time`, `path/filepath`.
- **Cross-platform helpers**:
  - [`github.com/Microsoft/go-winio`](https://github.com/Microsoft/go-winio) – named pipe listener/dialer and ACL control for Windows (pure Go).
  - (Optional) [`golang.org/x/sys`](https://pkg.go.dev/golang.org/x/sys) – finer-grained Unix permission checks if needed.

All dependencies avoid CGO and are already widely used in Go desktop agents.

---

## Verification Summary by Goal
- **Reliable tray-core communication**: Dedicated local socket/pipe, shared mux, fallback to TCP, integration tests and stress tests (Phases 2–5).
- **Smooth UX**: Auto-provisioned directories and listeners, zero manual permission steps, clear status logs (Phases 2, 4, 5).
- **Cross-platform support**: Unix sockets for macOS/Linux, go-winio pipes for Windows, validated in QA matrix (Phases 2, 4, 6).
- **Security posture maintained**: Tight file/ACL permissions, runtime validation, auth middleware isolation, documentation (Phases 2, 3, 5).

This phased approach allows incremental delivery while keeping the existing HTTP behavior intact until every platform path is proven and hardened.

