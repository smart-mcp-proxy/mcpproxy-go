# Personal-Edition Polish Initiative

**Started**: 2026-06-30 · **Owner**: Algis · **Status**: active (vertical 1 in progress)

> Durable brief for the personal-edition polish push. This is the master doc that
> survives a context clean — it captures **intent, scope, decisions, and the
> existing foundation** for each of the 5 verticals so any future session can
> resume a vertical cold. Live status/DAG is in [`../roadmap.yaml`](../roadmap.yaml)
> (rendered to [`../ROADMAP.md`](../ROADMAP.md)). Each vertical gets its own
> grounded `/speckit` brainstorm + spec when we reach it.

## North star

Make the **personal edition** so simple and reliable that developers tell their
teammates about it. Three themes cut across everything: **simpler, more reliable,
more transparent.** Paid-tier and server-edition work are **on hold** (minimal
priority) until this push lands.

## Sequencing

`ux-audit` (5) is a cross-cutting discovery pass that feeds 2/3/4. We chose to
start with **Scanner + Quarantine (1)** — highest user pain, and Spec 076's
detect engine gave us a foundation. Rough order: **1 → (5 discovery) → 3 / 2 / 4**.
Priorities in `roadmap.yaml`: ux-audit P0, action-log P0, scanner/analytics/
registries P1.

---

## Vertical 1 — Scanner + Quarantine simplification  ✅ IN PROGRESS

**Goal**: One simple, reliable, deterministic scanner that works offline with
zero Docker; demote the third-party-scanner mess to opt-in; single unified report.

**Status**: Spec written — [`specs/077-scanner-simplification/spec.md`](../specs/077-scanner-simplification/spec.md),
branch `077-scanner-simplification`. Next: `/speckit.plan`.

**Decisions (locked)**: See the spec + memory `project-personal-edition-polish-initiative`.
Summary: baseline = Spec 076 detect engine (always-on, offline); delete duplicate
legacy `tpaRules` + legacy embedded-secret path; new **hard-tier `phrase_injection`**
check preserves blocking posture for curated high-confidence phrases (rest stay
soft/review-only); Docker scanners + source extraction → opt-in `security.deep_scan`
(off by default, never blocks/degrades baseline); single merged report via existing
`ScanFinding`/`ScanSummary`/`CalculateRiskScore` with cross-scanner consensus;
baseline-only verdict; collapse MCP-2207 notification storm; remove orphaned
`auto_scan_quarantined`. **Out of scope**: removing Docker plugins, touching the
quarantine state machine, registry redesign.

**Foundation**: `internal/security/detect/` (Spec 076), `internal/security/scanner/`
(`inprocess.go`, `engine.go`, `docker.go`), `internal/runtime/tool_quarantine.go`
(unchanged). Docs: `docs/features/tool-scanner.md`, `docs/features/security-scanner-plugins.md`.

---

## Vertical 2 — Action Log / Transparency  (backlog, P0)

**Goal**: Make the activity/action log genuinely usable — the most important
signals (security, connection health, recent tool calls, errors) **at a glance**,
not buried. Transparency is a core selling point.

**Scope (intent)**: An at-a-glance action-log view surfacing top signals + health;
tie in retention/size so the view stays fast and bounded. **Out (for now)**: SIEM
export (parked epic `siem`), deep forensic tooling.

**Existing foundation** (substantial):
- `specs/019-activity-webui` — **shipped 73/73**. The activity Web UI already exists.
- `specs/024-expand-activity-log` — **~95% (63/66)**. Activity-log backend/expansion.
- `specs/073-activity-size-retention` — drafted (0/14). Retention/size work not started.
- Code: `internal/httpapi/activity.go` (JSONL), activity CLI (`mcpproxy activity …`),
  SSE `/events`. Every response carries `X-Request-Id` for correlation.

**Open questions to brainstorm when we start**: What are the "top signals" worth
promoting (security findings, disconnects, denied calls, sensitive-data hits)?
Is this a new default panel or a redesign of the existing activity view? How does
it relate to the analytics dashboard (vertical 3) — same landing surface?

---

## Vertical 3 — Analytics Dashboard as default page  (backlog, P1)

**Goal**: Make a **graph-first dashboard the default landing page**; show **which
server / which tool drains tokens** (and calls/latency/errors), so users see value
and cost at a glance.

**Scope (intent)**: Per-server and per-tool token-drain graphs; promote the
dashboard to the default route. **Out**: full BI/exports, cross-instance
aggregation.

**Existing foundation** (partial — good starting point):
- `specs/069-observability-usage-graphs` — **in-flight ~62% (16/26)**. Usage-graph
  work already underway; the token/usage metrics likely exist.
- `specs/039-connect-and-dashboard` — Approved, no tasks yet. The dashboard/connect
  surface concept.
- Metrics context: `mcpproxy_tool_calls_total{server,tool,status}` (cardinality-safe;
  user_id/profile are span attrs, not labels — see memory `mcp3207…`). OTLP spans
  carry richer per-call attributes.

**Open questions**: Where do per-tool **token** counts come from today (are tokens
measured per call, or estimated)? Is 069 close enough to extend, or does the
default-landing change belong to 039? What's the default time window / granularity?

---

## Vertical 4 — Registries: easier search + add-server  (backlog, P1)

**Goal**: Lower the friction of **finding a server in a registry and adding it** —
better search, one-click add.

**Scope (intent)**: Improved registry search UX; frictionless add-server flow,
leaning on the official registry protocol. **Out**: marketplace metadata/telemetry
(parked epic `marketplace`), custom private-registry hardening.

**Existing foundation**:
- `specs/071-official-registry-protocol` — **Implemented 12/12**. Official registry
  protocol integration is done — build on it.
- `specs/070-registry-easy-upstream-add` — **early (3/24)**. The easy-add work is
  mostly unstarted — this is where most of the vertical lives.
- Code: `Repositories.vue`, `add_from_registry.go`, `search_servers`/`list_registries`
  MCP tools. ~60% of a marketplace already ships (browse/search/one-click add).

**Open questions**: What's the current search's weakness (ranking? filters?
discoverability?)? Is "add server" friction in the UI flow, the quarantine gate, or
config plumbing? Web UI only, or tray deep-links too?

---

## Vertical 5 — UX audit (Web UI + macOS app)  (backlog, P0, cross-cutting)

**Goal**: A grounded, end-to-end UX pass across the **Web UI** and the **macOS tray
app** — the umbrella/discovery step that feeds concrete findings into verticals 2–4.

**Scope (intent)**: Heuristic + Playwright UX sweep of the Web UI; a macOS tray UX
sweep (settings parity, core flows). Produce a prioritized findings list, not a
redesign. **Out**: net-new features (those become their own verticals).

**Existing foundation / tooling**:
- `specs/064-glass-cockpit` — **Planned (spec + plan complete)**, no tasks. This is
  the likely home/umbrella for the UX vision.
- `specs/037-macos-swift-tray` — Draft. macOS tray.
- Tooling ready: Playwright Web-UI verification (`docs/development/web-ui-verification.md`),
  `mcpproxy-ui-test` MCP (macOS tray a11y), `claude-in-chrome` + `computer-use` MCPs,
  the `mcpproxy-qa` skill.

**Open questions**: Run the audit *first* (before 2–4) or interleave? What's the
severity bar / output format (the repo already publishes HTML QA reports under
`docs/qa/`)? Which flows are highest-traffic and worth auditing first?

---

## How to resume after a context clean

1. Read this doc + [`../roadmap.yaml`](../roadmap.yaml) (or `ROADMAP.md`).
2. Memory `project-personal-edition-polish-initiative` auto-loads the summary.
3. For the active vertical, read its `specs/<NNN>/` spec.
4. Before deep-designing a new vertical, dispatch a code explorer to ground it
   against the "Existing foundation" pointers above (as we did for the scanner).
