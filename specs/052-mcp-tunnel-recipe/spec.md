# Feature Specification: mcpproxy behind an MCP tunnel — Anthropic + open-source recipes

**Feature Branch**: `052-mcp-tunnel-recipe`
**Created**: 2026-05-20
**Status**: Draft
**Edition**: Both — personal users reach mcpproxy through open-source tunnels (Cloudflare Tunnel, Tailscale, ngrok); server-edition operators reach mcpproxy through Anthropic MCP Tunnels for Managed Agents.
**Input**: User description: "Document and (minimally) support exposing mcpproxy via a tunnel so MCP clients running on another machine — OpenCode, Claude Code, Continue, Cursor, or Anthropic Managed Agents — can reach a private mcpproxy without inbound firewall holes. One tunnel hostname fronts the full multiplexed `/mcp` surface, mcpproxy enforces auth via agent tokens, and the tunnel only provides transport."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Managed Agents reach private mcpproxy through one Anthropic tunnel hostname (Priority: P1, server edition)

A platform team runs `mcpproxy-server` inside their VPC, behind a typical no-inbound-ingress firewall, with N upstream MCP servers configured (GitHub, internal Postgres, JIRA, etc.). They register an Anthropic MCP Tunnel for the organization, deploy the two-container Anthropic stack (`mcp-proxy` + `cloudflared`) alongside `mcpproxy-server` using a recipe we ship, and route a single tunnel subdomain at mcpproxy's `/mcp` endpoint. From the Anthropic Console they create a Managed Agent that lists `https://mcpproxy.<their-tunnel>.tunnel.anthropic.com/mcp` as its MCP server. The agent immediately sees mcpproxy's `retrieve_tools` / `call_tool_*` surface and, through it, every tool from every configured upstream — without any inbound port open on the customer network.

**Why this priority**: This is the *only* Anthropic surface that MCP Tunnels reaches today (Managed Agents + Messages API; explicitly not claude.ai/mobile/desktop/Claude Code). It is also exactly the customer profile mcpproxy-server is built for. Without a documented, working deployment recipe the integration is theoretically possible but practically out of reach — operators have to reverse-engineer the auth bridge, the route map, and the recommended hardening on their own.

**Independent Test**: With the recipe checked into the repo, a fresh operator on a clean Linux VM can clone, copy `examples/mcp-tunnel-anthropic/`, set the documented environment variables (`TUNNEL_TOKEN`, `MCPPROXY_API_KEY`, tunnel domain, agent-token name), run `docker compose up`, and within 5 minutes have a Managed Agent in the Anthropic Console successfully calling `retrieve_tools` and one upstream tool through the tunnel.

**Acceptance Scenarios**:

1. **Given** an operator has a tunnel registered in their Anthropic workspace with a CA certificate uploaded, **When** they `docker compose up` the recipe and copy the tunnel URL into a Managed Agent's MCP server list, **Then** the agent can call `retrieve_tools` and receive the proxy's tool index.
2. **Given** the recipe is running with three configured upstream MCP servers, **When** the Managed Agent calls `call_tool_read` for any of them, **Then** the tool executes inside the customer's VPC and the response returns through the tunnel.
3. **Given** an operator follows the recipe verbatim, **When** they inspect the running containers, **Then** mcpproxy-server's `/mcp` endpoint is reachable from `mcp-proxy` only (not the public internet), accepts the agent token, and rejects requests without the token with HTTP 401.
4. **Given** the recipe is running, **When** the operator restarts the host, **Then** all three containers come back up in order (`mcpproxy-server` ready before `mcp-proxy` routes traffic) and Managed Agent calls resume working.

---

### User Story 2 - OpenCode user exposes laptop mcpproxy via Cloudflare Tunnel (Priority: P1, personal edition)

A developer runs OpenCode on their laptop and curates ~10 upstream MCP servers in mcpproxy (a personal-edition install). They want to call those same tools from OpenCode running on their work laptop, a Linux desktop, a colleague's machine — anywhere — without exposing their home IP, opening ports on their router, or running OpenCode on every device.

They follow our `examples/mcp-tunnel-cloudflared/` recipe: install `cloudflared`, log in once, run a two-line `tunnel.yaml` that maps their domain `mcp.alice.example.com` → `http://127.0.0.1:8080`. They start mcpproxy with `--tunnel-mode` and a freshly minted agent token. They paste the URL + bearer into OpenCode's `opencode.json` (`"type": "remote"`, `"url": "https://mcp.alice.example.com/mcp"`, `"headers": { "Authorization": "Bearer mcp_agt_..." }`). OpenCode on any machine now sees mcpproxy's full toolset.

**Why this priority**: This is the **answer to "Anthropic MCP Tunnels don't help OpenCode users"**. OpenCode runs against 75+ model providers (OpenAI, Gemini, Bedrock, Ollama, …) and accepts arbitrary HTTPS MCP URLs with `Authorization` headers. Cloudflare Tunnel is free for hobbyist scale and gives a stable URL. Pairing the two with mcpproxy as the multiplexer + auth layer means the user gets one URL, one bearer, N tools — and a real auth boundary instead of "whoever has the URL gets in".

**Independent Test**: On a fresh laptop with mcpproxy installed, `cloudflared` installed, and one domain in Cloudflare DNS, the user runs through the recipe (≤8 commands) and within 10 minutes has OpenCode on a second machine successfully calling at least two distinct upstream tools through the tunneled mcpproxy. The mcpproxy activity log shows both calls attributed to the agent token by name.

**Acceptance Scenarios**:

1. **Given** the cloudflared recipe is running and the OpenCode `opencode.json` has the `myproxy` remote MCP entry configured, **When** the user opens an OpenCode session, **Then** the session lists mcpproxy's tools (via `retrieve_tools`) within 3 seconds of opening.
2. **Given** the configured agent token has `read` permission only, **When** OpenCode attempts a write-classified tool, **Then** mcpproxy returns 403 with a clear error and the activity log records the denied attempt against the token name.
3. **Given** the recipe is running, **When** the user shuts the laptop or loses internet, **Then** OpenCode's MCP connection drops cleanly and reconnects automatically when connectivity returns.
4. **Given** the recipe is running, **When** a different user discovers the URL but doesn't have the token, **Then** every request returns 401 — no tool list, no metadata, no fingerprinting beyond the response code.

---

### User Story 3 - OpenCode user shares mcpproxy across personal devices via Tailscale (Priority: P2, personal edition)

A developer doesn't want their MCP traffic to traverse the public internet at all but wants their mcpproxy reachable across the four machines on their tailnet (laptop, desktop, home server, work laptop). They follow our `examples/mcp-tunnel-tailscale/` recipe — two flavours documented:

- **Funnel** (`tailscale funnel 8080`): public `*.ts.net` URL, useful for "share with a colleague who isn't on my tailnet"; **public-by-default**, so mcpproxy's agent-token auth is doing all the work.
- **Serve** (`tailscale serve --bg 8080`): tailnet-only URL; only their own four devices can reach it.

In both cases OpenCode points at the URL with the bearer header.

**Why this priority**: Tailscale Serve is *strictly safer* than the cloudflared variant for personal use because it constrains the network reachability instead of relying solely on mcpproxy's auth. Funnel is a strict subset of "expose via public URL" already covered by US2, so it doesn't add much, but Serve fills the "I want this LAN-tight" niche.

**Independent Test**: With Tailscale installed on at least two of the user's machines and mcpproxy running on one, the user runs `tailscale serve --bg 8080`, copies the URL into OpenCode on the second machine, and within 5 minutes makes a successful tool call. A third machine *not on the tailnet* receives connection-refused / unreachable when trying the same URL (the Serve mode invariant).

**Acceptance Scenarios**:

1. **Given** Tailscale Serve is active and mcpproxy is running with `--tunnel-mode`, **When** OpenCode on another tailnet device calls the URL, **Then** the request succeeds with valid bearer.
2. **Given** the same setup, **When** a device not on the tailnet attempts the URL, **Then** the request fails at the network layer (no DNS resolution / connection refused), not at mcpproxy.
3. **Given** Tailscale Funnel is active (the public variant), **When** anyone on the internet hits the URL, **Then** they get 401 without a valid token — mcpproxy's auth is the only gate.

---

### User Story 4 - ngrok recipe for ephemeral demos / one-off sharing (Priority: P3, personal edition)

A user wants to demo their mcpproxy setup to a colleague on a call: 30 minutes, a random URL, no DNS, no Cloudflare account. They follow `examples/mcp-tunnel-ngrok/`: `ngrok http 8080` produces a random `https://<random>.ngrok.app` URL, mcpproxy is started with `--tunnel-mode` and a single-use agent token, OpenCode/Claude Code gets a copy-paste config snippet. After the demo they `Ctrl-C` ngrok and revoke the agent token.

**Why this priority**: Quick win, very low cost to document, fills the "I want to share for an hour" gap that the more durable cloudflared/Tailscale recipes don't fit. P3 because the free-tier random URL is friction for any persistent use case.

**Independent Test**: A user on a fresh machine without prior tunnel setup can run through the recipe end-to-end (install ngrok, mint a token, copy paste config) and have a colleague's OpenCode call a tool through the ngrok URL within 5 minutes. After they stop ngrok, the URL stops resolving.

**Acceptance Scenarios**:

1. **Given** ngrok is running and the agent token is fresh, **When** the colleague pastes the config and opens OpenCode, **Then** tool calls succeed.
2. **Given** the user revokes the agent token (`mcpproxy token revoke demo-call`), **When** the colleague tries another call, **Then** mcpproxy returns 401 even while ngrok is still up.

---

### User Story 5 - Operators understand exactly what auth bridges what (Priority: P1, both editions)

The deployment doc makes it unambiguous which security boundary terminates where. For each of the four recipes (Anthropic, Cloudflare Tunnel, Tailscale, ngrok) the doc shows the per-layer auth: outer TLS termination, inner auth (if any), and the *MCP-level* auth between the tunnel proxy and `mcpproxy` (always an agent token issued by mcpproxy). An operator reading the doc knows where each secret lives, what rotates on what schedule, and what attack a leak of each one enables.

**Why this priority**: The tunnel transport "carries encrypted traffic but does not authenticate to it" — true for all four variants. If a user assumes the tunnel is the security boundary, they will over-trust requests. This is exactly the class of misconfiguration mcpproxy's agent-token model was designed to prevent — but only if the doc makes the boundary explicit.

**Independent Test**: A reviewer reads `docs/deployments/mcp-tunnel.md` cold and can answer, without looking at other files, for each of the four recipes: (a) what credential allows mcpproxy to call upstream X; (b) what credential allows the tunnel proxy to reach mcpproxy; (c) what credential allows the tunnel edge to be reached at all; (d) which credential rotates on what cadence; (e) which credential, if leaked alone, lets the attacker do what.

**Acceptance Scenarios**:

1. **Given** the deployment guide, **When** a reviewer reads it once, **Then** they can produce the per-recipe auth-layer table above unprompted.
2. **Given** any of the four recipes, **When** an operator runs the recipe's setup script, **Then** the generated `.env.example` (or equivalent) calls out every required secret with a one-line description and a one-line "what an attacker can do if this leaks".
3. **Given** the recipes, **When** mcpproxy starts inside any of them, **Then** it logs (info level) the agent-token name in use and the upstream servers it intends to route, so operators can confirm scoping at a glance.

---

### User Story 6 - mcpproxy runs with a tunnel-safe configuration profile (Priority: P1, both editions)

An operator who points `mcpproxy` at a config that includes `tunnel.enabled: true` (or passes `--tunnel-mode`) gets a tunnel-safe startup posture by default on the tunneled listener: bind `/mcp` on the in-cluster network name expected by the tunnel proxy (or on `0.0.0.0:<port>` for the standalone open-source variants), refuse to start the Web UI on the tunneled listener (Web UI continues to be available on the management listener for local operators), require agent-token auth on `/mcp` (not just API key), and expose `/healthz` for the tunnel proxy's liveness probe. The operator no longer has to manually compose four or five settings to harden the deployment.

**Why this priority**: User Stories 1–4 (the recipes) make deployment possible. Story 6 reduces the chance of misconfiguration to near-zero by giving operators a single switch that applies across all four tunnel variants. Without it the recipes still work; with it they are significantly safer for the median operator and the per-recipe doc shrinks.

**Independent Test**: Starting `mcpproxy` with `--tunnel-mode` on a fresh VM with no manual hardening produces a process that: (a) does not respond on `/ui/` on the tunneled port; (b) returns 401 on `/mcp` without a valid agent token; (c) returns 200 on `/healthz` immediately when the process is ready and 503 before; (d) logs a single banner line confirming tunnel-safe mode is active. Both editions exhibit this behaviour.

**Acceptance Scenarios**:

1. **Given** `tunnel.enabled: true` in the config, **When** mcpproxy starts, **Then** the tunneled listener serves `/mcp` and `/healthz` only.
2. **Given** tunnel mode is active, **When** a request hits `/mcp` without an `X-API-Key` matching an agent token (or an `Authorization: Bearer mcp_agt_…`), **Then** the response is 401 with a request-id (no fallthrough to the regular admin API key).
3. **Given** tunnel mode is active, **When** the operator separately enables the management listener on a different port, **Then** the Web UI and full REST API are available there for operators inside the network.

---

### User Story 7 - Day-2 operations: cert renewal and credential rotation (Priority: P2, both editions)

Each tunnel variant has its own renewal cadence. The recipes handle the common ones without operator intervention:

- **Anthropic**: Tunnel server cert renews every 90 days via the included CronJob; mcpproxy-server unaffected by `mcp-proxy` restarts; agent-token rotation is independent.
- **Cloudflare Tunnel**: Cloudflare manages the edge cert; the user's `cloudflared` credentials JSON sits in `~/.cloudflared/`; rotation is `cloudflared tunnel rotate`.
- **Tailscale**: Tailscale manages its own keys and Funnel certs; the operator does nothing.
- **ngrok**: Free-tier random URLs change on every restart (acknowledged in US4); paid reserved domains are stable until the user rotates the auth token.

Across all four, rotating the mcpproxy agent token never requires restarting the tunnel daemon.

**Why this priority**: Day-2 ops. Without it the recipes work for the first 90 days then quietly break. P2 because most users can paper over with operator runbooks while production hardening can wait.

**Independent Test**: For each recipe, simulate a credential rotation (override validity periods to minutes where possible) and confirm: (a) tool calls succeed across the transition with at most one auto-retry; (b) mcpproxy itself does not restart; (c) the agent token can be rotated independently with a single CLI command on the mcpproxy side.

**Acceptance Scenarios**:

1. **Given** any recipe is running and the relevant cert/credential renewal triggers, **When** renewal completes, **Then** `mcpproxy` continues serving without restart and client calls succeed across the transition.
2. **Given** an operator rotates the mcpproxy agent token via `mcpproxy token regenerate <name>`, **When** they update only the client config (no tunnel-side change), **Then** subsequent calls with the new token succeed within 30 s and the old token returns 401.

---

### Edge Cases

- **Beta header drift (Anthropic)**: Anthropic requires `anthropic-beta: mcp-client-2025-11-20` from Managed Agents callers and `anthropic-beta: mcp-tunnels-2026-05-19` for the Admin API. Both will rotate. The recipe MUST NOT bake the date into mcpproxy itself.
- **10-tunnels-per-org cap (Anthropic)**: A customer with multiple environments sharing one Anthropic workspace can run out of tunnels. The doc recommends fronting all environments through one mcpproxy with environment-scoped upstreams + agent tokens, not one tunnel per environment.
- **Anthropic tunnels are NOT on claude.ai today**: An operator may follow the Anthropic recipe and be surprised their colleagues cannot use the URL from Claude Desktop or claude.ai. Stated once, prominently, near the top of the Anthropic-recipe section.
- **claude.ai web custom connectors with the open-source recipes**: a Cloudflare Tunnel URL with a Bearer header is a normal HTTPS endpoint, so claude.ai web / mobile / Desktop's custom-connector flow *may* be able to consume it. The doc notes this as untested and points readers at Anthropic's custom-connector docs — we don't promise it works.
- **Tailscale Funnel is public-by-default**: anyone on the internet can hit the URL; the doc states this in bold inside the Funnel section and reminds the reader that mcpproxy's bearer-auth is the only gate.
- **ngrok free-tier random URLs**: the URL changes every restart; the doc says so before the user wastes 20 minutes wondering why their config broke.
- **MCP Streamable-HTTP keep-alives**: tunnel idle timeouts under 60 s break the MCP transport. The recipes set or document tunnel-side idle timeouts ≥ 180 s.
- **Web UI hard-codes `/ui/` base path**: tunneling the Web UI through a sub-path (`https://tunnel.example.com/mcpproxy/ui/`) doesn't work today (`frontend/vite.config.ts:8`). The recipes only tunnel `/mcp` and `/healthz`, never `/ui/`.
- **OAuth-on-upstream still required**: the tunnel doesn't authenticate mcpproxy to the upstream MCP servers it fronts. Per-upstream OAuth/tokens remain mcpproxy's responsibility, exactly as today.
- **Multiple OpenCode clients share one token**: nothing in mcpproxy distinguishes which OpenCode instance made a call when both use the same agent token. The doc recommends one token per client device for traceability.
- **Subprocessor concern (Anthropic + Cloudflare variants)**: Cloudflare sees egress IP and the tunnel subdomain. The doc notes this for customers with strict subprocessor reviews.

## Requirements *(mandatory)*

### Functional Requirements

**Recipes — Anthropic (server edition, Managed Agents)**

- **FR-001** The repository SHALL contain `examples/mcp-tunnel-anthropic/` with a working `docker-compose.yaml` that boots `mcpproxy-server`, Anthropic's `mcp-proxy`, and `cloudflared` together.
- **FR-002** The Anthropic compose file MUST pin Anthropic's `mcp-proxy` image by SHA, not floating tag.
- **FR-003** mcpproxy-server in this recipe MUST run with the management listener (REST + Web UI) bound to a non-tunneled internal port and the `/mcp` listener exposed only to the `mcp-proxy` container.
- **FR-004** The Anthropic compose MUST configure `mcp-proxy`'s `routes:` map to a single entry pointing at mcpproxy-server's `/mcp` listener and MUST NOT expose other mcpproxy endpoints through the tunnel.
- **FR-005** The Anthropic recipe MUST work with both Anthropic's manual setup flow (operator-supplied CA) and the programmatic-access flow (`setup init` via Workload Identity Federation).

**Recipes — Cloudflare Tunnel (both editions, OpenCode/Claude Code/Continue/Cursor consumers)**

- **FR-006** The repository SHALL contain `examples/mcp-tunnel-cloudflared/` with: a documented `cloudflared` quickstart, a sample `tunnel.yaml` mapping one hostname to `http://127.0.0.1:8080`, and a documented OpenCode `opencode.json` snippet that consumes the resulting URL with a Bearer agent token.
- **FR-007** The cloudflared recipe MUST work with personal-edition `mcpproxy` (no `server` build tag required); it MAY additionally document a server-edition variation in a sub-section.
- **FR-008** The cloudflared recipe doc MUST cover both "Cloudflare-managed domain" (the user's own DNS zone in Cloudflare) and "no domain — TryCloudflare random URL" variants, with the trade-off explicit (TryCloudflare = ephemeral, no auth gate at the edge).
- **FR-009** The cloudflared recipe MUST surface `cloudflared tunnel route` and `cloudflared tunnel rotate` commands for day-2 maintenance.

**Recipes — Tailscale (both editions)**

- **FR-010** The repository SHALL contain `examples/mcp-tunnel-tailscale/` covering both `tailscale serve` (tailnet-only) and `tailscale funnel` (public-via-Tailscale-edge), with a clear callout that Funnel has no built-in auth gate.
- **FR-011** The Tailscale recipe MUST document the port restriction (Funnel: 443/8443/10000) and recommend `--bg` for persistence.
- **FR-012** Client snippets MUST cover at least OpenCode and Claude Code; Continue + Cursor MAY follow.

**Recipes — ngrok (both editions, ephemeral)**

- **FR-013** The repository SHALL contain `examples/mcp-tunnel-ngrok/` with a one-liner setup and a sample traffic-policy file that adds a bearer-auth check (so the tunnel rejects unauthenticated traffic before it even reaches mcpproxy, as defence in depth).
- **FR-014** The ngrok recipe MUST clearly mark itself as "ephemeral/demo" use and document the free-tier random-URL limitation.

**`--tunnel-mode` (both editions, all recipes)**

- **FR-015** mcpproxy SHALL accept `--tunnel-mode` flag and equivalent `tunnel.enabled: true` config block. When set, mcpproxy:
  - Refuses to start if no agent token exists in storage (no silent fallback to admin API key on the tunneled listener).
  - Refuses to start if `enable_web_ui: true` is set on the tunneled listener.
  - Enables `/healthz` returning 200 when ready, 503 before.
  - Logs a single info banner line confirming tunnel-safe mode and the agent-token name(s) authorised for the listener.
  - Continues to expose the full Web UI / REST API on a separate management listener if one is configured.
- **FR-016** Tunnel mode MUST work in both personal and server editions. The hardening is identical; the additional server-edition multi-user constraints (per-user routing in `internal/teams/multiuser/router.go`) apply on top, unchanged, when the server tag is built in.
- **FR-017** Logs and activity records for tunnel-routed requests MUST include the agent-token name so operators can trace each call back to a specific client device / Managed Agent.

**Documentation (single source of truth)**

- **FR-018** The repository SHALL contain `docs/deployments/mcp-tunnel.md` covering: when to use each recipe, the per-recipe auth-layer table, step-by-step setup for all four recipes, day-2 operations (cert/key/token rotation, log inspection), client snippets for OpenCode (primary), Claude Code, Continue, Cursor, Anthropic Managed Agents, and limitations.
- **FR-019** The doc MUST link to upstream canonical sources (Anthropic MCP Tunnels docs, Cloudflare Tunnel docs, Tailscale Funnel/Serve docs, ngrok docs, OpenCode MCP servers docs) and MUST NOT duplicate their setup steps in a way that drifts.
- **FR-020** The doc MUST include a worked example of an OpenCode `opencode.json` calling the tunneled mcpproxy and a worked example of a Managed Agent definition doing the same.
- **FR-021** The doc MUST surface that Anthropic MCP Tunnels are Research Preview / "as-is", and that the open-source variants (Cloudflare/Tailscale/ngrok) have their own SLA / cost / support implications.

**Smoke testing**

- **FR-022** Each recipe SHALL include a smoke-test script (bash) that verifies: containers/daemons up, the tunnel URL resolves, `mcpproxy` rejects unauthenticated requests, end-to-end `retrieve_tools` succeeds via the tunneled URL with the bearer.
- **FR-023** The cloudflared and ngrok smoke tests MAY run in CI against a mocked tunnel (the tunnel daemons themselves require real credentials); the assertion that mcpproxy's `--tunnel-mode` produces the correct response shape is the CI-asserted part.

### Out of Scope (v1)

- **claude.ai web / mobile / Claude Desktop reach a private mcpproxy through Anthropic MCP Tunnels.** Anthropic's tunnel is gated to Managed Agents + Messages API only. The doc notes this once and moves on.
- **claude.ai custom-connector compatibility with the open-source recipes.** A cloudflared URL with bearer auth *may* work as a claude.ai custom connector but we make no promises; if/when verified, follow-up doc only.
- **Replacing Anthropic's `mcp-proxy` image** with a mcpproxy-native tunnel proxy. Substantial undertaking; revisit only if a real customer asks AND Anthropic signals partner integrations are welcome.
- **Per-end-user identity** propagated from Managed Agent / OpenCode client down to upstream MCP servers. Tunnels pass no user identity; mcpproxy sees one agent-token per client. Per-user scoping needs either one token per client device or a separate identity mechanism, neither in scope here.
- **Helm chart** for the recipes. Compose first; Helm follows if asked.
- **`mcpproxy expose` CLI** that wraps the tunnel daemons under one UX (Spec 053, follow-up). v1 stays recipe + doc.
- **Automated tunnel provisioning** from inside mcpproxy (Cloudflare API calls, Anthropic Admin API calls). Operator-driven setup, not runtime concern.
- **Latency/SLI dashboards** for tunneled traffic. Existing observability (`X-Request-Id`, activity log) suffices.
- **WireGuard / Headscale / ssh -R recipes**. The four covered (Anthropic + cloudflared + Tailscale + ngrok) hit the 95th-percentile use cases; the others are documented inline as "if you already use X, mcpproxy works the same way" footnotes only.

### Key Entities

- **Tunnel-Mode Listener**: The `/mcp` HTTP endpoint as exposed to any tunnel proxy when `tunnel.enabled` is true. Differs from the regular `/mcp` listener in three ways: agent-token-only auth, no Web UI, `/healthz` enabled. Same code path otherwise. Applies to both editions.
- **Recipe Bundles**: The four subdirectories under `examples/`: `mcp-tunnel-anthropic/`, `mcp-tunnel-cloudflared/`, `mcp-tunnel-tailscale/`, `mcp-tunnel-ngrok/`. Each contains a compose file (or equivalent), config skeletons, smoke-test script, README pointing at the canonical doc.
- **Agent Token (Tunnel-Scoped)**: A normal mcpproxy agent token (Spec 028), generated for use by exactly one client device / Managed Agent via exactly one tunnel. Identified by name in logs. Default permission `read` only; `write`/`destructive` require explicit operator opt-in.
- **Deployment Guide**: `docs/deployments/mcp-tunnel.md`. Single source of truth on the mcpproxy side. Owns the four per-recipe auth-layer tables and the client snippets for OpenCode + Claude Code + Continue + Cursor + Managed Agents.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001** A fresh operator following only `docs/deployments/mcp-tunnel.md` can stand up *any of the four recipes* in **≤ 30 minutes** measured from `git clone` to a successful `retrieve_tools` round-trip via the tunnel.
- **SC-002** For each recipe, the smoke-test script passes end-to-end on a clean Ubuntu 22.04 VM (with Docker 24+) in **≤ 5 minutes** when the operator has the relevant tunnel-side prerequisites (Cloudflare account / Tailscale tailnet / ngrok account / Anthropic tunnel).
- **SC-003** A reviewer reading `docs/deployments/mcp-tunnel.md` once can correctly answer the per-recipe auth-boundary questions (US5 independent test) in **≤ 10 minutes** for at least three of the four recipes.
- **SC-004** When `tunnel.enabled: true` is set without an agent token configured, `mcpproxy` refuses to start with a clear error pointing at the missing token, in **100 % of attempts** across the existing CI matrix.
- **SC-005** When `tunnel.enabled: true` is set with `enable_web_ui: true` on the tunneled listener, `mcpproxy` refuses to start with a clear error pointing at the conflict, in **100 % of attempts**.
- **SC-006** Credential / cert rotation simulations for each recipe (US7 independent test) complete with **zero requests dropped** as observed by a continuous `retrieve_tools` heartbeat.
- **SC-007** Personal-edition builds (`go build ./cmd/mcpproxy`, no `-tags server`) compile unchanged and the `--tunnel-mode` flag functions identically on personal-edition binaries for the three open-source recipes. The Anthropic recipe is server-edition-only; binary-size delta on personal-edition vs the pre-spec build is **≤ 1 %**.
- **SC-008** A working OpenCode `opencode.json` snippet copy-pasted from the doc into a fresh OpenCode install on a second machine produces a successful tool call via the tunneled mcpproxy in **≤ 60 seconds** of session open, for the cloudflared and Tailscale Serve recipes.

## Assumptions

- OpenCode's remote-MCP client (https://opencode.ai/docs/mcp-servers/) accepts arbitrary HTTPS URLs with arbitrary `Authorization` headers. Confirmed in current docs (`type: remote`, `url`, `headers`).
- Cloudflare Tunnel, Tailscale Funnel/Serve, and ngrok remain free at the entry tier we recommend; the recipes call out paid features (ngrok reserved domains, Cloudflare Access SSO) only where they materially improve security.
- Anthropic MCP Tunnels remain a Research Preview gated behind the Managed Agents access form, and Anthropic's `mcp-proxy` image stays publicly pinnable by SHA.
- The MCP Streamable-HTTP transport remains the default for mcpproxy's `/mcp` endpoint and for the `mark3labs/mcp-go` server library.
- mcpproxy's existing agent-token system (Spec 028) is sufficient as the client↔mcpproxy auth layer for every recipe; no new token type needed.
- The 10-tunnels-per-organization cap (Anthropic), 90-day default cert validity (Anthropic), and per-recipe limits (ngrok URL randomness, Tailscale Funnel port restriction) are stable enough for v1 documentation. If upstream changes them, the doc updates without code changes.

## References

**Anthropic**
- MCP Tunnels Overview — https://platform.claude.com/docs/en/agents-and-tools/mcp-tunnels/overview
- MCP Tunnels Quickstart — https://platform.claude.com/docs/en/agents-and-tools/mcp-tunnels/quickstart
- MCP Tunnels Security — https://platform.claude.com/docs/en/agents-and-tools/mcp-tunnels/security
- MCP Tunnels Compose Reference — https://platform.claude.com/docs/en/agents-and-tools/mcp-tunnels/deploy-compose

**Open-source tunnel daemons**
- Cloudflare Tunnel (`cloudflared`) — https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/
- Tailscale Funnel — https://tailscale.com/kb/1223/funnel
- Tailscale Serve — https://tailscale.com/kb/1242/tailscale-serve
- ngrok HTTP — https://ngrok.com/docs/http/

**MCP clients**
- OpenCode MCP servers — https://opencode.ai/docs/mcp-servers/
- Claude Code MCP — https://code.claude.com/docs/en/mcp
- Continue MCP — https://docs.continue.dev/customize/deep-dives/mcp
- Cursor MCP — https://docs.cursor.com/en/context/mcp

**mcpproxy**
- Agent tokens — Spec 028, `docs/features/agent-tokens.md`
- Server edition + multi-user — Spec 024 (`internal/teams/`)
- MCP server surface — `internal/server/mcp.go`, `internal/server/server.go:1660-1684`
- Adjacent spec — 051 Registry Modernization (`specs/051-registry-modernization/spec.md`)
- Follow-up — Spec 053 `mcpproxy expose` CLI (to be drafted if customer demand materialises)

## Commit Message Conventions *(mandatory)*

### Issue References

Tag commits and PRs with `Refs: spec/052` and link related upstream doc pages (Anthropic, Cloudflare, Tailscale, ngrok, OpenCode) in PR descriptions where relevant.

### Co-Authorship

No `Co-Authored-By: Claude` lines in commits or PR bodies — this repo overrides the global rule (see `MEMORY.md` → "No Claude git attribution").

### Example Commit Message

```
feat(052): tunnel-safe mode + four MCP-tunnel deploy recipes

Adds --tunnel-mode (and tunnel.enabled config block) for both editions.
When set, the /mcp listener requires an agent token, the Web UI is
refused on the tunneled port, and /healthz is enabled. Ships four
recipes under examples/mcp-tunnel-{anthropic,cloudflared,tailscale,
ngrok}/ + a single deployment doc at docs/deployments/mcp-tunnel.md
covering per-recipe auth-layer tables and OpenCode/Claude Code client
snippets.

Anthropic recipe stays server-edition-only; cloudflared / Tailscale /
ngrok work on personal edition too.

Refs: spec/052
```

## Changes

This spec introduces:

- **New code (both editions)**: `tunnel-mode` flag/config + the three guard rails (agent-token-only, no Web UI, `/healthz`) on the tunneled listener. Estimated < 250 LOC plus tests. Personal-edition impact: ≤ 1 % binary-size delta, no new dependencies.
- **New recipes (four)**: `examples/mcp-tunnel-anthropic/`, `examples/mcp-tunnel-cloudflared/`, `examples/mcp-tunnel-tailscale/`, `examples/mcp-tunnel-ngrok/`.
- **New doc**: `docs/deployments/mcp-tunnel.md` — single source of truth.
- **Doc updates**: server-edition section of `CLAUDE.md` gets one line pointing at the doc; a new "Sharing mcpproxy" subsection in the personal-edition top-level README/docs links to the three open-source recipes.

This spec does NOT:

- Add an `mcpproxy expose` CLI (deferred to Spec 053).
- Re-implement Anthropic's `mcp-proxy` image.
- Provision tunnels programmatically from inside mcpproxy.
- Pass end-user identity through any tunnel.

## Testing

- **Unit tests** (both editions): `tunnel-mode` config validation — reject missing agent token, reject `enable_web_ui: true` on tunneled listener, accept minimal valid config. Banner log assertion.
- **Integration test** (both editions): start `mcpproxy --tunnel-mode` with a sample agent token, hit `/mcp` without auth (expect 401), hit with the token (expect MCP `initialize` success), hit `/ui/` on the tunneled port (expect 404 or refused), hit `/healthz` (expect 200 when ready).
- **Smoke tests** (one per recipe, bash, shipped under each `examples/mcp-tunnel-*/`): asserts containers/daemons up, end-to-end `retrieve_tools` succeeds. Run in CI against mocked tunnels (real daemons need real credentials).
- **Personal-edition guard**: existing CI step that builds `./cmd/mcpproxy` without `-tags server` must continue to pass; `--tunnel-mode` flag MUST be available and functional on personal-edition binaries for the three open-source recipes.
- **Manual verification**: one walk-through per recipe against a real tunnel before tagging v1, documented in `specs/052-mcp-tunnel-recipe/verification/walkthrough-<recipe>.md`.
