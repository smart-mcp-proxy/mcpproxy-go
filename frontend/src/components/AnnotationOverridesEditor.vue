<template>
  <div class="space-y-3" data-test="annotation-overrides-editor">
    <div class="overflow-x-auto">
      <table class="table table-sm">
        <thead>
          <tr>
            <th>Tool</th>
            <th>Effective</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          <!-- Wildcard row pinned top -->
          <tr data-test="annotation-override-row-*">
            <td>
              <span class="badge badge-outline badge-sm" title="Applies to all tools">* wildcard</span>
            </td>
            <td>
              <span class="inline-flex items-center gap-1 flex-wrap">
                <AnnotationBadges v-if="effectiveFor('*')" :annotations="effectiveFor('*')!" class="scale-90 origin-left" />
                <span v-else class="text-xs text-base-content/50">inherit (no override)</span>
                <span v-if="effectiveFor('*')?.destructiveHint === false" class="badge badge-sm badge-success" title="Operator override: explicitly marked non-destructive">✓ Non-destructive</span>
                <span v-if="effectiveFor('*')?.readOnlyHint === false" class="badge badge-sm badge-warning" title="Operator override: explicitly marked writable">✏️ Write</span>
                <span v-if="isSafeDraft('*')" class="badge badge-sm badge-warning" data-test="annotation-override-safedraft-*" title="Mark-safe preset drafted, not yet saved">Safe-draft (unsaved)</span>
              </span>
            </td>
            <td>
              <button
                class="btn btn-ghost btn-xs"
                :data-test="`annotation-override-edit-*`"
                @click="openEdit('*')"
              >
                {{ hasOverride('*') ? 'Edit' : 'Add' }}
              </button>
              <button
                v-if="hasOverride('*')"
                class="btn btn-ghost btn-xs text-error"
                data-test="annotation-override-delete-*"
                @click="removeOverride('*')"
              >
                Delete
              </button>
              <button
                class="btn btn-outline btn-xs"
                data-test="annotation-override-marksafe-*"
                title="Draft a read-only + non-destructive wildcard override (unsaved until Save overrides)"
                @click="openMarkAllModal"
              >
                Mark all read-only safe…
              </button>
            </td>
          </tr>
          <tr
            v-for="toolName in sortedToolNames"
            :key="toolName"
            :data-test="`annotation-override-row-${toolName}`"
          >
            <td class="font-mono text-xs break-all">{{ toolName }}</td>
            <td>
              <span class="inline-flex items-center gap-1 flex-wrap">
                <AnnotationBadges v-if="effectiveFor(toolName)" :annotations="effectiveFor(toolName)!" class="scale-90 origin-left" />
                <span v-else class="text-xs text-base-content/50">—</span>
                <span v-if="effectiveFor(toolName)?.destructiveHint === false" class="badge badge-sm badge-success" title="Operator override: explicitly marked non-destructive">✓ Non-destructive</span>
                <span v-if="effectiveFor(toolName)?.readOnlyHint === false" class="badge badge-sm badge-warning" title="Operator override: explicitly marked writable">✏️ Write</span>
                <span v-if="isSafeDraft(toolName)" class="badge badge-sm badge-warning" :data-test="`annotation-override-safedraft-${toolName}`" title="Mark-safe preset drafted, not yet saved">Safe-draft (unsaved)</span>
                <span v-if="needsOpenWorldWarning(toolName)" class="badge badge-sm badge-warning" :data-test="`annotation-override-openworld-warn-${toolName}`" title="Upstream openWorldHint is not false, so the read-only tool filter may still exclude this tool">⚠ Open-world: may stay hidden from read-only filter</span>
              </span>
            </td>
            <td class="flex gap-1">
              <button
                class="btn btn-ghost btn-xs"
                :data-test="`annotation-override-edit-${toolName}`"
                @click="openEdit(toolName)"
              >
                {{ hasOverride(toolName) ? 'Edit' : 'Add' }}
              </button>
              <button
                v-if="effectiveFor(toolName)?.readOnlyHint !== true"
                class="btn btn-ghost btn-xs"
                :data-test="`annotation-override-marksafe-${toolName}`"
                title="Draft a read-only + non-destructive override for this tool (unsaved until Save overrides)"
                @click="markSafe(toolName)"
              >
                ✓ Mark safe
              </button>
              <button
                v-if="hasOverride(toolName)"
                class="btn btn-ghost btn-xs text-error"
                :data-test="`annotation-override-delete-${toolName}`"
                @click="removeOverride(toolName)"
              >
                Delete
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Add new tool override -->
    <div class="flex items-center gap-2">
      <select v-model="newToolName" class="select select-bordered select-sm" data-test="annotation-overrides-add-select">
        <option value="">Select tool to override…</option>
        <option v-for="t in availableToolsForAdd" :key="t" :value="t">{{ t }}</option>
      </select>
      <button class="btn btn-sm btn-outline" :disabled="!newToolName" data-test="annotation-overrides-add" @click="openEdit(newToolName)">Add override</button>
    </div>

    <!-- Popover for editing a single tool's hints -->
    <div v-if="editingTool !== null" class="card bg-base-200 p-4 space-y-3" data-test="annotation-override-popover">
      <h4 class="font-semibold text-sm">
        Override for <code class="bg-base-300 px-1 rounded">{{ editingTool }}</code>
      </h4>
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <label v-for="h in hintKeys" :key="h" class="form-control">
          <span class="label-text text-xs">{{ h }}</span>
          <select
            class="select select-bordered select-sm"
            :value="draftSelectValue(h)"
            :data-test="`annotation-override-select-${editingTool}-${h}`"
            @change="onSelectChange(h, ($event.target as HTMLSelectElement).value)"
          >
            <option value="inherit">Inherit</option>
            <option value="true">true</option>
            <option value="false">false</option>
          </select>
        </label>
      </div>
      <div class="form-control">
        <span class="label-text text-xs">title (optional)</span>
        <input
          v-model="draftTitle"
          type="text"
          placeholder="Override title or leave empty to inherit"
          class="input input-bordered input-sm"
          :data-test="`annotation-override-select-${editingTool}-title`"
        />
      </div>
      <div class="text-xs text-base-content/60">
        Effective preview:
        <AnnotationBadges v-if="effectivePreview" :annotations="effectivePreview" class="inline-flex ml-1 scale-90" />
        <span v-else class="ml-1">— (no hints)</span>
      </div>
      <div class="flex gap-2">
        <button class="btn btn-primary btn-sm" data-test="annotation-overrides-save" @click="applyEdit">Apply</button>
        <button class="btn btn-ghost btn-sm" data-test="annotation-overrides-cancel" @click="cancelEdit">Cancel</button>
      </div>
    </div>

    <!-- Mark-all confirm modal (inline card, same pattern as the popover above) -->
    <div v-if="showMarkAllModal" class="card bg-base-200 p-4 space-y-3" data-test="annotation-override-marksafe-modal">
      <h4 class="font-semibold text-sm">Mark all tools read-only safe?</h4>
      <p class="text-xs text-base-content/70">
        {{ markAllImpact.become }}/{{ markAllImpact.total }} tools become read-visible<span v-if="markAllImpact.conditionalOpen.length > 0">; {{ markAllImpact.conditionalOpen.length }} read-visible BUT network-unverified (⚠ nil openWorldHint: {{ markAllImpact.conditionalOpen.slice(0, 5).join(', ') }}<span v-if="markAllImpact.conditionalOpen.length > 5"> + {{ markAllImpact.conditionalOpen.length - 5 }} more</span>)</span>;
        {{ markAllImpact.blockedOpen.length }} tools still excluded by exclude_open_world<span v-if="markAllImpact.blockedOpen.length > 0"> ({{ markAllImpact.blockedOpen.slice(0, 5).join(', ') }}<span v-if="markAllImpact.blockedOpen.length > 5"> + {{ markAllImpact.blockedOpen.length - 5 }} more</span>)</span>.
        This only writes a draft — nothing is saved until Save overrides. Open-world hints are never changed.
      </p>
      <label class="form-control">
        <span class="label-text text-xs">Reason</span>
        <select
          v-model="markAllReason"
          class="select select-bordered select-sm"
          data-test="annotation-override-marksafe-reason"
        >
          <option value="">Select reason…</option>
          <option v-for="r in markSafeReasons" :key="r" :value="r">{{ r }}</option>
        </select>
      </label>
      <label class="flex items-center gap-2 text-xs cursor-pointer">
        <input
          v-model="markAllAck"
          type="checkbox"
          class="checkbox checkbox-sm"
          data-test="annotation-override-marksafe-ack"
        />
        <span>I understand this drafts a read-only + non-destructive wildcard override for every tool.</span>
      </label>
      <div class="flex gap-2">
        <button
          class="btn btn-primary btn-sm"
          data-test="annotation-override-marksafe-confirm"
          :disabled="!markAllAck || !markAllReason"
          @click="confirmMarkAll"
        >Confirm draft</button>
        <button class="btn btn-ghost btn-sm" data-test="annotation-override-marksafe-cancel" @click="cancelMarkAll">Cancel</button>
      </div>
    </div>

    <!-- Save all -->
    <div class="flex gap-2 items-center">
      <button class="btn btn-primary btn-sm" data-test="annotation-overrides-save-all" :disabled="saving" @click="saveAll">
        <span v-if="saving" class="loading loading-spinner loading-xs"></span>
        Save overrides
      </button>
      <button class="btn btn-ghost btn-sm" :disabled="saving" @click="resetLocal">Reset</button>
      <span v-if="unsavedCount > 0" class="badge badge-warning badge-sm" data-test="annotation-overrides-unsaved-count">{{ unsavedCount }} unsaved</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import type { Tool, ToolAnnotation } from '@/types'
import AnnotationBadges from '@/components/AnnotationBadges.vue'

type HintKey = 'readOnlyHint' | 'destructiveHint' | 'idempotentHint' | 'openWorldHint'
const hintKeys: HintKey[] = ['readOnlyHint', 'destructiveHint', 'idempotentHint', 'openWorldHint']

const props = defineProps<{
  serverName: string
  tools: Tool[]
  overrides: Record<string, ToolAnnotation | null>
  upstreamAnnotations: Record<string, ToolAnnotation | null | undefined>
}>()

const emit = defineEmits<{
  (e: 'save', overrides: Record<string, ToolAnnotation | null>): void
}>()

const localOverrides = ref<Record<string, ToolAnnotation>>({})
const pendingDeletes = ref<Set<string>>(new Set())
const editingTool = ref<string | null>(null)
const draft = reactive<Record<HintKey, boolean | undefined>>({
  readOnlyHint: undefined,
  destructiveHint: undefined,
  idempotentHint: undefined,
  openWorldHint: undefined,
})
const draftTitle = ref('')
const newToolName = ref('')
const saving = ref(false)
// Tools whose current local draft was created by a Mark-safe preset and not
// yet saved or hand-edited. Drives the amber Safe-draft badge only; the
// draft itself lives in localOverrides like any popover Apply.
// Reactivity note (explicit): Vue tracks Set identity on ref(), so a bare
// .add/.delete on markSafeDrafts.value alone does not reliably re-render.
// All mutators below go through addSafeDraft/clearSafeDraft, which replace
// the Set instance (new Set(old) +/- entry). Covered by unit test
// "Safe-draft badge tracks explicit Set replacement".
const markSafeDrafts = ref<Set<string>>(new Set())

function addSafeDraft(tool: string) {
  if (markSafeDrafts.value.has(tool)) return
  markSafeDrafts.value = new Set(markSafeDrafts.value).add(tool)
}

function clearSafeDraft(tool: string) {
  if (!markSafeDrafts.value.has(tool)) return
  const next = new Set(markSafeDrafts.value)
  next.delete(tool)
  markSafeDrafts.value = next
}
// Wildcard Mark-all confirm modal state (draft-preset, never instant-apply).
const showMarkAllModal = ref(false)
const markAllAck = ref(false)
const markAllReason = ref('')
const markSafeReasons = ['False positive', 'Vendor attestation', 'Local-only verified', 'Other']

// sync props -> local
function syncFromProps() {
  const next: Record<string, ToolAnnotation> = {}
  for (const [k, v] of Object.entries(props.overrides || {})) {
    if (v) next[k] = { ...v }
  }
  localOverrides.value = next
  pendingDeletes.value = new Set()
  markSafeDrafts.value = new Set()
}
watch(() => props.overrides, syncFromProps, { immediate: true, deep: true })

function hasOverride(tool: string): boolean {
  return tool in localOverrides.value
}

const sortedToolNames = computed(() => {
  const names = new Set<string>()
  for (const t of props.tools) names.add(t.name)
  for (const k of Object.keys(localOverrides.value)) if (k !== '*') names.add(k)
  return Array.from(names).sort((a, b) => a.localeCompare(b))
})

const availableToolsForAdd = computed(() => {
  return sortedToolNames.value.filter(n => !(n in localOverrides.value))
})

// mimic Go EffectiveAnnotationsForTool
function effectiveFor(toolName: string): ToolAnnotation | null {
  const upstream = props.upstreamAnnotations?.[toolName] as ToolAnnotation | null | undefined
  const wild = localOverrides.value['*'] ?? null
  const exact = localOverrides.value[toolName] ?? null
  if (!wild && !exact) return upstream ?? null
  const base: ToolAnnotation = { ...(upstream || {}) } as ToolAnnotation
  // ensure deep copy of hints already in base (they are primitives)
  for (const ov of [wild, exact]) {
    if (!ov) continue
    if (ov.title) base.title = ov.title
    if (ov.readOnlyHint !== undefined) base.readOnlyHint = ov.readOnlyHint
    if (ov.destructiveHint !== undefined) base.destructiveHint = ov.destructiveHint
    if (ov.idempotentHint !== undefined) base.idempotentHint = ov.idempotentHint
    if (ov.openWorldHint !== undefined) base.openWorldHint = ov.openWorldHint
  }
  if (!base.title && base.readOnlyHint === undefined && base.destructiveHint === undefined && base.idempotentHint === undefined && base.openWorldHint === undefined) return null
  return base
}

const effectivePreview = computed<ToolAnnotation | null>(() => {
  if (editingTool.value === null) return null
  const upstream = props.upstreamAnnotations?.[editingTool.value] as ToolAnnotation | null | undefined
  const wild = editingTool.value === '*' ? null : (localOverrides.value['*'] ?? null)
  const draftAnn: ToolAnnotation = {}
  if (draftTitle.value) draftAnn.title = draftTitle.value
  for (const k of hintKeys) {
    const v = draft[k]
    if (v !== undefined) (draftAnn as Record<string, unknown>)[k] = v
  }
  // compose effective as if draft were the exact override
  const base: ToolAnnotation = { ...(upstream || {}) } as ToolAnnotation
  for (const ov of [wild, draftAnn]) {
    if (!ov) continue
    if ((ov as ToolAnnotation).title) base.title = (ov as ToolAnnotation).title
    for (const k of hintKeys) {
      const v = (ov as Record<string, unknown>)[k] as boolean | undefined
      if (v !== undefined) (base as Record<string, unknown>)[k] = v
    }
  }
  if (!base.title && base.readOnlyHint === undefined && base.destructiveHint === undefined && base.idempotentHint === undefined && base.openWorldHint === undefined) return null
  return base
})

function draftSelectValue(h: HintKey): string {
  const v = draft[h]
  if (v === undefined) return 'inherit'
  return v ? 'true' : 'false'
}

function onSelectChange(h: HintKey, val: string) {
  if (val === 'inherit') draft[h] = undefined
  else draft[h] = val === 'true'
}

function openEdit(tool: string) {
  if (!tool) return
  editingTool.value = tool
  const existing = localOverrides.value[tool]
  draftTitle.value = existing?.title || ''
  for (const k of hintKeys) {
    const v = existing?.[k]
    draft[k] = v === undefined ? undefined : Boolean(v)
  }
  newToolName.value = ''
}

function cancelEdit() {
  editingTool.value = null
}

function applyEdit() {
  if (editingTool.value === null) return
  const tool = editingTool.value
  const ann: ToolAnnotation = {}
  let hasAny = false
  if (draftTitle.value) { ann.title = draftTitle.value; hasAny = true }
  for (const k of hintKeys) {
    const v = draft[k]
    if (v !== undefined) { (ann as Record<string, unknown>)[k] = v; hasAny = true }
  }
  if (!hasAny) {
    // empty override is invalid per backend validation; treat as delete
    delete localOverrides.value[tool]
    pendingDeletes.value.add(tool)
  } else {
    localOverrides.value[tool] = ann
    pendingDeletes.value.delete(tool)
  }
  // A hand-applied popover edit supersedes any preset draft for this tool.
  clearSafeDraft(tool)
  // trigger reactivity
  localOverrides.value = { ...localOverrides.value }
  editingTool.value = null
}

function removeOverride(tool: string) {
  if (tool in localOverrides.value) {
    delete localOverrides.value[tool]
    localOverrides.value = { ...localOverrides.value }
  }
  pendingDeletes.value.add(tool)
  clearSafeDraft(tool)
  if (editingTool.value === tool) editingTool.value = null
}

function resetLocal() {
  syncFromProps()
  editingTool.value = null
  cancelMarkAll()
}

// Mark-safe preset (variant A: draft-only, client-side). Writes an exact-tool
// draft of {readOnlyHint:true, destructiveHint:false} into localOverrides —
// the same shape as a popover Apply — and clears any pending delete, so the
// existing Save-all/Reset/Delete/popover paths pick it up with no new save
// path. Bulk-JSON (ServerDetail rawJson/applyRawJson) is NOT integrated: it
// PATCHes textarea content (saved state only) and bypasses localOverrides,
// so an unsaved preset draft is orphaned by Apply-JSON until Save+refetch.
// ServerDetail guards Apply-JSON with a confirm while hasUnsavedDrafts().
// openWorldHint is never set by the preset: it stays inherit (or keeps a
// previously hand-set explicit value), because a wildcard
// destructiveHint:false does not make openWorld:true tools read-visible.
// Per-tool drafts stamp title "[mark-safe: single-tool]" so the audit trail
// matches the wildcard "[mark-safe: <reason>]" format.
function markSafe(tool: string) {
  if (!tool) return
  const existing = localOverrides.value[tool]
  const ann: ToolAnnotation = { ...(existing || {}) }
  ann.readOnlyHint = true
  ann.destructiveHint = false
  ann.title = withMarkSafeSuffix(existing?.title, 'single-tool')
  localOverrides.value = { ...localOverrides.value, [tool]: ann }
  pendingDeletes.value.delete(tool)
  addSafeDraft(tool)
  if (editingTool.value === tool) editingTool.value = null
}

function isSafeDraft(tool: string): boolean {
  return markSafeDrafts.value.has(tool) && tool in localOverrides.value
}

// Inline warning: effective read-only but effective openWorldHint is not
// explicitly false, so the read_only_only / exclude_open_world filters may
// still hide this tool. Shown for preset and hand-made drafts alike. Uses
// effectiveFor (upstream + wildcard draft + exact draft) so a hand-set
// openWorld:false on either level clears the badge.
function needsOpenWorldWarning(tool: string): boolean {
  if (tool === '*') return false
  const eff = effectiveFor(tool)
  if (eff?.readOnlyHint !== true) return false
  return eff?.openWorldHint !== false
}

// Wildcard preset impact, computed from props (tools + upstream) and existing
// drafts (exact + wildcard): after a *:{readOnly:true, destructive:false}
// draft, a tool becomes read-visible only when its effective openWorldHint
// is explicitly false. Nil/undefined openWorld is NOT a success — it is
// network-unverified (backend default decides), matching the row ⚠ badge
// (needsOpenWorldWarning). Blocked = effective openWorld true (still
// excluded by exclude_open_world).
const markAllImpact = computed(() => {
  const names = sortedToolNames.value
  let become = 0
  const conditionalOpen: string[] = []
  const blockedOpen: string[] = []
  for (const n of names) {
    const exact = localOverrides.value[n] as ToolAnnotation | undefined
    const wild = localOverrides.value['*'] as ToolAnnotation | undefined
    const up = props.upstreamAnnotations?.[n] as ToolAnnotation | null | undefined
    const ow = exact?.openWorldHint ?? wild?.openWorldHint ?? up?.openWorldHint
    if (ow === true) {
      blockedOpen.push(n)
      continue
    }
    const ro = exact?.readOnlyHint ?? true
    if (ro !== true) continue
    if (ow === false) become++
    else conditionalOpen.push(n)
  }
  return { become, conditionalOpen, total: names.length, blockedOpen }
})

function openMarkAllModal() {
  showMarkAllModal.value = true
  markAllAck.value = false
  markAllReason.value = ''
}

function cancelMarkAll() {
  showMarkAllModal.value = false
  markAllAck.value = false
  markAllReason.value = ''
}

function withMarkSafeSuffix(title: string | undefined, reason: string): string {
  const suffix = `[mark-safe: ${reason}]`
  const base = (title || '').replace(/\s*\[mark-safe:[^\]]*\]\s*/g, '').trim()
  return base ? `${base} ${suffix}` : suffix
}

function confirmMarkAll() {
  if (!markAllAck.value || !markAllReason.value) return
  const existing = localOverrides.value['*']
  const ann: ToolAnnotation = { ...(existing || {}) }
  ann.readOnlyHint = true
  ann.destructiveHint = false
  // Reason travels in the draft title so it survives into audit without backend change.
  ann.title = withMarkSafeSuffix(existing?.title, markAllReason.value)
  localOverrides.value = { ...localOverrides.value, '*': ann }
  pendingDeletes.value.delete('*')
  addSafeDraft('*')
  cancelMarkAll()
}

function sameAnn(a: ToolAnnotation | null | undefined, b: ToolAnnotation | null | undefined): boolean {
  const na = a ?? null
  const nb = b ?? null
  if (!na && !nb) return true
  if (!na || !nb) return false
  return na.title === nb.title
    && na.readOnlyHint === nb.readOnlyHint
    && na.destructiveHint === nb.destructiveHint
    && na.idempotentHint === nb.idempotentHint
    && na.openWorldHint === nb.openWorldHint
}

// Unsaved-draft counter near Save-all: local drafts differing from saved
// props plus deletes of saved keys.
const unsavedCount = computed(() => {
  let n = 0
  const saved = props.overrides || {}
  for (const [k, v] of Object.entries(localOverrides.value)) {
    if (!sameAnn(v, saved[k] ?? null)) n++
  }
  for (const k of pendingDeletes.value) {
    if (saved[k]) n++
  }
  return n
})

function saveAll() {
  saving.value = true
  const payload: Record<string, ToolAnnotation | null> = {}
  for (const [k, v] of Object.entries(localOverrides.value)) payload[k] = v
  for (const k of pendingDeletes.value) {
    if (!(k in payload)) payload[k] = null
  }
  emit('save', payload)
  // parent will handle async and refresh; reset saving after emit (parent may control)
  setTimeout(() => (saving.value = false), 800)
}

// Shortcut-button predicate shared with the ServerDetail Tools-tab shortcut:
// hidden exactly when the effective (upstream + wildcard + exact drafts)
// annotation is read-visible. ServerDetail reuses this via the exposed
// method so both buttons compute visibility from the same effective source.
function isMarkSafeVisible(tool: string): boolean {
  return effectiveFor(tool)?.readOnlyHint !== true
}

// Dirty-check for the Bulk-JSON guard in ServerDetail: true while any
// unsaved preset/popover draft exists that Apply-JSON would orphan.
function hasUnsavedDrafts(): boolean {
  return unsavedCount.value > 0
}

defineExpose({ openEdit, markSafe, effectiveFor, isMarkSafeVisible, confirmMarkAll, cancelMarkAll, hasUnsavedDrafts, unsavedCount })
</script>
