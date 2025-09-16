
# REFACTORING.md — mcpproxy-go Refactor & Release Plan (LLM-Ready)

> **Goal**: Safely refactor `mcpproxy-go` to a **core + tray** split with a **v1 REST API + SSE**, embedded **Web UI**, hardened **OAuth/storage**, and robust **tests/observability** — while preserving the **current hotfix/release workflow** on `main` and running **prerelease** builds from `next`.

---

## Compact Execution Plan (what to do first, what can run in parallel)

**Sequence & Parallelization**  
- **P0–P2 (Release plumbing)** — *Run first on separate PRs*  
  - **P0. Branching & protections** → create `next`, hotfix template; set GitHub Environments.  
  - **P1. CI/CD (stable vs prerelease)** → split workflows; notarize both.  
  - **P2. Updater safety** → ignore prereleases (`-rc`/`-next`) unless canary flag enabled.
- **P3–P6 (Separation & API)** — *Core track*  
  - **P3. Split Tray into its own binary (CGO-on)**  
  - **P4. Add `/api/v1` + SSE**  
  - **P5. Wire Tray → REST/SSE**  
  - **P6. Embed Web UI (go:embed)**  
- **P7–P11 (Resilience & security)** — *Can be parallelized in feature branches that merge into `next`*  
  - **P7. Facades/Interfaces** (contracts + mocks)  
  - **P8. OAuth token store (keyring + age fallback)**  
  - **P9. Circuit breaker + backoff; rate limits**  
  - **P10. Health/Ready + Prometheus + OTel tracing**  
  - **P11. OpenAPI via swaggo; golden tests**
- **P12–P13 (Isolation & packaging)** — *After APIs stable*  
  - **P12. Docker isolation hardening**  
  - **P13. macOS packaging: add Tray.app to DMG; codesign/notarize**

> **Parallelization tips**: P7–P11 can proceed independently against the API surface from P4, provided **facade contracts** are stable. P12 depends on P7/P9. P13 depends on P3 and existing scripts.

---

## Conventions Used in Prompts

- **Autonomy**: Each prompt instructs the assistant to **make code changes**, **open PR(s)**, and **verify** via scripts/commands until the **Exit Criteria** pass.  
- **References**: Internal file paths are relative to repo root; external references include authoritative docs.  
- **Verification**: Every step has **bash** checks and **curl**/CLI probes.  
- **Rollback**: Lightweight undo guidance is included per step.

---

## P0 — Create Branching Model & Protections (main/next/hotfix)

**Context & Motivation**  
Keep `main` hotfixable and release-ready while `next` aggregates refactor work. Use GitHub **Environments** to separate secrets/gates for `production` vs `staging`.  
Refs: GitHub Actions Environments ([docs](https://docs.github.com/actions/deployment/targeting-different-environments/using-environments-for-deployment)), Deploying to environment ([docs](https://docs.github.com/en/actions/how-tos/write-workflows/choose-what-workflows-do/deploy-to-environment)).

**Instructions for the LLM**  
IMPORTANT: never mention claude as co-author in the commit messages of gh PRs.
1. Create branch `next` from latest `main`.  
2. Add `docs/releasing.md` describing **hotfix/x.y.z** flow (branch off tag, fix, tag, merge back to `main` and `next`).  
3. In repo settings (document steps), define environments: **production** and **staging** with appropriate protection rules.  
4. Add a PR template reminding devs to backport hotfixes to `next`.

U can use git and gh cli to do this.

**Verification**  
- `git branch --show-current` on CI shows `next` when building prerelease.  
- Docs exist: `docs/releasing.md`, PR template renders on new PRs.

**Exit Criteria**  
`next` exists, docs merged, and environments listed in repository settings.

**Rollback**  
Delete `next` (not recommended) or revert docs; no code impact.

---

## P1 — Split CI/CD: Stable vs Prerelease

**Context & Motivation**  
Two lanes: **stable** from `main` (SemVer tags `vX.Y.Z`) vs **prerelease** from `next` (tags `vX.Y.Z-rc.N` or `-next.N`). Use GitHub Releases **prerelease** flag; publish notarized DMGs.  
Refs: GitHub Releases ([about](https://docs.github.com/repositories/releasing-projects-on-github/about-releases), [REST API](https://docs.github.com/en/rest/releases/releases)); Environments ([docs](https://docs.github.com/actions/deployment/targeting-different-environments/using-environments-for-deployment)).

**Instructions for the LLM**  
1. Add `.github/workflows/release.yml` (trigger: `v*` tags on `main`, env: `production`).  
2. Add `.github/workflows/prerelease.yml` (trigger: pushes to `next` and tags matching `-rc.*|-next.*`, env: `staging`).  
3. Reuse existing build+DMG scripts; ensure codesign + **notarytool** submission + staple.  
4. Release job: **stable** → normal release; **prerelease** → mark as “Pre-release”.

**Verification**  
- Tag a dry-run in a fork or with `workflow_dispatch` → artifacts produced.  
- Releases show **Pre-release** when tagging `-rc|-next`.

**Exit Criteria**  
Two workflows green; artifacts attached to releases as designed.

**Rollback**  
Disable new workflows; revert to previous pipeline.

---

## P2 — Auto-Updater Safety for Prereleases

**Context & Motivation**  
Your updater prioritizes `*-latest-*` assets, then versioned ones. Prereleases must **not** expose `latest` assets so production users never auto-consume RCs.  
Refs: GitHub Releases ([REST](https://docs.github.com/en/rest/releases/releases)), updater logic rules (see project `AUTOUPDATE.md`).

**Instructions for the LLM**  
1. In prerelease workflow, **do not** upload `mcpproxy-latest-*` assets. Only versioned ones like `mcpproxy-vX.Y.Z-rc.N-*.dmg`.  
2. Add an env flag `MCPPROXY_ALLOW_PRERELEASE_UPDATES=true` to opt-in canary behavior; default off.  
3. Add unit tests to asset-selection code to ensure it **ignores** RC assets unless flag is set.

**Verification**  
- Simulate `GET /repos/:org/:repo/releases/latest` vs listing all releases; confirm selector picks stable only.  
- Unit test passes for both flag states.

**Exit Criteria**  
No `latest` assets on prerelease releases; selector respects flag.

**Rollback**  
Re-enable `latest` assets if needed for internal testing; keep flag off by default.

---

## P3 — Split Tray UI into a New Binary (CGO-on, darwin-only)

**Context & Motivation**  
Uncouple the tray from the core to remove CGO from the main binary and reduce accidental coupling.  
Refs: Go build tags ([pkg.go.dev](https://pkg.go.dev/go/build)), embed later for UI ([embed](https://pkg.go.dev/embed)).

**Instructions for the LLM**  
1. Create `cmd/mcpproxy-tray/` with current tray code (systray) and macOS build tag.  
2. Remove tray bootstrapping from `cmd/mcpproxy/`.  
3. Build constraints so core remains CGO-free; tray is `darwin` only.  
4. Keep existing **create-dmg** scripts; later DMG will package Tray.app.

**Verification**  
```bash
CGO_ENABLED=0 go build -o mcpproxy ./cmd/mcpproxy
GOOS=darwin CGO_ENABLED=1 go build -o mcpproxy-tray ./cmd/mcpproxy-tray
./mcpproxy --version
```

**Exit Criteria**  
Core builds without CGO; tray builds for macOS; behavior unchanged otherwise.

**Rollback**  
Revert to previous single-binary main.

---

## P4 — Implement `/api/v1` + SSE Event Stream

**Context & Motivation**  
Expose servers/tools/logs for tray & web UI. SSE provides efficient **uni-directional** updates; use REST for commands.  
Refs: chi router ([site](https://go-chi.io/), [pkg](https://pkg.go.dev/github.com/go-chi/chi)); SSE in Go ([guide](https://www.freecodecamp.org/news/how-to-implement-server-sent-events-in-go/)); SSE vs WS tradeoffs ([article](https://www.freecodecamp.org/news/server-sent-events-vs-websockets/)).

**Instructions for the LLM**  
1. Add `internal/httpapi` with chi router.  
2. Implement:  
   - `GET /api/v1/servers` (status + meta)  
   - `POST /api/v1/servers/{id}/enable|disable|restart|login`  
   - `GET /api/v1/servers/{id}/tools`  
   - `GET /api/v1/servers/{id}/logs?tail=N`  
   - `GET /api/v1/index/search?q=...`  
   - `GET /events` (SSE)  
3. Stream changes/log lines on SSE; REST remains command path.

**Verification**  
```bash
mcpproxy serve &
curl -s :8080/api/v1/servers | jq .
curl -N :8080/events | sed -n 's/^data: //p' | head -5
```

**Exit Criteria**  
JSON endpoints return data; SSE streams events; chi mux covered by unit tests.

**Rollback**  
Feature-flag the HTTP server; default off.

---

## P5 — Connect Tray to REST/SSE

**Context & Motivation**  
Tray must be a **pure client** over localhost HTTP/SSE to avoid tight coupling.

**Instructions for the LLM**  
1. Add an API client in `cmd/mcpproxy-tray/internal/api`.  
2. On launch, fetch `GET /api/v1/servers` to build menu; wire actions to POST calls.  
3. Subscribe to `/events` to reflect live status/log badges.  
4. Add “Open Web Control Panel” menu → open `http://localhost:8080/ui/` via `open`.

**Verification**  
Run `mcpproxy` + `mcpproxy-tray`; toggle a server in tray and confirm via `curl /api/v1/servers` that state changed.

**Exit Criteria**  
Tray reads/writes only via API; no imports from core packages.

**Rollback**  
Keep the tray polling path and disable SSE if issues arise.

---

## P6 — Embed Web UI (HTML/JS) into Core

**Context & Motivation**  
Serve SPA from the core using `//go:embed`; keep it **iframe-ready** for future embedding.  
Refs: Go embed ([pkg](https://pkg.go.dev/embed), [Go by Example](https://gobyexample.com/embed-directive)).

**Instructions for the LLM**  
1. Create `webui/dist/` (placeholder SPA) and embed via `embed.FS`.  
2. Serve at `/ui/…`; add SPA fallback route to `index.html`.  
3. SPA shows servers, tools, statuses, and a log tail using the new API/SSE.

**Verification**  
Open `http://localhost:8080/ui/` and confirm servers list + live updates.

**Exit Criteria**  
Web UI loads from embedded FS; no external files needed at runtime.

**Rollback**  
Disable UI serving with a flag; keep API operational.

---

## P7 — Introduce Facades & Interfaces (Testable Contracts)

**Context & Motivation**  
Stabilize surfaces so AI edits can’t break modules; enable mocks for tests.

**Instructions for the LLM**  
1. Define interfaces for: `Upstreams`, `Index`, `Storage`, `OAuth`, `DockerIso`, `Logs`.  
2. Add `internal/appctx` wiring that hands these interfaces to HTTP/MCP/CLI layers.  
3. Add **contract tests** (golden) that lock method sets on facades.

**Verification**  
- `go test ./...` passes.  
- Contract tests fail if an exported facade method changes signature.

**Exit Criteria**  
All adapters depend only on interfaces; coverage > baseline.

**Rollback**  
Interfaces remain; swap concrete implementations as needed.

---

## P8 — OAuth Token Store (Keychain first, age fallback)

**Context & Motivation**  
Secure tokens at rest via OS keyrings; fall back to `age`-encrypted files.  
Refs: 99designs/keyring ([repo](https://github.com/99designs/keyring), [pkg](https://pkg.go.dev/github.com/99designs/keyring)); age ([repo](https://github.com/FiloSottile/age), [pkg](https://pkg.go.dev/filippo.io/age)).

**Instructions for the LLM**  
1. Implement `internal/oauth/store.go` using **keyring**; fallback to age files under `~/.mcpproxy/tokens/`.  
2. Add refresh with **exponential backoff** and jitter.  
3. Typed errors + `%w` wrapping for clean API errors.

**Verification**  
- Unit tests: mock keyring; simulate fallback; assert no plaintext on disk.  
- Manual: revoke/refresh flows work; see logs.

**Exit Criteria**  
Token CRUD works with keyring; fallback tested; no secrets in plaintext.

**Rollback**  
Force fallback path via env flag for emergency.

---

## P9 — Circuit Breakers, Backoff, and Rate Limits

**Context & Motivation**  
Resilience for flaky upstreams; protect core under load.  
Refs: `cep21/circuit` ([repo](https://github.com/cep21/circuit), [pkg](https://pkg.go.dev/github.com/cep21/circuit/v3)); `cenkalti/backoff` ([pkg](https://pkg.go.dev/github.com/cenkalti/backoff)); Prometheus metric types ([docs](https://prometheus.io/docs/concepts/metric_types/)).

**Instructions for the LLM**  
1. Wrap upstream calls in a per-server **circuit breaker**.  
2. Add **exponential backoff + jitter** on retries.  
3. Introduce `x/time/rate` limiter for global and per-server calls; expose metrics.

**Verification**  
- Unit tests open/half-open/close the breaker deterministically.  
- `curl /metrics` shows counters/gauges for breaker states.

**Exit Criteria**  
Breakers trip on sustained errors and recover; metrics observed.

**Rollback**  
Feature-flag breakers; default settings conservative.

---

## P10 — Health/Ready, Prometheus & OpenTelemetry

**Context & Motivation**  
Observability improves operability and AI verification.  
Refs: Prometheus Go app guide ([docs](https://prometheus.io/docs/guides/go-application/)); OTel Go getting started ([docs](https://opentelemetry.io/docs/languages/go/getting-started/)).

**Instructions for the LLM**  
1. Add `GET /healthz` (process up) and `GET /readyz` (deps OK).  
2. Add `/metrics` via `promhttp`.  
3. Add basic OTel tracing around upstream calls and API handlers.

**Verification**  
- `curl :8080/healthz` → 200; `curl :8080/readyz` → 200 once ready.  
- `/metrics` exports go_* and custom metrics.  
- Traces appear in local OTLP receiver (optional).

**Exit Criteria**  
Health endpoints and metrics stable; traces emitted when configured.

**Rollback**  
Disable tracing/metrics via flags.

---

## P11 — OpenAPI (swaggo) + Golden Tests

**Context & Motivation**  
Document and test the API surface; aid client generation.  
Refs: swaggo/swag ([repo](https://github.com/swaggo/swag)).

**Instructions for the LLM**  
1. Annotate handlers; run `swag init` to generate docs.  
2. Serve Swagger UI at `/ui/swagger/`.  
3. Add **golden tests** for representative JSON responses to lock compatibility.

**Verification**  
- `curl /api/v1/servers` matches golden files.  
- Swagger JSON available and loads.

**Exit Criteria**  
OpenAPI generated; goldens stable in CI.

**Rollback**  
Keep handlers; remove swagger route if needed.

---

## P12 — Docker Isolation Hardening

**Context & Motivation**  
Tighten container execution for tool runs; enforce limits and optional sandbox backends.

**Instructions for the LLM**  
1. Add CPU/memory quotas, read-only FS, dropped capabilities, and isolated network mode.  
2. Optional: flag to use gVisor/Firecracker backends if present.  
3. Expose status via `/api/v1/servers/{id}/isolation` for AI checks.

**Verification**  
- Integration test via docker-compose; `docker inspect` shows limits.  
- Attempt privileged actions; confirm denial.

**Exit Criteria**  
Secure defaults applied; tests pass.

**Rollback**  
Relax to prior defaults via config.

---

## P13 — Packaging: DMG with Tray.app + Core

**Context & Motivation**  
Ship Tray as a proper `.app` bundle and package within a signed/notarized DMG.  
Refs: Apple notarization (`notarytool` + `stapler`) ([doc](https://developer.apple.com/documentation/Security/notarizing-macos-software-before-distribution), [API](https://developer.apple.com/documentation/NotaryAPI)).

**Instructions for the LLM**  
1. Extend existing `scripts/create-dmg.sh` to include `MCPProxy.app` (Tray) and the `mcpproxy` daemon if bundled.  
2. Codesign both app and DMG; submit for notarization; **staple**.  
3. CI: stable release → production env; prerelease → staging env.

**Verification**  
- `codesign --verify --verbose` and `spctl --assess --type execute` pass.  
- Notarization returns success; stapled DMG opens without warnings.

**Exit Criteria**  
Notarized DMG with Tray.app is published in both lanes.

**Rollback**  
Publish unsigned artifacts for internal testing only (not recommended).

---

## Appendix A — Success Criteria Summary (for CI gates)

- Core builds CGO-off; Tray builds CGO-on (darwin).  
- `/api/v1/*` endpoints + `/events` live; goldens pass.  
- Tokens secured via keyring/age; no plaintext.  
- Breakers/limits active; `/metrics` exposed; OTel optional.  
- Web UI embedded & functional.  
- DMG signed, notarized, and stapled; releases split stable/prerelease.

---

## Appendix B — Quick Command Matrix

```bash
# Build core and tray
CGO_ENABLED=0 go build -o mcpproxy ./cmd/mcpproxy
GOOS=darwin CGO_ENABLED=1 go build -o mcpproxy-tray ./cmd/mcpproxy-tray

# Run & probe API
./mcpproxy serve &
curl -s :8080/api/v1/servers | jq .
curl -N :8080/events | head -20
curl -s :8080/metrics | head -20

# UI
open http://localhost:8080/ui/

# Health
curl -i :8080/healthz ; curl -i :8080/readyz
```

---

## Appendix C — Notes on References to Current Codebase

- Auto-update asset priorities & flows are captured in **AUTOUPDATE.md**.  
- DMG creation, codesigning, and verification sequences are in **scripts/create-dmg.sh** and related plist/entitlement files.  
- Indexing uses **Bleve** in `internal/index/bleve.go`.  
- Hashing utilities live in `internal/hash/hash.go`.

(See repo files for exact implementations.)

---

*End of document.*
