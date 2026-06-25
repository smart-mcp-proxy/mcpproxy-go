# Native Sandbox Isolation (Linux, no Docker)

MCPProxy can isolate a stdio MCP server **without Docker** using the Linux
[Landlock LSM](https://docs.kernel.org/userspace-api/landlock.html) plus resource
limits (`setrlimit`). This is the `sandbox` isolation mode (MCP-34), built for
hosts where Docker is unavailable or broken — notably Ubuntu 24.04 with
snap-installed Docker under AppArmor. Unlike bubblewrap / user-namespace
sandboxes, Landlock does **not** require unprivileged user namespaces, so it is
not blocked by `kernel.apparmor_restrict_unprivileged_userns=1` (default on
Ubuntu 23.10+).

## Enabling

Set the isolation **mode** to `sandbox`, globally or per server:

```json
{
  "docker_isolation": { "mode": "sandbox" },
  "mcpServers": [
    { "name": "obsidian", "command": "uvx", "args": ["obsidian-mcp"],
      "isolation": { "mode": "sandbox" } }
  ]
}
```

A per-server `isolation.mode` wins over the global mode. The legacy
`docker_isolation.enabled` boolean still maps to `docker`/`none` for
back-compat; `mode` supersedes it (see MCP-34.2).

## What it enforces

- **Filesystem write allowlist.** Reads stay broad (the runtime can load
  interpreters, `node_modules`, and `site-packages` from anywhere), but **writes**
  are denied outside a small allowlist: the server's `working_dir`, the OS temp
  dir, and the common package caches (`~/.npm`, `~/.cache`, `~/.local/share`).
  Tightening reads is deferred — a read allowlist breaks tool discovery.
- **Resource limits.** Core dumps are disabled (`RLIMIT_CORE=0`, so in-memory
  secrets can't spill to disk) and the descriptor table is capped
  (`RLIMIT_NOFILE`).
- **Process-group cleanup.** Reuses the existing `Setpgid` group teardown, so a
  sandboxed server and its children are killed together on disconnect.

Confinement is applied by a tiny re-exec wrapper (`mcpproxy __sandbox_exec`) that
calls Landlock/`setrlimit` and then `exec`s the real command — so the server's
stdin/stdout pass straight through with no intervening multiplexer.

## Honest limits

- **No uid/gid separation by default.** The wrapper performs a *best-effort*
  privilege drop only when it is running as root with a non-root real user. In
  the personal edition mcpproxy runs as your user (not root), so this is a
  documented no-op — real uid/gid separation needs root or `CAP_SETUID`.
- **Reads are not confined** (write-allowlist only; see above).
- **Graceful degrade.** If the kernel lacks Landlock (pre-5.13, or LSM disabled),
  the server still starts but runs **unconfined**, and the host log records a
  `DEGRADED/unconfined` diagnostic. This favors availability; use `docker` mode
  when you need a hard guarantee.

## Platform support

| Platform | `mode: sandbox` behavior |
|----------|--------------------------|
| **Linux** (kernel 5.13+ with Landlock) | Enforced: write-allowlist + rlimits |
| **Linux** (no Landlock) | Degraded → runs unconfined, logged |
| **macOS / Windows** | Documented **no-op** → effective `none` (Landlock is Linux-only) |

See also: [docs/docker-isolation.md](../docker-isolation.md) for the Docker mode,
and [docs/development/sandbox-spike-mcp-34.md](../development/sandbox-spike-mcp-34.md)
for the mechanism spike.
