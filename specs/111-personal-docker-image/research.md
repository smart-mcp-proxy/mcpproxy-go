# Research: Personal-Edition Docker Images (Spec 111)

**Date**: 2026-09-26
**Spec**: [spec.md](spec.md)
**Method**: code reading of the current tree (`01ccfdf51`), the release workflows, the 2026-09-14 Server-edition research report and its internal inventory, GitHub issues, telemetry, and a local build-and-run prototype of both variants (section "Local verification").

## Clarifications (resolved 2026-09-26)

| # | Question | Where | Resolution |
|---|---|---|---|
| Q1 | Must this spec ship an OAuth sign-in flow that works through Docker port publishing, or only document the limitation? | spec FR-026, US5 | **Resolved: A.** Document the limitation only (host networking on Linux, or sign in on a desktop install and migrate that install's persisted OAuth token state — the data directory's database, not `mcp_config.json` — into the container's data volume with both proxies stopped). A container-friendly flow (paste-the-redirect-URL, option C) is a follow-up spec, not this one. See D11. |
| Q2 | Should the Personal images require MCP authentication on `/mcp` by default? | spec FR-036 | **Resolved: B, full/slim only (never the Server image).** The Personal images (full and slim) default `require_mcp_auth` to `true`; the desktop default and the Server image's default stay `false`. Applied via `DefaultConfig()` reading a new build-time boolean stamped only by the full/slim Dockerfiles (not the CI-only `image_variant` marker, which round 5 review found would leave FR-041 local builds with the default unset if reused here), since `require_mcp_auth` has no `MCPPROXY_*` env override. See D12. |

---

## Evidence base

### Demand

- **Telemetry, 90 days to 2026-09-14** (`docs/research/server-edition-2026-09-14/evidence/internal-inventory.md` §3.5): 362 Personal-edition installs from 86 distinct IPs ran in containers; 113 of them (32 IPs) had ≥ 24 h uptime. Server-edition container installs in the same window: 37 installs from 8 IPs, all self-built before the public Server image existed (the source gives no long-lived breakdown for the Server-edition cohort). Personal headless non-container Linux adds 153 long-lived installs. About 10× as many container installs run the Personal edition as the Server edition overall (362 vs. 37) — this is a total-population comparison, not a long-lived-to-long-lived one — and none of them has an official image.
- **Issue #40** (closed 2026-05-01, Kubernetes homelab SRE): asked for an image; use case was mounting an image in Kubernetes 1.33 to front a stdio HomeAssistant MCP server. A community contributor built a slim (distroless) + full image split on a branch on 2025-09-29.
- **Issue #1171** (closed 2026-09-12, Kubernetes vendor employee): asked for the disabled image workflow to be re-enabled. Resolved for `mcpproxy-server` only.
- **Issue #1143** (closed, priority/high): the isolation sandbox's default uv image is `-slim` and has no git, which breaks every `git+https://` server under Docker isolation. This is the sandbox image, not the product image; it shows that "full" must include git.
- **Discussion #948**: discoverability complaint; supports adding a README section, nothing more.
- **Server-edition report recommendations 1-3** (`docs/research/server-edition-2026-09-14/README.md` §7): recommendation 1 is to publish the Personal edition as the primary image in slim and full variants; recommendation 2 is to run it non-root; recommendation 3 is to ship docker-compose and Kubernetes examples that pre-solve the day-1 traps. Together they are the basis for this spec's scope.

### Runtime facts from the code

| # | Fact | Evidence | Consequence for the spec |
|---|---|---|---|
| R1 | Without `--config`, the config file is looked up only in cwd and `$HOME/.mcpproxy`. `--data-dir` / `MCPPROXY_DATA` are applied afterwards as a process-only override | `internal/config/loader.go:126-287`, `:767-769`; `cmd/mcpproxy/main.go:777-779` | Data-dir alone leaves the config (and the API key) outside the volume, so the key rotates when the container is recreated. The images must pass `--config /data/mcp_config.json` together with `--data-dir /data`, which goes through `EnsureConfigFile` + `LoadFromFile` (`main.go:745-761`). FR-010 |
| R2 | Only `env` and `keyring` secret providers exist | `internal/secret/resolver.go:17-18, 308-330` | `${keyring:}` cannot work headless; `${file:}` does not exist. FR-029, FR-030 |
| R3 | Container detection (`/.dockerenv`, `/run/.containerenv`) feeds `env_kind`; the `docker` channel never gets a self-update command | `internal/telemetry/env_kind.go:167-208`; `internal/updatecheck/channel.go:39-66,138-172`; `guidance.go:50-84` | No new self-update work. The build marker must be exactly `docker`: an unknown marker makes `DetectChannel` return `unknown` (see P5). The comment "No official image is published today" in `guidance.go` becomes stale. FR-031, FR-032 |
| R4 | Heartbeat has no image-variant field | `internal/telemetry/telemetry.go:206-236` | New field needed. FR-033 |
| R5 | OAuth callback listener binds to a random port on `127.0.0.1` unless `oauth.redirect_uri` pins it; redirect hosts are restricted to loopback names | `internal/oauth/config.go:1486-1556`; `internal/config/oauth_validation.go:101-142` | Docker `-p` delivers to the container's interface address, not to its loopback, so a browser on the host cannot reach the callback. Q1 |
| R6 | Docker isolation needs a resolvable `docker` binary and a reachable daemon; disabled by default | `internal/runtime/runtime.go:3651-3684`; `internal/shellwrap/shellwrap.go:171-360`; `internal/config/config.go:1597-1600` | Isolation in a container means mounting the host socket (siblings, not children). FR-022 to FR-024 |
| R7 | A plain (non-Docker-isolation) stdio spawn runs `<shell> -l -c "<command>"`; shell is `$SHELL` or hard-coded `/bin/bash`. A Docker-isolation-based spawn direct-execs the resolved `docker` binary without a login shell when it resolves to a verified absolute executable and the daemon env is guaranteed, falling back to the same shell-wrap otherwise (`internal/upstream/core/connection_docker.go:159-199`) | `internal/shellwrap/shellwrap.go:26-137`; `internal/upstream/core/connection_stdio.go:450` | Any shell-less image fails every plain `npx`/`uvx` stdio spawn (confirmed in the prototype: `fork/exec /bin/bash: no such file or directory`), since those go through the login-shell path. PATH must survive a login shell. FR-007, FR-008 |
| R8 | Current Dockerfile: Server edition (`-tags server`), distroless static, root, no shell, `ENTRYPOINT ["mcpproxy","serve","--listen","0.0.0.0:8080"]` | `Dockerfile:1-36` | The Personal images need new build targets; the Server target stays unchanged. US6 |
| R9 | The Connect wizard reads and writes client config files on the machine the proxy runs on | `internal/httpapi/connect.go:48-69,314` | In a container it would write files no client reads. FR-025 |
| R10 | `/healthz`, `/readyz`, `/livez` exist; SIGTERM gives graceful shutdown with a 60 s hard deadline (exit 6). No no-shell health-check invocation (`healthcheck` subcommand or equivalent) exists in `cmd/mcpproxy/` today | `internal/httpapi/server.go:929-935,1223,1301-1311`; `cmd/mcpproxy/signal_handler.go`; grep of `cmd/mcpproxy/` for a `healthcheck` subcommand (none found) | Probe endpoints and shutdown timing are documentation-only for this spec. The no-shell health-check invocation is new backend surface (a small `healthcheck` CLI subcommand), which the Scope Boundary table now reflects instead of saying "no backend change". FR-019 |
| R11 | An auto-generated API key is printed only when stderr is a terminal; logs carry a masked prefix and the config path | `cmd/mcpproxy/api_key_banner.go:50-120` | `docker logs` of a detached container never shows the key (correct), so a shell-free read-back command is needed. `docker run -it` shows it once. FR-014 |
| R12 | The data-dir permission check requires the directory to be owned by the running UID, else exit 5 | `internal/server/listener_permissions_unix.go:23` | Explains the prototype's slim first-boot failure (P1). FR-012. FR-013 as specified (write-probe failure, regardless of recorded owner — added to reconcile FR-011's arbitrary-UID support with the exit-5 message) requires changing this ownership check to a write probe; see D17, which the plan must carry as an implementation task, not just a spec amendment |
| R13 | A second process opening the locked BBolt database returns `*storage.DatabaseLockedError` (or `bbolterrors.ErrTimeout`), which `classifyError` maps to exit code 3 | `internal/storage/bbolt.go:18-28`; `cmd/mcpproxy/main.go:959-966` | Confirms the exit-3 claim in spec.md (Trap table T2, Edge Cases). FR-015 |
| R14 | The Web UI/CLI/status output surfaces no dedicated "tray socket" or "tray menu" affordance today; the local IPC (Unix) socket exists purely as a tray↔core transport with no user-facing hint text in `internal/httpapi/` | grep of `internal/httpapi/` for tray-facing strings (none found beyond the socket transport itself) | FR-027 is a preventive requirement (no regression to add such hints in image builds) rather than a removal of an existing surface; there is no code fact showing a surface that must be hidden today. FR-027's socket-disabled-by-default amendment is new behaviour, not preventive |
| R15 | File logging is on by default for `serve` mode **in addition to** console logging, not instead of it: `applyServeLoggingFlags` defaults `EnableConsole: true` (`cmd/mcpproxy/main.go:849`) and `DefaultConfig` does the same (`internal/config/config.go:1768`); `internal/logs/logger.go:66-74` writes to `os.Stderr` whenever `EnableConsole` is true, independent of `EnableFile`. The code comment "Enable file logging by default" (`main.go:865`) is about the *file* sink only | `cmd/mcpproxy/main.go:840-849`; `internal/config/config.go:1768`; `internal/logs/logger.go:66-74` | Left unchanged, an image build already writes to stderr (so `docker logs`/`kubectl logs` shows output today, contrary to the "shows nothing" framing this row previously used) **and additionally** writes a file under `/data`. The real gap is the extra file sink under the data volume growing unbounded and mixing operator concerns, not a silent hang. FR-042 should be reworded from "image builds MUST default to logging to stdout/stderr rather than the desktop default of file-only logging" (a default that does not exist) to "image builds MUST default to console-only logging (disable the additional file sink) so the data volume does not silently accumulate log files that duplicate `docker logs`/`kubectl logs`, with an explicit opt-in to also write files under `/data`" — the adopted FR-042 (spec.md) landed as this MUST, strengthened from this row's original SHOULD proposal. |

### CI and release facts

| # | Fact | Evidence | Consequence |
|---|---|---|---|
| C1 | `build-docker` job: `needs: [build, qa-gate]`, stable tags only (`!contains(github.ref_name, '-')`), `linux/amd64,linux/arm64` by Go cross-compile (no QEMU), pushes `mcpproxy-server:<tag>` and `:latest` with OCI labels | `.github/workflows/release.yml:1248-1291` | New jobs copy this shape. FR-001 to FR-005 |
| C2 | `TestPublishJobsGatedOnQAGate` fails if any job using `build-push-action` is not transitively behind the QA gate | `cmd/release-gate/workflow_audit_test.go:23-60,100-135` | New jobs must `needs: qa-gate` (directly or through `build-docker`). FR-003 |
| C3 | `prerelease.yml` publishes no image | grep of `prerelease.yml` | RC tags publish nothing. FR-002 |
| C4 | A GHCR package first created by `GITHUB_TOKEN` is private | comment at `release.yml:1264-1291` | Manual one-time visibility flip for `mcpproxy`. Assumptions |
| C5 | No SBOM or provenance on the Server image push itself (`release.yml:1264-1291`); the pinned `build-push-action` supports both, and the workflow already generates SBOM/provenance for release *binary* artifacts via a separate SBOM action and SLSA generator step (`release.yml:1417-1418`, `:1864-1877` — not verified byte-for-byte against current line numbers this round) | `release.yml:1264-1291` (image push job); `:1417-1418`, `:1864-1877` (binary-artifact SBOM/provenance, a distinct step) | FR-005 asks for SBOM/provenance on the new *image* pushes (SHOULD), which is separate work from the existing binary-artifact tooling; retrofitting the Server image push is optional |
| C6 | `make build-docker` builds only the Server image | `Makefile:109-112` | New local targets. FR-041 |
| C7 | `server.json` has `packages: []` on purpose | `server.json`; `docs/mcp-registry-publishing.md:98-103` | No registry change. Assumptions |
| C8 | `installation.md` is already in the docs sidebar (hand-authored entry) | `website/sidebars.js:25-32` | Adding a section needs no site config change. Assumptions |

---

## Local verification (2026-09-26)

Built and run on Docker Desktop, Darwin/arm64. Full command log: a scratchpad `proto/commands.log` (1014 lines) from the prototyping session, not committed to the repository and not present in this worktree — the summary below is what remains of it. Both images were built from this worktree's source with the Dockerfiles below. A root helper container seeded `/data/mcp_config.json` into each named volume with an `api_key` and two stdio servers (`everything` via `npx -y @modelcontextprotocol/server-everything`, `time` via `uvx mcp-server-time`).

### Results

| Check | slim | full |
|---|---|---|
| Base | `gcr.io/distroless/static-debian12:nonroot` | `debian:bookworm-slim` + Node.js 20 (NodeSource), uv/uvx 0.5.11, python3/pip/pipx, git, tini |
| Build time (native arm64) | ~2 m 2 s | ~1 m 3 s (warm Go cache) |
| Size, `docker image inspect .Size` | 18,638,689 B (18.6 MB) | 168,719,287 B (168.7 MB) |
| Runs as non-root | 65532, from `docker inspect`'s `Config.User`/the Dockerfile's `USER` directive — the slim base is distroless with no shell, so `id -u` could not be run inside it; only `full`'s value was checked with an in-container `id -u` | 65532 (`id -u`) |
| First start on the prepared volume | **Failed, exit 5**: `permission error for /data: data directory not owned by current user (uid=0, expected=65532)`. Started after `chown -R 65532:65532` from a helper container | Started |
| `/healthz` | `{"status":"ok"}` | `{"status":"ok"}` |
| `/ui/` | 200 | 200 |
| MCP `initialize` + `tools/call retrieve_tools` on `/mcp` | OK (no upstream tools) | OK; BM25 results from both upstreams |
| stdio via `npx` | Structured failure: `fork/exec /bin/bash: no such file or directory` (`MCPX_STDIO_SPAWN_ENOENT`), proxy stays healthy | `everything`: connected, ready, 13 tools |
| stdio via `uvx` | Same structured failure | `time`: connected, ready, 2 tools |
| git present | No | 2.39.5 |
| node / uvx versions | n/a | v20.20.2 / 0.5.11 |
| `docker run IMG version` | `MCPProxy dev (personal) linux/arm64` | Same |
| `docker run IMG mcpproxy version` | not tried | `Error: unknown command "mcpproxy"` (P2) |
| Restart: config, servers and seeded API key kept | Yes | Yes |
| `linux/amd64` cross-build (QEMU, no push) | not tried | Succeeded in 1 m 33 s, ~172 MB; not run |

### Problems and gaps found

- **P1 — slim first boot fails on a root-owned volume.** The distroless image has no `/data` directory, so a new named volume is root-owned and the non-root process exits with code 5. The image cannot chown (no root step, no shell). Fix: create `/data` in the image owned by 65532 (for example `COPY --chown=65532:65532` of an empty directory), because Docker copies an image directory's ownership into a new named volume on first mount. Bind mounts still need a documented `chown`, and the error must name it. **The full-variant result does not prove it is immune:** its volume was first mounted by a root helper, and the log does not show the resulting ownership. The implementation must test first boot on a fresh named volume for both variants, with no seeding. → FR-012, FR-013, SC-004.
- **P2 — repeating the binary name fails.** `docker run IMG mcpproxy version` becomes `mcpproxy mcpproxy version`. → FR-016, FR-017.
- **P3 — misleading size listings.** `docker images` showed 714 MB for full (the BuildKit manifest list including attestation manifests) against 168.7 MB from `docker image inspect`. Size checks must use per-platform image size. → SC-002.
- **P4 — the API-key bootstrap was not tested.** The prototype seeded `api_key`, so it proved persistence of a seeded key, not auto-generation on `/data` or read-back without a shell. → FR-014 and US2 need a test.
- **P5 — wrong channel marker.** The prototype stamped `updatecheck.buildChannel=docker-full` / `docker-slim`. Those are not in `knownChannels`, so `DetectChannel` returns `unknown` and never reaches the `/.dockerenv` heuristic. Stamp `docker`, and carry the variant in a separate marker. → FR-031, FR-033.
- **P6 — stale runtime versions.** Node.js 20 reached end of life on 2026-04-30; uv 0.5.11 is from late 2024; `debian:bookworm` is now oldstable (Debian 13 "trixie" is stable). → FR-007, D6.
- **P7 — build-only packages left in the full image.** `curl` and `gnupg` are installed to add the NodeSource key and stay in the image. `pipx` is not needed next to `uvx`. Removing them saves space and attack surface.
- **P8 — not tested:** read-only root filesystem, arbitrary UID, SIGTERM timing, a `git+https://` uvx server, Docker isolation with a mounted socket, OAuth upstreams, amd64 runtime. Each maps to an acceptance scenario in the spec.

### Prototype Dockerfiles (as built)

Both files are copied verbatim from the scratchpad prototype. They are evidence, not the implementation; P1, P5, P6 and P7 apply.

#### `Dockerfile.slim`

```dockerfile
# Prototype: Personal edition, slim (distroless static nonroot) - spec111 proto
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS builder

RUN apk add --no-cache git nodejs npm make

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN cd frontend && npm ci && npm run build
RUN mkdir -p web/frontend && cp -r frontend/dist web/frontend/

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ARG TARGETOS
ARG TARGETARCH
# NOTE: no "-tags server" => personal edition binary
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build \
    -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE} -X github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi.buildVersion=${VERSION} -X github.com/smart-mcp-proxy/mcpproxy-go/internal/updatecheck.buildChannel=docker-slim -s -w" \
    -o /mcpproxy ./cmd/mcpproxy

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /mcpproxy /usr/local/bin/mcpproxy

USER 65532:65532
VOLUME ["/data"]
EXPOSE 8080

ENTRYPOINT ["mcpproxy"]
CMD ["serve", "--listen", "0.0.0.0:8080", "--data-dir", "/data", "--config", "/data/mcp_config.json"]
```

#### `Dockerfile.full`

```dockerfile
# Prototype: Personal edition, full (node+uv+git for stdio upstreams) - spec111 proto
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS builder

RUN apk add --no-cache git nodejs npm make

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN cd frontend && npm ci && npm run build
RUN mkdir -p web/frontend && cp -r frontend/dist web/frontend/

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build \
    -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE} -X github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi.buildVersion=${VERSION} -X github.com/smart-mcp-proxy/mcpproxy-go/internal/updatecheck.buildChannel=docker-full -s -w" \
    -o /mcpproxy ./cmd/mcpproxy

# uv/uvx from astral's published distroless-ish image
FROM ghcr.io/astral-sh/uv:0.5.11 AS uv

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl gnupg git tini python3 python3-pip pipx \
    && mkdir -p /etc/apt/keyrings \
    && curl -fsSL https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key | gpg --dearmor -o /etc/apt/keyrings/nodesource.gpg \
    && echo "deb [signed-by=/etc/apt/keyrings/nodesource.gpg] https://deb.nodesource.com/node_20.x nodistro main" > /etc/apt/sources.list.d/nodesource.list \
    && apt-get update && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

COPY --from=uv /uv /uvx /usr/local/bin/
COPY --from=builder /mcpproxy /usr/local/bin/mcpproxy

RUN groupadd -g 65532 mcpproxy && useradd -u 65532 -g mcpproxy -m -d /home/mcpproxy mcpproxy \
    && mkdir -p /data && chown -R mcpproxy:mcpproxy /data /home/mcpproxy

USER mcpproxy:mcpproxy
ENV HOME=/home/mcpproxy
VOLUME ["/data"]
EXPOSE 8080

ENTRYPOINT ["tini", "--", "mcpproxy"]
CMD ["serve", "--listen", "0.0.0.0:8080", "--data-dir", "/data", "--config", "/data/mcp_config.json"]
```

---

## Decisions

### D1 — Full is the default tag; slim is the suffix
- **Decision**: `ghcr.io/smart-mcp-proxy/mcpproxy:<version>` and `:latest` are the full variant; `:<version>-slim` and `:slim` are the slim variant.
- **Rationale**: The demand (#40, homelab and Kubernetes users) is for stdio servers, which only the full variant can run. A user who pulls the obvious tag should get a working setup. The `-slim` suffix matches common image conventions (`python:3.13-slim`, `node:22-slim`) and the uv isolation image already used in this codebase.
- **Alternatives considered**: slim as default with `-full` suffix (smaller default pull, but the default then fails the main use case with a spawn error); two repositories `mcpproxy` and `mcpproxy-full` (two packages to make public and document, no benefit); `:latest-slim` instead of `:slim` (longer, and `:slim` already reads as "latest slim").

### D2 — Base images: distroless for slim, Debian slim for full
- **Decision**: slim on `gcr.io/distroless/static-debian12:nonroot` (the Server image's base family); full on a current Debian slim release.
- **Rationale**: slim needs nothing but a static binary. Full needs a shell at `/bin/bash` (R7), glibc for Node.js and uv binaries, and apt for git and Python. The prototype showed both work.
- **Alternatives considered**: Alpine for full (musl breaks some prebuilt npm and Python wheels that stdio servers pull at run time); multi-stage copy of Node.js from `node:<lts>-slim` instead of NodeSource apt (valid and avoids leaving `curl`/`gnupg` in the image; the plan may choose it); a single image with optional runtimes (no size win for anyone).

### D3 — Always pass an explicit config path on the volume
- **Decision**: the default command passes `--data-dir /data --config /data/mcp_config.json`.
- **Rationale**: R1. The prototype confirmed the config and key persist across restart with this pair.
- **Alternatives considered**: fixing `MCPPROXY_DATA` so it also moves the config lookup (right long-term, but a behaviour change for every edition and outside this spec; the docs keep the Server-image warning until then); mounting the volume at `$HOME/.mcpproxy` (ties state to the home path and to the user, which changes with an arbitrary UID).

### D4 — Non-root UID/GID 65532, `/data` owned by it in the image
- **Decision**: both variants run as 65532:65532 and ship `/data` owned by that user.
- **Rationale**: the distroless `nonroot` convention; Kubernetes `runAsNonRoot` works without overrides. Owning `/data` in the image fixes P1 for named volumes.
- **Alternatives considered**: a root entrypoint that chowns and drops privileges with `gosu`/`su-exec` (fixes bind mounts too, but the container starts as root, needs a shell or helper in slim, and conflicts with `runAsNonRoot`); root like the Server image (rejected by the research report's recommendation 2).

### D5 — Entrypoint is the binary; default command is `serve`
- **Decision**: `ENTRYPOINT ["mcpproxy"]` (slim) and `["tini","--","mcpproxy"]` (full); default `CMD` is `serve --listen 0.0.0.0:8080 --data-dir /data --config /data/mcp_config.json`.
- **Rationale**: `docker run IMG version|doctor|upstream list` works (verified). The full variant needs an init process because the proxy spawns and must reap many child processes.
- **Alternatives considered**: the Server image's fixed `ENTRYPOINT [... "serve", ...]` (subcommands impossible without `--entrypoint`); a shell entrypoint script (not possible in slim).
- **Note**: overriding `CMD` replaces the data-dir and config flags too; `docker run IMG serve --log-level debug` would drop them. FR-018 makes keeping the data volume paths under a partial override an unconditional MUST on the image/binary's own behaviour (for example by having the image set the config and data paths through environment defaults the binary reads instead of only through `CMD` flags), which a documentation example cannot substitute for enforcing — the plan must implement that defaulting mechanism, not just document the full command.

### D6 — Pin current runtime versions
- **Decision**: full ships a Node.js release in active or maintenance LTS at release time, a current uv, and a current Debian stable base; versions are pinned in the build and updated by the dependency automation.
- **Rationale**: P6. Node.js 20 is end of life.
- **Alternatives considered**: floating tags such as `node:lts` (non-reproducible builds).

### D7 — Multi-arch by QEMU for the full variant's package layer
- **Decision**: build both architectures in one `buildx` call; the Go binary stays cross-compiled natively, and only the apt/uv layers of full run under emulation.
- **Rationale**: the prototype cross-built full for amd64 under QEMU in 1 m 33 s; package installs are not the 5–10× cargo compile that forced native runners for the scanner images (`scanner-images.yml:73-89`).
- **Alternatives considered**: native per-arch runners plus `imagetools create` (the ramparts pattern), kept as the fallback if SC-006's 10-minute budget is missed.

### D8 — Publishing lives next to `build-docker`, gated on the QA gate
- **Decision**: a new job (or matrix) in `release.yml` with `needs: [build, qa-gate]` and the same stable-only guard; nothing in `prerelease.yml`.
- **Rationale**: C1–C3. Keeps `TestPublishJobsGatedOnQAGate` green.
- **Alternatives considered**: a separate workflow triggered on release publication (the audit test only inspects the two release workflows, so the gate would not be enforced).

### D9 — Tolerate a repeated binary name
- **Decision**: when the first argument equals the binary name, drop it (assumption in FR-017).
- **Rationale**: P2 is a common habit from images whose entrypoint is a shell; dropping it is harmless because `mcpproxy` is not a subcommand name.
- **Alternatives considered**: document only (the prototype's result shows the error is confusing); print a targeted error (acceptable fallback).

### D10 — Docker isolation: opt-in with the host socket; CLI only
- **Decision**: isolation stays off by default; full ships the Docker CLI (no daemon) so isolation works when the operator mounts `/var/run/docker.sock`, with a prominent warning.
- **Rationale**: R6. The full variant already contains the runtimes, so most users need no isolation. Mounting the socket grants root-equivalent host control, so it must be a deliberate choice.
- **Alternatives considered**: no Docker CLI and isolation documented as unsupported (smaller image, but blocks users who want per-server sandboxes on a dedicated box); Docker-in-Docker (privileged container, rejected).
- **Size check**: earlier text here estimated the static Docker CLI at roughly 20–40 MB; that is likely about 2× too low — current static Linux `docker` client release binaries (checked against Docker's own published `docker-<version>.tgz` static binary bundles for `linux-arm64`/`linux-x86_64`, not vendored in this repo so not independently re-measurable here) are closer to 60–80 MB. Against the 168.7 MB full-variant prototype baseline and SC-002's 250 MB cap, that leaves roughly 80 MB of headroom for the CLI plus everything else still to add (pinned current Node.js LTS, current uv, any additional hardening), not the far larger headroom the original 20–40 MB estimate implied. The plan MUST re-measure the actual static Docker CLI size once vendored and confirm the budget still holds; this is flagged low-confidence/low-severity because it is general knowledge about current release artifact sizes, not a fact this repository's own code or build output can confirm today.

### D11 — OAuth upstream sign-in (Q1, resolved: A)
- **Facts**: R5. The browser runs on the user's machine; the callback listener runs on the container's loopback, which `-p` does not reach. `--network host` works on Linux only (not Docker Desktop's default). Upstream OAuth tokens persist in the data directory's BBolt database via `PersistentTokenStore` (`internal/oauth/persistent_token_store.go`, `oauth_tokens` bucket, `internal/storage/bbolt.go`), never in `mcp_config.json` — a round-5 review finding corrected an earlier draft that said "import the token into the config".
- **Options**: (A) document the limitation and workarounds (host networking; sign in on a desktop install and migrate that install's persisted OAuth token state); (B) add a container-friendly flow: a configurable callback bind address plus a fixed port, keeping the loopback redirect URI that the provider sees; (C) a paste-the-redirect-URL completion step in the Web UI, which works through any network setup.
- **Decision**: A, resolved 2026-09-26 (spec Clarifications). C is recommended as a follow-up spec, because C also helps headless Linux installs and SSH users, but it does not ship with this spec. The Web UI/CLI sign-in surface for an upstream needing OAuth, when not running with `--network host`, must show the limitation and both workarounds rather than a dead redirect (spec FR-026, US5 scenario 4); under `--network host` the normal flow completes and no such message is shown. The desktop-migration workaround is a manual data-directory copy with both proxies stopped — this spec defines no export/import command.

### D12 — MCP authentication default in the images (Q2, resolved: B, full/slim only)
- **Facts**: `/mcp` is unauthenticated unless `require_mcp_auth` is on; the image listens on all interfaces inside the container; the docs publish on `127.0.0.1` by default (FR-035), but the homelab story exposes the port on the LAN, and a full-variant proxy can start commands on the host container. `require_mcp_auth` (`internal/config/config.go:368`) has no `MCPPROXY_*` environment-variable override — `internal/config/loader.go`'s `applyTLSEnvOverrides` and the other `os.Getenv("MCPPROXY_...")` call sites do not cover it, and `--require-mcp-auth` is a `serve` CLI flag only (`internal/config/process_overrides.go`'s `FieldRequireMCPAuth` records the override once applied, but nothing feeds it from an env var today).
- **Options**: (A) keep the product default and document enabling MCP auth for LAN use; (B) turn MCP auth on by default in image builds only; (C) turn it on only when the listen address is not loopback, in every edition.
- **Decision**: B, resolved 2026-09-26 (spec Clarifications, FR-036), **restricted to the full and slim Dockerfiles — never the Server image.** An earlier round-5 draft of this mechanism gated `DefaultConfig()` on the existing `image_variant` telemetry marker (Definitions) and included `server` in the "non-empty" check; a same-round zcode review (three independent chunks) found this doubly wrong: (a) the marker is CI-only per FR-033/FR-041's own wording ("a locally built image reports no `image_variant`"), so an FR-041 local build would get the default *unset*, defeating the point for exactly the self-builder population (362 installs) this spec's demand evidence cites; (b) including `server` in the check would flip the Server image's `/mcp` default, contradicting the Scope Boundary/US6/SC-007 guarantee that the Server image is unchanged apart from labels and the telemetry marker. Corrected mechanism: `internal/config/config.go`'s `DefaultConfig()` reads a **new, dedicated build-time boolean**, independent of `image_variant`, stamped only by the two new Personal Dockerfiles (full and slim) for both the official CI-published build and the FR-041 local build target, and never stamped by the existing (unchanged) Server Dockerfile. `DefaultConfig()` compiles in `RequireMCPAuth: true` only when this boolean is set. **This is not invent-from-scratch work**: the codebase already has the exact shape needed — `internal/config/build_edition_server.go`/`build_edition_stub.go` define `isServerEditionBuild` as build-tag twins (`//go:build server` / `!server`), consumed at `internal/config/audit_log.go:138` for a per-edition compiled default the code comment itself says is "keyed on the BUILD", and `serveredition_accessors.go`/`serveredition_accessors_stub.go` already tag-split `EffectiveRequireMCPAuth` for this very field. The plan should copy that pattern: a new build tag (e.g. `personalimage`) plus const twin files, not an `-ldflags -X` string, because `-X` can only set string variables and cannot set a `bool` — so the existing `image_variant` string marker must stay an ARG-gated `-X` value (CI-only, per FR-033/FR-041) while this new signal is a separate build-tag-driven const (set whenever either Dockerfile builds, local or CI). Two limitations found in the same review round are carried as documentation requirements rather than solved here: a config file copied in from a desktop/self-built install always carries an explicit `require_mcp_auth` (no `omitempty`), so a foreign config silently keeps `/mcp` open on the official image unless the migration docs (FR-037) tell the operator to set it explicitly; and a whole-document `POST /api/v1/config/apply` that omits the field decodes into a zero-valued `config.Config` (`internal/oauth/configview.go`'s `UnmaskLiveConfigDocument`, confirmed at lines 203-217; its caller `handleApplyConfig` in `internal/httpapi/server.go` applies that result with no defaulting step in between), bypassing `DefaultConfig()` entirely and able to persist an explicit `false` even on an image build — `internal/config/effective_bounds.go` already documents this class of zero-valued-decode bypass and its tri-state-reader mitigation pattern (used elsewhere for #1175-style fields); a tri-state reader cannot rescue a plain `bool` field like `require_mcp_auth` without a type change, which is why this spec documents the gap rather than fixing it. By contrast, `PATCH /api/v1/config` (`handlePatchConfig`) is *not* similarly exposed for this field: it deep-merges onto the fully-serialized stored config, which — because `require_mcp_auth` has no `omitempty` — always already contains the key, so an omitted field in a PATCH body preserves the stored value rather than zeroing it.
- **Alternatives considered**: reusing the `image_variant` marker (rejected per the correction above — CI-only, and includes `server`); a new `MCPPROXY_REQUIRE_MCP_AUTH` env var read at load time (rejected — this decision is about the *compiled-in default* for a build class, which an env var does not model as cleanly as a build-time boolean, and it would need its own precedence rules against the file value); a seeded default `mcp_config.json` baked into the image and copied to `/data` on first run only (rejected — `EnsureConfigFile`'s existing first-run path already creates the default via `DefaultConfig()`, so gating a build-time boolean there is strictly simpler than adding a second, image-specific config-seeding code path that must stay in sync with it).

### D13 — `${file:}` secret references
- **Decision**: in scope as P3 (US7, FR-030). It reads an absolute path at resolve time, strips one trailing newline and, when present, a preceding carriage return (so a CRLF-terminated, Windows-authored secret file resolves the same value as an LF-terminated one), and is resolved like `${env:}`.
- **Rationale**: the idiomatic Docker and Kubernetes secret form (R2). Kubernetes can also expose secrets as environment variables, so this does not block the images. CRLF handling matters because secret files are sometimes authored or edited on Windows before being mounted into a Linux container.
- **Alternatives considered**: document env vars only (works, but secrets end up in the process environment and in `docker inspect`); LF-only stripping (rejected — would silently include a trailing `\r` in the resolved value for CRLF files).

### D14 — Image variant marker and telemetry
- **Decision**: a build-time marker (`full`, `slim`, `server`, empty otherwise) reported as `image_variant` in heartbeats and shown in the Web UI; it also lets update guidance name the right tag (FR-032). This marker is stamped in official, CI-published image builds only — it is empty on an FR-041 local build of any variant, same as on a desktop build (FR-033, FR-041); it is a distinct build-time signal from D12's new full/slim-only boolean, which is stamped in both official and local builds.
- **Rationale**: R4 and P5; the install channel stays `docker`.
- **Alternatives considered**: encoding the variant in the channel (breaks channel detection, P5); inferring it at run time from the presence of `node` (fragile).

### D15 — Connect wizard in images
- **Decision**: in image builds the Connect write endpoints refuse with a reason, and the UI shows the endpoint URL and per-client snippets instead.
- **Rationale**: R9. Writing a file inside the container and reporting success is misleading.
- **Alternatives considered**: hide Connect completely (loses the snippets that users need to configure clients by hand); detect container at run time instead of build time (`env_kind` also flags users who run the desktop binary in a dev container, where Connect may still be useful with mounted configs).

### D16 — Named writable paths under a read-only root filesystem
- **Decision**: when `readOnlyRootFilesystem: true`, the full image's writable surface is the data volume plus one scratch mount at `$HOME` (`/home/mcpproxy`); `XDG_CACHE_HOME` is pointed at that scratch mount, and it is the mechanism that actually reaches spawned stdio processes today. The slim image needs no such scratch mount — it has no package-manager caches to write, so the data volume alone satisfies a read-only root there.
- **Rationale**: `internal/secureenv/manager.go`'s `DefaultEnvConfig` allowlist (lines ~54-108) confirms `HOME` and, on Unix, `XDG_CONFIG_HOME`/`XDG_DATA_HOME`/`XDG_CACHE_HOME`/`XDG_RUNTIME_DIR` pass through `BuildSecureEnvironment` to a spawned stdio process; `npm` and `uv` fall back to `$HOME`-relative caches when no cache env var is set, so naming both `$HOME` and `XDG_CACHE_HOME` covers stdio spawns that read either. **Setting `npm_config_cache` or `UV_CACHE_DIR` in the container's own environment does nothing today**, because neither name is in `isKeyAllowed`'s allowlist (same file, lines ~506-527): `BuildSecureEnvironment` strips both before the child process (npx/uvx) ever sees them, so a container operator who sets them expecting them to redirect the cache is silently ignored. The plan has two options, and must pick one rather than leaving the FR pointing at a mechanism that does not work: (a) add `npm_config_cache` and `UV_CACHE_DIR` to the `secureenv` allowlist as a small backend change (matches the existing pattern used for `XDG_CACHE_HOME` and the MCP-2751 tool-home vars), or (b) drop them from the documented mechanism and rely on `$HOME`/`XDG_CACHE_HOME` alone, since both already reach the child process and cover the common cache-location fallback for `npm`/`uv` when no explicit cache var is set. This closes the gap P8 flagged as untested and gives FR-021 a concrete, code-verified mechanism instead of a plausible-sounding one.
- **Alternatives considered**: rely on `$HOME` and `XDG_CACHE_HOME` alone (works today with no `secureenv` change, but some `npm`/`npx` installs still prefer an explicit `npm_config_cache` when set, so a future allowlist addition remains a documented follow-up rather than a blocker); a `tmpfs` at `/tmp` only (does not cover `$HOME`-anchored caches; still recommended in addition, not instead); document `npm_config_cache`/`UV_CACHE_DIR` as-is without checking the allowlist (rejected — this was the round-1 mistake this decision corrects).

### D17 — FR-013's permission check moves from ownership to a write probe, and the existing chmod-repair branch must also be reworked
- **Decision**: `internal/server/listener_permissions_unix.go`'s data-dir permission check (R12, currently an ownership-equality test against the running UID) changes to an actual write probe (create/remove a marker file, or equivalent), so a group-writable or `fsGroup`-adjusted volume owned by a different UID still passes, matching FR-011's arbitrary-UID support and FR-013's amended wording. **This is not the only change needed.** The same function has a second, independent check after the ownership test: when the directory's permission bits have any group/other bit set (`perm&0077 != 0`), it calls `os.Chmod(dataDir, 0700)` and fails if that chmod errors or does not fully clear the bits (`listener_permissions_unix.go:23-67`). A non-owner UID (exactly the `fsGroup`-adjusted case FR-011/FR-013 must pass, since `fsGroup` makes a volume group-writable, i.e. `perm&0070 != 0`) cannot `chmod` a directory it does not own — `os.Chmod` returns `EPERM` — so this repair branch would still fail the container with exit 5 even after the ownership check is replaced with a write probe. The plan MUST also change this branch: on a non-owner UID, a chmod failure must not be fatal as long as the write probe itself succeeds (the actual thing `serve` needs); the plan should either skip the chmod attempt when the process does not own the directory (only log a warning that the directory's permissions are looser than 0700 and it cannot tighten them), or catch `EPERM` from `os.Chmod` and fall through to the write-probe result instead of returning its own error.
- **Rationale**: round 1 amended FR-013 to trigger "regardless of recorded owner" specifically to stop it contradicting FR-011, but the current code only checks ownership before ever reaching the chmod-repair branch (R12); a write probe replacing the ownership check is necessary but not sufficient, because the chmod-repair branch runs unconditionally whenever the directory is group/other-writable — precisely the `fsGroup` case this FR exists to support — and its failure path returns an error today. Raised independently by two separate zcode review chunks against the same code path, which corroborates it as a real, not marginal, gap. This is real implementation work the plan must schedule, not a documentation change.
- **Alternatives considered**: keep the ownership check and instead require operators to always match the exact UID (rejected — breaks FR-011's arbitrary-UID promise for Kubernetes `fsGroup` setups, which grant group write, not UID-owner equality); check both ownership and group-writability without a live write probe (more permissive than checking supplementary groups reliably in Go without a probe; a live probe is simpler and matches what actually determines whether `serve` can write there); leave the chmod-repair branch as-is and just loosen the ownership check (rejected — this is exactly D17's original gap: the loosened ownership check alone still lets the chmod-repair branch fail a non-owner `fsGroup` volume, contradicting FR-013).

### D18 — Recommend pinning `npx`/`uvx` package versions in server commands (FR-037)
- **Decision**: the installation guide recommends pinning `npx`/`uvx` package versions (or an equivalent lockfile/digest pin) in stdio server commands.
- **Rationale**: this is general package-ecosystem guidance (a floating package name run via `npx`/`uvx` is a typosquatting and floating-version risk in any environment, not one specific to this codebase) rather than a fact discovered by reading this repository; it was added in review round 1 at the maintainer's request as a documentation-only, low-cost mitigation the images cannot enforce on the operator's behalf. No code change follows from it.
- **Alternatives considered**: have the proxy validate or warn on unpinned `npx`/`uvx` commands at config-save time (a real feature, out of scope for this spec — it would need parsing arbitrary shell command strings); leave the risk undocumented (rejected — the risk is the same one #1143's isolation-image git gap already flagged for `uvx`, so it is worth naming here too).

### D19 — Documented examples must raise the orchestrator's default grace period to match the existing 60 s stop bound
- **Decision**: the compose and Kubernetes examples (FR-038, FR-040) set `stop_grace_period: 60s` / `terminationGracePeriodSeconds: 60` explicitly (FR-019a), rather than relying on Docker Compose's 10 s or Kubernetes' 30 s defaults.
- **Rationale**: the 60 s bound is the existing hard-shutdown deadline (R10) and is not something this spec can change without touching shutdown behaviour outside its scope; the platform defaults are shorter, so an example that does not override them would have the orchestrator SIGKILL the process before the documented bound is reached, silently truncating the graceful-shutdown window. The Server-edition research report's own Kubernetes recommendation of `terminationGracePeriodSeconds: 45` (`docs/research/server-edition-2026-09-14/README.md:146`) is itself below this spec's 60 s bound, which is further evidence the bound needs an explicit override, not an assumption that a default suffices.
- **Alternatives considered**: lower the documented bound to 30 s to fit Kubernetes' default (rejected — changes existing shutdown behaviour, out of scope); leave the examples silent on grace period and only mention it in prose (rejected — an operator who copies the example verbatim gets truncated shutdown with no signal that anything is wrong).

### D20 — Docker socket GID mismatch, and the isolation status message must distinguish absent-socket from permission-denied
- **Decision**: FR-022's documentation names the `group_add` (Compose/`docker run`) or `supplementalGroups` (Kubernetes) fix for a host socket that is group-owned by a `docker` group the runtime user (GID 65532) is not a member of; FR-024's status message distinguishes "socket not mounted / no CLI" from "socket mounted but permission-denied" rather than treating both as "isolation needs the host socket".
- **Rationale**: the host Docker socket is conventionally `root:docker` mode `660` on Linux distributions that ship a `docker` group (Debian/Ubuntu's `docker.io`/`docker-ce` packages, most Kubernetes node images); a container running as a fixed UID/GID with no matching supplementary group gets `EACCES` on `docker info` even with the socket bind-mounted. `Runtime.IsDockerAvailable()` (`internal/runtime/runtime.go:3651-3699`) probes with `docker info --format {{.ServerVersion}}` and reports only a bool from `cmd.Run()`'s error — it does not branch on the underlying error to tell a permission error apart from a missing binary or an unreachable daemon, so the status-message layer built on top of it (not `IsDockerAvailable` itself) is what must make the distinction, using what it already knows independently about whether the socket path exists and whether the CLI resolved (`GetDockerCLISource`, same file, "path" | "bundled" | "login_shell" | "absent").
- **Alternatives considered**: run the full variant's Docker CLI as root regardless of the process UID via a setuid helper (rejected — reintroduces a root-equivalent process inside the container, defeating FR-011's non-root default); document only "add the runtime user to the host's docker group" without the Kubernetes `supplementalGroups` equivalent (rejected — the GID that owns the socket on a Kubernetes node is not always known or settable by the pod spec via `group_add`, so the Kubernetes-specific mechanism needs its own line); silently expand `IsDockerAvailable()` to also expose the raw error (a real, small backend change the plan could still choose to make, but not required to satisfy FR-024 — the status layer can distinguish the two cases from socket-path and CLI-resolution facts it already has, without changing `IsDockerAvailable()`'s bool return).

### D21 — Network-filesystem data volumes are unsupported (general knowledge, not code-sourced)
- **Decision**: the data volume MUST be a local filesystem; NFS, SMB/CIFS and NFS-backed Kubernetes `ReadWriteMany` `StorageClass`es are unsupported and the docs carry an explicit warning (Definitions "Data volume", Edge Cases, FR-037).
- **Rationale**: this is a well-known limitation of BBolt-family embedded databases (and mmap+`flock`-based stores generally): `mmap` over NFS is not coherent across clients and `flock` is unreliable or a no-op on many NFS/SMB implementations, so concurrent or even single-writer access under network filesystems can silently corrupt the file. This is general storage-engine knowledge, the same class of fact as D18's `npx`/`uvx` pinning recommendation, rather than something this repository's code asserts about itself — no `internal/storage/` code inspects or rejects a network filesystem at runtime, so the mitigation here is documentation only, not a code change. (Spec round 1 cited this as "research A2", a numbering scheme this file has never used; that citation is replaced by this entry.)
- **Alternatives considered**: detect a network filesystem at startup and refuse to start (real protection, but no portable, dependency-free way to detect NFS/CIFS mounts from Go across Linux/Windows containers without shelling out to `mount`/`findmnt`, which the slim variant does not have; left as a possible follow-up, not required by this spec); say nothing and let operators discover corruption themselves (rejected — silent data-loss risk for exactly the homelab/NAS persona (US1) most likely to reach for a network share).
