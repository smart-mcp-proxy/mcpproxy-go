# MCPProxy Server Edition — Research Report and Recommendation

Date: 2026-09-14 · Author: planning/design strategist agent · Inputs: four collector files in `evidence/` (`hn-reddit.md`, `github.md`, `web-vendors.md`, `internal-inventory.md`), repo `CLAUDE.md`, `docs/development/server-edition-multiuser-auth.md`, `Dockerfile`, `docs/getting-started/installation.md`, `README.md` (all read-only). Citations are `file:line` into the collector files or URLs. Where the evidence does not reach, the word used is "unknown".

---

## 1. Executive summary

- **One-line recommendation: adopt posture (a) — "the personal edition, first-class when run headless in a container" — plus a strictly bounded slice of (c): keep the shipped SSO front door, finish only the three server-edition items the demand evidence ranks highest (generic OIDC, IdP-group → server allowlist, attributable JSONL audit), and cut or freeze the rest of the latent multiuser/credential-injection code until a real user asks for it.**
- The real container audience is ~10× the server-edition one: in 90 days **362 personal-edition installs from 86 IPs ran in containers, 113 of them long-lived (≥24 h)**, plus 153 long-lived headless Linux installs, versus **37 server-edition installs from 8 IPs** (`internal-inventory.md:241-261`). All server-edition installs to date were self-built and pre-date the public image (`internal-inventory.md:197`).
- What people ask a team gateway for is boring and consistent across all three external lanes: **SSO/IdP login + group-based access (14 explicit asks), per-call audit with user attribution (13), credentials off the laptop (12), per-tool/per-server policy (10), one central endpoint/catalog (9)** (`hn-reddit.md:152-156`). The canonical demand post says exactly this in one sentence (`hn-reddit.md:78`).
- **SIEM/Splunk/Datadog exporters (1 ask), air-gapped installs (0), quotas as a headline (5, mostly vendor)** are vendor-checklist items, not user demand (`hn-reddit.md:158,166,167`; `github.md:169`). Do not build connectors.
- Vault integration is asked for as *behaviour* ("gateway holds creds, agents get sessions"), not as a HashiCorp SDK; the one concrete OSS ask (obot #6180) was satisfied by syncing Vault into Kubernetes Secrets (`github.md:96`). A `${file:/run/secrets/x}` provider (S effort) covers Vault/ESO/1Password-operator/SOPS at once; MCPProxy has only `${env:}` and a keyring provider that is useless in containers (`internal-inventory.md:95-97`).
- The server edition today is an SSO login + admin REST + agent-token scoping on top of the personal proxy. The multi-user `Router`, `workspace.Manager`, `TokenExchanger` and the whole credential-injection chain (`Client.SetBrokeredAuth`) have **zero production callers**, and `docs/cli/credential-commands.md` claims injection happens (`internal-inventory.md:10,45-52`). The docs overclaim; that is a Phase 0 fix regardless of strategy.
- Competing feature-for-feature is not viable: agentgateway (4.8k★, Linux Foundation), IBM ContextForge (4.5k★), ToolHive (2.2k★, Go, VC-backed enterprise tier) all ship Helm, OIDC, RBAC, rate limits and OTel for free (`github.md:22-24`); ~60 "Show HN: MCP gateway" launches since 2025-08 almost all scored <10 points (`hn-reddit.md:45`); mcpproxy-go has 348★, no organic HN/Reddit mention, and zero external SSO/RBAC/Helm/Vault asks (`hn-reddit.md:63`; `github.md:152`).
- MCPProxy's genuine differentiators are edition-neutral and already shipped: BM25 tool discovery (context bloat is a top-voted ask in *competitor* repos: metamcp #202, docker #187, tbxark #38 — `github.md:39,69,82`), tool-drift/rug-pull detection that InfoQ says gateways lack (`web-vendors.md:46`), quarantine states that map onto the Approved/Review/Blocked model Noma sells (`web-vendors.md:57`), and intent-typed `call_tool_read|write|destructive`. Market these into the container audience instead of adding gateway parity.
- Docker image: keep investing, but make it the **personal edition's** image with a `full` variant (node/uv/git for stdio upstreams — issue #40's actual use case, `github.md:140`), non-root, docker-compose, a minimal Helm chart, and fix the day-1 traps first (`Secure=false` cookie, read-only config, `/root/.mcpproxy`, no `${file:}` secrets — `internal-inventory.md:135-145`).
- Measurement gap to close in Phase 0: telemetry cannot see whether `server_edition` is enabled, which IdP, or how many users — the field does not exist (`internal-inventory.md:129,155`).

---

## 2. Method and evidence base

| Lane | What was collected | Volume | Known gaps |
|---|---|---|---|
| HN + Reddit (`hn-reddit.md`) | HN Algolia story/comment searches (12 queries, 40 full comment trees); Reddit via Arctic Shift archive (~600 posts, ~450 comments, 22 full threads) | 65 tabulated signals, 17 counter-signals, capability tally (`hn-reddit.md:76-169`) | Reddit rate-limited: the 11×11 subreddit×query matrix was not completed (`hn-reddit.md:49-61`); scores are archive snapshots; Algolia typo-tolerance inflates "sso"/"helm" hits |
| GitHub (`github.md`) | 33 repos' metadata, 41k issue/PR records across 23 dedicated gateway repos, discussions for 20 repos, file-tree greps, mcpproxy-go's own 274 issues + 12 discussions, 58-user stargazer sample | 50 ranked competitor issues, 12 discussions, matrix (`github.md:20-124`) | Keyword regex may miss asks phrased differently; stargazer company rate (34 %) has no competitor baseline (`github.md:162`); Kong/Traefik/Composio lanes were empty (`github.md:126-132`) |
| Web / vendors / guidance (`web-vendors.md`) | 30 WebSearch queries; ~22 vendor product/pricing pages; NSA CSI via a law-firm summary (primary PDF 403); X only via blog embeds | 48 signals, 24-vendor matrix, guidance→control mapping (`web-vendors.md:28-133`) | Lane is vendor/analyst-heavy (V/A dominate); several pricing pages 404 or price-less; Unit 42/Trend Micro not verified; X texts unrecoverable except via embeds (`web-vendors.md:15-22,79`) |
| Internal (`internal-inventory.md`) | Code inventory at HEAD b39800a89 with file:line, D1 telemetry queries with CI filter, release-asset downloads, GHCR tag check | Capability matrix, day-1 operator list, 7 telemetry tables | GHCR pull counts unavailable (403, `internal-inventory.md:296`); server rows have no `machine_id`, containers without volumes churn anonymous IDs (`internal-inventory.md:191-197`); multi-user enablement is unmeasurable (`internal-inventory.md:155`) |

**Weighting rule used below.** The HN/Reddit and GitHub lanes are demand-side (people describing needs or filing issues); the web lane is mostly supply-side (vendors describing what they sell). Where the lanes disagree, demand-side wins. The one material disagreement: the web collector rated "log/audit exporters (OTel/SIEM)" as the highest-frequency ask (`web-vendors.md:141`), but the demand-side lanes count exactly one SIEM ask (a checklist reply) and no unfulfilled OTel/SIEM issue anywhere (`hn-reddit.md:166`; `github.md:169`). The reconciling read: users want an **attributable audit log with a stable schema**; vendors sell **connectors**. This report treats the former as must-have and the latter as no-demand.

---

## 3. What users need from a server edition (evidence-ranked)

Explicit asks = people without a product to sell describing the need (`hn-reddit.md:148`).

| Rank | Capability | Tier | Evidence strength | Representative quote | Sources |
|---|---|---|---|---|---|
| 1 | SSO / corporate IdP login (Okta, Entra, Keycloak) + group-based access | **Must-have** | Strong: 14 asks / 9 built / 8 vendor (`hn-reddit.md:152`); top competitor issue agentgateway #239 👍24 (`github.md:58`); generic-OIDC asks agentic-community #189, mcphub #527 (`github.md:76,84`) | "MCP servers hosted centrally, users authenticate with their corporate identity, access is granted by group membership, and every tool call is logged somewhere I can query." | https://www.reddit.com/r/mcp/comments/1w4oiiv/ (`hn-reddit.md:78`) |
| 2 | Per-call audit with user attribution, stable schema security can approve once | **Must-have** | Strong: 13 asks (`hn-reddit.md:153`); "tipping point from proxy to gateway" (`hn-reddit.md:97`); NSA "what tool was requested, by whom, and what resulted" (`web-vendors.md:121`); MCP roadmap names audit trails first (`web-vendors.md:30`) | "the moment someone asks 'can you show me every time this tool was called and by whom' and you realize you can't." | https://www.reddit.com/r/mcp/comments/1uye015/ ; https://www.reddit.com/r/mcp/comments/1uye015/comment/oy21g3f/ (`hn-reddit.md:99`) |
| 3 | Credentials off the laptop; gateway holds/refreshes upstream creds, agents get a session | **Must-have (behaviour), differentiator (per-user injection)** | Strong: 12 asks (`hn-reddit.md:154`); Composio breach 5,241 keys (`web-vendors.md:51`); Snowflake "inject authorized credentials on the fly" (`web-vendors.md:34`); OSS peers mostly lack per-user upstream creds (agentgateway #239 open; mcpjungle #254; mcp-hub #116 — `github.md:58,77,95`) | "Broker pattern: the client holds one short-lived session token; a server-side broker holds the provider creds, does OAuth refresh, injects per request, scopes per tool call, and writes the audit line." | https://www.reddit.com/r/mcp/comments/1voezx0/comment/p3r541p/ (`hn-reddit.md:104`) |
| 4 | Per-server first, per-tool for mixed-risk servers; deny/HITL for destructive; re-consent on tool drift | **Must-have (per-server) / differentiator (drift)** | Medium-strong: 10 asks, split on granularity (`hn-reddit.md:155,205`); metamcp #103/#179 (`github.md:74,92`); drift re-consent 3 asks (`hn-reddit.md:169`); InfoQ: gateways "do not detect when the tool definitions a team approved last week change" (`web-vendors.md:46`) | "risk-tier the tools, not the servers. Read-only on non-sensitive data = fast lane, self-service. Writes or anything touching personal data = full review." | https://www.reddit.com/r/mcp/comments/1uye015/comment/oy21g3f/ (`hn-reddit.md:99`) |
| 5 | One central endpoint; approved-server list; disable public catalogs | **Must-have** | Medium: 9 asks (`hn-reddit.md:156`); docker #195 "heavily regulated industry", disc #180 "I'd happily pay" (`github.md:83,115`) | "Everyone installs this gateway as their only 'MCP', then at a central location we can add different MCP tools and everyone automatically gains access to them." | https://news.ycombinator.com/item?id=45012257 (`hn-reddit.md:124`) |
| 6 | Self-hosted, open source, runs as a normal service in existing K8s/observability; governance not paywalled | **Must-have (posture)** | Medium: 6 hard-requirement asks (`hn-reddit.md:163`); fin-services eval "hard requirement" (`web-vendors.md:44`) | "Does it run as a normal service you can stick in your existing K8s/observability/CI setup, or does it want to own its own world?" | https://www.reddit.com/r/mcp/comments/1u12f3w/comment/oqzwhhe/ (`hn-reddit.md:93`) |
| 7 | Small context / tool discovery (not a "server" ask, but the top ask inside gateway repos) | **Differentiator (already shipped)** | Medium: metamcp #202 👍6, docker #187, tbxark #38 16💬 (`github.md:39,69,82`); Simon Willison embed (`web-vendors.md:43`) | "50+ tools consume 20,000-25,000 tokens (60-80% of context window)" | https://github.com/metatool-ai/metamcp/issues/202 |
| 8 | Kubernetes deployment / Helm | **Table stakes, not a feature** | Medium-weak as an ask (6 assumed, 3 explicit Helm — `hn-reddit.md:157`); every serious peer ships a chart (`github.md:168`) | one user "3 shot" his own gateway "if you count a second prompt to generate the Helm Chart" | https://news.ycombinator.com/item?id=48885712 (`hn-reddit.md:123`) |
| 9 | PII/DLP redaction of the gateway's own logs and traces | Differentiator (later) | Weak-medium: 5 asks (`hn-reddit.md:159`); redaction is paid at Lasso/MCP Manager (`web-vendors.md:100,104`) | "They are going to be a PII landmine." | https://news.ycombinator.com/item?id=45523623 (`hn-reddit.md:128`) |
| 10 | HITL approval hold for irreversible tools | Differentiator (later) | Weak: 4 asks vs 6 vendor pitches (`hn-reddit.md:161`); IBM #5437 0👍/13💬 (`github.md:102`) | "the Gateway holds the call instead of invoking the downstream tool." | https://www.truefoundry.com/blog/mcp-tool-approval-human-gate-call-path (`web-vendors.md:63`) |
| 11 | Rate limits / per-tenant quotas | No-demand as headline | Weak: 5 asks, LLM-gateway-shaped (`hn-reddit.md:158`; `github.md:169`); mcpproxy #955 from one "shared systemd service" user (`github.md:142`) | — | — |
| 12 | SIEM / Splunk / Datadog export | **No-demand** | 1 checklist ask, 0 built (`hn-reddit.md:166`); Datadog appears only as an upstream target | — | — |
| 13 | Air-gapped install | **No-demand** | 0 asks on HN/Reddit (`hn-reddit.md:167`); one IBM issue, closed (`github.md:94`) | — | — |
| 14 | SCIM, SAML, org-level multi-tenancy | **No-demand** (vendor tiering) | Only in vendor copy (`web-vendors.md:61-62`); multi-tenancy 3 asks (`hn-reddit.md:162`) | — | — |

Two cross-cutting facts: **the counter-signals never argue against central control** — they argue it should live in IdP scopes, DB roles, oauth2proxy, or 1Password (`hn-reddit.md:196`); and **first-party absorption is coming** — Anthropic's Enterprise-Managed Authorization (XAA/ID-JAG) removes the "OAuth relay" value for Claude clients (`hn-reddit.md:193`). Both narrow what a solo-maintained gateway can durably own to the things that sit *between* IdP and upstream: policy, drift detection, audit, and (if at all) credential brokering.

---

## 4. What we have today

Condensed from `internal-inventory.md §1`; "half-built" = code exists with no production caller.

| Area | Shipped | Half-built / unwired | Missing |
|---|---|---|---|
| Identity | Google, GitHub, Microsoft OAuth login; email-domain allowlist; admin role (hot-reloadable, #1169); sessions on BBolt; HS256 JWT; agent tokens with tenant identity, owner gate fail-closed, scope narrowed to entitlement (`internal-inventory.md:25-38`) | — | **Generic OIDC** (registry has exactly 3 keys, `internal-inventory.md:28`); GitHub-org/Workspace-group gating; SAML; any role beyond admin/user |
| Multi-user MCP | Shared-server flag; per-user activity attribution; per-user AES-256-GCM credential store + OAuth "connect" flow; broker audit into the activity log (`internal-inventory.md:44-54`) | **`multiuser.Router`/`ToolFilter`** (`router.go:42-56` "NOT YET WIRED … LATENT"); **`workspace.Manager`** (personal servers are DB rows, never connected); **`TokenExchanger`** RFC 8693/OBO; **`CredentialResolver`/`HeaderInjector` → `Client.SetBrokeredAuth` zero callers** (`internal-inventory.md:45-52`). `docs/cli/credential-commands.md` says injection happens (`internal-inventory.md:10`) | Teams/groups entity; per-tenant token cap (#1177); per-user rate limit (`internal-inventory.md:37,108`) |
| Isolation on `/mcp` today | Rests **only** on agent-token `allowed_servers` (`internal-inventory.md:46`); 17 of 46 server installs run with `require_mcp_auth=0` (`internal-inventory.md:143,225-229`) | — | — |
| Observability | `/healthz` `/readyz` `/livez`; Prometheus 25 series (opt-in); OTLP traces with `user_id` span attr; zap JSON logs + rotation; activity log 90 d in BBolt; `GET /api/v1/activity/export` + pull-based SIEM recipe (`internal-inventory.md:79-88`) | — | OTel metrics/logs; syslog/log shipping; push audit sink; per-user metric labels (by design) |
| Secrets | `${env:}` everywhere; `${keyring:}` (no backend in containers) (`internal-inventory.md:94-95`) | `MCPPROXY_CRED_KEY` from env or plaintext config; store silently disabled if absent (`internal-inventory.md:48,98,140`) | `${file:}`; Vault/AWS/GCP/1Password/SOPS; KMS |
| Limits | Global concurrency + queue (#955), per-server concurrency, graceful shutdown, TLS/mTLS (`internal-inventory.md:106-112`) | Reverse-proxy awareness partial: `Secure` cookie hardcoded false, no `X-Forwarded-For` (`internal-inventory.md:33,113`) | Per-tenant anything; HA (BBolt single writer, exit 3 — `internal-inventory.md:127,136`) |
| Distribution | `ghcr.io/smart-mcp-proxy/mcpproxy-server` (distroless static, multi-arch, root user, tags `latest`/`v0.66.0`/`v0.66.1`); personal-edition `.deb`/`.rpm`/tar.gz with hardened systemd unit (`internal-inventory.md:119-121`) | Server-edition tarball matrix commented out; `docs/configuration.md` has no `server_edition` section (`internal-inventory.md:120,124`) | Helm, compose, K8s docs; README has zero mention of the image/K8s (repo check) |

**Verdict on the half-built code.** Split it in two:

- **Credential injection for shared HTTP upstreams** (resolver → `SetBrokeredAuth`): the transport is done and fail-closed; the missing piece is a per-(user, server) client keyed on `Router.BrokeredConnectionKey` (`internal-inventory.md:317`). This targets the single loudest market ask and the one OSS peers leave open (agentgateway #239 is open at 👍24). Worth finishing — **but only once there is a signal that a mcpproxy user needs it** (see Phase 2 trigger). Until then, fix the docs so they stop claiming it.
- **`workspace.Manager` / connected personal servers, `TokenExchanger` (RFC 8693/OBO), and the general-purpose `Router`/`ToolFilter`**: cut. Process-per-user upstreams are what the market calls "too costly" (`hn-reddit.md:101`), token exchange has one competitor RFC with 👍7 (`github.md:67`) and needs an IdP-side setup most shops refuse (DCR wall, `hn-reddit.md:107,120`), and agent-token scoping already provides the per-user view the Router was meant to compute. Delete or move under an `experimental` build tag; keep the REST records only if the Teams UI depends on them.

---

## 5. Competitive landscape (condensed)

From `github.md:20-50` and `web-vendors.md:87-113`.

| Capability | Free in OSS peers | Paid / gated somewhere | MCPProxy today |
|---|---|---|---|
| Proxy + OIDC/JWT login | IBM, agentgateway, ToolHive, Obot, agentic-community, metamcp, mcphub | Turnkey Okta/Entra + SCIM group mapping: ToolHive Ent, LiteLLM Ent, Portkey Ent, Composio Ent (`web-vendors.md:157`) | 3 hardcoded IdPs, no generic OIDC |
| RBAC / tool allowlists | IBM (RBAC), agentgateway (CEL), ToolHive (Cedar), LiteLLM per-key/team, Obot ACLs | IdP-group→role mapping: ToolHive Ent (`web-vendors.md:99`) | admin/user + agent-token `allowed_servers`; profiles |
| Helm / K8s | IBM, agentgateway, ToolHive operator, Obot, Unla, agentic-community, Lunar, Agent Router | — | none |
| Secrets backends | IBM `plugins/vault`, ToolHive 1Password, APISIX/LiteLLM Vault | Docker Enterprise call-time injection (`web-vendors.md:89`) | `${env:}` only |
| OTel / Prometheus | IBM, agentgateway, ToolHive, Agent Router, mcpjungle, Lunar | OTel logging: MCP Manager Ent (`web-vendors.md:104`) | Prometheus + OTLP traces (opt-in) |
| Rate limits | IBM, agentgateway, ToolHive, Obot, Agent Router quotas | — | global concurrency queue |
| Audit export | Obot `auditlogexport.go`, ToolHive `audit.go`, Lunar | Retention windows / HIPAA logging: Portkey Ent, MCP Manager Ent | pull `GET /activity/export` |
| **Per-user upstream credentials** | **largely absent** (agentgateway #239 open; mcpjungle #254 open; mcp-hub #116 open) | Docker Enterprise, Obot (OAuth cred mgmt) | store + connect flow shipped; injection unwired |
| **Tool-drift / rug-pull detection** | not found in matrix; InfoQ says gateways lack it (`web-vendors.md:46`) | — | **shipped (Spec 032)** |
| **Token-saving tool discovery** | metamcp/docker/tbxark asks open (`github.md:39,69,82`) | — | **shipped (BM25 retrieve_tools)** |
| HITL approval hold | IBM #5437 open | TrueFoundry native (`web-vendors.md:109`) | intent variants only |
| DLP / redaction | Lasso basic masking, IBM plugins | Lasso paid, MCP Manager Pro+ | detection shipped; no redaction |

Reading: everything in the first seven rows is free at $0 from at least three well-funded projects. The last five rows are where an OSS gateway can still be *different*, and MCPProxy already owns two of them.

---

## 6. Evaluation of candidate features

Effort is for a solo Go developer: S ≈ days, M ≈ 2–4 weeks, L ≈ months.

| Feature | Demand (strength, source) | Effort | Free elsewhere? | Verdict | Why |
|---|---|---|---|---|---|
| **Generic OIDC provider (discovery-based; covers Okta/Keycloak/Authentik/Auth0)** | Strong — 14 asks (`hn-reddit.md:152`); agentic-community #189, mcphub #527 (`github.md:76,84`) | **S** ("~1 file in `oauth_providers.go` plus validator", `internal-inventory.md:316`) | Yes, everywhere | **Build now** | Highest-ranked need, cheapest gap; without it Okta/Keycloak shops are excluded outright (`internal-inventory.md:144`) |
| **IdP-group → server allowlist (config map of `groups` claim → servers; per-server, not per-tool)** | Strong — group-based access is in the canonical ask (`hn-reddit.md:78`); "start with access per server" (`hn-reddit.md:86`); metamcp #103/#179 (`github.md:74,92`) | **S–M** (no Team entity; reuse `entitledServerNames` + agent-token scope; needs `groups` claim from generic OIDC) | Yes (IBM/ToolHive/agentgateway), but IdP-group mapping is paid at ToolHive Ent (`web-vendors.md:99`) | **Cheap partial now** | Delivers the "onboarding is adding someone to a group" story (`hn-reddit.md:83`) without building RBAC; per-tool policy waits for a real ask (`hn-reddit.md:85` warns it "can quietly kill adoption") |
| **Attributable append-only JSONL audit line (stable schema: who/user/token, server, tool, decision, request-id, args-hash, outcome) to file/stdout** | Strong — 13 asks (`hn-reddit.md:153`); NSA (`web-vendors.md:121`); Datadog schema (`web-vendors.md:128`) | **S** (activity records already carry `UserID`; add a sink and a documented schema) | Yes in spirit (Obot/ToolHive) | **Build now** | Turns "we have an activity DB" into "any log forwarder (Loki/Datadog agent/Splunk UF) ships our audit" — satisfies the SIEM checklist without one connector; the "stable audit schema security approves once" ask (`hn-reddit.md:99`) |
| **`${file:/path}` secret provider** | Medium-narrow — obot #6180 (Vault→K8s Secret via VSO), toolhive #1249 (no keyring on K8s), docker #317 (`github.md:68,96,97`) | **S** | Yes | **Build now** | One provider unlocks K8s/Docker secrets, ESO/VSO, 1Password Operator, SOPS-decrypted files; today `${keyring:}` is dead in containers (`internal-inventory.md:95-96`) |
| **Vault / AWS SM / GCP SM / 1Password SDK integration** | Weak as a named integration — "Nobody asked for a specific Vault plugin" (`hn-reddit.md:154`) | **M** each (+ dependency, auth methods, lease renewal) | Yes (IBM, ToolHive, APISIX) | **Don't build** | `${file:}` + the cluster's existing secret operator covers every named store with zero SDKs; revisit only on a direct ask |
| **Docker image: personal edition, `slim` + `full` (node/uv/git), non-root** | Strong for the *personal* edition in containers — 86 IPs / 113 long-lived (`internal-inventory.md:241-259`); #40 wanted stdio upstreams (`github.md:140`) | **S–M** (`full` variant needs a base with node+uv+git and the isolation-image lesson from #1143) | Docker's own gateway is the peer here | **Build now** | See §7 |
| **docker-compose example + K8s manifests + docs** | Table stakes (`github.md:168`) | **S** | Yes | **Build now** | Cheapest way to make the 86-IP audience's day 1 not hit the seven traps in `internal-inventory.md:135-145` |
| **Helm chart (single replica, `Recreate`, PVC, ConfigMap, Secret, probes, Ingress toggle)** | Table stakes; asks are about chart quality (`github.md:168`) | **M** (chart is S; the day-1 fixes it depends on are the M: `Secure` cookie, read-only config, `${file:}`, `MCPPROXY_DATA` bug) | Yes, everywhere | **Build later (Phase 1)** | Shipping a chart before the traps are fixed produces IBM-#1477/agentic-#625-style "upgrade broke" issues (`github.md:101,120`) |
| **Credential injection last mile (per-(user,server) HTTP client → `SetBrokeredAuth`)** | Strongest market ask (`hn-reddit.md:154`; agentgateway #239 👍24 open) — but **0 asks from mcpproxy users** (`github.md:152`) | **M** | Mostly *not* free (open in agentgateway/mcpjungle/mcp-hub; paid at Docker Enterprise) | **Build later, gated** | The one server-edition feature that would be a differentiator rather than parity, and ~80 % exists; but building it for 4–5 deployments who never asked is speculative. Trigger: first external ask, or ≥10 server-edition IPs/week after Phase 1 |
| **Approved-server policy: admin-locked inventory, registry allowlist, user server-adds disabled** | Medium — 9 asks (`hn-reddit.md:156`); docker #195/#180 (`github.md:83,115`) | **S** | Docker: no (the paid Enterprise does it); IBM/ToolHive: yes | **Cheap partial now** | Mostly a config switch over existing quarantine + admin routes; markets directly against "shadow MCP" (`web-vendors.md:53`) |
| **Destructive-tool deny-by-default per server (policy on `call_tool_destructive`)** | Medium — risk-tiering (`hn-reddit.md:99`); NSA human-approval note (`web-vendors.md:125`) | **S** | Partially (CEL/Cedar policies elsewhere) | **Cheap partial now** | Reuses intent variants; gives "read-only fast lane, writes need review" without an approval workflow |
| **HITL approval hold (async, notify, resume)** | Weak-medium; vendor-driven (`hn-reddit.md:161`) | **L** (no MCP approval protocol, client timeouts, notification channel) | No (TrueFoundry paid, IBM open) | **Don't build now** | Cost/evidence ratio is the worst in the table; the deny-by-default partial captures most of the value |
| **Per-tenant token cap (#1177)** | Weak, but a real abuse hole in multi-tenant (`internal-inventory.md:37,109`) | **S** | n/a | **Build now (hygiene)** | One tenant can exhaust the global slot cap; trivial to fix, embarrassing not to |
| **Per-user rate limit / quota** | Weak — 5 asks, LLM-gateway-shaped (`hn-reddit.md:158`; `github.md:169`) | **M** | Yes | **Don't build** | Wait for a second ask like #955 |
| **SIEM/Splunk/Datadog/OTLP-logs push connectors** | **No-demand** — 1 ask (`hn-reddit.md:166`); no unfulfilled competitor issue (`github.md:169`) | **M** each | Yes | **Don't build** | The JSONL audit line + forwarder pattern is what SIEM teams actually deploy |
| **OTel metrics/logs export** | Weak — "OTel appears mostly in vendor copy" (`hn-reddit.md:160`) | **M** | Yes | **Don't build** | Prometheus + OTLP traces already exist |
| **HA / external DB (Postgres/Redis)** | Weak — HA appears in one checklist (`hn-reddit.md:94`) | **L** | Partially (IBM multi-cluster) | **Don't build** | Document the single-replica/`Recreate` contract in the chart instead (`internal-inventory.md:323`) |
| **DLP redaction (both directions) / trace redaction config** | Weak-medium — 5 asks (`hn-reddit.md:159`) | **M** | Paid at Lasso/MCP Manager | **Build later** | Natural extension of shipped detection; do activity-log redaction first (S) if the PII-landmine ask recurs |
| **Air-gapped SKU** | **No-demand** (`hn-reddit.md:167`) | S (docs) | Docker "coming soon" | **Don't build**; document `MCPPROXY_TELEMETRY=false` + `MCPPROXY_DISABLE_AUTO_UPDATE` and whether any other outbound call exists (unknown from the inventory) | — |
| **SAML / SCIM / org-level tenancy / teams entity** | Vendor-only (`web-vendors.md:61-62,157`) | M–L | Paid tiers | **Don't build** | This is precisely the paywalled layer; a solo project should not chase it |
| **Telemetry fields: `server_edition_enabled`, `idp_provider`, `user_count_bucket`, `is_k8s`** | n/a (measurement) | **S** | n/a | **Build now** | Every later gate in this plan depends on it (`internal-inventory.md:325`) |

---

## 7. The Docker image decision

**Facts.** The only server-edition artifact is `ghcr.io/smart-mcp-proxy/mcpproxy-server` (distroless static, multi-arch, three tags, root user, `ENTRYPOINT ["mcpproxy","serve",…]`, no shell, no `npx`/`uvx`/`docker`) — `internal-inventory.md:119,138`; `Dockerfile:30-36`. The install docs' only container recipe points at that image and warns not to relocate the data dir because `MCPPROXY_DATA` breaks config loading (`docs/getting-started/installation.md:374-395`; `internal-inventory.md:123`). The README does not mention the image, Docker, Helm, or Kubernetes at all (repo grep, 0 hits). GHCR pull counts are not available (`internal-inventory.md:296`).

**The telemetry twist and what it implies.** 362 personal installs / 86 IPs ran in containers in 90 days, 113 of them ≥24 h uptime (32 IPs); a further 153 personal headless Linux installs are long-lived; Linux tar.gz downloads run 4–6× the .deb and roughly equal the macOS DMG (`internal-inventory.md:241-261,308`). Server edition: 37 installs / 8 IPs, flat at 1–4 IPs/week for four months, all self-built (`internal-inventory.md:197,292`). Both external Docker asks came from Kubernetes people, and #40's concrete use case was fronting a **stdio** HomeAssistant server — impossible in the current distroless image (`github.md:140`; `internal-inventory.md:138`). Caveat: some of the 248 sub-1-hour container installs are agent sandboxes/devcontainers, not ops deployments (`internal-inventory.md:263`); the ≥24 h bucket is the defensible number.

**Implication:** the audience that runs MCPProxy as a long-lived container wants the *personal* feature set (stdio upstreams via npx/uvx, Docker isolation, registries, quarantine, the Web UI) in a container — not SSO. They are currently building their own images or dropping the tarball into a base image. The published image serves the smaller audience and cannot serve the larger one.

**Recommendation — keep investing, reposition the image:**

1. **Publish the personal edition as the primary image** (`ghcr.io/smart-mcp-proxy/mcpproxy`) in two variants: `slim` (current distroless) and **`full`** (node + uv + git, so stdio upstreams and `git+https://` installs work — the `-slim` uv image already bit users, #1143 `github.md:152`). technicalpickles prototyped exactly slim+full on a branch in 2025-09 (`github.md:140`). Keep `mcpproxy-server` as the SSO variant of the same tags. Effort S–M.
2. **Non-root** (`distroless/static-debian12:nonroot`, `USER 65532`, state under `/home/nonroot/.mcpproxy` or better a fixed `/data` via a *working* data-dir flag). This forces the `MCPPROXY_DATA`/`--data-dir` config-loading bug to be fixed rather than documented around (`internal-inventory.md:123,135`). Effort S once the data-dir fix lands.
3. **docker-compose example + `deploy/kubernetes/` manifests** with the seven day-1 traps pre-solved: `MCPPROXY_API_KEY` from a Secret, `${file:}` secrets, `require_mcp_auth: true`, `trusted_hosts`, probes, `terminationGracePeriodSeconds: 45`, single replica. Effort S.
4. **Helm chart** after (2) and the `Secure`-cookie/`X-Forwarded-*` fixes; single-replica `Recreate` contract stated in `values.yaml`. Effort M total (Phase 1).
5. **Docker Hub listing:** low value on its own — GHCR is where the K8s askers already looked (#1171 asked for the *workflow*, not a registry). Do the cheap version: a package README on GHCR and a "Run in Docker / Kubernetes" section in the README (currently zero mentions — this is the discoverability complaint in disc #948, `github.md:154`). Mirror to Docker Hub only if it is a one-time CI change.
6. **Tag hygiene:** publish RC images under `-rc` tags so `latest` does not move on every stable (`internal-inventory.md:145`; memory note on the tag guard).
7. **Measure:** add `is_k8s`/`image_variant` to heartbeats so the next review can count image users instead of inferring from `/.dockerenv` (`internal-inventory.md:156`).

---

## 8. Recommended positioning and phased roadmap

**Positioning statement.** *MCPProxy is the smart local MCP proxy — smaller context, quarantined servers, drift detection — that also runs unchanged as a headless service in a container. The server edition adds an SSO front door for a small team behind the same proxy: sign in with your IdP, get the servers your group is allowed, every call attributed to you.* It is not a Kubernetes-native enterprise gateway and does not claim to be.

**Why (a)+bounded (c), not (b) or (d).**

- (b) *full team gateway* loses on every axis: three well-funded OSS projects give the parity list away free (§5), the supply side is ~60 launches deep (`hn-reddit.md:45`), and 8.6k LOC of server code has produced 4–5 deployments and zero inbound feature asks (`internal-inventory.md:9,13`; `github.md:152`). Pursuing SCIM/HA/HITL/Vault as a solo maintainer is how the project stops improving the thing 2,493 personal installs use (`internal-inventory.md:188`).
- (d) *park it* ignores that the container/headless audience is real and 10× larger than the server one, that the shipped SSO code works for its narrow purpose, and that the market gap ("OSS that self-hosts *with* the enterprise-tier features", `web-vendors.md:158`) is genuinely open for the two items MCPProxy already owns (drift detection, token-saving discovery). Parking without fixing the over-claiming docs would also leave a credibility problem.
- (a) is the posture the demand-side evidence actually describes — "run as a normal service inside existing K8s/observability" (`hn-reddit.md:93`), "adding an MCP server isn't a special case" (`hn-reddit.md:84`) — and it is what the guiding question rewards: every deliverable below improves the personal edition or its container form.
- The bounded (c) slice exists because the three top asks (generic OIDC, group allowlist, attributable audit) are each S-effort on top of shipped code and turn the server edition from "three consumer IdPs" into something a small Okta/Keycloak team can actually deploy. The genuinely differentiating wedge item — per-user credential injection — is gated, not scheduled.

### Phase 0 — Honesty and hygiene (S, ~1–2 weeks)

| Deliverable | Evidence |
|---|---|
| Fix docs: `credential-commands.md` no longer claims call-time injection; `server-edition-multiuser-auth.md` marks Router/workspace/token-exchange as unwired or removed; CLAUDE.md editions table stops listing `.deb`/tar.gz for server | `internal-inventory.md:10-11,120` |
| Cut or `experimental`-tag `workspace.Manager`, `multiuser.Router`/`ToolFilter`, `TokenExchanger` | `internal-inventory.md:45-52`; §4 verdict |
| `Secure` session cookie derived from `X-Forwarded-Proto`/TLS; trusted-proxy `X-Forwarded-For` for session IPs | `internal-inventory.md:33,113,141` |
| Fix `MCPPROXY_DATA`/`--data-dir` so config is read from the data dir; then non-root image | `internal-inventory.md:123,135` |
| `${file:}` secret provider | `github.md:96-97`; `internal-inventory.md:96` |
| Fix #1177 per-tenant token cap | `internal-inventory.md:37,109` |
| Telemetry fields: `server_edition_enabled`, `idp_provider`, `user_count_bucket`, `is_k8s`, `image_variant` | `internal-inventory.md:129,155,325` |
| README "Run as a service / in Docker / on Kubernetes" section; GHCR package README | repo grep (0 hits); `github.md:154` |

### Phase 1 — The container is first-class; the SSO door fits real IdPs (M, ~4–6 weeks)

| Deliverable | Evidence |
|---|---|
| Personal-edition image `slim` + `full` (node/uv/git); `mcpproxy-server` as the SSO variant; RC tags | `internal-inventory.md:241-259`; `github.md:140,152` |
| docker-compose example; `deploy/kubernetes/` manifests; then a minimal Helm chart (single replica, `Recreate`, probes, Secret/ConfigMap wiring, Ingress toggle) with the single-writer contract stated | `github.md:168`; `internal-inventory.md:127,136,323` |
| Generic OIDC provider (discovery URL, client id/secret, `groups` claim name) | `hn-reddit.md:152`; `github.md:84`; `internal-inventory.md:28,316` |
| IdP-group → server allowlist in `server_edition` config; enforced through the existing entitlement predicate and agent-token scope | `hn-reddit.md:78,83,86`; design doc "entitledServerNames" |
| JSONL audit sink (file/stdout) with a documented, versioned schema; `activity export` gains the same schema | `hn-reddit.md:153,99`; `web-vendors.md:121,128` |
| Admin-locked inventory switch (users cannot add servers; registry allowlist) and per-server "deny `call_tool_destructive`" policy | `hn-reddit.md:156,99`; `github.md:83,115` |
| Server-edition docs: a `server_edition` section in `docs/configuration.md`; a "deploying for a team" guide that is honest about scope (single node, per-server grants, no per-user upstream creds yet) | `internal-inventory.md:124,143` |

### Phase 2 — Gated wedge (M, only on trigger)

| Deliverable | Trigger | Evidence |
|---|---|---|
| Per-(user, server) brokered credential injection for HTTP upstreams (wire `CredentialResolver` → `SetBrokeredAuth`; fail-closed; audit line per injection) | First external issue asking for per-user upstream credentials **or** ≥10 distinct server-edition IPs/week for 4 consecutive weeks (vs 1–4 today) | `hn-reddit.md:154`; `github.md:58,166`; `internal-inventory.md:52,317` |
| Activity-log/trace redaction config | A second "PII landmine"/redaction ask against mcpproxy | `hn-reddit.md:128-129` |
| Per-tool allow/deny on top of the group map | A user hits the mixed-risk-server case | `hn-reddit.md:86,155` |

Marketing note for all phases: lead with the two differentiators competitors' *own* users are asking for — context savings (`github.md:69,82`) and tool-drift re-consent (`web-vendors.md:46`; `hn-reddit.md:169`) — and quote USD alongside tokens when claiming savings (project memory rule).

---

## 9. Kill list

| Do not build | Why |
|---|---|
| SIEM/Splunk/Datadog/OTLP-logs push connectors | 1 demand-side ask in ~1,050 posts/comments (`hn-reddit.md:166`); JSONL + forwarder is the deployed pattern; every connector is an M with an SDK |
| Vault / AWS SM / GCP SM / 1Password SDK clients | Asked as behaviour, not integration (`hn-reddit.md:154`); `${file:}` + ESO/VSO/1Password-operator covers all of them at S |
| HITL approval hold | L effort, 4 asks vs 6 vendor pitches (`hn-reddit.md:161`); deny-by-default on destructive intents captures most value at S |
| HA / external database | One checklist mention (`hn-reddit.md:94`); BBolt single-writer is fine for a small team if the chart says so (`internal-inventory.md:127`) |
| SAML, SCIM, org/workspace multi-tenancy, Teams entity | Vendor-tier features (`web-vendors.md:61-62,157`); 3 tenancy asks (`hn-reddit.md:162`); this is the paywalled layer a solo project should not chase |
| RFC 8693 / Entra OBO token exchange | Requires IdP-side trust most shops refuse (DCR wall, `hn-reddit.md:107,120`); one competitor RFC at 👍7 (`github.md:67`) |
| Connected per-user personal upstreams (`workspace.Manager`) | Process-per-user is "too costly" (`hn-reddit.md:101`); the demanded per-user thing is credentials on shared servers, not private processes |
| Per-user rate limiting / quotas | 5 LLM-gateway-shaped asks (`hn-reddit.md:158`; `github.md:169`); one mcpproxy ask already served by #955 |
| OTel metrics/logs exporters | "mostly vendor copy" (`hn-reddit.md:160`); Prometheus + OTLP traces exist (`internal-inventory.md:79-81`) |
| Air-gapped SKU | 0 asks (`hn-reddit.md:167`); a doc paragraph on disabling outbound calls suffices |
| Server-edition `.deb`/`.rpm` | The Linux/headless audience arrives via tarball and personal .deb (`internal-inventory.md:308`); the personal package already ships the hardened systemd unit (`internal-inventory.md:121`) |
| Docker Hub as a separate investment | GHCR askers found GHCR; README/GHCR-README is the discoverability fix (`github.md:154`) |

---

## 10. Open questions and measurement gaps

1. **Multi-user enablement is invisible.** No heartbeat field for `server_edition.enabled`, IdP, or user count (`internal-inventory.md:129,155`). Every Phase 2 gate above is unmeasurable until the Phase 0 telemetry fields ship and ~4 weeks of data accumulate.
2. **Image adoption is invisible.** GHCR pulls need `read:packages` (403 today); containers without volumes churn `anonymous_id` (248 sub-1-hour installs, `internal-inventory.md:259,296`). Decide whether to request the scope for the telemetry PAT or add `image_variant` to heartbeats.
3. **Are the 86 container IPs ops deployments or agent sandboxes?** The ≥24 h bucket (113 installs / 32 IPs) is defensible; the rest is unknown (`internal-inventory.md:263`). A `container_runtime`/cgroup hint would settle it.
4. **Does anything in the personal edition make outbound calls besides telemetry and update checks?** Unknown from the inventory; needed only for the air-gap doc paragraph.
5. **One binary or two?** The `server` build tag keeps the personal edition untouched but hides server routes from `swag`/OAS/CI lint (design doc, last note) and forces a separate image. If the personal-edition image becomes primary, consider whether `server_edition.enabled` could be a runtime block in one binary. Not evidenced either way; flagged for a design decision, not recommended here.
6. **Reddit lane is incomplete, but the ops subreddits are thin.** The subreddit×query matrix was cut short by rate limits (`hn-reddit.md:49-61`); the cells that did complete show "mcp gateway" post counts of 34 (r/selfhosted), 7 (r/devops), 9 (r/kubernetes) and 0 (r/platformengineering) versus ≥100 in r/mcp (`hn-reddit.md:53-59`), and the "mcp kubernetes"/"mcp helm" cells for those subreddits never completed. A follow-up pass could raise the Helm/K8s ask count, but the completed cells point the same way as the tally: platform teams assume K8s rather than ask a gateway for it (`hn-reddit.md:157`).
7. **No competitor stargazer baseline.** The 34 % company-field rate for mcpproxy stargazers has nothing to compare against (`github.md:162`).
8. **The 17 of 46 server installs with `require_mcp_auth=0`** (`internal-inventory.md:143`) suggest the server edition should default that to `true` — a behaviour change for the server tag only; needs a decision.
9. **First-party absorption timing.** Anthropic's Enterprise-Managed Authorization (`hn-reddit.md:193`) may remove the IdP-relay value for Claude clients; unknown when and for which clients. It strengthens the case for owning policy/drift/audit rather than auth plumbing.
