---
title: "Native Sandbox Isolation"
sidebar_label: "Sandbox Isolation"
description: "Isolate stdio MCP servers on Linux without Docker using the Landlock LSM plus resource limits."
---

# Native Sandbox Isolation (Linux, no Docker)

MCPProxy can isolate a stdio MCP server **without Docker** using the Linux
[Landlock LSM](https://docs.kernel.org/userspace-api/landlock.html) plus resource
limits (`setrlimit`). This is the `sandbox` isolation mode (MCP-34), built for
hosts where Docker is unavailable or broken — notably Ubuntu 24.04 with
snap-installed Docker under AppArmor. Unlike bubblewrap / user-namespace
sandboxes, Landlock does **not** require unprivileged user namespaces, so it is
not blocked by `kernel.apparmor_restrict_unprivileged_userns=1` (default on
Ubuntu 23.10+).

:::caution The sandbox confines writes, not identity
A stdio server under `sandbox` (or `none`) still runs as the proxy's user. It can
read `mcp_config.json` and `config.db`, and while `enable_socket` is on it can
connect to the admin socket **without a key and mint itself an agent token**, in
both modes. If you run stdio servers you do not fully trust outside `docker`
isolation, **`enable_socket: false` is the setting that matters**. Details and
the verified matrix: [What the sandbox does not hide](#what-the-sandbox-does-not-hide).
:::

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
  dir, `/dev/null`, and the common package caches (`~/.npm`, `~/.cache`,
  `~/.local/share`). Tightening reads is deferred — a read allowlist breaks
  tool discovery. (`/dev/null` is on the list because the read-only `/` rule
  does not let a confined `sh -c '… 2>/dev/null'`, Node's `stdio: "ignore"`
  or Python's `subprocess.DEVNULL` open the device for writing.)
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
- **The data directory and the admin socket stay reachable.** In `none` and
  `sandbox` mode a stdio server runs as the proxy's user and can read the data
  directory and, if `enable_socket` is on, use the admin socket. Set
  `enable_socket: false` for headless installs and use `docker` isolation for
  servers you do not trust. Details and the verification table are in
  [What the sandbox does not hide](#what-the-sandbox-does-not-hide).
- **Snap-packaged runtimes do not start.** Landlock requires `no_new_privs`,
  and `snap-confine` needs privileges that flag forbids, so a `uvx` or `node`
  installed from a snap fails with *snap-confine is packaged without necessary
  permissions*. Use a non-snap install of the runtime (the official `uv`
  installer, a distro package, `nvm`).

## What the sandbox does not hide

Landlock confines **writes**. It does not change who the server runs as, and
it does not hide files or sockets the proxy's user can reach. Two things in
the data directory matter here:

- `mcp_config.json` holds `api_key`; `config.db` holds the upstream OAuth
  tokens. The directory is `0700` and the files `0600`, but the child runs as
  the same user, so that does not help.
- `mcpproxy.sock` (on by default, `enable_socket: true`) is the local admin
  endpoint. Connections to it are trusted **without an API key**, because the
  OS already authenticated the peer as the same user — which a stdio child is.

Verified by a contributor on `main` (Linux, Landlock ABI 8) with
`enable_socket` at its default and the key passed as `MCPPROXY_API_KEY`
([#1704](https://github.com/smart-mcp-proxy/mcpproxy-go/issues/1704)); a stdio
child ran a probe script:

| | `none` | `sandbox` (Landlock) | `docker` (rootless Podman) |
|---|---|---|---|
| Read `mcp_config.json` (has `api_key`) | read | read | not visible (data dir not mounted) |
| Read `config.db` (OAuth tokens) | read | read | not visible |
| `api_key` in the child's environment | no | no | no |
| Connect to `<data-dir>/mcpproxy.sock` with no key | **yes**, `GET /api/v1/status` returns data | **yes**, same | the socket is not visible |
| Mint a token over the socket (`POST /api/v1/tokens`, read+write+destructive, all servers) | same socket | **201, `mcp_agt_…` returned** | n/a |
| `GET /api/v1/config` over the socket | same socket | returns the config, `api_key` masked | n/a |
| Read the proxy's `/proc/<pid>/environ` (has `MCPPROXY_API_KEY`) | **yes** | **no**, permission denied (a Landlock domain cannot reach processes outside it) | n/a |

So in `sandbox` mode the environment is protected, but the unauthenticated
socket gives the child admin on its own, and hiding the files would not help.
With `enable_socket: false` the socket row disappears; the files remain
readable in `none` and `sandbox`.

What to do:

- **Set `enable_socket: false`** unless you use the tray. This is the one
  setting that closes the token-minting path in both `none` and `sandbox`; the
  tray is the only socket client, so a headless service loses nothing.
- **Servers you do not trust**: use `docker` isolation
  (`docker_isolation.mode: "docker"`, or per-server `isolation.mode`). The
  container does not mount the data directory, so neither the files nor the
  socket are visible.
- `mcpproxy doctor` reports every enabled stdio server that resolves to `none`
  or `sandbox` while `enable_socket` is on, so a config that drifts into this
  state is visible without re-reading this page.

The same exposure applies to a server-edition container that runs stdio
children next to its `/data` volume unless Docker isolation is turned on
there.

## Platform support

| Platform | `mode: sandbox` behavior |
|----------|--------------------------|
| **Linux** (kernel 5.13+ with Landlock) | Enforced: write-allowlist + rlimits |
| **Linux** (no Landlock) | Degraded → runs unconfined, logged |
| **macOS / Windows** | Documented **no-op** → effective `none` (Landlock is Linux-only) |

The API says so too: where the sandbox cannot be enforced, a server with
`mode: sandbox` reports `isolation.enabled: false` and
`isolation_effective.source: "sandbox-unavailable"` (its `mode` stays `sandbox`,
because on Linux the wrapper still applies its rlimits). The read surface never
claims confinement the spawn path will not deliver.

See also: [Docker Isolation](/features/docker-isolation) for the Docker mode, and
the [non-Docker sandbox spike](https://github.com/smart-mcp-proxy/mcpproxy-go/blob/main/docs/development/sandbox-spike-mcp-34.md) for the
mechanism evaluation.
