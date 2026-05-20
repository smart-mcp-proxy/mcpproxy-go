# Feature Specification: mcpproxy-server behind Anthropic MCP Tunnel (deploy recipe)

**Feature Branch**: `052-mcp-tunnel-recipe`
**Created**: 2026-05-20
**Status**: Draft
**Edition**: Server only (no impact on personal edition)
**Input**: User description: "Document and (minimally) support deploying mcpproxy-server behind an Anthropic MCP Tunnel (https://platform.claude.com/docs/en/agents-and-tools/mcp-tunnels/overview) so that Managed Agents and Messages-API customers can reach a private mcpproxy in their VPC without opening inbound firewall ports — exactly one tunnel hostname fronting the proxy's full multiplexed toolset."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Managed Agents reaches private mcpproxy through one tunnel hostname (Priority: P1)

A platform team runs `mcpproxy-server` inside their VPC, behind a typical no-inbound-ingress firewall, with N upstream MCP servers configured (GitHub, internal Postgres, JIRA, etc.). They register an Anthropic MCP Tunnel for the organization, deploy the two-container Anthropic stack (`mcp-proxy` + `cloudflared`) alongside `mcpproxy-server` using a recipe we ship, and route a single tunnel subdomain at mcpproxy's `/mcp` endpoint. From the Anthropic Console they create a Managed Agent that lists `https://mcpproxy.<their-tunnel>.tunnel.anthropic.com/mcp` as its MCP server. The agent immediately sees mcpproxy's `retrieve_tools` / `call_tool_*` surface and, through it, every tool from every configured upstream — without any inbound port open on the customer network.

**Why this priority**: This is the *only* Anthropic surface that MCP Tunnels reaches today (Managed Agents + Messages API; explicitly not claude.ai/mobile/desktop/Claude Code). It is also exactly the customer profile mcpproxy-server is built for. Without a documented, working deployment recipe the integration is theoretically possible but practically out of reach — operators have to reverse-engineer the auth bridge, the route map, and the recommended hardening on their own.

**Independent Test**: With the recipe checked into the repo, a fresh operator on a clean Linux VM can clone, copy `examples/mcp-tunnel/`, set the documented environment variables (`TUNNEL_TOKEN`, `MCPPROXY_API_KEY`, tunnel domain, agent-token name), run `docker compose up`, and within 5 minutes have a Managed Agent in the Anthropic Console successfully calling `retrieve_tools` and one upstream tool through the tunnel.

**Acceptance Scenarios**:

1. **Given** an operator has a tunnel registered in their Anthropic workspace with a CA certificate uploaded, **When** they `docker compose up` the recipe and copy the tunnel URL into a Managed Agent's MCP server list, **Then** the agent can call `retrieve_tools` and receive the proxy's tool index.
2. **Given** the recipe is running with three configured upstream MCP servers, **When** the Managed Agent calls `call_tool_read` for any of them, **Then** the tool executes inside the customer's VPC and the response returns through the tunnel.
3. **Given** an operator follows the recipe verbatim, **When** they inspect the running containers, **Then** mcpproxy-server's `/mcp` endpoint is reachable from `mcp-proxy` only (not the public internet), accepts the agent token, and rejects requests without the token with HTTP 401.
4. **Given** the recipe is running, **When** the operator restarts the host, **Then** all three containers come back up in order (`mcpproxy-server` ready before `mcp-proxy` routes traffic) and Managed Agent calls resume working.

---

### User Story 2 - Operators understand exactly what auth bridges what (Priority: P1)

The recipe makes it unambiguous which security boundary terminates where: outer mTLS (Anthropic↔Cloudflare), inner TLS (Cloudflare↔customer's `mcp-proxy`, terminated with a customer-managed CA), and the *MCP-level* auth between `mcp-proxy` and `mcpproxy-server` (an agent token issued by mcpproxy with read/write/destructive scoping). An operator reading the recipe knows where to put each secret, what rotates on what schedule, and what attack a leak of each one enables.

**Why this priority**: The tunnel transport itself "carries encrypted traffic but does not authenticate to it" (Anthropic's own framing). MCP Tunnels pass no end-user identity downstream. If an operator deploys the recipe without understanding that `mcpproxy-server` is the *only* component enforcing per-agent permissions, they will assume the tunnel is the security boundary and over-trust agent requests. This is the exact class of misconfiguration mcpproxy's agent-token model was designed to prevent — but only if operators know to wire it in.

**Independent Test**: A reviewer reads `docs/deployments/mcp-tunnel.md` cold and can answer, without looking at other files: (a) what credential allows mcpproxy to call upstream X; (b) what credential allows Anthropic to call mcpproxy; (c) what credential allows Cloudflare to reach Anthropic's edge; (d) which credential rotates on what cadence; (e) which credential, if leaked alone, lets the attacker do what.

**Acceptance Scenarios**:

1. **Given** the deployment guide, **When** a reviewer reads it once, **Then** they can produce the auth-layer table above unprompted.
2. **Given** the recipe, **When** an operator runs `make tunnel-up`, **Then** the generated `.env.example` or equivalent calls out every required secret with a one-line description and a one-line "what an attacker can do if this leaks".
3. **Given** the recipe, **When** mcpproxy starts inside it, **Then** it logs (info level) the agent-token name in use and the upstream servers it intends to route, so operators can confirm scoping at a glance.

---

### User Story 3 - mcpproxy-server runs with a tunnel-safe configuration profile (Priority: P2)

An operator who points `mcpproxy-server` at a config that includes a top-level `tunnel: {}` block (or equivalent flag) gets a tunnel-safe startup posture by default: bind `/mcp` only on the in-cluster network name expected by `mcp-proxy`'s `routes:` map, refuse to start the Web UI on the tunneled listener (Web UI continues to be available on the management listener for operators), require agent-token auth on `/mcp` (not just API key), and expose a `/healthz` for the tunnel proxy's liveness probe. The operator no longer has to manually compose four or five settings to harden the deployment.

**Why this priority**: Story 1 (the recipe) makes deployment possible. Story 3 reduces the chance of misconfiguration to near-zero by giving operators a single switch. Important but not blocking — without it, the recipe still works; with it, the recipe is significantly safer for the median operator and the documentation can shrink.

**Independent Test**: Starting `mcpproxy-server` with `--tunnel-mode` (or `tunnel.enabled: true`) on a fresh VM with no manual hardening produces a process that: (a) does not respond on `/ui/` on the tunneled port; (b) returns 401 on `/mcp` without a valid agent token; (c) returns 200 on `/healthz` immediately when the process is ready and 503 before; (d) logs a single banner line confirming tunnel-safe mode is active.

**Acceptance Scenarios**:

1. **Given** `tunnel.enabled: true` in the config, **When** mcpproxy starts, **Then** the tunneled listener serves `/mcp` and `/healthz` only.
2. **Given** tunnel mode is active, **When** a request hits `/mcp` without an `X-API-Key` matching an agent token, **Then** the response is 401 with a request-id (no fallthrough to the regular API key).
3. **Given** tunnel mode is active, **When** the operator separately enables the management listener on a different port, **Then** the Web UI and full REST API are available there for operators inside the VPC.

---

### User Story 4 - The recipe survives container restarts and credential rotation (Priority: P2)

Tunnel server certificates renew every ~90 days by default (Anthropic's setup binary ships a CronJob). The recipe handles this without operator intervention: the cert-renew cron lives in the compose/helm stack, restarts only `mcp-proxy` (not `mcpproxy-server` or the upstreams), and mcpproxy-server is restartable independently of the tunnel side. An operator who rotates the mcpproxy agent token does so without needing to restart `mcp-proxy` or `cloudflared`.

**Why this priority**: Day-2 operations. Without it, the recipe works for the first 90 days then quietly breaks. Lower priority than US3 because it can be papered over with operator runbooks while US3 is fixed in code.

**Independent Test**: Run the recipe for a simulated 90-day cycle (override cert validity to 5 minutes, renew threshold to 1 minute). Observe: `mcp-proxy` reloads with a fresh cert without dropping the cloudflared tunnel; `mcpproxy-server` is unaffected; agent-token rotation (re-issuing via `mcpproxy token regenerate <name>`) only requires updating the env var on `mcp-proxy`'s side and a `docker compose up -d --no-deps mcp-proxy`.

**Acceptance Scenarios**:

1. **Given** the recipe is running and cert renewal triggers, **When** renewal completes, **Then** `mcpproxy-server` continues serving without restart and Managed Agent calls succeed across the transition.
2. **Given** an operator rotates the mcpproxy agent token, **When** they restart only `mcp-proxy` with the new token, **Then** Managed Agent calls succeed within 30s of the restart with no downtime longer than that.

---

### Edge Cases

- **Beta header drift**: Anthropic requires `anthropic-beta: mcp-client-2025-11-20` from Managed Agents callers and `anthropic-beta: mcp-tunnels-2026-05-19` for the Admin API. Both will rotate. The recipe MUST NOT bake the date into mcpproxy itself; the headers belong on the Anthropic side. If a future date breaks the recipe, the fix is doc-only.
- **10-tunnels-per-org cap**: A customer with multiple environments (dev/stage/prod) sharing one Anthropic workspace can run out of tunnels. The recipe documents fronting all environments through a single `mcpproxy-server` with environment-scoped upstreams + agent tokens, rather than one tunnel per environment.
- **Tunnels are not on claude.ai today**: An operator may follow the recipe and then be surprised their colleagues cannot use the resulting URL from Claude Desktop or claude.ai. The doc states this once, prominently, near the top.
- **MCP Tunnels are Research Preview, "as-is"**: Anthropic reserves the right to discontinue. The recipe must include a fallback recommendation ("if tunnels go away, here's how the same stack runs behind a customer-managed ingress + OAuth").
- **OAuth-on-upstream still required**: The tunnel does not authenticate Anthropic to the upstream MCP servers mcpproxy is fronting. Per-upstream OAuth (or per-upstream tokens) remain mcpproxy's responsibility, exactly as today.
- **Subprocessor concern**: Cloudflare sees egress IP and `*.tunnel.anthropic.com` subdomain. The doc notes this for customers with strict subprocessor reviews.
- **Path is forwarded untouched**: `mcp-proxy`'s `routes:` map strips the subdomain but preserves the path. mcpproxy's `/mcp` already aligns with FastMCP's default; the recipe should hard-code this and warn against changing it.
- **No user identity flows down the tunnel**: A single Managed Agent → one agent token in mcpproxy → one set of allowed servers and permissions. Per-end-user scoping needs a different design (one tunnel hostname per Managed Agent, or per-Agent agent tokens), documented as a non-default pattern.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001** The repository SHALL contain `examples/mcp-tunnel/` with a working `docker-compose.yaml` that boots `mcpproxy-server`, Anthropic's `mcp-proxy`, and `cloudflared` together.
- **FR-002** The compose file MUST pin Anthropic's `mcp-proxy` image by SHA, not floating tag, so the recipe stays reproducible across Anthropic image rolls.
- **FR-003** The compose file MUST run `mcpproxy-server` with the management listener (REST + Web UI) bound to a non-tunneled internal port and the `/mcp` listener exposed only to the `mcp-proxy` container.
- **FR-004** The compose file MUST configure `mcp-proxy`'s `routes:` map to a single entry pointing at `mcpproxy-server`'s `/mcp` listener and MUST NOT expose other mcpproxy endpoints through the tunnel.
- **FR-005** The recipe MUST require an agent token (from mcpproxy's `mcp_agt_` family, Spec 028) on the `/mcp` listener and MUST NOT permit unauthenticated access from `mcp-proxy`.
- **FR-006** The recipe MUST ship a sample `.env.example` enumerating every required secret with a one-line "what an attacker can do if leaked" annotation, and a `.gitignore` for the real `.env`.
- **FR-007** The repository SHALL contain `docs/deployments/mcp-tunnel.md` covering: when to use this recipe, the auth-layer table (outer mTLS / inner TLS / agent token / upstream OAuth), step-by-step setup, day-2 operations (cert renewal, agent-token rotation, log inspection), and limitations (not on claude.ai, 10-tunnels cap, Research Preview status).
- **FR-008** The doc MUST link to Anthropic's `mcp-tunnels/overview` and `mcp-tunnels/security` pages as canonical sources for tunnel-side mechanics; mcpproxy's doc MUST NOT duplicate Anthropic's setup steps in a way that drifts over time.
- **FR-009** The doc MUST include a worked example of a Managed Agent definition that calls the tunneled mcpproxy and successfully invokes `retrieve_tools` and one downstream tool.
- **FR-010** Server edition's existing `mcpproxy-server` binary SHALL gain a `--tunnel-mode` flag (or `tunnel.enabled: true` config block) that, when set, applies the User Story 3 hardening profile: agent-token-only auth on `/mcp`, no Web UI on the tunneled port, `/healthz` enabled, banner log on startup confirming the mode.
- **FR-011** When tunnel mode is active, mcpproxy MUST refuse to start if no agent token is configured for the tunneled listener.
- **FR-012** When tunnel mode is active, mcpproxy MUST refuse to start if `enable_web_ui: true` is set on the tunneled listener.
- **FR-013** Logs and activity records for tunnel-routed requests MUST include the agent-token name so operators can trace each call back to a specific Managed Agent.
- **FR-014** The recipe SHALL include a healthcheck for `mcpproxy-server` (`GET /healthz`) wired into compose so `mcp-proxy` starts only after `mcpproxy-server` is ready.
- **FR-015** The recipe SHALL include a smoke-test script (bash) that verifies: containers up, `mcp-proxy` rejects unauthenticated direct requests, `mcpproxy-server` rejects requests without an agent token, end-to-end `retrieve_tools` succeeds via the tunnel URL.
- **FR-016** The recipe MUST work both with Anthropic's manual setup flow (operator-supplied CA) and the programmatic-access flow (`setup init` via Workload Identity Federation). The doc covers both; the compose stays generic.
- **FR-017** Tunnel mode MUST NOT affect personal-edition behavior. Personal edition builds MUST NOT compile in any tunnel-specific code or expose `--tunnel-mode`. (Behind the existing `//go:build server` tag.)
- **FR-018** The doc MUST surface the Research-Preview / "as-is" status of Anthropic MCP Tunnels prominently and recommend mcpproxy operators sign Anthropic's preview form before relying on the recipe in production.

### Out of Scope (v1)

- **Personal edition** integration. Tunnels are server-edition-only. The personal-laptop-to-mobile-Claude story is not addressed here and is explicitly called out as a non-goal in the doc.
- **Replacing Anthropic's `mcp-proxy` image** with a mcpproxy-native tunnel proxy. That is a substantially larger undertaking (re-implementing inner-TLS-in-WebSocket framing, hostname fan-out, CA registration flow); revisit only if a real customer asks AND Anthropic signals partner integrations are welcome.
- **Per-end-user identity** propagated from Managed Agent down to upstream MCP servers. Tunnels pass no user identity; mcpproxy sees one agent-token per tunnel. Per-user scoping requires either one tunnel per agent or a separate identity mechanism, neither in scope here.
- **Helm chart** for the recipe. Anthropic ships a Helm chart for the tunnel side. mcpproxy-server already has its own deployment surface. v1 ships Docker Compose; Helm is a follow-up if asked.
- **Automated tunnel provisioning** from inside mcpproxy. Calling Anthropic's Admin API to create tunnels, upload CA certs, or rotate them is an admin-CLI concern, not a runtime concern. v1 has the operator do this through the Anthropic Console.
- **Sharing mcpproxy across multiple Managed Agents through different tool subsets per agent** — possible via N agent tokens with `allowed_servers` scoping, but UX/CLI to manage that fleet is its own spec.
- **Latency/SLI dashboards** for tunneled traffic. Existing mcpproxy observability (`X-Request-Id`, activity log, request_id correlation) is sufficient; tunnel-specific p99 dashboards can wait.

### Key Entities

- **Tunnel-Mode Listener**: The `/mcp` HTTP endpoint as exposed to `mcp-proxy` when `tunnel.enabled` is true. Differs from the regular `/mcp` listener in three ways: agent-token-only auth, no Web UI, `/healthz` enabled. Same code path otherwise.
- **Recipe Bundle**: The contents of `examples/mcp-tunnel/` — compose file, `.env.example`, `config/mcpproxy.json` skeleton, `config/mcp-gateway.yaml` skeleton, smoke-test script, README pointing at the canonical doc.
- **Agent Token (Tunnel-Scoped)**: A normal mcpproxy agent token (Spec 028), generated for use by exactly one Managed Agent via exactly one tunnel. Identified by name in logs. Permissions default to `read` only; `write`/`destructive` require explicit operator action.
- **Deployment Guide**: `docs/deployments/mcp-tunnel.md`. Single source of truth on the mcpproxy side for this integration. Links to Anthropic's docs for tunnel-side details. Owns the auth-layer table.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001** A fresh operator (no prior tunnel exposure) following only `docs/deployments/mcp-tunnel.md` can stand up a working Managed Agent ↔ private-VPC mcpproxy ↔ upstream MCP server connection in **≤ 30 minutes**, measured from `git clone` to a successful `retrieve_tools` round-trip.
- **SC-002** The recipe's smoke-test script (FR-015) passes end-to-end on a clean Ubuntu 22.04 VM, with Docker 24+, against a freshly provisioned Anthropic tunnel in a sandbox workspace, in **≤ 5 minutes**.
- **SC-003** A reviewer reading `docs/deployments/mcp-tunnel.md` once can correctly answer five auth-boundary questions (US2 independent test) in **≤ 10 minutes** without consulting other sources.
- **SC-004** When `tunnel.enabled: true` is set without an agent token configured, `mcpproxy-server` refuses to start with a clear error pointing at the missing token, in **100% of attempts** across the existing CI matrix (Linux/macOS/Windows server builds).
- **SC-005** When `tunnel.enabled: true` is set with `enable_web_ui: true` on the tunneled listener, `mcpproxy-server` refuses to start with a clear error pointing at the conflict, in **100% of attempts**.
- **SC-006** Cert-renewal day-2 simulation (US4 independent test) completes a full rotation with **zero requests dropped** as observed by a continuous `retrieve_tools` heartbeat from a test Managed Agent.
- **SC-007** Personal-edition builds (`go build ./cmd/mcpproxy` with no `-tags server`) succeed unchanged after this spec lands; binary size delta ≤ **1 %** vs the pre-spec personal build; no tunnel symbols present in the personal binary (`go nm` check).

## Assumptions

- Anthropic MCP Tunnels remain a Research Preview gated behind the Managed Agents access form. The recipe assumes the operator has been approved for that preview and has at least one workspace where they can create tunnels.
- Anthropic's `mcp-proxy` Docker image continues to be published to a public registry pinnable by SHA. If Anthropic migrates to a closed/auth-required registry, the recipe needs a doc update only.
- The MCP Streamable-HTTP transport (the only one Anthropic's tunnel supports for upstreams) remains the default for mcpproxy's `/mcp` endpoint and for the `mark3labs/mcp-go` server library mcpproxy uses.
- The 10-tunnels-per-organization cap and 90-day default cert validity are stable enough for v1 documentation. If Anthropic changes them, the doc updates without code changes.
- mcpproxy-server's existing agent-token system (Spec 028) is sufficient as the Anthropic↔mcpproxy auth layer; no new token type is needed. If a future Anthropic feature passes an end-user identity claim that we want to honor, that is a separate spec.
- The personal edition does not need any awareness of MCP Tunnels in v1. (Confirmed: tunnels are explicitly excluded from claude.ai/Code/Desktop, which are the surfaces the personal edition targets.)

## References

- Anthropic MCP Tunnels Overview — https://platform.claude.com/docs/en/agents-and-tools/mcp-tunnels/overview
- Anthropic MCP Tunnels Quickstart — https://platform.claude.com/docs/en/agents-and-tools/mcp-tunnels/quickstart
- Anthropic MCP Tunnels Security — https://platform.claude.com/docs/en/agents-and-tools/mcp-tunnels/security
- Anthropic MCP Tunnels Compose Reference — https://platform.claude.com/docs/en/agents-and-tools/mcp-tunnels/deploy-compose
- Anthropic MCP Tunnels Troubleshooting — https://platform.claude.com/docs/en/agents-and-tools/mcp-tunnels/troubleshooting
- mcpproxy agent tokens — Spec 028, `docs/features/agent-tokens.md`
- mcpproxy server edition + multi-user — Spec 024 (server multi-user OAuth), `internal/teams/`
- mcpproxy MCP server surface — `internal/server/mcp.go`, `internal/server/server.go:1662-1684`
- Adjacent spec — 051 Registry Modernization (`specs/051-registry-modernization/spec.md`)

## Commit Message Conventions *(mandatory)*

### Issue References

Tag commits and PRs with `Refs: spec/052` and link related Anthropic doc pages in PR descriptions where relevant.

### Co-Authorship

No `Co-Authored-By: Claude` lines in commits or PR bodies — this repo overrides the global rule (see `MEMORY.md` → "No Claude git attribution").

### Example Commit Message

```
feat(server/052): mcpproxy-server tunnel-safe mode + deploy recipe

Adds --tunnel-mode (and tunnel.enabled config block) for the server
edition. When set, the /mcp listener requires an agent token, the Web
UI is refused on the tunneled port, and /healthz is enabled. Ships the
recipe under examples/mcp-tunnel/ + docs/deployments/mcp-tunnel.md.

Personal edition unaffected; tunnel code lives behind //go:build server.

Refs: spec/052
```

## Changes

This spec introduces:

- **New code (server edition only, build tag `server`)**: `tunnel-mode` flag/config + the three guard rails it enables on the `/mcp` listener (agent-token-only, no Web UI, `/healthz`). Estimated < 200 LOC plus tests.
- **New recipe**: `examples/mcp-tunnel/` with compose file, env example, config skeletons, smoke-test script.
- **New doc**: `docs/deployments/mcp-tunnel.md` — single source of truth on the mcpproxy side.
- **Doc updates**: server-edition section of `CLAUDE.md` gains one line pointing at the doc and example. No personal-edition docs touched.

This spec does NOT:

- Add any personal-edition behavior.
- Re-implement Anthropic's `mcp-proxy` image.
- Provision tunnels programmatically from inside mcpproxy.
- Pass end-user identity through the tunnel.

## Testing

- **Unit tests** (server tag): `tunnel-mode` config validation — reject missing agent token, reject `enable_web_ui: true` on tunneled listener, accept the minimal valid config. Banner log assertion.
- **Integration test** (server tag): start `mcpproxy-server --tunnel-mode` with a sample agent token, hit `/mcp` without auth (expect 401), hit `/mcp` with the token (expect MCP `initialize` success), hit `/ui/` on the tunneled port (expect 404 or refused), hit `/healthz` (expect 200 when ready).
- **Smoke test** (bash, shipped in `examples/mcp-tunnel/`): asserts the four points above against a live compose stack. Runs in CI against a stub tunnel (since real tunnels need an Anthropic workspace) — a local `mcp-proxy` mock is sufficient.
- **Personal-edition guard**: existing CI step that builds `./cmd/mcpproxy` without `-tags server` must continue to pass; new check: `go nm mcpproxy | grep -i tunnel` returns empty.
- **Manual verification**: one walk-through of the recipe against a real Anthropic tunnel in a sandbox workspace before tagging v1. Documented in `specs/052-mcp-tunnel-recipe/verification/walkthrough.md` (created alongside the implementation PR, not this spec).
