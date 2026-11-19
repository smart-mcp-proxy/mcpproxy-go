# First-run journey vs Spec 126 (checked 2026-11-19)

The clean-install journey was run by hand on `main` at `58a824e66` (the v0.75.0 candidate), with:
- a fresh data directory;
- an isolated `HOME`, so no real client config was touched;
- telemetry off (`MCPPROXY_TELEMETRY=false`);
- the core on a private port.

This note maps what a new user does to the Spec 126 first-run requirements (US1–US3, FR-001 to FR-017). Each requirement is marked met, partly met or missing, with the evidence behind it.

## The journey as observed

| Step | What happened | Result |
|---|---|---|
| Start | `mcpproxy serve` on an empty config | works |
| See what to do next | `mcpproxy upstream list` printed only `No results found` | **dead end** (fixed by https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1939, held) |
| Add a server (CLI) | `mcpproxy upstream add context7 https://mcp.context7.com/mcp` → quarantined, with next steps printed | works |
| Add a server (catalog CLI) | `catalog search` prints `reference/sequentialthinking`; `catalog add reference/sequentialthinking` → `server not found in registry`. Only the doubled `reference/reference/…` worked | **broken** (fixed by https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1939, held) |
| After a catalog add | The hint said `approve with: mcpproxy upstream approve <name>`. That approves pending tools only: it printed `Approved 0 tools` and the server stayed quarantined | **misleading** (fixed by https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1939, held) |
| Add a server (Web UI) | `+` → Server → Add Server page (Catalog / Paste / Import / Manual) → **Add to MCPProxy** → server page | works; Quick Start wording was stale (fixed by https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1938) |
| Add a server (agent) | Anonymous client: `upstream_servers add` works and the server lands quarantined. A client connected with `mcpproxy connect` gets `tool 'upstream_servers' not found` | by design (client credentials cannot change servers), but the error does not tell the agent or the user what to do (gap G3) |
| Review and approve | Scan-default admission scan ran on add: clean, risk 0/100. `review show` lists tools and verdicts. `review approve --yes` (CLI) or **Review & approve** → **Approve server (N of N tools)** (Web). Nothing became callable before the explicit approval | works |
| Connect a client | `mcpproxy connect claude-code` writes an HTTP entry with a per-client `mcp_cli_` credential, after a backup. `mcpproxy connect claude-desktop` writes an `npx mcp-remote … --header X-API-Key:…` entry | works |
| Real tool call | Claude Code 2.1.318, with only the written entry (`--strict-mcp-config`), ran `retrieve_tools` → `call_tool_read context7:resolve-library-id`. The activity log shows `tool_call … client claude-code … success` | works |
| Fallback: self-healing args | `call_tool_read` without a required argument → `invalid_params`, `missing property 'query'`, `input_schema` attached | works as documented |
| Fallback: stdio bridge | `npx -y mcp-remote http://127.0.0.1:<port>/mcp` (anonymous) and the wizard's Claude Desktop entry (with credential) each ran `initialize`, `tools/list` and a real call (`context7:resolve-library-id`, `time:get_current_time`) | works as documented |
| Restart | State, approvals and tools survived a restart over the same data directory | works |

## Spec 126 first-run requirements

| Requirement | Status | Evidence |
|---|---|---|
| FR-001 starter offer in the wizard's Servers step | **missing** | No starter code. The Servers step routes to `/add-server?tab=catalog` (`frontend/src/components/OnboardingWizard.vue` ~1177) |
| FR-002 compiled-in allowlist | **missing** | No `internal/onboarding/starter.go`. Spec drift: the live `mcp-server-time` inventory now also has `get_time_ago`, but FR-002 lists `get_current_time` and `convert_time`. The captured inventory stays authoritative (US1 scenario 4); the audited list needs updating when the allowlist is written |
| FR-003 starter added through the catalog door | **partly met** | The door exists (REST `POST /api/v1/registries/{id}/servers/{sid}/add`, CLI `catalog add`). The CLI door could not resolve the reference ids it prints; fixed by https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1939 (held) |
| FR-004 to FR-007 offer UI, captured-tools review, skip | **missing** | No starter surface. The general review flow (Review tab, `review approve --yes`) already meets the "explicit approval bound to shown definitions" part for any server |
| FR-008 Home / native empty-state card offering the starter | **partly met** | The Web Home has a "Get started" card but no starter. The CLI now prints an empty-state next step (https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1939, held). That is a static hint, not the FR-012 offer decision |
| FR-009 launcher resolution on the effective PATH | **missing** in the core | The CLI hint uses `exec.LookPath` on the CLI's own PATH, which is the user's shell PATH. That is enough for a terminal user, but it is not the core's login-shell resolution |
| FR-010 capture timeout, Retry, Remove | **missing** | starter only |
| FR-011 Verify step: indexed count and a first prompt | **met** (generic) | The Verify step shows "No upstream tool call recorded yet" and suggested prompts built from usable servers (`OnboardingWizard.vue` ~465–530). Not specific to a starter kind |
| FR-012 one offer endpoint | **missing** | no `GET /api/v1/onboarding/starter` |
| FR-013 "Connect another client" after the first | **partly met** | The Clients tab lists detected, pinned and other clients, each with Connect (`OnboardingWizard.vue` ~71–140). There is no "Connect another client" framing and no "none detected" line. CLI `mcpproxy connect` does not suggest a second client (gap G2) |
| FR-014 preview → write → undo | **met** | existing flow; CLI connect backs up first |
| FR-015 denied / malformed remediation | **met** (Spec 075) | not exercised in this run |
| FR-016 second-client facts | **missing** | owned by the activation-baseline session (onboarding facts, PR-B) |
| FR-017 native sheet "Connect another client" | not checked | the tray was not run |

## In-product gaps left

- **G1. No starter (FR-001 to FR-012).** A new user must still pick a first server from a large catalog. The smallest next slice is the read-only core:
  - the FR-002 allowlist with the updated tool list;
  - FR-009 launcher resolution;
  - FR-012 `GET /api/v1/onboarding/starter`;
  - the CLI empty-state hint switched to that endpoint.

  After that comes the wizard and Home surfaces (FR-004 to FR-008, FR-010). None of it may change the quarantine, trust-mode or scan defaults (FR-003).
- **G2. CLI `connect` has no second-client step.** `mcpproxy connect <client>` could list other detected, unconnected clients after a success, from the same core list the wizard uses.
- **G3. An agent on a client credential gets an opaque refusal when asked to add a server.** Hiding `upstream_servers` is correct (`docs/features/profiles.md`). But `tool 'upstream_servers' not found` gives neither the agent nor the user the next step. A refusal that names the operator routes (`mcpproxy upstream add`, the Web UI Add Server page, or connect with `--keyless`) would close it. Whether to show such a refusal on a hidden tool is a product decision.

## Sequencing

Measurement ships first. The Spec 126 PR-A instrumentation, the receiver (mcpproxy-telemetry#13) and the cohort query (mcpproxy-dash#15) go out in a release with no onboarding change, so the baseline is clean.

The only first-run code change from this check, https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1939, is a draft marked **HOLD** until that release ships and the owner approves the onboarding rollout. Nothing in it is behind a flag; the held PR is the mechanism.

The docs correction (https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1938) changes no product behaviour: it describes what `main` already does.
