# TPA Scanner: Inline Findings & Background Scanning — Authoritative Design

*All paths relative to the worktree root. Line numbers verified against the working tree on 2026-09-01.*

**Status: Phase 1 IMPLEMENTED (2026-09-01), D1 deferred (see §6a).** Three maintainer decisions were
locked during implementation and are authoritative wherever they differ from the
body of this document:

| | Decision | Where it lands |
|---|---|---|
| **D1** | `shadowing.cross_server`'s **reference** branch demote from `TierHard` to `TierSoft` is **APPROVED but DEFERRED** — see §6a. The chosen fix is the demote, **not** a `distinctiveName()` patch; neither is applied yet. Phase 1 ships the **span** only, which is tier-independent. | §3.4, §6a, §7 Risk 1, §8 Q1 |
| **D2** | Per-server `auto_baseline_scan: false` means **no automatic scanning of that server at all** — the admission baseline, the tool-change rescan and the bundle-activation rescan are all suppressed. One switch. | §4.2, §4.3, §8 Q2 (Phase 3) |
| **D3** | Finding suppression is keyed per **(tool, rule, span text)**, so the same rule matching *different* words after a rug-pull re-fires. | §6 Phase 3, §8 Q3 (Phase 3) |

---

## 1. The two pains, and the one-sentence fix for each

**Pain 1 — the finding is a destination, not an annotation.** Reading one finding costs 4 clicks and a 16-page scan-history table, and `finding.location` (`ScanReport.vue:285`, `:391`) is inert `<code>` — repo-wide grep confirms it is never a link.
**Fix:** make `finding.location` a router-link into the server's Tools tab, and render the flagged tools inline where the tools already live (`ServerDetail.vue` Tools tab), using `scanReport.value.findings[]` which is **already in memory on every tab** (`loadScanReport()` runs unconditionally in `onMounted`, `ServerDetail.vue:3785-3787`).

**Pain 2 — the finding names a tool but never the words.** `Evidence` for the reported rule is a *fabricated sentence*, not a quote: `shadowing.go:216` emits `fmt.Sprintf("description references cross-server tool %q", tok)`. There is no offset field anywhere on `Signal` (`signal.go:39-46`), `detect.Finding`, or `ScanFinding`.
**Fix:** add a `Span` (offsets + escaped snippet checksum) to the signal→finding→ScanFinding chain, populated from the offsets the raw-text checks *already compute and throw away* — `tpa_bundle.go:155` literally has `loc := r.re.FindStringIndex(tool.Description)` in hand.

**Grounding case, and why highlighting is the product argument:** the reported finding is a **false positive**. `distinctiveName()` (`shadowing.go:48-59`) accepts any lowercase token ≥6 chars not in a 29-entry `commonNames` map; `"reason"` is exactly 6 and absent from it; `perplexity` exposes a tool named `reason`; `create_user`'s 4,500-char description contains *"For this reason, you can't add two IAM users…"*. A highlight turns a ten-minute investigation into a one-second dismissal.

---

## 2. Information architecture

Only surfaces that actually change.

| Surface (file:line) | What's added | Why |
|---|---|---|
| `frontend/src/views/ScanReport.vue:283-286`, `:389-392` | `finding.location` → `<router-link :to="toolTarget(...)">`; falls back to `<code>` when the location is not `server:tool` | Closes the report→evidence loop in ~8 lines. Other scanners emit file paths and `tool:`+name (`engine.go:1014`), so the parse must be total. |
| `frontend/src/views/ServerDetail.vue:424` (above the quarantine banner) | New `<FlaggedToolsPanel>` — one row per flagged tool: threat chip · rule id · one-line detail · **Show in description** | The flagged list belongs where the tools are. Needs **zero** new backend: `scanReport.value` (ref at `:1680`) is loaded on every tab. |
| `frontend/src/views/ServerDetail.vue:727-729` (Available Tools card) | Bare `{{ tool.description }}` → `<ToolDescription>` (highlighted) | The one surface that already renders the full, untruncated flagged description with nothing marking it. |
| `frontend/src/views/ServerDetail.vue:687-699` (badge row) | `<FindingChip>` beside `new` / `changed` / `🔒 locked by config` | Same slot pattern `HoldEvidenceBadge` uses at `:528-533`. |
| `frontend/src/views/ServerDetail.vue:1446-1497` (Security tab) | Counts-only `1 dangerous` → the same `<FlaggedToolsPanel>`; `View Full Report →` stays | The tab whose entire job is security currently refuses to name which of 15 tools is the problem. |
| `frontend/src/views/ServerDetail.vue:854-889` (Configuration tab) | Per-server "Scan this server's tool descriptions" toggle under the trust-mode selector | Phase 3. Per-server opt-out does not exist today. |
| `frontend/src/views/Tools.vue:341-345` | New narrow **Scan** column rendering `<FindingChip>` — chip only, **no highlight** | Phase 2. The Description cell is `max-w-xs truncate` single-line (`:336`); a highlight landing in the clipped half is a false negative. |
| `frontend/src/views/Tools.vue:458-461` (detail modal) | `<ToolDescription>` + findings list | Phase 2. Full text renders here; lazily fetch the report on modal open. |
| `frontend/src/views/Tools.vue:139-145` (filters) | `Findings: All / Flagged / Dangerous / Not scanned`, bound to `?finding=` | Phase 2. Mirrors the existing `?q=` handling at `:1039-1060`. |
| `frontend/src/views/settings/fields.ts` `SECURITY_FIELDS` | `security.auto_baseline_scan` toggle, above the existing `deep_scan.enabled` entry | Phase 3. Repo-wide grep for `auto_baseline_scan` under `frontend/src` returns zero hits today. |
| `frontend/src/components/SidebarNav.vue:319-323` | Label `Security Scanners` → **`Security`** | The page `<h1>` already says Security. |

**Explicitly not changing:** no new route, no sidebar count badge, no Dashboard alert, no toast. See §7.

---

## 3. The highlighting mechanic

### 3.1 Do offsets exist today? Partly — they are computed and discarded.

| Check | What it matches today | Offsets available? |
|---|---|---|
| `tpa.bundle.*` (`scanner/tpa_bundle.go:155`) | `loc := r.re.FindStringIndex(tool.Description)` | **Yes — already computed**, discarded at `:169` |
| `shadowing.cross_server` (`checks/shadowing.go:176`) | `wordRe.FindAllString(tool.Description, -1)` | **Yes — one-token change** to `FindAllStringIndex` |
| `shadowing.clone` (`checks/shadowing.go:76`) | raw description token | Yes |
| `unicode.hidden` (`checks/unicode_hidden.go:41`) | `raw := Description + schemas`, `for i, r := range raw` | Yes, needs field attribution |
| `payload.decoded` (`checks/payload_decoded.go:44,55`) | `base64Re.FindAllString(text, -1)` on raw | Yes, → `FindAllStringIndex` |
| `directive.imperative` (`checks/directive_imperative.go:130`) | `tool.NormalizedText` | **No** — needs `NormalizeWithSpans` (Phase 3) |
| `phrase.injection` (`checks/phrase_injection.go:150`) | `tool.NormalizedText` | **No** — same |

`detect.Normalize` (`normalize.go:37,81`) is NFKC + Cf-strip + lowercase + contraction-expand + collapse + **stem** — lossy and non-invertible as written. It is, however, token-structured, so a raw-range-carrying variant is mechanical bookkeeping, not inference. **Decision:** those two checks get no span in Phases 1–2 and a proven token map in Phase 3 — the refactor deserves a fuzz parity gate, not a UI deadline.

### 3.2 Backend contract — exact fields to add

New file `internal/security/detect/span.go`:

```go
package detect

type SpanField string

const (
	SpanFieldDescription  SpanField = "description"
	SpanFieldInputSchema  SpanField = "input_schema"
	SpanFieldOutputSchema SpanField = "output_schema"
)

// MaxSpansPerFinding bounds the payload for pathological descriptions.
const MaxSpansPerFinding = 32

// Span locates one check's match inside one RAW (un-normalized) tool text field.
//
// Start/End are half-open [Start, End) offsets in UTF-16 CODE UNITS — i.e.
// JavaScript string indices — because the only consumer that renders them is the
// Web UI, where description.slice(start, end) is then exactly correct including
// surrogate pairs. Producers convert byte offsets with UTF16Offsets.
//
// Snippet is CapEvidence(raw[byteStart:byteEnd]): render-SAFE (control and Cf
// runes escaped to literal \uXXXX, capped at MaxEvidenceLen). It is NOT the
// render source — the UI renders the tool's own LIVE description sliced by the
// offsets. Snippet exists solely as the staleness checksum, and it is escaped for
// the reason tpa_bundle.go already documents: a dot-all bundle regex can match
// bidi/zero-width runes from an attacker-controlled description, and the raw span
// must never land verbatim in a JSON payload.
type Span struct {
	Field   SpanField `json:"field"`
	Start   int       `json:"start"`
	End     int       `json:"end"`
	CheckID string    `json:"check_id"`
	Tier    string    `json:"tier"`    // "hard" | "soft" — per-span severity
	Snippet string    `json:"snippet,omitempty"`
}

// UTF16Offsets converts a byte range in s to UTF-16 code-unit offsets.
func UTF16Offsets(s string, byteStart, byteEnd int) (start, end int, ok bool)
```

Threaded additively through three structs:

```go
// internal/security/detect/signal.go — Signal (currently signal.go:39-46, no offsets)
Spans []Span // raw-text locations; empty when the check matched normalized text

// internal/security/detect/aggregate.go — Finding
Spans []Span

// internal/security/scanner/types.go — ScanFinding
Spans []detect.Span `json:"spans,omitempty"`
```

**`aggregate()` must union spans across every signal, not just `primary`.** Today `Evidence`/`Description` come from `primary` alone (`aggregate.go:99-114`) and `aggregate` emits exactly one `Finding` per tool. Primary-wins is tolerable for prose and fatal for highlighting: one description tripping both `tpa.bundle` and `shadowing.cross_server` would highlight one and silently swallow the other. Union, dedupe on `(Field,Start,End,CheckID)` in first-seen order, sort, cap at `MaxSpansPerFinding`. `Span.CheckID`/`Tier` are what let the frontend label a mark correctly despite the single finding-level `rule_id`.

`detectFindingToScanFinding` (`inprocess.go:274-299`) gains one line: `Spans: f.Spans`.

**Field attribution.** `unicode_hidden.go` and `payload_decoded.go` match over a concatenated buffer. Compute the two boundary indices before matching, attribute each hit to the field it falls in, subtract that field's origin, and **drop any span straddling a boundary** rather than emitting a wrong one.

### 3.3 API shape

Phase 1 needs **no new endpoint and no contract change**. `GET /api/v1/servers/{id}/scan/report` already returns `data.findings[]`, and `ServerDetail.vue` already holds it. Spans simply appear on each finding:

```jsonc
{
  "rule_id": "detect.shadowing.cross_server",
  "threat_level": "dangerous",
  "location": "com.googleapis.sqladmin/mcp:create_user",
  "evidence": "description references cross-server tool \"reason\"",
  "signals": ["shadowing.cross_server"],
  "tier": "hard",
  "spans": [
    { "field": "description", "start": 1893, "end": 1899,
      "check_id": "shadowing.cross_server", "tier": "hard", "snippet": "reason" }
  ]
}
```

**Join key** is `Location`, built at `aggregate.go:108` as `fmt.Sprintf("%s:%s", tool.Server, tool.Name)` — byte-identical to `storage.ToolApprovalKey` (`models.go:381-383`). **Split on the LAST colon**: server names legitimately contain `/` and `.` (`com.googleapis.sqladmin/mcp`). This lives in one tested pure util, `frontend/src/utils/toolLocation.ts`.

Phase 2 (global Tools list) adds `contracts.Tool.Scan *ToolScanSummary` populated in `enrichServerTools` (`internal/httpapi/server.go:3455`, right beside the `HeldReason/HeldVerdict/HeldSignals` block at `:3473-3475`, which both tools endpoints share). It is backed by an index populated **on the write path** at report-save time, keyed by `Location`; a miss returns `nil` (no badge). It is *not* lazily rebuilt from `GetScanReport` — that call does two full `bucket.ForEach` walks including `SarifRaw` deserialization (`storage/scanner.go:139-158`, `:349`), which would turn one HTTP request into 28 full-bucket scans.

### 3.4 Vue render — text nodes only, verify or degrade

Two pure modules, unit-tested standalone (house pattern: `views/security/deepScanState.ts`, `utils/toolDiff.ts`).

`frontend/src/utils/capEvidence.ts` — a ~15-line TS mirror of `detect.CapEvidence` (`signal.go:126-141`), used only for verification.

`frontend/src/utils/highlightSpans.ts`:

```ts
export interface Segment { text: string; sources: SpanSource[]; level: 'dangerous'|'warning'|null }

/** Boundary sweep. Every char of `text` appears in exactly one segment, in order:
 *  segments.map(s => s.text).join('') === text   (asserted by unit test). */
export function segmentByFindings(text: string, sources: SpanSource[]): Segment[]

export function isSpanUsable(text: string, span: FindingSpan): boolean {
  if (span.field !== 'description') return false
  if (span.start < 0 || span.end > text.length || span.start >= span.end) return false
  if (!span.snippet) return true
  const actual = capEvidence(text.slice(span.start, span.end))   // escape BOTH sides
  return span.snippet.endsWith('…')
    ? actual.startsWith(span.snippet.slice(0, -1))
    : actual === span.snippet
}
```

Sweep, not interval nesting: collect all starts/ends into a sorted unique boundary set, emit one segment per interval carrying every covering span. Overlaps become one segment with two `sources` — no nested DOM, no lost or invented text.

**Snippet truncation must clamp the SPAN, not just the checksum.** `CapEvidence` caps at 200 runes, and the bundle's dot-all rules routinely match far more, so a prefix-only comparison verified the first 200 characters of a span and said nothing about the rest — edit only the tail of such a description after the scan and the span still "verifies", drawing a `dangerous` mark across prose no rule ever matched. `detect.DescriptionSpan` therefore clamps `End` to the byte extent `CapEvidenceSpan` actually covers, so a mark can never extend past verified text.

**Excerpt windows are surrogate-safe.** `start - EXCERPT_CONTEXT_CHARS` is arithmetic that knows nothing about code points and can land between the halves of an astral character; the edges are snapped outward before slicing (`snapOffSurrogate`), or the excerpt opens with a lone surrogate the browser paints as U+FFFD.

**No `indexOf` fallback.** A 6-char token appearing four times in a 4,500-char description makes it a coin flip that confidently marks innocuous prose. Verify, or degrade honestly.

`frontend/src/components/ToolDescription.vue`:

```vue
<p class="text-sm text-base-content/70 mt-2 whitespace-pre-wrap">
  <template v-for="(seg, i) in segments" :key="i">
    <span v-if="!seg.level">{{ seg.text }}</span>
    <mark
      v-else
      class="bg-transparent text-inherit px-0.5 rounded-sm underline underline-offset-4 decoration-2"
      :class="markClass(seg.level)"
    >
      <!-- IMPLEMENTED WITHOUT tabindex / role="button" / aria-label /
           aria-describedby / the three handlers this sketch showed. Phase 1
           ships no findings list for a mark to move focus TO, so role="button"
           plus "Activate to show the finding" was a tab stop that lied, and
           @keydown.space.prevent swallowed page scrolling in exchange for
           nothing. role="button" is also Children Presentational in ARIA, so
           the aria-label REPLACED the flagged words and truncated them at 60
           characters — hiding the back half of a long payload from the only
           readers who cannot see it on screen. The severity/rule prefix is a
           .sr-only span instead, leaving the flagged text itself as the
           accessible content. Wire the affordance back on in the phase that
           actually ships the target. -->
      <template v-for="(p, j) in revealInvisibles(seg.text)" :key="j">
        <code v-if="p.hidden" class="badge badge-xs badge-error font-mono align-baseline"
              :aria-label="`${p.name}, U+${p.hex}`">U+{{ p.hex }}</code>
        <template v-else>{{ p.text }}</template>
      </template>
      <sup class="ml-0.5 text-[10px] font-bold">{{ seg.sources[0].index }}</sup>
    </mark>
  </template>
</p>
```

Every upstream character reaches the DOM through `{{ }}`. **No `v-html` on any path.** The guard is a **test**, `frontend/tests/unit/tool-description-no-v-html.spec.ts`, not an ESLint override: `npm run lint` in this repo is a literal `echo` no-op and `eslint.config.cjs` is an eslintrc-shaped file while the installed ESLint is v10, which reads only flat config — `npx eslint src/components/ToolDescription.vue` answers "File ignored because no matching configuration was supplied" and exits 0. A rule added there would read like a guarantee and enforce nothing. `revealInvisibles()` turns zero-width/bidi runes into visible chips — otherwise a `unicode.hidden` highlight has nothing to draw. `<mark>`'s UA default is a hard yellow that is illegible on the dark DaisyUI theme, hence the explicit `bg-transparent text-inherit` reset before the tint.

**Density policy — both tiers get a mark.** As drafted: only `tier === "hard"` spans get an inline `<mark>`; soft spans get a paragraph rule instead. That policy is self-defeating either way. Today both span producers are hard (`tpa.bundle.*` and, until D1 lands, `shadowing.cross_server`), so a hard-only rule is a no-op; once D1 lands it would render *zero* marks for the "For this reason," false positive that is this document's own grounding case. **Implemented: soft spans get an inline mark too**, distinguished by tone, glyph and decoration (`▮` + `decoration-wavy decoration-warning` vs `▲` + `decoration-double decoration-error`), never by colour alone. Cap at 20 verified **spans** per description, then "+N more matches" — note this bounds spans, not `<mark>` elements: the boundary sweep splits overlaps, so N spans render as up to 2N-1 marks. Capping marks instead would mean dropping part of a span mid-word. Descriptions over 1,200 chars render a windowed excerpt around each marked span (±200 chars) with a "show full description" toggle — never truncating *through* a mark.

Severity is encoded three ways, never colour alone: `▲ dangerous` / `decoration-double decoration-error`, `▮ warning` / `decoration-wavy decoration-warning`. DaisyUI semantic tokens only, never raw Tailwind colours.

### 3.5 Fallback ladder

| Case | Render |
|---|---|
| Spans present, all usable | Highlighted text + findings list |
| Spans present, **all** fail `isSpanUsable` | Plain description + findings list + `⟳ Description changed since the last scan — highlights hidden.` |
| Spans present, **some** fail `isSpanUsable` | The verified marks + `⟳ Description changed since the last scan — N flagged passages could not be located.` Partial staleness must be counted and announced, or a description that changed since the scan renders as confidently and completely annotated while a finding silently vanishes. |
| Spans absent (normalized-text check, or a Docker/SARIF scanner) | Plain description + findings list; the finding row reads `Location: whole description` and shows **no** "Show in description" button |
| No scan for this tool | Nothing. No badge, no "not scanned" nag on the card |

Governing rule: **the description text never disappears behind a scan state.** Every failure mode degrades to today's rendering, which is strictly not worse.

---

## 4. Background scanning policy

### 4.1 What already ships — do not rebuild it

`security.auto_baseline_scan` (`config.go:2939`, resolver `IsAutoBaselineScanEnabled()` at `:3005`, nil ⇒ **true**, env kill-switch `MCPPROXY_AUTO_BASELINE_SCAN` at `:2021`) already drives `internal/server/scan_informational.go`: an informational baseline scan on every newly admitted server in **any** trust mode, plus a one-shot per-installation sweep. On-by-default was won in #1031. Missing: **recurrence** (`claimInformationalScan` refuses any server whose `GetScanSummary != nil` — a server is auto-scanned exactly once, ever), **per-server opt-out**, and **any UI**.

### 4.2 Trigger matrix

| Event | Scan type | Cost | Status |
|---|---|---|---|
| Server admitted / first enabled | full `StartScan`, baseline only | up to ~60s if disconnected | **exists**, unchanged |
| One-shot post-upgrade sweep | full `StartScan`, serialized, 45s delay | bounded, once per install | **exists**, unchanged |
| **Tool-set change (all trust modes)** | `StartScan`, **Ready-only** | ~2.9s observed for 15 tools | **NEW (Phase 3)** |
| **Signature-bundle fingerprint change** | in-process pass over cached metadata | one pass, no connections | **NEW (Phase 3)** |
| Manual "Scan Now" / REST / CLI / MCP | unchanged | — | exists |
| Daily / periodic timer | — | — | **REJECTED** |

**Why no daily timer:** nothing between a tool-set change and a bundle change can alter a verdict, so a ticker buys zero findings at maximum churn — and `EnsureConnected` inside `StartScan` **restarts stdio servers** (`service.go:1108-1115`) and can burn ~60s per server (`scan_informational.go:46-57`). The two event triggers strictly dominate it.

**D2 — per-server opt-out is one switch.** `auto_baseline_scan: false` on a server suppresses **every** automatic scan of it: the admission baseline, the tool-change rescan and the bundle-activation rescan alike. It does not touch manual "Scan Now" / REST / CLI / MCP scans, which are an operator action, not automation.

**Hard precondition on the new triggers:** *never call `EnsureConnected` from a rescan path.* Enqueue only when the server is already `Ready`; otherwise drop the request and mark the verdict stale. Without this, a reconnect storm becomes self-amplifying (reconnect → tool churn → rescan → restart → reconnect).

**Why the bundle trigger gates on fingerprint, not on `ConfigureBundle`:** `ConfigureBundle` (`service.go:293`) runs on *every* config hot-reload; gate on a change in `BundleInfo.Fingerprint` or a config save fans out to 28 servers.

**Concurrency & rate limits (28 servers):** 60s per-server debounce · 10-minute per-server minimum interval, any reason · globally serialized behind the existing `infoScanRunMu` (one background scan proxy-wide) · baseline `tpa-descriptions` only (`InProcess: true`, `NetworkReq: false`, `registry_bundled.go:140-156`). Deep scan is never reachable from any background path.

**Provenance ships in the same PR as the new triggers, not after.** `scan_informational.go:99-118` documents a HIGH-severity window where a clean informational settle can auto-unquarantine a `trust_mode:"scan"` server via `maybeAutoApproveScanSettled` (`server.go:661`); broadening triggers widens it. Add `ScanJob.Origin string` (`admission|informational|tool_change|bundle_activation|manual`), carry `job_id` + `origin` on `publishScanSettled` (`event_bus.go:764-775`), and make `maybeAutoApproveScanSettled` accept **only** `origin == "admission"`.

### 4.3 Config keys

```jsonc
{
  "security": {
    "auto_baseline_scan": true        // EXISTS: *bool, nil ⇒ true, env MCPPROXY_AUTO_BASELINE_SCAN
  },
  "mcpServers": [
    { "name": "internal-tools", "trust_mode": "auto", "auto_baseline_scan": false }  // NEW
  ]
}
```

Per-server field on `ServerConfig` (`config.go`, beside `TrustMode` at `:611`), reusing the global key's name and the `AutoApproveToolChanges` tri-state pointer convention:

```go
// AutoBaselineScan overrides security.auto_baseline_scan for this server.
// nil (default) inherits the global value. Orthogonal to TrustMode, which
// governs APPROVAL gating, not scanning.
//
// false means NO AUTOMATIC SCANNING OF THIS SERVER AT ALL (D2): the admission
// baseline, the tool-change rescan and the bundle-activation rescan are all
// suppressed. Manual scans are unaffected.
AutoBaselineScan *bool `json:"auto_baseline_scan,omitempty" mapstructure:"auto-baseline-scan"`
```

Six wiring points, all mandatory (spec 086 FR-017 had to add exactly this set for `TrustMode`): `DetectConfigChanges`/`slices.Equal` · `MergeServerConfig` (`merge.go:159`) · `CopyServerConfig` (`merge.go:591`) · `make swagger` (`Makefile:41`; `make swagger-verify` is the CI gate) · no per-server env override · `docs/features/security-quarantine.md`.

### 4.4 Opt-out UI

- **Global:** `frontend/src/views/settings/fields.ts` `SECURITY_FIELDS`, above `security.deep_scan.enabled`. Toggle, with a `danger.confirmValue: false` confirmation ("new and changed tool descriptions are never scanned for poisoning"). The `security` block is deep-compared and hot-reloads — **no `restart: true`**.
- **Per-server:** `ServerDetail.vue` Configuration tab, directly under the trust-mode block (`:854-889`), `data-test="server-auto-scan"`, rendering "Following the global setting (on)" when the value is `null` — the same tri-state presentation `TrustModeSelector.vue` uses.

### 4.5 Progress and failure — no nagging

**No toasts, no modals, no banners, no tray badge for any background scan result.** Running → the existing chip slot shows `loading loading-xs`, driven through `decideScanReconcile()` (`utils/scanState.ts:65`), never re-derived — that reducer is what fixed the stuck-"Scanning…" bug MCP-2740. Failed → a neutral `Scan could not complete` chip in the `precaution` tone, reusing the `HOLD_REASON_SCAN_COVERAGE` vocabulary (`utils/holdEvidence.ts:201`); a coverage failure is not a threat verdict and must never render red. New finding while the operator is on the page → the row appears; nothing steals focus.

---

## 5. States

| Surface | Empty | Scanning | Stale | Failed | Clean | Flagged |
|---|---|---|---|---|---|---|
| `FlaggedToolsPanel` | not rendered at all — absence *is* the clean state | 3 skeleton rows | banner `Some findings predate the current tool descriptions. [Rescan]` | `Could not load scan findings. [Retry]`, header kept | not rendered | one `<details>` row per tool: chip · rule id · detail · **Show in description** |
| `ToolDescription` | plain `<p>`, byte-identical to today | plain (never a skeleton over readable text) | plain + `⟳ Description changed since the last scan — highlights hidden. [Rescan]` | plain + small `⚠ findings unavailable` note | plain | highlighted + findings list |
| `FindingChip` (card / row) | absent | `loading-xs` | chip + `⟳` with `title` | neutral `Scan could not complete` | absent | `▲ dangerous` / `▮ warning` |
| Tools **Scan** column | `—`, `title="Not scanned yet"` | `skeleton h-4 w-12` | chip + `⟳` | `—` (a scan failure must never hide a tool) | `—` | chip |
| Tools `?finding=flagged` | `No flagged tools.` + link to `/security` | skeleton rows | — | existing table error panel | same as empty | filtered rows |
| ServerDetail Security tab | existing "no findings" copy | existing progress card (`:1293-1327`) | `Scanned <rel>; N tools changed since.` | existing scan-failed banner | counts + `View Full Report →` | `FlaggedToolsPanel` + report link |
| ScanReport location link | plain `<code>` for non-`server:tool` | n/a | n/a | n/a | n/a | `router-link` |
| Per-server scan toggle | n/a | disabled + spinner during `PUT` | n/a | revert optimistic state + inline error | n/a | n/a |

Two invariants: **an unscanned tool is never styled as a safe tool**, and **a failed scan is never styled as a threat**.

---

## 6. Phase plan

### Phase 1 — one click from finding to highlighted word — **IMPLEMENTED 2026-09-01** *(no storage, no contracts, no codegen, no swagger)*

**Backend**
- `internal/security/detect/span.go` **(new)** — `Span`, `SpanField*`, `MaxSpansPerFinding`, `UTF16Offsets`.
- `internal/security/detect/signal.go` — `Signal.Spans []Span`.
- `internal/security/detect/aggregate.go:103-114` — `Finding.Spans`; union across **all** signals, dedupe, sort, cap.
- `internal/security/detect/checks/shadowing.go:176` — `FindAllString` → `FindAllStringIndex`, emit spans in both the reference branch (`:216`) and the clone branch (`:76`).
- `internal/security/scanner/tpa_bundle.go:155-169` — emit a span from the `loc` it already computes.
- `internal/security/scanner/types.go` — `ScanFinding.Spans []detect.Span \`json:"spans,omitempty"\``.
- `internal/security/scanner/inprocess.go:274-299` — carry `Spans`.
- **D1 is NOT in this PR — deferred, see §6a.** `shadowing.go` still emits `TierHard`/0.85 on both branches. What ships is the reference branch's **span**, which is tier-independent. The eval corpus, `cmd/scan-eval/gate.go` and the three feature docs are **untouched**; the gate passes at `recall=1.0000` against the unmodified corpus.
  - **Not migrated:** `ScanFinding.Tier` is persisted, and nothing rescans an already-scanned server on upgrade, so an install that already hit this false positive keeps its stored `tier:"hard"` / `threat_level:"dangerous"` report until it rescans manually. There is no ruleset-version invalidation mechanism in the tree today; adding one belongs with the Phase 3 rescan triggers (§4.2), where a bundle-fingerprint change already has to invalidate stale reports.

**API changed fields:** `ScanFinding.spans[]` on the existing `GET /servers/{id}/scan/report` and `GET /security/scans/{jobId}/report`. Nothing else.

**Frontend**
- `frontend/src/utils/capEvidence.ts`, `frontend/src/utils/highlightSpans.ts`, `frontend/src/utils/toolLocation.ts` **(new)**.
- `frontend/src/components/ToolDescription.vue`, `FindingChip.vue`, `FlaggedToolsPanel.vue` **(new)**.
- `frontend/src/views/ServerDetail.vue` — mount panel at `:424`; `<ToolDescription>` at `:727-729`; `<FindingChip>` at `:687-699`; group `scanReport.value.findings` by `location`; honour `?tool=` deep link near `:3785`.
- `frontend/src/views/ScanReport.vue:283-286`, `:389-392` — location → `router-link`.
- `frontend/src/components/SidebarNav.vue:319-323` — label rename only.

**Tests**
Go: `internal/security/detect/span_test.go` (UTF-16 conversion incl. surrogate pairs; out-of-range rejection) · `internal/security/detect/checks/shadowing_span_test.go` (**the `"For this reason,"` fixture — assert the span lands exactly on `reason` when the token is distinctive, and that `reason` no longer fires after the precision fix**) · `internal/security/scanner/tpa_bundle_span_test.go` · `internal/security/detect/aggregate_span_test.go` (union / dedupe / cap).
Frontend, all at `frontend/tests/unit/*.spec.ts` (`src/**/__tests__/*` never runs — `vitest.config.ts:10`): `highlight-spans.spec.ts` (**round-trip `segments.map(s=>s.text).join('') === text`**; overlap → one segment, two sources; clamping) · `cap-evidence.spec.ts` (vectors copied from the Go test) · `tool-description-highlight.spec.ts` (a description containing `<img src=x onerror=1>` produces no markup; mark count and order) · `tool-location.spec.ts` (last-colon split; `com.googleapis.sqladmin/mcp:create_user`; non-tool locations → `null`) · `flagged-tools-panel.spec.ts` · `scan-report-location-link.spec.ts`.
Plus the Playwright sweep required whenever `frontend/src/` is touched (`docs/development/web-ui-verification.md`).

### 6a. Why D1 (the demote) is deferred — decided 2026-09-01

The demote is approved. It is held back until scan reports carry a **ruleset version**, for one reason: **a tier change only reaches an install on rescan.** `ThreatLevel` and `tier` are persisted as data on the stored report, and nothing in the tree invalidates a report when the ruleset that produced it changes. Shipping the demote today fixes new scans and silently leaves every existing `dangerous` verdict standing — a half-landed fix that is harder to reason about than either end state.

Two facts make waiting cheap:

1. **The stale direction is the safe one.** An un-rescanned install keeps over-flagging, not under-flagging. Nothing becomes callable that should not be.
2. **The measured blast radius is small.** On the maintainer's 28-server install: **7 `shadowing.cross_server` findings out of 947, across 3 of 19 scanned servers.** This branch is not the noise driver (see §7 Risk 1).

Deferring also keeps Phase 1 **purely additive**: no detection-semantics change, no eval-corpus edit, no feature-doc rewrites, no persisted-state skew. The whole PR adds a field and a render path.

**Where the demote lands:** with the Phase 3 bundle-fingerprint rescan trigger, which has to invalidate stale reports anyway. Its tripwire is already in the tree — `TestShadowing_ReferenceOnOrdinaryProseIsSpannedAndStillHard` pins today's hard/dangerous behaviour and names itself as the test to flip. Flipping it will re-break the CI eval gate (shadowing recall 1.00 -> 0.50, overall 0.9310 against a 0.90 floor), because `cmd/scan-eval/gate.go` scores malicious samples at the HARD tier only; the fix is a per-entry `expected_tier` on `gateEntry` defaulting to `hard`, set to `soft` on `sh_ref_database` / `sh_ref_deploy`. That change ships **with** the demote, not before it.

### Phase 2 — reach

- `contracts.Tool.Scan *ToolScanSummary{threat_level, count, rule_ids, job_id, scanned_at}` in `internal/contracts/types.go`; populated in `enrichServerTools` (`internal/httpapi/server.go:3455`) from a write-path index on `scanner.Service` keyed by `Location`, sibling to `summaryCache` (`service.go:2256/2275`); a new `GetToolScanSummary` method on the httpapi controller interface.
- `cmd/generate-types/main.go` — hand-edit the `Tool` literal + add `ToolScanSummary`; `go run ./cmd/generate-types`; commit both or `TestContractsInSync` fails. Mirror on the hand-maintained `GlobalTool` (`frontend/src/types/api.ts:294-312`), which does **not** inherit from generated `Tool`.
- Swagger annotations + `make swagger` (all 21 scan endpoints are currently absent from `oas/swagger.yaml`).
- `Tools.vue` — Scan column, `?finding=` filter, `<ToolDescription>` in the modal (`:458-461`), `scan-settled` SSE subscription.
- Spans for `unicode.hidden` and `payload.decoded`, with field attribution and the control-rune reveal.
- Tests: `internal/httpapi/server_tools_scan_test.go` (pin `CI=""`) · `internal/security/detect/checks/unicode_hidden_span_test.go` · `frontend/tests/unit/tools-view-scan-column.spec.ts`, `tools-findings-filter.spec.ts`.

### Phase 3 — freshness, control, judgement

- `NormalizeWithSpans` in `internal/security/detect/normalize.go` + `FuzzNormalizeParity` against a frozen copy of the current output; spans for `phrase.injection` and `directive.imperative`. **Ship only if parity is proven.**
- `ScanJob.Origin` + `job_id`/`origin` on `publishScanSettled`; `maybeAutoApproveScanSettled` accepts `admission` only.
- Rescan on tool-set change in `internal/runtime/lifecycle.go` `applyDifferentialToolUpdate` (Ready-only, debounced); rescan on `BundleInfo.Fingerprint` change in `ConfigureBundle`.
- `ServerConfig.AutoBaselineScan` + all six wiring points; both UI toggles.
- **D3** — suppression is keyed per **(tool, rule, span text)**, not per (tool, rule): `storage.ToolApprovalRecord` carries suppression records capped like `MaxToolHeldSignals = 16`, applied in the scanner package at verdict time. Keying on the span text is what makes a rug-pull re-fire — the same rule matching *different* words after a description change is a new finding, not a muted one. Suppressed findings still appear in the report, greyed, with who and when.
- Tests: `internal/security/detect/normalize_parity_test.go` · `internal/server/scan_provenance_test.go` (**a rescan settle must never auto-approve**) · `internal/security/scanner/rescan_test.go` (debounce, min-interval, not-Ready skip, per-server opt-out) · `internal/config/auto_baseline_scan_test.go` (tri-state inherit) · `frontend/tests/unit/settings-auto-baseline-scan-field.spec.ts`, `server-detail-scan-optout.spec.ts`.

---

## 7. Risks & non-goals

**Risks**
1. **Alarm fatigue is real but comes from a DIFFERENT scanner.** Measured on the maintainer's 28-server install, latest report per server: **947 findings, 564 `dangerous`** — and **536 of those 564 come from `mcp-ai-scanner`**, not from tool descriptions: `MCP-MC-001` "Obfuscated code pattern in usr/local/lib/python3.13/_pyio.py" (405) and `MCP-MC-003` "Reverse shell or backdoor in tools.json" (131), i.e. a deep scanner flagging CPython's own standard library inside the container image. The `tpa-descriptions` scanner this design surfaces produces **329 findings, of which 7 are `dangerous`** (`shadowing.cross_server`); its bulk is `directive.imperative` at 317, already `TierSoft`/`warning`/`low` — noisy in count, correctly tiered, never blocking. **The inline UI is structurally insulated from the real noise:** `MCP-MC-*` findings carry file-path locations, so `parseToolLocation` returns `null` and they never render against a tool. An earlier draft of this document cited "7,928 findings / 6,095 high"; that figure does not reproduce and appears to have summed historical scan jobs rather than the latest report per server. The Phase 2 gate still stands — if the flagged-tool count on a real install is absurd, fix detection before putting a chip on every row — but the rule to fix first is `MCP-MC-001`, not anything in `tpa-descriptions`.
2. **Stale spans.** Nothing rescans a `manual` server after admission today, so Phase 1 spans can be arbitrarily old. `isSpanUsable` degrades honestly; Phase 3 removes the cause.
3. **`aggregate` emits one `Finding` per tool**, so a finding's `rule_id`/`threat_level` describe only the primary signal. Per-mark labelling relies on `Span.CheckID`/`Tier`, not on the finding-level fields.

**Non-goals**
1. **No sidebar count badge and no Dashboard alert until suppression exists (Phase 3).** A permanent, undismissable `99+` driven by a check we already know is leaky trains the operator to ignore it — permanently, including on the real payload. Ambient per-server chips are fine; a counting interrupt is not.
2. **No highlighting in the `Tools.vue` table cell.** It is `max-w-xs truncate` (`:336`); a mark in the clipped half is a false negative.
3. **No periodic/daily rescan.** §4.2.
4. **No client-side substring matching of `evidence`.** For the flagship rule it is a synthesized sentence (`shadowing.go:216`); for the phrase checks it is stemmed normalized text.
5. **No new `GET /api/v1/security/findings` endpoint.** Phase 1 needs none; Phase 2's `Tool.scan` rides existing payloads. A batch endpoint is a second source of truth for the same data.
6. **Not lifting the quarantined-server skip** in `handleGetGlobalTools` (`internal/httpapi/server.go:3559-3565`) — quarantined descriptions *are* the TPA payload (#1061). `GET /servers/{id}/tools` remains the review surface, which is where Phase 1 lands anyway.
7. **Not rewriting `ScanReport.vue`.** It gains four changed lines. Extracting the duplicated finding cards (`:212-303` / `:328-409`) is a separate cleanup with no user-visible payoff.
8. **Deep scan default unchanged** — off, manual-only (`IsDeepScanEnabled()` nil ⇒ false, `config.go:2971`; Spec 077 US3 FR-006).
9. **Inline findings gate nothing.** `scan_informational.go:26-32` is explicit that the informational path "drives NO gating whatsoever." The panel must say so: *"Informational — these findings do not block the tool."*
10. **No markdown rendering and no `v-html`** anywhere in this feature.

---

## 8. Questions for the maintainer — ANSWERED 2026-09-01

1. **`shadowing.cross_server` precision — patch or demote?** -> **DEMOTE, and defer it (D1).** The reference branch will move to `TierSoft`; `distinctiveName()` and `commonNames` are left exactly as they are, because a stop-list patch is a guess about someone else's tool names and the next collision just moves to the next word. The clone branch stays `TierHard`. **Not shipped in Phase 1** — it waits on ruleset versioning so the change can actually propagate to existing installs; see §6a.

2. **Per-server opt-out semantics.** -> **One switch (D2).** `auto_baseline_scan: false` suppresses every automatic scan of that server — admission baseline, tool-change rescan, bundle-activation rescan. Manual scans are unaffected. Phase 3.

3. **Suppression scope.** -> **Per `(tool, rule, span text)` (D3).** The span-text key is what makes a rug-pull re-fire: the same rule matching different words after a description change is a new finding, not a muted one. Phase 3.

---

## 9. Phase 1 — known gaps at merge

- **The Playwright web-UI sweep** required by `docs/development/web-ui-verification.md` whenever `frontend/src/` is touched has **not** been run: it needs a live core, and the implementation ran under an explicit instruction not to start one (another instance holds the BBolt lock). A merge gate to clear, not a code defect.
- **Stored `tier` is not migrated** — see §6 Phase 1, "Not migrated".
- **The tool-quarantine review card** (`ServerDetail.vue`, the approve/reject surface) still renders the description as a bare `<p>` with no marks; highlighting landed on the informational Available Tools card below it. That matches the Phase 1 file list, but it is the surface where the evidence matters most — take it with the Phase 2 `Tools.vue` work.
- **`FindingChip`'s `stale` / `scanning` / `failed` states and `FlaggedToolsPanel`'s `loading` / `error` / `retry` / `stale` / `actions` slot have no production caller in Phase 1.** They are the §5 states table's contract and Phase 2/3 wires them (`Tools.vue`'s Scan column, `decideScanReconcile()`); they are unit-tested standalone in the meantime.
