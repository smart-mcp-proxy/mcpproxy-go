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

    <!-- Save all -->
    <div class="flex gap-2">
      <button class="btn btn-primary btn-sm" data-test="annotation-overrides-save-all" :disabled="saving" @click="saveAll">
        <span v-if="saving" class="loading loading-spinner loading-xs"></span>
        Save overrides
      </button>
      <button class="btn btn-ghost btn-sm" :disabled="saving" @click="resetLocal">Reset</button>
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

// sync props -> local
function syncFromProps() {
  const next: Record<string, ToolAnnotation> = {}
  for (const [k, v] of Object.entries(props.overrides || {})) {
    if (v) next[k] = { ...v }
  }
  localOverrides.value = next
  pendingDeletes.value = new Set()
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
  if (editingTool.value === tool) editingTool.value = null
}

function resetLocal() {
  syncFromProps()
  editingTool.value = null
}

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

defineExpose({ openEdit })
</script>
