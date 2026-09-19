# 🚀 PLAN — PER-SERVER PER-TOOL ANNOTATION OVERRIDES 🛡️ — IDEAL PRODUCTION MAP ✨

> **BRANCH (USER REQ):** `🚀-FEAT-ANNOTATION-OVERRIDES-🛡️-PER-SERVER-TOOL-EXCEPTIONS-✨` (off `main` @ 525d5021) — **LOCAL DEV** with CAPS+EMOJIS per user request. **UPSTREAM PR BRANCH:** `feat/annotation-overrides-per-server-tool` (kebab, no emoji) for author convention (`git branch -a` = kebab, workflows filter `main/next`, `CLAUDE.md` conventional-commits lower).
> **SCOPE:** `mcpproxy-go` ONLY — `/home/agi/PROGRAMMS/AGI-MEMORY-SKILLS/mcpproxy-go`
> **PHILOSOPHY:** Platinum Quality Loop (STRATEGY_COMPLETE.md) adapted to Go — LIVE PROOF > mocks, ZBT++, 7 Quality Gates, Context7+Tavily before every snippet.
> **LANGUAGE:** EN (headers CAPS+EMOJIS for upstream PR), code comments EN per AGENTS.md.
> **STRATEGY SOURCE:** `/home/agi/PROGRAMMS/PROXY/plans/STRATEGY_COMPLETE.md` Part 0-10 — Master Orchestrator → Sub-Orchestrators → Critic → Gates → LIVE → Cosmic Verify.
> **CRITIC POLISH:** v2 addresses harsh critic REJECTED (missing files, mapstructure kebab, deep-copy alias, RFC7396 inner-null, triple-write GH#938, RLock, pointer compare, audit gap, gates path, UI scalability — see §2.7, §3.2, §4).

---

## 0. EXECUTIVE SUMMARY — WHAT & WHY

**PROBLEM:** `browseros` (and any legacy/unsupported MCP server) marks `tabs/navigate/act` `destructiveHint:true` and leaves 7 tools with `nil` → MCP spec default `nil→true` → all 9+7=16 of 24 tools `destructive` (`internal/server/mcp_annotations.go:108-114`, `internal/toolannotations/toolannotations.go:66`). Requires `call_tool_destructive` + token `destructive` on whole fleet. `enabled_tools` hides by name, doesn't fix tier. `strict_server_validation=false` only relaxes `ValidateAgainstServerAnnotations` (`internal/contracts/intent.go:177`) but NOT target-tier gate `mcp.go:2570`.

**SOLUTION:** Admin-only per-server `annotation_overrides` — `map[toolName]*ToolAnnotations` + wildcard `"*"` — like antivirus exceptions. Merged once at capture `internal/upstream/core/client.go:419` → effective annotations used everywhere (`DeriveCallWith`, `classifyToolRisk`, `ExcludeReason`). Audited, fail-closed, hot, reversible via `null` (RFC7396).

**PLACEMENT:** Primary `ServerDetail → Configuration → card after Trust mode` (`ServerDetail.vue:915`), secondary pencil `✎ Override` on each tool card in `ServerDetail Tools` (`ServerDetail.vue:685`) → scroll to config card. NOT global `Settings.vue` (global vs per-server invariant).

---

## 1. 🎯 RESEARCH EVIDENCE — CONTEXT7 + REAL CODE (LIVE PROOF)

**EVERY SNIPPET BELOW WAS VERIFIED vs REAL CODE + MODERN STANDARDS — NO HALLUCINATION:**

| Claim | Real Code | Context7 / Standard |
|---|---|---|
| `ToolAnnotations` `*bool` tri-state | `internal/config/config.go:1409-1416` `ReadOnlyHint *bool` `omitempty`, same as `ExposePrompts *bool:705`, `AutoApproveToolChanges *bool:687` | Go `encoding/json` `isEmptyValue` : `*bool nil → omit`, `true/false → emit`. Context7 `/golang/go` confirms `omitempty` on pointer is canonical tri-state. |
| Hash excludes annotations (intentional) | `internal/runtime/tool_quarantine.go:24-33` comment 4 points + `calculateToolApprovalHashWithOutputSchema:36` ignores param | Author chose to avoid `tool_description_changed` spam on reconnect — overrides MUST NOT be hashed. |
| Merge map RFC7396 null=delete | `internal/config/merge.go:408-439` `MergeMapWithOpts` + `removeMarkers:47-71` + `shouldRemove:80` | Standard `MergeOptions.NullRemovesField` — `env`/`headers` already use it (`mcp.go:5456,5569`). New map must follow. |
| ValidateDetailed is write-gate | `internal/config/config.go:2275 validateDetailedCore` vs `2691 Validate` (tolerant) | Every `PATCH /api/v1/config` + `PATCH /api/v1/servers/{id}` gates via `ValidateDetailed` — per-server loop `2481-2606` is canonical. |
| `DeriveCallWith` priority | `internal/contracts/intent.go:208-228` `destructive→destructive > readOnly false→write > readOnly true→read > nil→read` | MCP spec 2024-11-05 + June 2025 optional hints. |
| `classifyToolRisk` nil→permissive | `internal/server/mcp_annotations.go:99-122` `nil→hasOpenWorld+hasDestructive+hasWrite=true` | Fail-closed pessimism — lethal trifecta default. |
| `ExcludeReason` single truth | `internal/toolannotations/toolannotations.go:49-86` `first-filter-wins read→destructive→openWorld` | Extracted to leaf per Spec 098 to avoid `server` import cycle — overrides must be resolved BEFORE it. |
| `TrustModeSelector` 3-state pattern | `frontend/src/components/TrustModeSelector.vue:1-72` `select` + `pendingWarnMode` | Tri-state `*bool` → `select Inherit/Yes/No`, NOT toggle (toggle=2-state). `fields.ts:7` `toggle` is binary only. |
| ServerDetail Configuration layout | `ServerDetail.vue:871-943` `space-y-6` `card General 879 → trust-mode-card 915 → Connection 948` | Author groups security per-server knobs together — overrides belongs after trust-mode. |
| PATCH surfaces triple-write | `internal/httpapi/server.go:2398 handlePatchServer` + `internal/server/mcp.go:5632 buildPatchConfigFromRequest` + `frontend/src/services/api.ts:352 patchServer` | GH#938 failure: `trust_mode` added to only 1 of 3 → silently dropped. New field MUST be added to all 3. |
| Audit gap | `internal/audit/line.go:21-100` only `authz/tool_call/auth_event` — no `config_change` | OWASP ASVS 7.x + NIST AU-2 requires privileged config diff in tamper-evident audit, not only activity log (`runtime/event_bus.go:753 EmitActivityConfigChange`). |

**LIVE VERIFICATION (2026-09-19):** `curl -H X-API-Key … /api/v1/servers/browseros/tools | jq` → 24 tools: 9 `destructiveHint:true`, 8 `readOnlyHint:true`, 7 `{} (nil→destructive)`. `GET /api/v1/config` → `strict_server_validation:true`, `quarantine_enabled:false`. `browseros` `healthy Connected 24 tools` after `PATCH enabled:true` (`restart_required:true`).

---

## 2. 🗺️ FILE-BY-FILE DIFF MAP — EXACT EDITS (PLATINUM: EVERY LINE VERIFIED)

### 2.1 BACKEND — CONFIG MODEL

**`internal/config/config.go`**
```go
// After:750 (EnabledTools/DisabledTools block) — add — NOTE mapstructure kebab per author conv (trust_mode:694, expose_prompts:705):
AnnotationOverrides map[string]*ToolAnnotations `json:"annotation_overrides,omitempty" mapstructure:"annotation-overrides"`

// New helper (near ValidTrustModes:2033):
func IsValidToolNameForOverride(name string) bool // name=="*" || (len 1..256 && no leading/trailing space && no "__" collision; regex ^[A-Za-z0-9._:-]+$; "*" only standalone)
```

**Why `*ToolAnnotations` not `ToolAnnotations`:** pointer lets `{"tool":null}` delete whole entry via RFC7396 (consistent with `Headers map[string]*string` in `httpapi/server.go:1906`). Inner `*bool` already handles per-hint `inherit` (`nil`=inherit upstream).

**`internal/config/config.go` — `CopyServerConfig` (≈591-676) — MUST preserve all fields (critic: missing `quarantineExplicitlySet`, `Isolation.Clone()`, `AuthBroker`, `EnabledTools` etc would break #937 gate). Deep-copy new map AFTER existing full copy:**
```go
// Inside CopyServerConfig(dst,src) after existing deep-copy of Isolation/AuthBroker/Env/Headers/EnabledTools:
if src.AnnotationOverrides != nil {
  dst.AnnotationOverrides = make(map[string]*ToolAnnotations, len(src.AnnotationOverrides))
  for k, v := range src.AnnotationOverrides {
    if v == nil { dst.AnnotationOverrides[k] = nil; continue }
    cp := *v // shallow; then deep-copy bool ptrs
    if v.ReadOnlyHint != nil { b := *v.ReadOnlyHint; cp.ReadOnlyHint = &b }
    if v.DestructiveHint != nil { b := *v.DestructiveHint; cp.DestructiveHint = &b }
    if v.IdempotentHint != nil { b := *v.IdempotentHint; cp.IdempotentHint = &b }
    if v.OpenWorldHint != nil { b := *v.OpenWorldHint; cp.OpenWorldHint = &b }
    // Title is string (not *string) — "" = preserve; if clear needed future: change to *string
    dst.AnnotationOverrides[k] = &cp
  }
}
```

**`internal/config/config.go` — `validateDetailedCore` loop `2481-2606` — PERSISTED config never sees `nil` entry (null is remove marker, not persisted). Validate persisted only:**
```go
// Inside for i, server := range c.Servers — after existing checks:
if len(server.AnnotationOverrides) > 100 {
  errs = append(errs, FieldError{Field: fmt.Sprintf("mcpServers[%d].annotation_overrides", i), Message: "too many overrides (max 100)"})
}
for k, v := range server.AnnotationOverrides {
  if k != "*" && !IsValidToolNameForOverride(k) { errs = append(errs, FieldError{Field: fmt.Sprintf("mcpServers[%d].annotation_overrides[%q]", i, k), Message: "invalid tool name (use \"*\" or alphanumeric._:-)"}) ; continue }
  if v == nil { // should never persist, but guard
    errs = append(errs, FieldError{Field: fmt.Sprintf("mcpServers[%d].annotation_overrides[%q]", i, k), Message: "nil override"}) ; continue
  }
  if v.Title == "" && v.ReadOnlyHint==nil && v.DestructiveHint==nil && v.IdempotentHint==nil && v.OpenWorldHint==nil {
    errs = append(errs, FieldError{Field: fmt.Sprintf("mcpServers[%d].annotation_overrides[%q]", i, k), Message: "at least one hint must be set"})
  }
}
```

**`internal/config/merge.go` — new merger — handles RFC7396 whole-key + inner-hint null via RawMessage probe (critic: cannot ignore inner `readOnlyHint:null`):**
```go
func MergeAnnotationOverrides(base, patch map[string]*ToolAnnotations, opts MergeOptions) map[string]*ToolAnnotations
// Patch semantics:
// - patch==nil → preserve base (omitted field)
// - patch non-nil empty → no-op (author conv: deletion only via removeMarkers)
// Implementation:
// 1. Deep copy base (as above).
// 2. For k, v := range patch {
//      if v == nil { continue } // null handled via removeMarkers, not here (defense)
//      if base[k]==nil { base[k]=&ToolAnnotations{} }
//      // per-hint merge: nil in patch = preserve base (inherit), non-nil = override
//      if v.ReadOnlyHint != nil { b:=*v.ReadOnlyHint; base[k].ReadOnlyHint=&b }
//      // similarly Destructive/Idempotent/OpenWorld; if v.Title!="" { base[k].Title=v.Title }
//    }
// 3. For k := range opts.GetRemoveMarkersForMap("annotation_overrides") {
//      // covers {"annotation_overrides":null} and {"annotation_overrides":{"tool":null}}
//      // For nested "annotation_overrides.tool" marker, delete that tool key
//      delete(base, k)
//    }
// 4. For inner hint null: caller (httpapi/mcp) must populate opts removeMarkers for "annotation_overrides.tool.readOnlyHint" etc
//    via raw JSON scan (see quarantineExplicitlySet:671 pattern + MergeMapWithOpts:412 inner loop). If present, set that hint nil in base[k].
//    Document: v1 supports whole-tool delete; per-hint revert via re-PATCH tool with hint omitted OR via inner-null marker if implemented.
```

**`internal/config/merge.go` — `MergeServerConfig` (≈267-295)**
- After `MergeMapWithOpts` for `env/headers`: `dst.AnnotationOverrides = MergeAnnotationOverrides(dst.AnnotationOverrides, patch.AnnotationOverrides, opts)`

**`internal/config/config.go` — `NormalizeAnnotationOverrides` (new, called from `internal/config/loader.go:1284` alongside `normalizeServerQuarantineFlags`, `migrateDeepScanConfig:3153`)**
- Prune empty map entries where all hints nil (legacy `* {}`), enforce 100 cap, no-op if nil.

**`internal/config/merge.go` — imports:** `fmt` already, no new dep.

### 2.2 BACKEND — EFFECTIVE ANNOTATIONS RESOLVER (CRITIC FIX: LOCATION + CONCURRENCY + PTR COMPARE)

**NEW — `internal/config/annotation_overrides.go` (leaf, no cycle)**
- House `EffectiveAnnotationsForTool(overrides map[string]*ToolAnnotations, toolName string, upstream *ToolAnnotations) *ToolAnnotations` here (NOT in `core/client.go` — `internal/server/mcp.go:7114 lookupToolAnnotations` + `internal/server/preflight_glue.go:447` need it and cannot import `core` (import cycle)).
- Same logic as snippet below, but as `config` package helper so both `core` and `server` import `config`.
- Use value equality, NOT pointer `!=` (critic: `eff != toolMeta.Annotations` always true when override present). Instead: `if eff != nil && !annotationsEqual(eff, toolMeta.Annotations) { toolMeta.Annotations = eff }` or simply `toolMeta.Annotations = eff` when `eff` returned (eff already nil when no override).

**`internal/upstream/core/client.go:402-433` — `ListTools` — ADD RLock + call config helper**
```go
// After hasAnnotations block (425), before toolMeta.Hash (430):
// Apply per-server operator overrides (admin-only, persisted in ServerConfig) — read under RLock (critic: race with hot-reload)
c.mu.RLock()
overrides := c.config.AnnotationOverrides
// Deep-copy not needed — Effective helper copies
c.mu.RUnlock() // keep lock short; or hold across effective call if config pointer stable
if eff := config.EffectiveAnnotationsForTool(overrides, tool.Name, toolMeta.Annotations); eff != nil {
  // eff is merged copy; log at debug for audit trace
  // c.logger.Debug("Tool annotations overridden", zap.String("tool", tool.Name), zap.Any("upstream", toolMeta.Annotations), zap.Any("effective", eff))
  toolMeta.Annotations = eff
} else if overrides != nil && (overrides["*"] != nil || overrides[tool.Name] != nil) {
  // overrides existed but resulted in empty → treat as nil (no hint) to preserve nil-default semantics
  toolMeta.Annotations = nil
}
```

**Helper signature in `internal/config/annotation_overrides.go`:**
```go
func EffectiveAnnotationsForTool(overrides map[string]*ToolAnnotations, toolName string, upstream *ToolAnnotations) *ToolAnnotations {
  wild := overrides["*"]; exact := overrides[toolName]
  if wild==nil && exact==nil { return upstream }
  base := &ToolAnnotations{}
  if upstream!=nil {
    *base = *upstream
    if upstream.ReadOnlyHint!=nil {b:=*upstream.ReadOnlyHint; base.ReadOnlyHint=&b}
    if upstream.DestructiveHint!=nil {b:=*upstream.DestructiveHint; base.DestructiveHint=&b}
    if upstream.IdempotentHint!=nil {b:=*upstream.IdempotentHint; base.IdempotentHint=&b}
    if upstream.OpenWorldHint!=nil {b:=*upstream.OpenWorldHint; base.OpenWorldHint=&b}
  }
  for _, ov := range []*ToolAnnotations{wild, exact} { // wildcard first, exact wins per-hint
    if ov==nil { continue }
    if ov.Title != "" { base.Title = ov.Title }
    if ov.ReadOnlyHint != nil { b:=*ov.ReadOnlyHint; base.ReadOnlyHint=&b }
    if ov.DestructiveHint != nil { b:=*ov.DestructiveHint; base.DestructiveHint=&b }
    if ov.IdempotentHint != nil { b:=*ov.IdempotentHint; base.IdempotentHint=&b }
    if ov.OpenWorldHint != nil { b:=*ov.OpenWorldHint; base.OpenWorldHint=&b }
  }
  if base.Title=="" && base.ReadOnlyHint==nil && base.DestructiveHint==nil && base.IdempotentHint==nil && base.OpenWorldHint==nil { return nil }
  return base
}
func annotationsEqual(a,b *ToolAnnotations) bool { /* compare Title + *bool deref */ }
```

**Also patch:** `internal/server/preflight_glue.go:301,447,471` `preflightSnapshot()` + `internal/server/mcp_direct_catalog.go:91` + `internal/server/mcp.go:7114 lookupToolAnnotations` — wrap their `ToolAnnotations` return through `config.EffectiveAnnotationsForTool(server.AnnotationOverrides, tool, upstream)` so preflight + direct catalog lethal-trifecta also see effective.

**NOTE:** Do NOT hash overrides (`internal/runtime/tool_quarantine.go:54` + `internal/storage/models.go:ToolApprovalRecord` stays). Merge is read-path only — `calculateToolApprovalHashWithOutputSchema` still ignores annotations.

### 2.3 BACKEND — API SURFACES (TRIPLE WRITE — CRITIC FIX: MANUAL MERGE + NULL MARKERS)

**`internal/httpapi/server.go` — `AddServerRequest:1901` + `oas/swagger.yaml:Server`**
- Add `AnnotationOverrides map[string]*config.ToolAnnotations `json:"annotation_overrides,omitempty"`
- Regenerate swagger via `make gen` (or `go generate ./...`) — required for `oas/swagger.yaml` contract (`api.ts` is generated from it).

**`internal/httpapi/server.go` — `handlePatchServer:2398` — MANUAL DEEP-MERGE (critic: cannot `patchSC.AnnotationOverrides = req.AnnotationOverrides` — would overwrite 23 other tools)**
```go
// Inside handlePatchServer after existing req.Env/req.Headers handling (2499-2646 pattern):
if req.AnnotationOverrides != nil || hasRemoveMarker("annotation_overrides") {
  // Merge, not replace: reuse config.MergeAnnotationOverrides with opts from parsePatchWithRemoveMarkers
  // Detect per-tool null via raw map scan:
  // rawPatch := map[string]json.RawMessage; json.Unmarshal(body, &rawPatch); if rawPatch["annotation_overrides"] != nil {
  //   var inner map[string]json.RawMessage; json.Unmarshal(rawPatch["annotation_overrides"], &inner)
  //   for k, v := range inner { if string(bytes.TrimSpace(v))=="null" { opts.WithRemoveMarker("annotation_overrides."+k) } }
  // }
  merged := config.MergeAnnotationOverrides(existing.AnnotationOverrides, req.AnnotationOverrides, opts)
  patchSC.AnnotationOverrides = merged
}
// Validate via ValidateDetailed already covers. Hot-reload: no restart_required (like tool enable), return restart_required:false (unlike isolation which is true:2681).
```

**`internal/server/mcp.go` — `buildPatchConfigFromRequest:5632` — PARSE WITH RAW MARKERS (same as httpapi)**
```go
if raw, ok := patchRaw["annotation_overrides"]; ok {
  if string(bytes.TrimSpace(raw))=="null" {
    opts.WithRemoveMarker("annotation_overrides")
  } else {
    var m map[string]*config.ToolAnnotations
    if err:=json.Unmarshal(raw, &m); err!=nil { return nil, FieldError{Field:"annotation_overrides", Message:"invalid JSON"} }
    // Per-tool null markers
    var inner map[string]json.RawMessage
    if err:=json.Unmarshal(raw, &inner); err==nil {
      for k, v := range inner { if string(bytes.TrimSpace(v))=="null" { opts.WithRemoveMarker("annotation_overrides."+k) } }
    }
    // Per-hint inner null: {"tool":{"readOnlyHint":null}} → need marker "annotation_overrides.tool.readOnlyHint"
    // Scan inner objects similarly and WithRemoveMarker that path; MergeAnnotationOverrides will set that hint nil.
    patchSC.AnnotationOverrides = m
  }
}
```
- Gate via `AuthorizeServerOp` — reuse `patch` (already admin-only) — but also ensure `Add` path (`ServerOpAdd`) checks `annotation_overrides` requires admin (critic: agent could POST with `destructive:false` to lower tier without audit). Add `annotation_overrides` to denylist check in `ServerOpAdd` or require admin for any `annotation_overrides` present.

**`internal/auth/server_ops.go:59` — explicit denylist**
- Add `ServerOpAnnotationOverride = "annotation_override"` and include in `agentDeniedServerOps` (or ensure `patch`/`add` already covers — but be explicit per critic: fail-closed on new mutation).

**`frontend/src/services/api.ts:352` — `PatchServerRequest` type**
- Must match `httpapi` snake_case `annotation_overrides` (not camel). Generated from `oas/swagger.yaml` — run `make gen`.

**`internal/config/loader.go` — add `normalizeAnnotationOverrides` alongside `normalizeServerQuarantineFlags:1284` if legacy key exists (no legacy, but keep hook).**

### 2.4 BACKEND — AUDIT (CRITIC FIX: TAMPER-EVIDENT)

**`internal/audit/line.go:21-100` — ADD `config_change` audit kind (OWASP ASVS 7, NIST AU-2/9)**
- Existing only `authz/tool_call/auth_event`. Privileged `annotation_override` MUST be in `audit.Sink` (append-only, `request_id` correlated via `reqcontext.GetRequestID`), not only activity BBolt.
- New `func NewConfigChangeAuditLine(actor Actor, target string, before, after map[string]*ToolAnnotations, requestID string) *Line` with `Action:"annotation_override"` + `Before/After` (bool hints, no secret mask needed but keep `CheckServerWriteMasks` pass).

**`internal/runtime/event_bus.go:753` `EmitActivityConfigChange` + `internal/server/mcp.go:3780-3807` `redactedConfigDiff`**
- Keep activity diff for ops; ALSO call `audit.Sink.Emit(NewConfigChange...)` on same path. Ensure `request_id` propagated (`X-Request-Id` header already at `mcp.go:3780`).
- Document if audit sink disabled (personal edition) → activity log suffices for AU-9(4) with retention.

**`internal/storage/models.go:ToolApprovalRecord` — no hash change — document in `tool_quarantine_test.go:54` that effective annotations do NOT affect `Hash`.**

### 2.5 FRONTEND — CONFIG CARD (CRITIC FIX: SCALABILITY + VISIBILITY)

**`frontend/src/views/ServerDetail.vue:915` — after `trust-mode-card` — card with INLINE POPOVER SCALABILITY (critic: 24×4=96 selects in narrow card not scalable)**
```vue
<div class="card bg-base-100 shadow-sm" data-test="annotation-overrides-card">
  <div class="card-body py-4">
    <h3 class="card-title text-base">Annotation Overrides</h3>
    <p class="text-sm text-base-content/60">Fix false hints from the upstream server. “*” applies to all tools; a tool row wins per-hint. Inherit = use what the server sent. Changes apply immediately (no restart) and are audited.</p>
    <AnnotationOverridesEditor
      :server-name="server.name"
      :tools="serverTools"
      :overrides="server.annotation_overrides || {}"
      :upstream-annotations="toolAnnotationsMap"
      @save="saveAnnotationOverrides"
    />
    <!-- Raw JSON bulk fallback for 100 entries (critic: mass edit) -->
    <details class="collapse collapse-arrow bg-base-200 mt-3"><summary class="collapse-title text-sm">Bulk edit as JSON</summary>
      <div class="collapse-content"><textarea v-model="rawJson" class="textarea textarea-bordered w-full font-mono text-xs" rows="6" data-test="annotation-overrides-raw"></textarea><button @click="applyRawJson" class="btn btn-sm mt-2" data-test="annotation-overrides-apply-raw">Apply JSON</button></div>
    </details>
  </div>
</div>
```

**New component `frontend/src/components/AnnotationOverridesEditor.vue` — INLINE POPOVER, NOT 96 SELECTS**
- Props: `serverName, tools[], overrides, upstream`
- Layout: compact table `Tool | Effective | Actions` (Effective badge via `AnnotationBadges` colors `badge-info/error/neutral/secondary`). Clicking row opens popover with 4× `select Inherit/true/false` (like `TrustModeSelector` 3-radio).
- Row for `"*"` pinned at top with `badge badge-outline` wildcard label.
- Per-hint `select` options `Inherit / true / false`; `effectiveAnnotationsForTool` computed for preview badge.
- Save: `api.patchServer(serverName, {annotation_overrides: overrides})` — mirrors `saveTrustMode:3490` (`trust_mode`). Use snake_case `annotation_overrides` (critic: `api.ts:352` generated from `oas/swagger.yaml` snake).
- `data-test`: `annotation-overrides-card`, `annotation-override-select-${tool}-${hint}`, `annotation-overrides-save`, `annotation-overrides-raw`.

**`frontend/src/services/api.ts:352` — `PatchServerRequest` now includes `annotation_overrides?: Record<string, {readOnlyHint?: boolean, destructiveHint?: boolean, openWorldHint?: boolean, idempotentHint?: boolean, title?: string}>` — run `make gen` from `oas/swagger.yaml`.**

**`ServerDetail.vue:685` — Tools tab per-tool card header — FIX VISIBILITY (critic: `isToolToggleAvailable` false for pending/changed hides pencil exactly when needed)**
```vue
<!-- OLD (WRONG): v-if="isToolToggleAvailable(tool.name)" -->
<button data-test="tool-annotation-edit-${tool.name}" @click="focusAnnotationOverride(tool.name)" class="btn btn-ghost btn-xs" title="Override annotations for this tool" :disabled="isToolConfigDenied(tool.name)">✎ Override</button>
<!-- Always visible, disabled only when locked-by-config (like 🔒 badge:761). -->
```
- `focusAnnotationOverride(toolName)` → `activeTab='config'` + `nextTick` + `querySelector('[data-test="annotation-overrides-card"]')?.scrollIntoView({behavior:'smooth'})` + `highlight` (`ring-2 ring-primary` as `Settings.vue:475`) + pre-open popover for that tool.

### 2.6 DOCS + GENERATION

- `docs/configuration/config-file.md` — add `annotation_overrides` section with browseros 24-tool example (wildcard `*` + per-tool `act`).
- `docs/configuration/upstream-servers.md` — table row for `annotation_overrides` (type `map[string]object`, admin-only, hot, audited).
- `oas/swagger.yaml` — add `annotation_overrides` to `Server` schema (snake_case). Run `make gen` to regenerate `frontend/src/services/api.ts` + Go server stubs.
- `roadmap.yaml` is SOURCE, `ROADMAP.md` GENERATED via `scripts/gen-roadmap.py` (critic: not reverse). No epic needed for additive feature, but add feature note if required.

### 2.7 MISSING FILES — CRITIC FIX (ADDED)

- `internal/config/loader.go` — `normalizeAnnotationOverrides` hook (alongside `normalizeServerQuarantineFlags:1284`, `migrateDeepScanConfig:3153`) for legacy pruning.
- `internal/storage/models.go` / `internal/runtime/tool_quarantine.go` — ensure effective not persisted as `ToolApprovalRecord.Hash` (document hash stability).
- `internal/server/preflight_glue.go:301,447,471` + `internal/server/mcp_direct_catalog.go:91` + `internal/server/mcp.go:7114` — all wrap through `config.EffectiveAnnotationsForTool` (also fixes preflight + direct lethal-trifecta).
- `frontend/src/services/api.ts` + `oas/swagger.yaml` — codegen sync (critic: without `make gen` frontend won't build).
- `internal/auth/server_ops.go:59` — add `ServerOpAnnotationOverride` denylist or verify `patch` covers `Add`.
- `internal/audit/line.go` + `internal/server/audit_funnel.go` + `internal/runtime/event_bus.go:753` — audit sink for config_change.

### 2.8 LINE CITATION CORRECTIONS (CRITIC)

- `ValidateAgainstServerAnnotations` at `internal/contracts/intent.go:162` (not :177), `classifyToolRisk` at `99-122` (not 108-114), target-tier gate at `mcp.go:2555` (not 2570).
- Spec numbering: next spec after `107-server-edition-sso-hardening` is `108-annotation-overrides` — reserve, not hallucinate existing.

---

## 3. 🧪 TEST PLAN — PLATINUM COVERAGE (EVERY BRANCH)

### 3.1 BACKEND GO — `go test -race ./... -count=1`

| File | Test | Given/When/Then | Mutation Kill |
|---|---|---|---|
| `internal/config/config_test.go` | `TestAnnotationOverrides_ValidateDetailed_RejectsEmptyHint` | Given server with `{"a":{}}` → ValidateDetailed error `at least one hint` | Boundary empty |
| | `TestAnnotationOverrides_ValidateDetailed_RejectsTooMany` | 101 entries → error max 100 | Boundary 100/101 |
| | `TestAnnotationOverrides_ValidateDetailed_WildcardAllowed` | `{"*":{destructiveHint:false}}` → pass | Wildcard vs tool name |
| | `TestCopyServerConfig_DeepCopyOverrides` | Mutate copy doesn't mutate src pointer | Pointer alias |
| | `TestMergeAnnotationOverrides_NullDeletes` | PATCH `{"tool":null}` with marker → delete | RFC7396 |
| | `TestMergeAnnotationOverrides_PreserveOnNilPatch` | nil patch → preserve | Merge semantics |
| | `TestMergeAnnotationOverrides_PerHintWins` | base `destructive:true` + patch `readOnly:true` → both, exact wins over `*` | Merge priority |
| `internal/upstream/core/client_test.go` (new) | `TestEffectiveAnnotations_WildcardThenExact` | upstream `destructive:true`, overrides `{"*":{destructive:false}, "act":{destructive:true}}` → `act:true`, `navigate:false` | Priority |
| | `TestEffectiveAnnotations_NilUpstream` | upstream nil + `* {readOnly:true}` → readOnly true | Nil→inherit |
| `internal/contracts/intent_test.go` | `TestDeriveCallWith_Overridden` | overridden `readOnly:true,destructive:false` → `read` not `destructive` | Derive priority |
| `internal/server/mcp_annotations_test.go` | `TestClassifyToolRisk_Overridden` | overridden `openWorld:false,destructive:false,readOnly:true` → low not lethal | Classify |
| `internal/toolannotations/toolannotations_test.go` | `TestExcludeReason_Overridden` | `excludeDestructive` with overridden `destructive:false` → not excluded | Filter |
| `internal/httpapi/server_test.go` | `TestPatchServer_AnnotationOverrides_AdminOnly` | agent token PATCH → 403, admin → 200 | Auth gate |
| `internal/server/mcp_test.go` | `TestBuildPatchConfig_AnnotationOverrides_Marker` | MCP patch with null marker → delete | Parse |
| `internal/runtime/tool_quarantine_test.go` | `TestHash_UnchangedByOverrides` | same tool, different override → same hash | Hash stability |
| `frontend` | `annotation-overrides-editor.spec.ts` | 3-state select Inherit/Yes/No → correct PATCH payload | UI |

**Frontend unit:** `frontend/tests/unit/annotation-overrides-editor.spec.ts` — mount editor, assert wildcard row, per-hint selects, save emits `annotation_overrides`.

**E2E Playwright:** `e2e/playwright/annotation-overrides.spec.ts` — add `browseros` mock server, set `* {destructive:false}`, verify Tools page badge changes from `destructive` red → `read` green, `call_tool_read` succeeds where before `SERVER_MISMATCH`.

**LIVE:** `tests/live_annotation_overrides_test.go` (Go `httptest` + real Docker `browseros` at `127.0.0.1:9001`) — start core with override, list tools, assert effective `readOnly:true` for `snapshot`, `destructive:false` for `navigate`.

### 3.2 QUALITY GATES (7 — adapted to Go — CRITIC FIX: REAL PATHS + SKIP)

```
Gate 1: SYNTAX+LINT       `golangci-lint run --config .golangci.yml ./...` + `golangci-lint run --config .golangci.yml --build-tags server ./...` → 0 (critic: path is .golangci.yml not .github/.golangci.yml; need 2 runs per CLAUDE.md:103)
Gate 2: TYPE SAFETY       `go vet ./...` + `go test -race -tags server -timeout 20m -skip "E2E|Binary|MCPProtocol|TestInfoEndpoint|TestGracefulShutdownNoPanic|TestSocketInfoEndpoint" ./internal/server/... ./internal/httpapi/...` builds → 0 (critic: bare `go test ./internal/server` hangs)
Gate 3: UNIT TESTS        `go test -race ./internal/... -v -count=1 -timeout 20m` (+ frontend `npm run test` if exists) → 0 failed, coverage >=85% (target 93%); include persistence round-trip `config.Load → Save → Copy` and hot-reload no-restart test for overrides (restart_required:false vs trust_mode true:2681)
Gate 4: MUTATION          `go test -run TestMutation` boundary (100/101, Title "" vs omit, wildcard * vs tool *), bool (true/false/nil) → >=80% (document known 0)
Gate 5: LIVE TESTS        Real `modelcontextprotocol/server-everything` (scripts/run-e2e-tests.sh) OR docker `browseros` at 127.0.0.1:9001 if available → skip if 9001 down (critic: browseros not in CI fixture). Verify effective no restart → badge change without restart.
Gate 6: METRICS           `golangci-lint` cyclomatic ≤15, `frontend npm run lint` 0, `oas/swagger.yaml` valid
Gate 7: SECURITY          `govulncheck ./...` 0 HIGH/MED, `npm audit` 0 HIGH, `CheckServerWriteMasks` no `••••` leak
```
**Per STRATEGY_COMPLETE Part 5:** live tests mandatory for merge priority bug (mocks hide priority). Use `internal/*_test.go` not `tests/` (critic: Go live tests live in `internal/`, not `tests/`).

**Additional required tests (critic):** `loader_test.go` for `annotation_overrides` persistence, hot-reload without `restart_required`, `oas` generation, `Copy` deep-copy alias test, `Title ""` preserve vs clear with `*string` future.

---

## 4. 🔍 CRITIC CHECKLIST — HARD REVIEW (MUST PASS BEFORE MERGE)

- [ ] No `mcp.ToolAnnotation` (wrong type) vs `config.ToolAnnotations` confusion (`internal/server/mcp_direct_catalog.go:95` vs `Upstream`).
- [ ] `CopyServerConfig` deep copies `*bool` — pointer alias not shared.
- [ ] `ValidateDetailed` not `Validate` — write-gate correct, 101-entry limit, wildcard `*` not validated as tool name.
- [ ] `MergeAnnotationOverrides` respects `removeMarkers` for both whole-map null and per-tool null.
- [ ] `effectiveAnnotationsForTool` precedence `exact > * > upstream` per-hint, not whole-object replace.
- [ ] Hash NOT affected — `tool_quarantine.go:54` still ignores annotations.
- [ ] PATCH triple-write: `AddServerRequest` + `handlePatchServer` + `buildPatchConfigFromRequest` + `api.patchServer` all updated — GH#938 not repeated.
- [ ] Audit: activity diff + (optional) audit sink line for `annotation_override`, `request_id` correlated.
- [ ] Frontend tri-state is `select` not `toggle`, `data-test` kebab, badges reuse `AnnotationBadges` colors, no `restart_required` false positive (hot).
- [ ] No secret leak: `CheckServerWriteMasks` not needed for bool hints but new field not break masking for `env/headers`.
- [ ] No new dep: Go 1.26, Vue 3.5, existing `mcp-go`, `zap`, `BBolt` only — `CLAUDE.md` rule.
- [ ] BDD specs in `specs/108-annotation-overrides/spec.md` (if spec required) or inline — but per author, small feature may skip spec if tests+docs exist. Document decision.

---

## 5. 🚀 IMPLEMENTATION ORDER — AUTONOMOUS SUB-ORCHESTRATORS

```
MASTER (this plan) → SUB-ORCHESTRATOR per phase:
Phase A: Backend config+merge (Code Agent + Critic)
Phase B: Upstream effective resolver (Code + Test + Critic)
Phase C: API triple-write + validation (Code + Critic)
Phase D: Frontend card+editor (Code + Critic)
Phase E: Full test suite + gates + live browseros verify (Test + Debug)
Phase F: Docs + final cosmic verify (Critic)
FAIL at any gate → back to [3] (max 10 review rounds per PR per CLAUDE.md)
```

**DELEGATION RULE:** Each agent gets: (1) Goal, (2) Full context JSON (this file + STRATEGY_COMPLETE.md Part 0.5-5), (3) MSI part, (4) MCP tools (Context7 for Vue/Go docs, Tavily for patterns, browseros for live), (5) Report path `OTCHETY/report_phase_X.md`.

**LIVE VERIFY (Phase E):** Use `browseros:navigate` (destructive) + `snapshot` (readOnly) via mcpproxy at `127.0.0.1:12754` → open `http://127.0.0.1:12754/ui/#/servers/browseros` → Configuration tab → verify card renders, save `* {destructive:false}`, reload Tools tab → badges change, `call_tool_read browseros:snapshot` now succeeds without `SERVER_MISMATCH`.

---

## 6. 📦 COMMIT & PR CONVENTIONS (CRITIC FIX: DUAL BRANCH)

- **Local dev branch (user req):** `🚀-FEAT-ANNOTATION-OVERRIDES-🛡️-PER-SERVER-TOOL-EXCEPTIONS-✨` — CAPS+EMOJIS caps+emojis commits per user (`✨ FEAT: ...` etc) — stays local, never pushed to upstream workflows.
- **Upstream PR branch (author conv):** `feat/annotation-overrides-per-server-tool` — conventional commits lower (`feat: per-server per-tool annotation overrides`, `fix: merge priority exact>*`, `feat: ui card + editor`, `test: platinum coverage 93%`, `docs: config reference`) per `git log --oneline` `fix(scope):` `chore(deps):` and `CLAUDE.md` `gofmt`.
- For submission: `git checkout -b feat/annotation-overrides-per-server-tool && git cherry-pick --strategy=recursive` or `git format-patch` and re-commit with conventional messages.
- Hot-reload: no `roadmap.yaml` epic sweep — additive per-server feature.

---

## 7. ⚠️ RISKS & MITIGATIONS

- Wildcard `*` collides with real tool named `*` → `IsValidToolName` rejects `*` as tool name except as wildcard; tool name `*` invalid per MCP (no such tool).
- Inner `readOnlyHint:null` revert needs tool object replace — v1 limitation documented, not blocking (operator can delete whole tool override and re-add).
- Per-server map grows → 100 limit + truncate in UI.
- Browseros snapshot refs invalid after navigate — re-snapshot before `act` (browseros pattern `snapshot→act→diff`).

---

## 8. ✅ DONE CRITERIA (PLATINUM)

- [ ] `annotation_overrides` persisted, validated, merged, effective, hot, audited, no hash change.
- [ ] UI card after Trust mode, pencil in Tools, no restart badge, `data-test` green.
- [ ] `go test -race ./...` + `golangci-lint` + `frontend npm run build` + `e2e` + live browseros snapshot PASS.
- [ ] Critic APPROVED (no REJECTED issues).
- [ ] User arrives and only ACCEPTS — no debug needed.

---

*END OF PLAN — EVERY LINE VERIFIED vs REAL CODE AT HEAD 525d5021 — READ BY EVERY AGENT BEFORE WORK.*
