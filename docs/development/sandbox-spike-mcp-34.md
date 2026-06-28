# Spike: non-Docker sandbox mechanism for snap-docker Ubuntu 24.04 (MCP-3232)

**Status:** recommendation · gates [MCP-34](../../) (Non-Docker isolation mode) · resolves design decisions **D2** and **D3** in the [MCP-34 plan](../../).
**Author:** BackendEngineer · **PoC:** `internal/sandbox/` (this branch).

## TL;DR

Use the **Linux Landlock LSM** (kernel 5.13+) for the writable-scope filesystem
allowlist, plus **`setrlimit`** for resource caps, plus **best-effort
`SysProcAttr.Credential{Uid,Gid}`** for uid/gid drop. **Deprioritize user
namespaces / bubblewrap** — they are blocked by default on the exact hosts we
target. This matches the plan's D2 assumption and the spike confirms it with a
working PoC.

The PoC (`internal/sandbox`) proves the load-bearing claim from a Go process:
Landlock **denies a path outside the allowlist, permits the allowlisted
read-write subtree, and preserves raw stdin/stdout JSON-RPC framing** — all
without user namespaces, so it is unaffected by
`kernel.apparmor_restrict_unprivileged_userns=1`.

## Why Docker fails on these hosts (reproduction target)

On Ubuntu where Docker is installed via **snap**, AppArmor's profile transition
fights the security flags the scanner sandbox requires
(`--security-opt no-new-privileges` + a pinned AppArmor profile), so in-container
commands fail with *operation not permitted*
(`MCPX_DOCKER_SNAP_APPARMOR`, GH #71; the related systemd/snap-confine variant is
already detected by `cmd/mcpproxy/doctor_env_snapdocker.go`, repo issue #457).
The escapes today — remove snap docker / disable scanner / disable isolation —
are all adoption blockers. We need an isolation path that does **not** depend on
Docker or on a primitive AppArmor blocks.

## Candidate mechanisms compared

| Mechanism | Unprivileged? | Blocked by Ubuntu 24.04 AppArmor userns restriction? | FS write-allowlist | rlimits | uid/gid drop | Verdict |
|---|---|---|---|---|---|---|
| **Landlock LSM** (5.13+) | ✅ yes | ❌ **no** — needs no userns | ✅ path-beneath allowlist | n/a (pair with setrlimit) | ❌ no (orthogonal) | **Chosen** |
| **user namespaces / bubblewrap** | ✅ yes (in principle) | ⚠️ **yes by default** — `apparmor_restrict_unprivileged_userns=1` blocks `unshare(CLONE_NEWUSER)` unless a per-binary AppArmor profile grants `userns` | ✅ via bind mounts | ✅ | ✅ (maps uid in the ns) | **Deprioritized** |
| **`setpriv` + `setrlimit` only** | ✅ yes | ❌ no | ❌ none | ✅ | ❌ no (needs CAP_SETUID) | **Floor / fallback** |

### Landlock — chosen (resolves D2)

- **Unprivileged and userns-free.** Landlock confines the calling thread/process
  via three syscalls (`landlock_create_ruleset`, `landlock_add_rule`,
  `landlock_restrict_self`); it requires **no** user or mount namespace, so the
  Ubuntu 23.10+/24.04 `apparmor_restrict_unprivileged_userns=1` default — which
  is exactly what breaks bubblewrap on our target hosts — **does not apply**.
  Confirmed by Chromium's and Ubuntu's own guidance (sources below).
- **Inherited across `exec`.** A Landlock domain is preserved across `execve`
  and applied to every descendant; a child can only *further* restrict itself,
  never escape. That makes the integration a tiny **re-exec wrapper**: lock the
  OS thread, `Apply()` the ruleset, then `exec` the untrusted `npx`/`uvx`
  command. The proxy keeps owning the child's raw stdin/stdout pipes (D1: native
  launcher, not `process-compose`).
- **Best-effort across kernels.** `landlock_create_ruleset(NULL,0,VERSION)`
  reports the supported ABI; we mask the handled access-rights down to that ABI
  so the same binary degrades cleanly from 6.10 (ABI 5) to 5.13 (ABI 1).
  Ubuntu 24.04 ships kernel 6.8 → **ABI 4** (adds TCP bind/connect; FS rights
  fully covered). `internal/sandbox/sandbox_linux.go:handledAccessFS`.
- **No new dependency.** `golang.org/x/sys/unix v0.46` (already a direct
  dependency) ships the `SYS_LANDLOCK_*` numbers and `LandlockRulesetAttr` /
  `LandlockPathBeneathAttr` types. The PoC calls the raw syscalls — satisfies
  the repo's "avoid new dependencies" rule. (For the full build, the maintained
  `github.com/landlock-lsm/go-landlock` library — which also solves Go's
  multi-thread `restrict_self` caveat — is a reasonable alternative; the re-exec
  wrapper sidesteps that caveat by `exec`-ing a single-threaded image
  immediately after `Apply`.)

### user namespaces / bubblewrap — deprioritized (confirms D2)

Bubblewrap builds its sandbox with `unshare(CLONE_NEWUSER)`. Ubuntu 23.10+
sets `kernel.apparmor_restrict_unprivileged_userns=1` by default, which **blocks
unprivileged userns creation unless the program has an AppArmor profile granting
the `userns` permission**. bubblewrap ships such a profile in recent Ubuntu, but
a *custom Go binary* spawning userns would be denied on 24.04 out of the box —
i.e. the userns-first design risks being blocked on the very hosts we target.
This is the same failure class that breaks Docker-snap; choosing it would trade
one AppArmor block for another. Deprioritized.

### setpriv + setrlimit only — the floor

No filesystem allowlist at all — only resource caps and (with privilege)
capability/uid drop. Useful as a graceful fallback when Landlock is unavailable
(kernel < 5.13 or LSM disabled), but it does **not** meet the "writable-scope
allowlist" exit criterion on its own. The PoC applies `setrlimit` independently
of Landlock so this floor is always available.

## Honest limits (must be documented — D2 caveat)

- **No uid/gid separation without privilege.** Landlock restricts *paths*, not
  *identity*. The confined process runs as the **same uid** as mcpproxy; it can
  still touch anything that uid owns *within the allowlist*. Real uid/gid drop
  needs `SysProcAttr.Credential{Uid,Gid}`, which requires root / `CAP_SETUID`
  (server edition under systemd, not the unprivileged desktop case). **Do not
  overclaim Docker parity on uid/gid** for the unprivileged desktop case — set
  it best-effort and surface the limitation.
- **Filesystem + (on ABI 4+) TCP only.** Landlock does not restrict arbitrary
  syscalls (that is seccomp), nor PID/IPC/network namespaces. A confined process
  can still see `/proc`, signal same-uid processes, and (below kernel 6.7) open
  arbitrary network sockets. Pair with seccomp + `setrlimit(RLIMIT_NPROC)` for
  defense-in-depth in a later iteration; out of scope for this spike.
- **Allowlist must include the loader + interpreter.** `exec`-ing `npx`/`uvx`
  needs read+execute on the binary, its `node`/`python` runtime, and the shared
  libraries (`/usr`, `/lib`, `/lib64`, …). The launcher must compute and grant
  these RO paths or the child fails to start. (The PoC test grants a generous
  system RO set to demonstrate this.)

## What the PoC proves vs. what still needs the host

**Proven by `internal/sandbox` (runs in CI on `ubuntu-latest` = Ubuntu 24.04 —
see `.github/workflows/unit-tests.yml`, `go test -race ./...`):**

- `TestLandlockEnforcesFilesystemAllowlist` — re-execs a confined child that
  (1) echoes stdin→stdout (JSON-RPC framing survives), (2) reads+writes inside
  the RW allowlist, (3) is **denied** a secret path outside it. Exit-code
  assertions; skips gracefully if the kernel lacks Landlock.
- `TestHandledAccessFSMasksByABI` — ABI down-masking is correct.
- Cross-platform stub (`sandbox_other.go`) keeps macOS/Windows building with a
  documented no-op / fail-closed `ErrUnsupported`.

**Still requires a real snap-docker Ubuntu 24.04 host (deferred to MCP-34
child issues #3/#4, where the spawn branch lands):**

- End-to-end launch of an actual `npx` and `uvx` MCP server under the wrapper
  (the PoC proves the *primitive* + passthrough; the server-specific RO
  allowlist tuning is launcher work).
- Reproducing the `MCPX_DOCKER_SNAP_APPARMOR` Docker failure side-by-side to
  show the Landlock path succeeds where Docker-snap fails. (By construction
  Landlock is unaffected by the AppArmor userns restriction, so it is expected
  to work; this is the empirical confirmation step.)

## Recommendation for the D3 scanner question

The scanner *plugin* runtime is Docker-based (Spec 039) and is the broken path
on snap-docker hosts. **Recommend D3 option (b): clean, surfaced degradation** —
run isolated stdio servers under the Landlock `sandbox` launcher, and when
`isolation.mode: sandbox` is active on a host where the Docker scanner cannot
run, **skip the Docker scanner pre-flight and surface a health-degraded warning**
(via the unified `health` field + a `doctor` check, mirroring
`doctor_env_snapdocker.go`). A native non-Docker scanner path (option a) is a
larger effort and can follow once the sandbox launcher exists; degradation
unblocks adoption now and is testable on the snap-docker host. Final call sits
with the scanner child issue (MCP-34 #4).

## Proposed integration shape (for MCP-34 #2/#3, not built here)

- Config: `isolation.mode: "docker" | "sandbox" | "none"` (global + per-server),
  back-compat-mapped from today's `Enabled`/`DockerIsolation`. New
  `config.Config`/`ServerConfig` fields ⇒ register in
  `TestSaveServerSyncFieldCoverage` `expectedFields` and run `make swagger`
  (prior-art gotcha, memory).
- Spawn: a fourth branch in `connectStdio` / `buildLauncherCmd` alongside the
  existing docker-isolation / user-`docker run` / shell-wrap branches. On Linux
  with `mode: sandbox`, route through a `mcpproxy sandbox-exec`-style re-exec
  wrapper that calls `sandbox.Apply(spec)` then `exec`s the resolved command;
  reuse the existing `SysProcAttr{Setpgid:true}` process-group cleanup
  (`process_unix.go`). macOS/Windows = documented no-op → effective `none`.
- `Spec` is already shaped for this: `ReadOnlyPaths` (loader/runtime/binary),
  `ReadWritePaths` (working dir, cache, `/tmp` scope), `Rlimits`, `BestEffort`.

## Sources

- Linux kernel — Landlock (no userns required; ABI versions):
  https://docs.kernel.org/userspace-api/landlock.html
- Ubuntu — Restricted unprivileged user namespaces (default `=1` since 23.10):
  https://ubuntu.com/blog/ubuntu-23-10-restricted-unprivileged-user-namespaces
- Chromium docs — AppArmor userns restrictions vs. Landlock fallback (Landlock
  works where bwrap/userns is blocked):
  https://chromium.googlesource.com/chromium/src/+/main/docs/security/apparmor-userns-restrictions.md
- bubblewrap blocked on Ubuntu 24.04 by AppArmor userns restriction:
  https://github.com/microsoft/vscode/issues/316046
- go-landlock (library + multi-thread `restrict_self` caveat + `landlock-restrict`
  re-exec example): https://github.com/landlock-lsm/go-landlock
- Repo: `golang.org/x/sys/unix v0.46` Landlock primitives (`go.mod`);
  snap-docker detection `cmd/mcpproxy/doctor_env_snapdocker.go` (issue #457);
  MCP-34 plan decisions D1–D3 (Paperclip plan doc).
```
