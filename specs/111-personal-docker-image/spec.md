# Feature Specification: Personal-Edition Docker Images (full and slim)

**Feature Branch**: `claude/mcpproxy-docker-images-55d84f` (spec directory `111-personal-docker-image`; written on the current branch, no feature branch was created)
**Created**: 2026-09-26
**Status**: Draft. Both clarifications (FR-026, FR-036) resolved — see Clarifications below. Evidence, decisions and the local prototype results are in [research.md](research.md); the quality checklist is [checklists/requirements.md](checklists/requirements.md).
**Input**: Maintainer request (2026-09-26): publish the Personal edition of MCPProxy as a Docker image, `ghcr.io/smart-mcp-proxy/mcpproxy`, in a **full** variant (Node.js, uv and git, so stdio upstreams started with `npx`/`uvx` work) and a **slim** variant, alongside the existing Server-edition image `ghcr.io/smart-mcp-proxy/mcpproxy-server`. Research inputs: the Server-edition research report of 2026-09-14 (`docs/research/server-edition-2026-09-14/`, section 7 and its internal inventory), issues #40, #1171 and #1143, telemetry over the 90 days before 2026-09-14, and a local build-and-run prototype of both variants on 2026-09-26.
**Related**: Spec 079/092 (install channel detection and update guidance), Spec 022 (OAuth callback port persistence), Spec 032 (tool-level quarantine), Spec 024 (Server-edition multi-user auth; its image is unchanged by this spec), `docs/docker-isolation.md` (isolation sandbox images, a separate concern; see Definitions).

## Clarifications

### Session 2026-09-26

- Q: Must this spec ship an upstream OAuth sign-in flow that works through Docker port publishing, or is it enough to document the limitation? → A: Document the limitation only. No in-container sign-in flow ships in this spec. The docs name two workarounds — host networking on Linux (`--network host`), or signing in on a desktop install and copying that install's persisted OAuth token state (stored in the data directory's database, not `mcp_config.json`) into the container's data volume with both proxies stopped — and a paste-the-redirect-URL completion step is deferred to a follow-up spec (research D11, option C).
- Q: Should the Personal images require MCP authentication on `/mcp` by default? → A: Yes, for the full and slim Personal images only (never the Server image). Because `require_mcp_auth` has no `MCPPROXY_*` environment-variable override (confirmed in `internal/config/loader.go` and `internal/config/process_overrides.go` — `FieldRequireMCPAuth` is a process-override target for flags/env in general but no `applyTLSEnvOverrides`-style `MCPPROXY_REQUIRE_MCP_AUTH` lookup exists), the default is applied via `internal/config/config.go`'s `DefaultConfig()` reading a **new, dedicated build-time boolean** — deliberately *not* the `image_variant` marker (Definitions), which FR-033/FR-041 keep CI-only for telemetry accuracy and which a plan reusing verbatim would leave empty (hence the default un-set) on every FR-041 local build. This boolean is stamped only by the two new Personal Dockerfiles (full and slim), for both the official CI-published build and the FR-041 local build target, and is never stamped by the existing, unchanged Server Dockerfile — so it is `true` for every full/slim image (official or local) and unset everywhere else (Server image, desktop binary), which by construction cannot touch the Server image's default (Scope Boundary, US6, SC-007). `DefaultConfig()` sets `RequireMCPAuth: true` only when this boolean is set. This resolves into FR-036 below. Two known limitations the plan must carry forward, not solve in this spec: (1) any saved config always materializes `require_mcp_auth` explicitly (no `omitempty`), so a config copied in from a desktop install or a self-built image (Edge Cases, "Volume carried over from a self-built image") brings its own explicit `false` and silently keeps `/mcp` open — the migration docs (FR-037) MUST tell migrators to set `require_mcp_auth: true` explicitly, or delete the key, when copying in a foreign config; (2) a whole-document write to `POST /api/v1/config/apply` that omits `require_mcp_auth` decodes into a zero-valued `config.Config` (`internal/oauth/configview.go`'s `UnmaskLiveConfigDocument`), not through `DefaultConfig()`, so it can persist an explicit `false` even on an image build — this is a pre-existing API behavior this spec does not change, and the docs MUST warn against a whole-document config edit that omits the field. An operator MAY still explicitly set `require_mcp_auth: false` in their own mounted config, or pass `--require-mcp-auth=false` on the container's command, to turn it back off.

## Context & Motivation

MCPProxy's only published container image today is the Server edition. It is distroless, has no shell and no Node.js, uv or git, and runs as root. It cannot start any stdio upstream server, because every stdio spawn goes through a login shell that the image does not contain. It is aimed at multi-user SSO deployments.

The people who actually run MCPProxy in containers are mostly Personal-edition users who build their own images:

| Signal | Value (90 days to 2026-09-14) |
|---|---|
| Personal-edition installs running in a container | 362 installs from 86 distinct IPs |
| ...of which ran ≥ 24 h (long-lived service) | 113 installs from 32 IPs |
| Server-edition installs running in a container | 37 installs from 8 IPs, all self-built |
| Personal-edition headless, non-container Linux, long-lived | 153 installs |
| Issue #40 (SRE, Kubernetes homelab) | Asked for an image to front a stdio HomeAssistant MCP server; a community contributor prototyped a slim + full split in 2025-09 |
| Issue #1171 (Kubernetes vendor employee) | Asked for the image workflow to be re-enabled; satisfied only for the Server edition |

About ten times as many container installs run the Personal edition as the Server edition (362 vs. 37 installs; the source report does not break the Server-edition figure down by uptime, so this is a total-population comparison, not a long-lived-to-long-lived one), and none of them has an official image. The typical need, fronting local stdio servers such as HomeAssistant, filesystem or time servers for agents on a home network or in a cluster, cannot be met by the Server image.

The 2026-09-14 inventory lists the day-1 traps a container operator hits today. This spec addresses them for the new images:

| # | Trap | Addressed by |
|---|---|---|
| T1 | State must live on a volume; relocating it with the data-dir override alone silently rotates the API key on every boot | FR-010 to FR-013 |
| T2 | Only one process may open the database; a second replica exits with code 3 | FR-015, Edge Cases |
| T3 | The auto-generated API key is written into the config file and cannot be read back without a shell | FR-014 |
| T4 | No shell, Node.js, uv, git or docker CLI, so stdio upstreams and Docker isolation cannot work | FR-007 to FR-009, FR-022 |
| T5 | Nested config blocks (`server_edition.*`, `observability.*`) are file-only, not settable via env | Out of scope: `server_edition.*` is Server-edition-only, and `observability.*`, though edition-independent (`internal/config/config.go`'s `DefaultObservabilityConfig` and `internal/runtime/runtime.go` wire it for every edition, no `server` build tag), needs no env override for the Personal image either; the Personal image sets no nested block via env for either reason. The secrets half of this trap (no `${file:}`) is FR-030 |
| T6 | Secrets: the OS keyring has no backend in a container, and there is no file-based secret reference | FR-029, FR-030 |
| T7 | Session cookie `Secure=false` is hardcoded | Out of scope: cookie-based web sessions are a Server-edition (multi-user login) concern; the Personal image authenticates the Web UI/REST via the API key, not a session cookie |
| T8 | Health probes are unauthenticated by design; `/metrics` shares the same unauthenticated listener | Probes: FR-019 documents the probe paths, health-check command, stop bound and exit codes, and FR-037's installation guide MUST also state that the probe endpoints are unauthenticated by design (a separate fact from FR-019's mechanics). `/metrics` NetworkPolicy guidance is out of scope for this spec |
| T9 | `require_mcp_auth` defaults to off, so `/mcp` is open to anyone who can reach the pod/container | FR-036: the Personal images default `require_mcp_auth` to `true` (Clarifications, Session 2026-09-26); see also Edge Cases line "`/mcp` published to the LAN" and FR-035 |
| T10 | Only three IdPs (Google/GitHub/Microsoft), no generic OIDC | Out of scope: SSO/IdP breadth is a Server-edition concern, not applicable to the Personal image |
| T11 | Only two tags exist, and `latest` moves on every stable release | FR-001 to FR-003 |

T1-T11 correspond to the numbered day-1 list in `docs/research/server-edition-2026-09-14/evidence/internal-inventory.md` §2 (items 1-11).

## Scope Boundary *(read before planning)*

| Already exists (reused, not rebuilt) | Changed or added by this spec |
|---|---|
| Server-edition image `mcpproxy-server` and its release job (stable tags only, gated on the release QA gate, `linux/amd64` + `linux/arm64`) | Unchanged in behaviour, tags, user and paths. May gain only labels and the image-variant telemetry marker (FR-005, FR-033) |
| Health probe endpoints (`/healthz`, `/readyz`, `/livez`) and bounded graceful shutdown on SIGTERM (60 s hard deadline, exit code 6) | Documented as the container probe and stop contract (FR-016, FR-019). Probe endpoints and shutdown timing are unchanged; the one backend addition is a new no-shell `healthcheck` CLI subcommand for container probes (FR-019) |
| Container detection for telemetry (`env_kind=container`) and the `docker` update channel, which never offers a self-update command | The new images are stamped with the `docker` channel; update guidance names the published image and tag (FR-031, FR-032) |
| Explicit config path + data directory, which together create and read the config inside the data directory | The images always pass both, pointing at the fixed data volume (FR-010) |
| API key from `MCPPROXY_API_KEY`, or auto-generated and saved to the config file | A shell-free way to read the saved key back (FR-014) |
| `${env:NAME}` secret references | Documented as the primary container secret mechanism. A `${file:PATH}` reference is added (FR-030) |
| Docker isolation for upstream servers (disabled by default) | Stays disabled; the full image supports it only when the operator mounts the host Docker socket, with a warning (FR-022, FR-023) |
| Structured stdio spawn failure (`MCPX_STDIO_SPAWN_ENOENT`) that leaves the proxy running | Reused as the slim variant's behaviour for stdio servers (FR-009) |
| Installation docs section "Docker (Server edition)" | A Personal-edition section, a docker-compose example and a README section are added (FR-037 to FR-040) |

## Definitions *(binding)*

- **Full variant**: the Personal-edition image that contains the proxy plus a shell, a supported Node.js LTS with `npm`/`npx`, `uv`/`uvx`, Python 3, git, a Docker CLI (no daemon; FR-022), CA certificates, time-zone data and a minimal init process. Published as the default tag.
- **Slim variant**: the Personal-edition image that contains only the proxy binary, CA certificates and time-zone data; no shell and no language runtimes. It serves remote (HTTP/SSE) upstreams, the Web UI, the REST API and MCP. Because it has no package-manager caches to write, a read-only root filesystem needs no scratch mount beyond the data volume (contrast FR-021, full variant only).
- **Data volume**: the single fixed path in the container, `/data`, that holds all persistent state: config file, database, search index, keys, and logs if file logging is on. The volume MUST be backed by a local filesystem (a Docker named volume, a local bind mount, or a Kubernetes PVC on a local-filesystem `StorageClass`, e.g. `local-path`, `ebs`, `pd`). A network filesystem (NFS, SMB/CIFS, most Kubernetes `ReadWriteMany` NFS-backed classes) is unsupported: the database's mmap+flock model silently corrupts under those (research D21), and the docs MUST carry this warning next to the volume guidance (FR-037, which now also names the network-filesystem risk explicitly rather than only cross-referencing this Definition).
- **Runtime user**: the fixed non-root user (UID/GID 65532) that the proxy process runs as.
- **Image variant marker**: a build-time value (`full`, `slim` or `server`) that the binary reports about the image it was built for, used for telemetry (FR-033), the Web UI's "official image" display (FR-028), and update guidance naming the image and tag (FR-032, US5 scenario 2). It is set only in official, CI-published image builds; it is empty in every other build, including an FR-041 local build of any variant and every non-image (desktop) build (FR-033, FR-041). It is distinct from the FR-036 build-time boolean, which covers every full/slim build — official or local — and drives a different set of behaviours (FR-025, FR-027, FR-036, FR-042); the two signals are not interchangeable.
- **Container-unsupported feature**: a feature whose effect only makes sense on the user's own desktop (tray, Connect wizard writes to client config files, self-update). The images must say so rather than act on the container's own filesystem.
- **Isolation sandbox image**: an image that MCPProxy's Docker-isolation feature pulls to run one upstream server (`docker_isolation.default_images`). It is not the product image this spec publishes, and its defaults (including the missing git in the uv slim image, #1143) are out of scope.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Homelab user fronts stdio servers from an always-on box (Priority: P1)

A user runs a NAS or small home server. They want MCPProxy always on, fronting stdio MCP servers such as a HomeAssistant server started with `uvx`, a filesystem server started with `npx`, and one installed from a git URL, so that agents on their laptop and phone reach one endpoint. They copy the documented docker-compose file, start it, open the Web UI, add the servers, approve them, and their agent calls a tool.

**Why this priority**: This is the concrete use case behind #40 and most of the 113 long-lived container installs. The Server image cannot do it at all.

**Independent Test**: On a clean Linux host (amd64) and on an arm64 host, start the documented compose example with only the image and a named volume, including the example's FR-035/FR-036 authentication setup (`require_mcp_auth: true`, or an agent token provisioned by the documented `mcpproxy token create` step) since the example publishes on the LAN. Add `npx -y @modelcontextprotocol/server-everything`, `uvx mcp-server-time` and one `uvx --from git+https://…` server through the Web UI, approve them, and call `retrieve_tools` then a tool on each through `/mcp` using the example's own authentication. All three succeed, and a call to `/mcp` without that authentication is rejected.

**Acceptance Scenarios**:

1. **Given** a host with Docker and no MCPProxy state, **When** the user runs the documented compose example, **Then** the container becomes ready (readiness probe passes) and the Web UI is reachable on the published port without any manual step inside or before the container.
2. **Given** the running full container, **When** the user adds an `npx`-based and a `uvx`-based stdio server and approves them, **Then** both reach the ready state and their tools appear in `retrieve_tools` results.
3. **Given** a stdio server installed from a `git+https://` URL, **When** it is added, **Then** it starts, because git is present in the full variant.
4. **Given** a stdio server that needs a runtime the full variant does not ship (for example Java, Go or Docker), **When** it is added, **Then** the server shows a clear error that names the missing command and the proxy and its other servers stay healthy.
5. **Given** the host restarts, **When** the container restarts under its restart policy, **Then** the same servers, approvals and API key are back without user action.

---

### User Story 2 — State and the API key survive container replacement, with no shell needed (Priority: P1)

A user upgrades by pulling a newer tag and recreating the container. Their configuration, approvals, activity history and API key must survive, and they must be able to find out the API key without a shell in the image.

**Why this priority**: Traps T1 and T3. An image that forgets its state or silently rotates its credential on recreate is worse than no image.

**Independent Test**: Start the slim variant and the full variant on fresh named volumes with no `MCPPROXY_API_KEY`. Read the generated key with the documented shell-free command. Add a server, `docker rm -f` the container, start a new container from a newer tag on the same volume, and check that the same key authenticates and the server, its approval and its activity records are present.

**Acceptance Scenarios**:

1. **Given** a fresh named volume and no API key in the environment, **When** the container starts for the first time, **Then** it starts without a manual ownership fix, generates a key, saves it on the data volume, and its log names the FR-014 read-back command (`mcpproxy config get-api-key`) without printing the key itself. [FR-014 is amended to require the startup log to name this command; the requirement previously only covered the key's own generation/read-back/permission behaviour, not the log message pointing to it.]
2. **Given** that running container, **When** the operator runs the documented read-back command (an invocation of the proxy binary through the container runtime, with no shell), **Then** it prints the saved key.
3. **Given** the container is removed and recreated from any later tag with the same volume, **Then** the same key is accepted and all configuration and history are present.
4. **Given** `MCPPROXY_API_KEY` is set, **Then** that key is used, and the docs state what happens to a different key already saved in the config.
5. **Given** a host-directory bind mount owned by root, **When** the slim container starts, **Then** it exits with the permission exit code and a message that names the fix (the documented one-line ownership command, or running with a matching user), rather than an unexplained failure.

---

### User Story 3 — Kubernetes operator runs it as a well-behaved workload (Priority: P2)

An SRE deploys MCPProxy on a small cluster (the #40 reporter's setup). They need a non-root image, working liveness and readiness probes, a bounded graceful stop, secrets from Kubernetes Secrets, multi-arch images, and a clear rule that only one replica may run.

**Why this priority**: The second largest audience and the one most likely to recommend the project to a team. It depends on US2's state model.

**Independent Test**: Apply the documented minimal manifest (one Deployment with `replicas: 1` and `strategy: Recreate`, one PVC, one Secret) on a cluster with `runAsNonRoot: true`. Probes pass; `kubectl rollout restart` completes without a database-lock crash loop; the pod stops within the grace period; an upstream header uses a secret from the Secret.

**Acceptance Scenarios**:

1. **Given** a pod security context requiring a non-root user, **Then** both variants run without overriding the image user.
2. **Given** the documented probe paths, **When** the pod starts with no upstreams, **Then** readiness passes within 30 seconds and liveness stays green.
3. **Given** SIGTERM, **Then** the process shuts down gracefully within the documented bound and exits 0 (or 6 if the hard deadline is hit).
4. **Given** a second replica is started against the same volume by mistake, **Then** it exits with the documented database-locked exit code, and the docs explain why the workload must be single-replica.
5. **Given** a secret mounted as a file or exposed as an environment variable, **Then** a server config can reference it without the value appearing in the config file.
6. **Given** an arm64 node, **Then** the image pulls and runs natively.

---

### User Story 4 — CI or agent-sandbox user runs the slim image and its subcommands (Priority: P2)

A developer runs MCPProxy in CI or in an agent sandbox to aggregate remote MCP servers, or uses the image as a CLI (`version`, `doctor`, `upstream list`, `tools`). They want a small pull and subcommands that work the way `docker run <image> <subcommand>` works for other tools.

**Why this priority**: A small but distinct audience; the slim variant costs little once the full variant exists.

**Independent Test**: `docker run --rm <slim> version` prints the version and edition; `docker run --rm <slim> --help` prints help; a slim container with only HTTP upstreams, started with `require_mcp_auth: false` (or with a provisioned agent token used on the call), serves `retrieve_tools` over `/mcp` — since FR-036 makes MCP auth the slim image's default, this test must either use a credential or explicitly opt out for the test.

**Acceptance Scenarios**:

1. **Given** either variant, **When** the user runs `docker run --rm <image> version`, **Then** it prints the version, `personal` edition and platform, and exits 0.
2. **Given** the user runs `docker run --rm <image> mcpproxy version` out of habit, **Then** the result is the same output as without the repeated name, since FR-017 requires the repeated name to be dropped rather than surfaced as an error.
3. **Given** no arguments, **Then** the container serves on the fixed port with the data volume, exactly as in US1.
4. **Given** the slim variant with a stdio server in its config, **Then** that server reports a structured spawn error, the proxy stays up, and the Web UI suggests the full variant.
5. **Given** a partial override of the default command, for example `docker run <image> serve --log-level debug`, **Then** the container still reads and writes state on the data volume (the data-dir and config flags are not dropped by the override) (FR-018).

---

### User Story 5 — Desktop-only features behave honestly in a container (Priority: P2)

A container user opens the Web UI and sees features built for the desktop: the Connect wizard that writes client config files, update prompts, Docker isolation, and upstream OAuth sign-in. None of them may silently do the wrong thing inside the container.

**Why this priority**: Without it, the Connect wizard reports success after writing a file inside the container that no client will ever read, and the update banner offers a command that cannot work.

**Independent Test**: In a full container, open the Connect page, trigger an update check against a fixture with a newer release, open Docker isolation settings, and start an OAuth sign-in for a fixture OAuth upstream. Each shows the container-specific message defined below and none writes outside the data volume.

**Acceptance Scenarios**:

1. **Given** a container image build, **When** the user opens Connect, **Then** it explains that client configs live on the user's machine and shows the endpoint URL and a copyable client snippet instead of offering to write a file; the write endpoints refuse with a clear reason.
2. **Given** a newer stable release exists and the running image carries the FR-033 `image_variant` marker (an official, CI-published build), **Then** update guidance names the image and tag to pull; **given** the marker is absent (an FR-041 local build), **then** update guidance stays generic (FR-032). Neither case ever offers a self-update command.
3. **Given** Docker isolation is enabled but the host Docker socket is not mounted, **Then** the server fails with a message that says isolation needs the host socket, and the docs carry the security warning for mounting it.
4. **Given** an upstream that needs OAuth sign-in, and no host networking, **When** the user starts the sign-in flow from the Web UI, **Then** the UI shows a clear message that upstream OAuth sign-in cannot complete through Docker's default port publishing, names the two documented workarounds (run the container with `--network host` on Linux, or sign in on a desktop install and copy that install's persisted OAuth token state into this container's data volume with both proxies stopped), and does not leave the user staring at a browser redirect that will never arrive. **Given** the container instead runs with `--network host`, the sign-in flow proceeds normally with no such message.

---

### User Story 6 — Existing Server-image users are unaffected (Priority: P2)

Operators running `ghcr.io/smart-mcp-proxy/mcpproxy-server` keep their compose files, volumes at `/root/.mcpproxy`, entrypoint and tags.

**Why this priority**: A guard story. The Server image is published and in use (#1171).

**Independent Test**: Run the existing documented Server-image command against the first release that ships this feature, using a volume created by the previous release. It starts with the same state; its tags, entrypoint, user and state path are unchanged.

**Acceptance Scenarios**:

1. **Given** the previous release's Server-image volume, **When** the new Server image starts, **Then** it reads the same state and API key.
2. **Given** the release workflow, **Then** the Server image is still published under the same names and tags, on the same conditions.

---

### User Story 7 — Secrets from mounted files (Priority: P3)

An operator stores upstream tokens as Docker or Kubernetes secrets mounted as files (for example under `/run/secrets/`) and references them from the server config.

**Why this priority**: Environment variables already cover the need; file references are the idiomatic container form and avoid secrets in process environments.

**Independent Test**: Mount a file containing a token, reference it from an upstream header as a file secret, and confirm the upstream receives the value while the config file, logs and REST responses show only the reference.

**Acceptance Scenarios**:

1. **Given** a readable file with a trailing LF newline, and separately a readable file with the same content but a trailing CRLF, **Then** both resolve to the identical value, with neither the trailing newline nor (for the CRLF file) the preceding carriage return included.
2. **Given** a missing or unreadable file, **Then** the server shows a missing-secret error naming the reference (not the value) and the proxy stays up.

---

### User Story 8 — The maintainer sees adoption by variant and releases stay gated (Priority: P3)

The maintainer wants to know how many installs use each official image and needs every image publish to stay behind the release QA gate.

**Independent Test**: A heartbeat from each official, CI-published Personal variant (full or slim) carries its variant value; a release dry run shows the new publish jobs depend on the QA gate and are skipped for RC tags.

**Acceptance Scenarios**:

1. **Given** an official, CI-published full or slim image, **Then** telemetry reports `full` or `slim` (FR-033, MUST); **given** a Server image, **then** telemetry SHOULD report `server` but MAY omit it if that retrofit is deferred (FR-033's Server-image marker is a SHOULD, not a MUST — Scope Boundary); **given** an FR-041 local build of any variant, or any non-image build, **then** telemetry omits the field, since the `image_variant` marker is CI-only by design (FR-033, FR-041).
2. **Given** an RC tag, **Then** no Personal image is published; **given** a stable tag, **then** both variants are published for both architectures after the QA gate passes.

---

### Edge Cases

- **Fresh named volume**: Docker copies the ownership of the image's `/data` directory into a new named volume only when the image contains that directory. The prototype slim image did not, so the volume was root-owned and the container exited with code 5. Both variants must ship `/data` owned by the runtime user (FR-012).
- **Root-owned bind mount**: the image cannot fix ownership without running as root. The container must fail with a message that names the fix (US2 scenario 5).
- **Volume carried over from a self-built image** with state under `/root/.mcpproxy` or `$HOME/.mcpproxy`: the docs give the migration step (copy into the data volume and fix ownership). No automatic migration. Because a saved config always materializes `require_mcp_auth` explicitly (no `omitempty`), a copied-in config that predates FR-036 brings its own explicit `require_mcp_auth: false` and silently keeps `/mcp` open on the official image; the migration step MUST also tell the operator to set `require_mcp_auth: true` in the copied config (or delete the key so the image default applies).
- **API key set in the environment and a different key saved in config**: the environment key wins for that process and is not written back; the docs state this.
- **Two containers on one volume**: the second exits with the database-locked code (3). Docs require single replica and `Recreate` strategy.
- **Read-only root filesystem** (Kubernetes `readOnlyRootFilesystem`): `npx`/`uvx` write caches under the home directory. The docs name the writable paths needed: the data volume, plus `$HOME` (`/home/mcpproxy`, or an `emptyDir` mounted there) and `XDG_CACHE_HOME`, both pointed at one writable scratch mount, so stdio servers still start (FR-021, research D16). `npm_config_cache` and `UV_CACHE_DIR` are not documented as env vars to set, because the stdio-spawn environment filter (`internal/secureenv/manager.go`) does not allowlist either today and strips them before the spawned process sees them (research D16); adding that allowlist entry is a candidate follow-up, not something this spec's docs can rely on.
- **Rollback / downgrade**: an operator pins an older tag against a volume last written by a newer release. Only the upgrade direction is guaranteed (SC-003); a downgrade that hits a newer on-disk schema is unsupported and the docs MUST say so and recommend a config/data backup before any tag change, in either direction.
- **First `npx`/`uvx` launch with no network**: the server fails with the package-manager error surfaced in its status; the proxy stays up.
- **Slim image with stdio servers configured**: structured spawn error, proxy stays healthy (verified in the prototype).
- **User repeats the binary name** (`docker run <image> mcpproxy version`): see US4 scenario 2.
- **`/mcp` published to the LAN**: MCP requests are unauthenticated by *desktop* product default unless `require_mcp_auth` is enabled; the Personal images change that default to `true` (FR-036, Clarifications). FR-035 additionally requires the documented flagship LAN-facing compose example (US1's Independent Test) to set `require_mcp_auth: true` or provision an agent token explicitly in the example's own config, so that example stays secure even if an operator's own build, an older image predating FR-036, or a future default change were to differ — belt-and-suspenders on top of FR-036's image default, not a substitute for it. The Web UI and REST API are unaffected either way — they already always require the API key (CLAUDE.md's Security Model section: "API key always required"; existing behaviour) regardless of listen address; FR-036's scope is `/mcp`'s separate `require_mcp_auth` gate.
- **Upstream OAuth sign-in**: the callback listener binds to the container's loopback, which Docker port publishing does not reach; see FR-026.
- **Docker socket mounted, isolation enabled**: spawned containers are siblings on the host, not children; relative bind paths and container networking resolve on the host. Documented with the warning (FR-023), and the Web UI's isolation settings surface the same root-equivalence warning specifically in the case where the daemon is reachable (socket mounted and accessible), not in the absent-socket or permission-denied cases, which instead get FR-024(a)'s and FR-024(b)'s own distinct messages (FR-024).
- **Host Docker socket mounted into the slim variant**: isolation cannot work there because the slim variant has no Docker CLI (Definitions), even though the daemon itself may be reachable; the status names the full variant as the fix rather than saying the socket is missing (FR-024).
- **Time zone**: logs and activity timestamps honour `TZ` when set.
- **Data volume backed by a network filesystem**: an operator mounts `/data` from an NFS or SMB/CIFS share (or an NFS-backed Kubernetes `StorageClass`), which the flagship NAS/homelab persona (US1) is prone to reach for. BBolt's mmap+flock model is not safe on network filesystems and can silently corrupt the database. The docs MUST warn against this and name local-filesystem alternatives (Definitions, Data volume).

## Requirements *(mandatory)*

### Functional Requirements

**Images and tags**

- **FR-001**: The project MUST publish a Personal-edition image at `ghcr.io/smart-mcp-proxy/mcpproxy` in two variants. The full variant is the default: `:<version>` (e.g. `:v0.70.0`) and `:latest`. The slim variant uses `:<version>-slim` and `:slim`.
- **FR-002**: `:latest` and `:slim` MUST move only on stable releases. RC and other prerelease tags MUST NOT publish any Personal image or move any moving tag.
- **FR-003**: Personal images MUST be published only after the release QA gate passes, in the same way as the Server image, and the existing workflow audit that checks every publish job depends on the gate MUST keep passing. The release checklist MUST include, as a required (not optional) step for the first publish, flipping the GHCR `mcpproxy` package from private to public — every pull-based acceptance test (SC-001, SC-002, SC-003, SC-004, SC-005) fails against a private image (SC-009 is a downstream outcome metric — issue counts — not itself a pull-based test, so it is not listed here), so this step is a release-blocking requirement, not only an assumption.
- **FR-004**: Both variants MUST be published for `linux/amd64` and `linux/arm64` under one multi-arch reference per tag.
- **FR-005**: Every published Personal image (full and slim) MUST carry the standard OCI labels (source, description, license, revision, version) and SHOULD carry an SBOM and build provenance attestation. The Server image is out of scope for the SBOM/provenance SHOULD (Scope Boundary: it may gain only labels and the variant marker, SC-007); retrofitting it is optional and not required by this FR. The Dockerfiles' own base images (the Debian/Node/uv/distroless bases they `FROM`) SHOULD be pinned by digest, not by floating tag, and published images SHOULD be scanned for known CVEs as part of the release gate; both are supply-chain hardening distinct from FR-037's operator-facing `npx`/`uvx` pinning guidance, and neither blocks the initial release if the plan defers them.
- **FR-006**: The Personal images MUST contain the Personal edition (the binary reports edition `personal`), with the embedded Web UI.

**What each variant contains**

- **FR-007**: The full variant MUST contain a POSIX login shell at the path the proxy uses for stdio spawns, a Node.js release that is in active or maintenance LTS on the release date (with `npm` and `npx`), `uv` and `uvx`, Python 3, git, CA certificates, time-zone data, and an init process that reaps child processes and forwards signals.
- **FR-008**: Tool paths added by the image MUST be visible to commands started through a login shell, so that `npx`, `uvx` and `git` resolve in stdio spawns.
- **FR-009**: The slim variant MUST contain only the proxy binary, CA certificates and time-zone data, and therefore runs as PID 1 with no init process (contrast FR-007's init process in the full variant). Because Docker reparents an exec'd process (a `docker exec` health probe or read-back command) to PID 1 when the exec'd process exits, and a plain Go binary at PID 1 does not reap adopted children by default, a long-lived slim container that receives repeated `docker exec`/Kubernetes `exec` probes (FR-019) or read-back invocations (FR-014) MUST NOT accumulate zombie processes; the plan MUST address this (for example the binary reaping adopted children when it detects it is running as PID 1, or documenting that the slim variant's healthcheck/readback commands are exec'd in a way that does not leave a reapable child, whichever the implementation confirms is actually needed) rather than leaving slim's PID-1 duty unaddressed while FR-007 states it for full. A stdio server configured on the slim variant MUST fail with a structured spawn error while the proxy stays healthy, and the server's error or the docs MUST point to the full variant.

**Runtime user, data volume, entrypoint**

- **FR-010**: Both variants MUST keep all persistent state on a single fixed data volume at `/data`, and MUST start the proxy so that the config file is read from and written to that volume. Relying on the data-directory override alone is not allowed, because it leaves the config file outside the volume (research D3).
- **FR-011**: Both variants MUST run as a fixed non-root user (UID/GID 65532) by default and MUST work under an arbitrary non-root UID supplied by the orchestrator when the data volume is writable by that UID.
- **FR-012**: Both variants MUST ship `/data` owned by the runtime user, so that a fresh named volume starts without any manual ownership step.
- **FR-013**: When the data volume fails a write probe by the running user (regardless of its recorded owner, so a group-writable or `fsGroup`-adjusted volume passes), the container MUST exit with the documented permission exit code (5) and a message that names the volume path, the expected owner and the one-line fix. [Amended: the trigger is write-probe failure only, not ownership, so it does not contradict FR-011's arbitrary-UID support.]
- **FR-014**: When no API key is supplied, the first start MUST generate one, save it on the data volume, and log a message naming the FR-014 read-back command (`mcpproxy config get-api-key`) without printing the key itself, and the operator MUST be able to print the saved key by running `docker exec <container> mcpproxy config get-api-key` (a new, no-shell CLI subcommand this spec adds; the exact form is `mcpproxy config get-api-key [--config PATH]`, matching the existing `--config`/`-c` flag convention used by `token`, `doctor` and other subcommands). That command MUST resolve and print the key through a config-only read path that does not open the BBolt database, so a read-back against a running container never contends the database lock (research R12/R13). In a detached (non-TTY) container — the documented and expected deployment mode for this spec's compose/Kubernetes examples — the container log MUST NOT contain the key. [Amended: the existing terminal-only banner behaviour (research R11) prints the key once when stderr is a TTY, e.g. under `docker run -it`, and `docker logs` captures that stream regardless of TTY allocation; this FR does not require changing that existing behaviour, and the documentation MUST say that starting the container with `-it` (or `kubectl exec`-style TTY attach at first boot) will put the key in `docker logs`/`kubectl logs` history, so operators should use the FR-014 read-back command instead of an interactive first run when they want the key kept out of log history.] The saved config file MUST be written with owner-only permissions (0600) so the key is not readable by other UIDs on a shared bind mount regardless of umask. An `MCPPROXY_API_KEY` environment variable set to an empty string MUST be treated the same as not set (a key is generated and saved), matching how Compose/Kubernetes interpolation leaves an unset variable as an empty string rather than an absent one.
- **FR-015**: The documentation MUST state that one data volume supports exactly one running container, and the Kubernetes example MUST use one replica with a recreate update strategy.
- **FR-016**: The image entrypoint MUST run the proxy binary (behind the init process in the full variant) and the default command MUST be `serve` on `0.0.0.0:8080` with the data volume, so that `docker run <image> <subcommand> [flags]` runs any subcommand without repeating the binary name, and no arguments start the server.
- **FR-017**: When the first argument is the binary's own name, the image MUST treat it as absent (drop it) rather than passing it through as a subcommand. [Assumption: research D9.]
- **FR-018**: Subcommands run inside a running container (`docker exec <container> mcpproxy <subcommand>`), and subcommands given to `docker run` with the data volume mounted, MUST use the data volume's config and data without extra flags. A partial override of the default command (for example `serve --log-level debug`) MUST keep the data volume paths; this is a MUST on the binary/image's own defaulting behaviour, not something a documentation example can substitute for. (research D5)
- **FR-019**: The documentation MUST give the liveness and readiness probe paths, the container health-check command that works in each variant without a shell, the graceful-stop bound, and the exit codes. Because the slim variant ships no shell and no `curl`/`wget` (Definitions), this spec adds a new no-shell `healthcheck` CLI subcommand to the proxy binary (a backend addition, not documentation-only; see Scope Boundary): it issues its own HTTP GET to `/healthz` at the listen address resolved the same way `serve` resolves flags/env/config for its *own* invocation. The `healthcheck` subcommand runs as a separate `docker exec`/Kubernetes `exec`-probed process, not as a thread inside the running `serve` process, so it cannot see command-line flags that were passed only to that already-running `serve` invocation (for example `serve --listen 0.0.0.0:9090`, FR-018's partial-override case) — a separately exec'd process has no visibility into another process's argv. The plan MUST therefore give the `healthcheck` subcommand an out-of-band way to learn the effective listen address when it was set only via a `serve` flag (for example `serve` persisting the effective address to a well-known path under `/data` on start, which `healthcheck` then reads, or documenting that operators overriding the listen address via a flag MUST also pass a matching `--listen`/env var to the healthcheck invocation itself). It exits 0 on a 2xx response or 1 otherwise, requiring no external HTTP client tool. A Docker `HEALTHCHECK`/Kubernetes `exec` probe in both variants runs this subcommand.
- **FR-019a**: The documented graceful-stop bound (60 s, matching the existing hard shutdown deadline) exceeds Docker Compose's default `stop_grace_period` (10 s) and Kubernetes' default `terminationGracePeriodSeconds` (30 s). Every documented compose and Kubernetes example (FR-038, FR-040) MUST explicitly set `stop_grace_period: 60s` / `terminationGracePeriodSeconds: 60`, so the documented bound is one the examples can actually reach rather than one the platform truncates by default. [Amended: the earlier alternative of dropping the example's own stop timing to fit a shorter platform default is rejected, matching research D19's own "alternatives considered" — it would change existing shutdown behaviour, which is out of this spec's scope.]
- **FR-020**: Each image MUST declare the data volume and the listen port.
- **FR-021**: The full variant MUST start stdio servers whose package managers need writable home and cache directories, including when the root filesystem is read-only and only the data volume and one documented temporary path are writable. The documented writable paths are named, not left generic: `$HOME` (`/home/mcpproxy`) and `XDG_CACHE_HOME`, both pointed at one writable scratch mount (an `emptyDir` in Kubernetes, or a plain volume in Compose) when the root filesystem is read-only — these two are the vars the existing stdio-spawn environment allowlist (`internal/secureenv/manager.go`) actually passes through to a spawned `npx`/`uvx` process (research D16). `npm_config_cache` and `UV_CACHE_DIR` are NOT documented as a mechanism here, because that allowlist strips both today; the plan MAY add them to the allowlist as a small backend change, in which case the docs gain them too, but this FR does not depend on that happening. The slim variant has no package-manager caches and needs no scratch mount beyond `/data` under a read-only root filesystem (Definitions, Slim variant).

**Docker isolation**

- **FR-022**: Docker isolation MUST stay disabled by default in both variants. The full variant MUST support it only when the operator mounts the host Docker socket; it therefore MUST include a Docker CLI or the documented equivalent. [Assumption: include the Docker CLI only, no daemon; research D10.] Because the host socket is typically owned `root:docker` at mode `660` (research D20), mounting it alone is not sufficient for a non-root container: the documentation MUST also instruct the operator to either add the runtime user to a group whose GID matches the host's `docker` group (Docker's `group_add`, or the Kubernetes pod/container `securityContext.supplementalGroups` equivalent) or otherwise make the socket group-readable/writable by GID 65532, and MUST say that skipping this step leaves the daemon mounted but unreachable due to a permission error, not a missing socket.
- **FR-023**: The documentation MUST warn that mounting the host Docker socket gives the container root-equivalent control of the host, and MUST explain that isolated servers run as sibling containers on the host.
- **FR-024**: The affected server's status MUST distinguish the two ways isolation can fail to reach a daemon, because `IsDockerAvailable()`'s `docker info` probe (research D20) currently reports only a bool and cannot itself tell them apart — this FR requires the status message layer to make that distinction from what is independently known about the socket mount and CLI presence, not from the probe result alone: (a) **socket absent** (no `/var/run/docker.sock` mount, or no Docker CLI in the image) — the status MUST say that isolation needs the host socket mounted (and, in the slim variant specifically, where no Docker CLI is present per Definitions, that it needs the full variant instead, since the blocker there is the missing CLI, not an unreachable daemon); (b) **socket mounted but permission-denied** (the CLI resolves and the socket path exists, but `docker info` fails with an access error, the FR-022 GID-mismatch case) — the status MUST say that the socket is mounted but not accessible to the runtime user, and name the FR-022 fix (matching GID or `group_add`), rather than reusing the "socket needs to be mounted" wording, which would be misleading when the socket is already mounted. When the daemon *is* reachable (the socket is mounted and accessible) but isolation is enabled, the Web UI's isolation settings MUST also surface the root-equivalence warning (FR-023), since a reachable daemon on a mounted socket is exactly the root-equivalent configuration the warning is about — it is not conditioned on the socket being absent.

**Features unsupported in the container**

- **FR-025**: In image builds, the Connect wizard MUST NOT write client configuration files. The Web UI, CLI and REST API MUST show the MCP endpoint URL and a copyable per-client snippet instead, and write requests MUST be refused with a reason that names the container deployment. The endpoint URL MUST be derived from the incoming request (the `Host` header, or the standard proxy forwarding headers when present) rather than the container's own listen address, since `0.0.0.0:8080` is not a reachable address for a client; when no usable request context is available (for example the CLI/`--help-json` surface), it MUST fall back to an operator-configured external URL if one is set, and otherwise show the local `listen` address with a note that it may need a host/port substitution for the client's actual network path. Because FR-036 makes `require_mcp_auth` the image default, the copyable snippet MUST include a credential placeholder for `/mcp` (`/mcp` accepts either the API key or an agent token, `internal/server/server.go`'s auth middleware) and SHOULD recommend a scoped agent token over the admin API key; it MUST NOT silently omit a credential from the snippet.
- **FR-026**: Upstream OAuth sign-in from a container is documented as a limitation, not shipped as a container-friendly flow, in this spec (Clarifications, Session 2026-09-26; research D11 option A). The installation guide MUST state that the OAuth callback listener binds to the container's own loopback, which Docker's default `-p` port publishing does not reach, and MUST name two workarounds: running the container with `--network host` on Linux (under which the normal in-container sign-in flow completes unmodified, because the container's loopback is then the host's loopback), or completing sign-in on a desktop install and migrating that install's persisted OAuth token state — stored in the data directory's database (`PersistentTokenStore`, the `oauth_tokens` bucket), not in `mcp_config.json` — into the container's data volume, with the proxy stopped on both sides during the copy. This spec does not define a token export/import command; documenting a manual data-directory copy as the interim mechanism is sufficient here. The Web UI and CLI sign-in surfaces for an upstream that needs OAuth, when the container is not running with `--network host`, MUST show a message naming this limitation and the two workarounds rather than leaving the user waiting on a redirect that cannot arrive (US5 scenario 4). Because a running container cannot directly observe its own `docker run --network` mode from inside — and a successful loopback *bind* does not discriminate host- from bridge-networking, since the callback listener binds its own loopback either way — the plan MUST give this detection a concrete, working basis rather than leaving "when not running `--network host`" as an unimplementable precondition. The default mechanism is behavioural: start the sign-in flow optimistically in every case, and only show the limitation message if the callback has not received the provider's redirect within a bounded timeout (under `--network host` the redirect arrives normally and no message is ever shown; under the default bridge network it never arrives, so the message appears once the timeout elapses). A documented operator-set environment variable MAY additionally let an operator who knows host networking is in effect suppress even the optimistic wait, but such a variable is a supplement to the timeout-based detection, not a substitute for it, since its absence must not be read as "assume bridge networking and show the message immediately" when the operator in fact used `--network host`. A configurable callback bind address, and a paste-the-redirect-URL completion step that would work through any network setup, are out of scope for this spec and are candidate follow-up work (research D11, options B/C).
- **FR-027**: Tray-specific surfaces (tray socket, tray menu hints) MUST NOT be advertised in the Web UI or CLI output of image builds. The local IPC (Unix) socket, which has no app-layer auth and relies on filesystem/OS permissions ("socket connections bypass the API key" per CLAUDE.md's Architecture section), MUST be disabled by default in image builds — there is no tray for it to serve in a container — and MAY be re-enabled by an operator who explicitly opts in, at their own risk of exposing an unauthenticated control channel to whatever else can reach the socket path (for example another container sharing the data volume). "Image builds" for this FR, and for FR-025 and FR-042, means the FR-036 build-time boolean (or an equivalent signal covering every full/slim build, official or local) — not the CI-only `image_variant` marker (FR-033/FR-041), which is absent on local builds and would wrongly leave these behaviours off there.
- **FR-028**: The Web UI MUST show that the instance runs from the official image and which variant, in the place where it shows version and edition, whenever the FR-033 `image_variant` marker is present (not the FR-036/FR-027/FR-042 boolean, which is a separate signal). A locally built image (FR-041) carries no marker, so it correctly shows neither the "official image" badge nor a variant name there — it MAY still show its variant from other means (for example a value the local build target passes some other way), but that is not this FR's requirement.

**Secrets**

- **FR-029**: `${env:NAME}` references MUST be documented as the primary way to pass secrets to upstream servers in containers. OS-keyring references MUST fail with a message that says the keyring is unavailable in containers and names the alternatives.
- **FR-030**: The proxy MUST support `${file:/absolute/path}` secret references that read a secret from a file, strip one trailing newline (and, when present, a preceding carriage return, so CRLF-terminated Windows-authored secret files resolve the same value as LF-terminated ones), never write the value back to config, and never show it in logs or API responses.

**Updates and telemetry**

- **FR-031**: Personal images MUST be stamped with the existing `docker` install channel, so the self-update command is never offered.
- **FR-032**: Update guidance for the `docker` channel MUST name the image reference and variant tag to pull for the newer release when the variant is known, and MUST stay generic when it is not.
- **FR-033**: Telemetry heartbeats from an official, CI-published Personal (full or slim) image build MUST include an `image_variant` field with `full` or `slim`. The Server image SHOULD be stamped `server` in the same field (a SHOULD, not a MUST — Scope Boundary). Every other build — an FR-041 local build of any variant, and every non-image (desktop) build — MUST omit the field, per FR-041. The field MUST NOT carry any other information.
- **FR-034**: The telemetry documentation MUST list the new field.

**Network exposure**

- **FR-035**: Every *quickstart/minimal* documented `docker run` and compose example MUST publish the port on `127.0.0.1` by default. The documented homelab compose example that backs US1's Independent Test (laptop-and-phone access) is the one documented exception: it MAY publish on `0.0.0.0`/the LAN interface, but only when the example's own config actually enables `/mcp` authentication explicitly (`require_mcp_auth: true`, or an agent token the operator provisions with an explicit `mcpproxy token create` step before first use — agent tokens are never auto-created on first run, unlike the API key, so the example MUST show that command rather than assume a token already exists), not merely mentioned nearby in prose or left to FR-036's now-default-on behaviour. Concretely: the example's `mcp_config.json` MUST set `require_mcp_auth: true` (there is no environment-variable override for this field — `--require-mcp-auth` is a CLI flag and is not among the env-mapped config fields, so "compose environment" here means the mounted `mcp_config.json`, or a compose `command:` override appending `--require-mcp-auth`, not a Compose `environment:` block), or the example MUST include the `mcpproxy token create` step that provisions an agent token before the client snippet uses it. Either way, a reader who copies the example verbatim gets a `/mcp` that is actually authenticated, not just a written warning next to an open one, and not merely a reliance on FR-036's compiled-in default. No example may show LAN exposure without that pairing. This resolves the tension between FR-035's loopback default and US1's phone-reachability requirement: they describe two different documented examples, not one.
- **FR-036**: MCP endpoint authentication in the images is on by default for the full and slim variants only, never for the Server image (Clarifications, Session 2026-09-26; research D12 option B). The Personal images (full and slim) MUST default `require_mcp_auth` to `true`; the desktop (non-image) product default and the Server image's default are unchanged (`false`, subject to the Server edition's own existing `EffectiveRequireMCPAuth` override when `server_edition.enabled` is true — unaffected by this FR). Because `require_mcp_auth` has no `MCPPROXY_*` environment-variable override, the default MUST be applied through `internal/config/config.go`'s `DefaultConfig()` reading a new, dedicated build-time boolean that only the full and slim Dockerfiles stamp (for both the official CI-published build and the FR-041 local build target) — not the `image_variant` marker used by FR-033/FR-041, which stays CI-only and would leave FR-041 local builds with the default unset if reused here. `DefaultConfig()` MUST compile in `RequireMCPAuth: true` only when this boolean is set; the Server Dockerfile MUST NOT set it. Two limitations apply and MUST be documented rather than solved by this spec: a config file copied in from a desktop install or a self-built image always carries an explicit `require_mcp_auth` value (no `omitempty`) that overrides this default, so FR-037's migration guidance MUST tell migrators to set it to `true` explicitly; and a whole-document `POST /api/v1/config/apply` that omits the field bypasses `DefaultConfig()` (it decodes into a zero-valued `config.Config`) and can persist an explicit `false` even on an image build, which the docs MUST warn against. An operator MAY still explicitly set `require_mcp_auth: false` in their own mounted config, or override the container's command with `--require-mcp-auth=false`, to turn it back off. Web UI and REST API auth are unaffected by this default — they already always require the API key regardless of listen address; only `/mcp`'s separate `require_mcp_auth` gate is in question.

**Documentation**

- **FR-037**: The installation guide MUST gain a "Docker (Personal edition)" section covering: variant choice, tags, the data volume (including the network-filesystem warning from Definitions/D21 — the volume MUST be a local filesystem, never NFS/SMB/CIFS or an NFS-backed Kubernetes `StorageClass`), first-run API key retrieval, ownership of bind mounts, probes, secrets, unsupported features, OAuth caveats, Docker isolation with its warning, upgrades and downgrades (including the recommendation, per the Edge Cases "Rollback / downgrade" entry, to take a config/data backup before any tag change in either direction — not only before a downgrade), and migration from self-built images. It MUST also recommend pinning `npx`/`uvx` package versions (or an equivalent lockfile/digest pin) in stdio server commands, since a floating package name is a typosquatting and floating-tag risk the images cannot mitigate on the operator's behalf. The existing Server section MUST stay accurate.
- **FR-038**: A ready-to-copy docker-compose example for the full variant MUST be published in the docs and kept in the repository, and CI MUST build and run it as part of the test suite (not merely a documented manual test), so the example cannot silently drift from the shipped image. It MUST also set `stop_grace_period: 60s` (FR-019a). Because FR-036 makes `require_mcp_auth` the image default, any step in this example that calls `/mcp` MUST use a credential (the read-back API key, FR-014, or a token from `mcpproxy token create`) rather than assuming an open endpoint.
- **FR-039**: The README MUST gain a short "Run in Docker" section with the one-line command and a link to the installation guide. If that section shows an `/mcp` call, it MUST show the credential it uses, consistent with FR-036's default.
- **FR-040**: A minimal Kubernetes example (Deployment, PVC, Secret, probes, security context) MUST be included in the installation guide, with `terminationGracePeriodSeconds: 60` (FR-019a) and a PVC backed by a local-filesystem `StorageClass`, not an NFS-backed one (Definitions, Data volume). Because a freshly provisioned PVC is typically root-owned regardless of the pod's `runAsNonRoot`/`runAsUser` settings, the example's pod `securityContext` MUST also set `fsGroup: 65532` (so the volume's group ownership and permissions are fixed to match the runtime user before the container starts), so the example passes FR-013's write probe on first apply rather than crash-looping on a root-owned volume.
- **FR-041**: A local build target MUST exist for each Personal variant, with the same version and commit stamping as the release builds. It MUST NOT stamp the same `image_variant` telemetry marker (FR-033) that official CI-published images carry — a locally built image reports no `image_variant`, so SC-008's "non-empty `image_variant`" metric cannot be satisfied by a self-built image and stays a true measure of official-image adoption. It MUST stamp the separate FR-036 build-time boolean exactly as the official Dockerfiles do (both read the same Dockerfile build stage), so a locally built full or slim image still defaults `require_mcp_auth` to `true` on `/mcp`, disables the local tray socket by default (FR-027), and defaults to console-only logging (FR-042) — the local target differs from the official build only in the `image_variant` marker it omits, not in this boolean.
- **FR-042**: Console logging (stderr) is already on by default in every edition, including `serve` mode (`cmd/mcpproxy/main.go`'s `EnableConsole: true` default, `internal/config/config.go`'s matching default; research R15) — so `docker logs`/`kubectl logs` already shows proxy output today, and this FR is not fixing a silent-hang gap. The actual gap is the desktop default *additionally* enabling file logging under `/data`, which duplicates the console stream into a file the container image's data volume did not ask for. Image builds MUST default to console-only logging (the additional file sink OFF) so `/data` does not silently accumulate log files on every boot. An operator MAY still opt into file logging under `/data` explicitly.

### Key Entities

- **Personal image variant**: name (`full` or `slim`), image reference, tags, contained runtimes, default user, data path, entrypoint and default command.
- **Data volume**: config file, database, search index, keys and optional logs; owned by the runtime user; one running container at a time.
- **Image variant marker**: build-time value reported by the binary, in telemetry, the Web UI and update guidance (FR-032).
- **File secret reference**: a config reference to an absolute path; resolved at use; never persisted as a value.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A user with Docker installed and no MCPProxy experience goes from the docs page to a successful tool call on an `npx`- or `uvx`-based stdio server through the full image in under 5 minutes, excluding image download time, in 4 of 5 moderated or scripted walk-throughs.
- **SC-002**: Per-platform uncompressed size, measured with `docker image inspect --format='{{.Size}}'` (or `docker manifest inspect` per platform digest) on the pulled single-platform image, not `docker images`' human-readable listing, which includes multi-platform manifest-list overhead and any attestation manifests (research P3). Attestations and SBOM (FR-005) do not count toward the budget. Full variant ≤ 250 MB, slim variant ≤ 30 MB. The prototype measured 168.7 MB and 18.6 MB on arm64.
- **SC-003**: Across 10 remove-and-recreate cycles on the same volume, including one tag upgrade, 100% of cycles keep the same API key, servers, approvals and activity history.
- **SC-004**: On a fresh named volume, 100% of first starts of both variants reach readiness with no manual step; on a root-owned bind mount, 100% of failed starts print the fix.
- **SC-005**: With no upstreams, the container passes readiness within 30 seconds of start on a 2-vCPU host, and stops within 60 seconds of SIGTERM.
- **SC-006**: Every stable release publishes both variants for both architectures; zero Personal images are published for RC tags; the image publish adds no more than 10 minutes to the release's critical path.
- **SC-007**: The Server image's tags, entrypoint, user and state path are byte-for-byte unchanged in its image configuration, apart from labels and the variant marker; the previous release's documented run command works unmodified.
- **SC-008**: Within 90 days of the first release, at least 30% of Personal-edition container installs reporting telemetry come from an official image (non-empty `image_variant`).
- **SC-009**: Issues asking for a Personal Docker image, or reporting that stdio servers cannot start in the official image, drop to zero after release, excluding runtimes the full variant documents as not included.

## Assumptions

- **Default tag is full.** Most container users need stdio servers (US1); the ~169 MB cost (SC-002's measured 168.7 MB; D10 flags the Docker CLI addition as needing re-measurement) is acceptable for the default. Slim gets the suffix. (research D1)
- **Slim is distroless, full is Debian slim.** Slim keeps the Server image's minimal base with the non-root user; full uses a Debian slim base because it needs a shell, apt packages and glibc for Node.js and uv. (research D2)
- **UID/GID 65532**, the distroless `nonroot` convention, for both variants. (research D4)
- **The Server image keeps running as root at `/root/.mcpproxy`.** Changing it would break US6; unifying it with this model is a follow-up.
- **Tags follow the Server image**: exact version with the `v` prefix plus one moving tag per variant. Minor-version rolling tags (`:v0.70`) are not published in this spec.
- **QEMU emulation is accepted** for the full variant's arm64 package layer; the prototype cross-built it in about 1.5 minutes. Native per-arch runners are a fallback if SC-006 fails. (research D7)
- **Node.js LTS and uv versions are pinned per release** and bumped by the existing dependency automation. The prototype's Node 20 is past end of life and its uv is old; the plan picks current versions. (research D6)
- **Docker CLI but no daemon in full** (for socket-mounted isolation only). Docker-in-Docker is not supported.
- **The `${file:}` provider is in scope** (US7, P3); it is small, and it is the idiomatic container secret form. It can ship after the images without blocking them.
- **The package visibility flip is manual.** A GHCR package created by the workflow token starts private; an org admin makes `mcpproxy` public once after the first publish. This is a required release-checklist step (FR-003), not merely an assumption.
- **No Docker Hub mirror.** GHCR matches the Server image and needs no extra credentials.
- **The MCP registry `server.json` stays unchanged.** Container images do not fit its package types (see `docs/mcp-registry-publishing.md`).
- **The docs site needs no allowlist or sidebar change**, because the new content goes into the existing installation page. A separate Docker page would need both.

## Out of Scope

- A Helm chart or operator (a follow-up once the image is stable).
- A Docker Hub (or other registry) mirror.
- Per-user credentials, multi-user or SSO features in the Personal image (that is the Server edition).
- Changing the Server image's base, user or state path.
- Isolation sandbox image defaults, including a git-capable default for `uvx` servers (#1143).
- Docker-in-Docker.
- Runtimes beyond Node.js, Python/uv and git in the full variant (for example Java, Go, Bun, Deno). Users extend the image with their own `FROM`.
- Publishing images for RC tags.
- Windows container images.

## Commit Message Conventions *(mandatory)*

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Message
```
feat(docker): publish Personal-edition full and slim images

Related #40

Adds ghcr.io/smart-mcp-proxy/mcpproxy (full default, -slim variant),
non-root with a fixed /data volume, gated on the release QA gate.

## Changes
- New Personal-edition Dockerfile targets (full, slim)
- Release job publishing both variants for amd64 and arm64
- Installation docs, compose example, README section

## Testing
- Local build and run of both variants; stdio npx/uvx servers on full
- Workflow audit test for QA-gate dependency
```
