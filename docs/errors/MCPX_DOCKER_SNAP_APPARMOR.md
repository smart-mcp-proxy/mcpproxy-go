---
id: MCPX_DOCKER_SNAP_APPARMOR
title: MCPX_DOCKER_SNAP_APPARMOR
sidebar_label: SNAP_APPARMOR
description: Snap-installed Docker on Ubuntu blocks mcpproxy's security scanner via AppArmor.
---

# `MCPX_DOCKER_SNAP_APPARMOR`

**Severity:** warn
**Domain:** Docker

## What happened

mcpproxy's security scanner sandbox launches the upstream stdio server inside
a Docker container with `--security-opt no-new-privileges` and a pinned
AppArmor profile. On Ubuntu's snap-installed Docker, AppArmor's profile
transition combined with `no-new-privileges` causes every command in the
container to fail with *operation not permitted* — the scanner can't run.

This is a known incompatibility between snap Docker's AppArmor confinement
and the security flags mcpproxy needs for the scanner. Other (non-scanner)
Docker isolation works fine.

## How to fix

You have four options:

### 1. Switch to non-snap Docker (recommended)

```bash
sudo snap remove docker
# Then install Docker Desktop, Colima, or rootless Docker
# https://docs.docker.com/engine/install/ubuntu/
```

### 2. Use native `sandbox` isolation instead of Docker (no Docker daemon needed)

If the real goal is to confine stdio servers on a snap-docker host, switch the
isolation **mode** to `sandbox`. Servers are confined natively with a Linux
Landlock filesystem allowlist + `setrlimit` (kernel 5.13+), which is unaffected
by the snap-docker/AppArmor conflict because it needs no Docker and no user
namespaces:

```json
{ "docker_isolation": { "mode": "sandbox" } }
```

Trade-off: the Docker-based scanner plugins cannot run under `sandbox`, so they
are **skipped** and the affected server's `security_scan.status` becomes
`degraded` (the always-on in-process `tpa-descriptions` scanner still runs).
This is MCP-34.4 / D3 option (b) — clean, surfaced degradation. See
[Security Isolation → Scanner behaviour](../docker-isolation.md#scanner-behaviour-under-each-mode-mcp-344).

### 3. Disable the scanner for this server (dry-run shown by default)

The error panel includes a **Disable scanner for this server** fix-step. The
CLI equivalent:

```bash
mcpproxy upstream patch <server-name> --no-scanner --dry-run
```

Drop `--dry-run` to apply. The server will still run with isolation, but
without TPA pre-flight scanning.

### 4. Run mcpproxy without isolation for that server

If you trust the upstream and don't need isolation:

```json
{ "isolation": { "enabled": false } }
```

## Background

See [scanner snap-docker AppArmor incompatibility](https://github.com/smart-mcp-proxy/mcpproxy-go/issues?q=snap+apparmor)
for the upstream tracking issue.

## Related

- [Docker Isolation](../features/docker-isolation.md)
- [Security Quarantine](../features/security-quarantine.md)
