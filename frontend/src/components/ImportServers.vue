<template>
  <div data-test="import-servers-panel">
    <template v-if="detected">
      <div v-if="detectedLoading" class="flex justify-center py-4"><span class="loading loading-spinner loading-md" /></div>
      <div v-else-if="detectedSources.length" class="border border-base-300 rounded-lg overflow-hidden max-h-[32vh] overflow-y-auto" data-test="detected-import-sources">
        <div v-for="source in detectedSources" :key="source.path" :data-test="`import-section-${source.format}`" class="border-b border-base-300 last:border-b-0">
          <label class="flex items-center gap-3 px-3 py-2 bg-base-200/50 cursor-pointer">
            <input v-model="source.all" type="checkbox" class="checkbox checkbox-sm" :data-test="`select-all-${source.format}`" @change="toggleSource(source)" />
            <span class="min-w-0"><span class="font-medium text-sm">{{ source.name }}</span><span class="block text-[11px] opacity-50 font-mono truncate">{{ source.path }}</span></span>
          </label>
          <label v-for="server in source.servers" :key="server.name" class="flex items-start gap-3 pl-10 pr-3 py-2 hover:bg-base-200/40 cursor-pointer">
            <input v-model="source.selected[server.name]" type="checkbox" class="checkbox checkbox-sm mt-0.5" :data-test="`server-checkbox-${source.format}-${server.name}`" @change="syncAll(source)" />
            <span class="min-w-0"><span class="text-sm block">{{ server.name }}</span><span :data-test="`import-summary-${source.format}-${server.name}`" class="text-[11px] opacity-50 font-mono block truncate">{{ server.summary }}</span>
              <span v-if="server.tags?.length" class="mt-1 flex flex-wrap gap-1">
                <span v-for="tag in server.tags" :key="tag" :data-test="`import-tag-${source.format}-${server.name}-${tag.replaceAll(' ', '-')}`" class="badge badge-xs" :class="tag === 'needs secret' ? 'badge-warning' : 'badge-ghost'">{{ tag }}</span>
              </span>
            </span>
            <span v-if="detectedRename(source, server.name)" class="badge badge-warning badge-sm font-normal">→ {{ detectedRename(source, server.name) }}</span>
          </label>
        </div>
      </div>
      <div v-else-if="showEmpty" class="text-sm opacity-70" data-test="detected-import-empty">{{ importedOnce ? 'Nothing left to import — every server in your client configs is on MCPProxy.' : 'No importable servers found in local client configs.' }}</div>
      <div v-if="detectedError" class="alert alert-error text-sm mt-3">{{ detectedError }}</div>
      <p v-if="detectedMessage && showMessage" class="text-sm mt-3" :class="detectedImportedCount > 0 ? 'text-success' : ''" data-test="detected-import-message">{{ detectedImportedCount > 0 ? '✓ ' : '' }}{{ detectedMessage }}</p>
      <p v-if="detectedSelectedCount" class="text-xs mt-2" data-test="detected-selection-summary">
        <span class="font-semibold">{{ detectedSelectedCount }}</span> selected
        <span v-if="detectedRenames.size" class="text-warning"> · {{ detectedRenames.size }} renamed</span>
      </p>
      <label v-if="detectedSources.length" class="label cursor-pointer justify-start gap-2 mt-2">
        <input :checked="detectedQuarantine" type="checkbox" class="checkbox checkbox-sm" data-test="footer-quarantine-checkbox" @change="toggleDetectedQuarantine" />
        <span class="label-text text-sm">Quarantine imported servers for review</span>
      </label>
      <button v-if="detectedSources.length" type="button" class="btn btn-primary btn-sm mt-3" :disabled="detectedImporting || detectedSelectedCount === 0" data-test="bulk-import-primary" @click="importDetected">
        {{ detectedImporting ? 'Importing…' : `Import ${detectedSelectedCount} server${detectedSelectedCount === 1 ? '' : 's'}` }}
      </button>
    </template>
    <template v-else>
    <div v-if="canonicalPaths.length > 0" class="mb-4">
      <label class="label"><span class="label-text font-semibold">Quick import</span></label>
      <div class="flex flex-wrap gap-2">
        <button
          v-for="p in canonicalPaths"
          :key="p.path"
          type="button"
          class="btn btn-sm"
          :class="p.exists ? 'btn-outline' : 'btn-disabled'"
          :disabled="!p.exists"
          :data-test="`import-canonical-${p.format}`"
          @click="importFromPath(p.path)"
        >
          {{ p.name }}
        </button>
      </div>
    </div>

    <label class="label"><span class="label-text font-semibold">Or paste a config file's content</span></label>
    <textarea
      v-model="content"
      rows="6"
      class="textarea textarea-bordered w-full font-mono text-sm"
      placeholder='{ "mcpServers": { ... } }'
      data-test="import-content-textarea"
    />
    <div class="mt-2">
      <button type="button" class="btn btn-sm" :disabled="loading || !content.trim()" data-test="import-preview-button" @click="() => runPreview()">
        Preview
      </button>
    </div>

    <div v-if="loading" class="py-3 text-sm text-base-content/70" data-test="import-loading">Detecting servers…</div>
    <div v-else-if="error" class="alert alert-error text-sm mt-3" data-test="import-error">{{ error }}</div>

    <div v-else-if="preview" class="mt-4" data-test="import-preview">
      <p class="text-sm text-base-content/70 mb-2">{{ preview.format_name }} — {{ preview.imported.length }} server(s) detected</p>
      <div v-for="(s, i) in preview.imported" :key="s.name" class="flex items-start gap-2 py-1">
        <input v-model="selected[s.name]" type="checkbox" class="checkbox checkbox-sm" :data-test="`import-select-${i}`" />
        <span class="min-w-0">
          <span class="font-mono text-sm">{{ s.name }}</span>
          <span class="text-xs text-base-content/60 ml-2">{{ s.protocol }}</span>
          <span v-if="s.summary" :data-test="`import-summary-preview-${s.name}`" class="text-[11px] opacity-50 font-mono block truncate">{{ s.summary }}</span>
          <span v-if="s.tags?.length" class="mt-1 flex flex-wrap gap-1">
            <span v-for="tag in s.tags" :key="tag" :data-test="`import-tag-preview-${s.name}-${tag.replaceAll(' ', '-')}`" class="badge badge-xs" :class="tag === 'needs secret' ? 'badge-warning' : 'badge-ghost'">{{ tag }}</span>
          </span>
        </span>
      </div>
      <div v-if="addError" class="alert alert-error text-sm mt-3" data-test="import-add-error">{{ addError }}</div>
      <button type="button" class="btn btn-primary mt-3" :disabled="importing || selectedCount === 0" data-test="import-confirm-button" @click="handleImport">
        {{ importing ? 'Importing…' : `Import ${selectedCount} server(s)` }}
      </button>
    </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import api, { type CanonicalConfigPath } from '@/services/api'
import type { ImportResponse, ImportedServer } from '@/types'
import { importSummary } from '@/utils/onboardingServersStep'

// showEmpty: the wizard owns its own empty and completion states, so it turns
// this one off; standalone use keeps the first-load empty line.
const props = withDefaults(defineProps<{ detected?: boolean; showEmpty?: boolean; showMessage?: boolean }>(), { detected: false, showEmpty: true, showMessage: true })
const emit = defineEmits<{ imported: [count: number, names?: string[]] }>()

const content = ref('')
const loading = ref(false)
const error = ref<string | null>(null)
const preview = ref<ImportResponse | null>(null)
const selected = reactive<Record<string, boolean>>({})
const addError = ref<string | null>(null)
const importing = ref(false)
const canonicalPaths = ref<CanonicalConfigPath[]>([])
let activePath = ''

type DetectedSource = { name: string; format: string; path: string; servers: ImportedServer[]; selected: Record<string, boolean>; all: boolean }
const detectedSources = ref<DetectedSource[]>([])
const detectedLoading = ref(false)
const detectedImporting = ref(false)
const detectedError = ref<string | null>(null)
const detectedMessage = ref('')
const detectedImportedCount = ref(0)
// True once an import completed in this panel, so an empty reload reads
// "nothing left" instead of "nothing found".
const importedOnce = ref(false)
const detectedQuarantine = ref(true)
const detectedSelectedCount = computed(() => detectedSources.value.reduce((n, source) => n + Object.values(source.selected).filter(Boolean).length, 0))
const detectedRenames = computed(() => {
  const pathsByName = new Map<string, DetectedSource[]>()
  for (const source of detectedSources.value) for (const server of source.servers) {
    if (!source.selected[server.name]) continue
    const sources = pathsByName.get(server.name) ?? []
    sources.push(source); pathsByName.set(server.name, sources)
  }
  const names = new Map<string, string>()
  for (const [name, sources] of pathsByName) if (sources.length > 1) {
    for (const source of sources) names.set(`${source.path}::${name}`, `${name}_${source.format.replace(/-/g, '_')}`)
  }
  return names
})

function syncAll(source: DetectedSource) { source.all = source.servers.length > 0 && source.servers.every(server => source.selected[server.name]) }
function toggleSource(source: DetectedSource) { for (const server of source.servers) source.selected[server.name] = source.all }
function detectedRename(source: DetectedSource, name: string) { return detectedRenames.value.get(`${source.path}::${name}`) }
function toggleDetectedQuarantine(event: Event) {
  const target = event.target as HTMLInputElement
  const checked = target.checked
  if (!checked && !window.confirm('Import without quarantine? Servers become active immediately.')) {
    // `checked` is a one-way binding so a cancelled browser event needs an
    // explicit DOM restore as well as retaining the reactive safe default.
    target.checked = true
    return
  }
  detectedQuarantine.value = checked
}

async function loadDetectedSources(clearMessage = true) {
  detectedLoading.value = true
  detectedError.value = null
  if (clearMessage) { detectedMessage.value = ''; detectedImportedCount.value = 0 }
  try {
    const paths = await api.getCanonicalConfigPaths()
    if (!paths.success || !paths.data) return
    const sources = await Promise.all(paths.data.paths.filter(path => path.exists).map(async path => {
      const response = await api.importServersFromPath({ path: path.path, format: path.format, preview: true })
      const servers = response.success && response.data ? response.data.imported ?? [] : []
      return { name: path.name, format: path.format, path: path.path, servers, selected: Object.fromEntries(servers.map(server => [server.name, false])), all: false }
    }))
    detectedSources.value = sources.filter(source => source.servers.length > 0)
  } catch (error) { detectedError.value = error instanceof Error ? error.message : 'Could not discover client configs' }
  finally { detectedLoading.value = false }
}

// The names the core actually imported, so a server skipped as already_exists
// (possibly still quarantined from an earlier run) is never reported as
// "just imported". An old core that returns no list falls back to the request.
function actuallyImported(data: ImportResponse | undefined, requested: string[], rename: Record<string, string> = {}): string[] {
  if (Array.isArray(data?.imported) && data.imported.length > 0) return data.imported.map(server => server.name)
  const skipped = new Set([...(data?.skipped ?? []), ...(data?.failed ?? [])].map(item => item.name))
  return requested.map(name => rename[name] ?? name).filter((name, i) => !skipped.has(name) && !skipped.has(requested[i]))
}

async function importDetected() {
  detectedImporting.value = true
  detectedError.value = null
  try {
    let imported = 0
    let renamed = 0
    const importedNames: string[] = []
    const skipped: Array<{ reason?: string }> = []
    for (const source of detectedSources.value) {
      const server_names = source.servers.filter(server => source.selected[server.name]).map(server => server.name)
      if (!server_names.length) continue
      const rename = Object.fromEntries(server_names.flatMap(name => {
        const target = detectedRename(source, name)
        return target ? [[name, target]] : []
      }))
      renamed += Object.keys(rename).length
      const response = await api.importServersFromPath({ path: source.path, format: source.format, server_names, rename: Object.keys(rename).length ? rename : undefined, skip_quarantine: !detectedQuarantine.value })
      if (!response.success) throw new Error(response.error || `Could not import ${source.name}`)
      imported += response.data?.summary?.imported ?? server_names.length
      importedNames.push(...actuallyImported(response.data, server_names, rename as Record<string, string>))
      skipped.push(...(response.data?.skipped ?? []))
    }
    detectedMessage.value = importSummary({ imported, renamed, skipped })
    detectedImportedCount.value = imported
    importedOnce.value = true
    emit('imported', imported, importedNames)
    await loadDetectedSources(false)
  } catch (error) { detectedError.value = error instanceof Error ? error.message : 'Import failed' }
  finally { detectedImporting.value = false }
}

const selectedCount = computed(() => Object.values(selected).filter(Boolean).length)

onMounted(async () => {
  if (props.detected) { await loadDetectedSources(); return }
  const resp = await api.getCanonicalConfigPaths()
  if (resp.success && resp.data) canonicalPaths.value = resp.data.paths
})

async function runPreview() {
  if (!content.value.trim()) return
  loading.value = true
  error.value = null
  activePath = ''
  try {
    const resp = await api.importServersFromJSON({ content: content.value, preview: true })
    if (!resp.success || !resp.data) {
      error.value = resp.error || 'Preview failed'
      preview.value = null
      return
    }
    preview.value = resp.data
    for (const key of Object.keys(selected)) delete selected[key]
    for (const s of resp.data.imported) selected[s.name] = true
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Preview failed'
  } finally {
    loading.value = false
  }
}

async function importFromPath(path: string) {
  loading.value = true
  error.value = null
  activePath = path
  try {
    const resp = await api.importServersFromPath({ path, preview: true })
    if (!resp.success || !resp.data) {
      error.value = resp.error || 'Preview failed'
      preview.value = null
      return
    }
    preview.value = resp.data
    for (const key of Object.keys(selected)) delete selected[key]
    for (const s of resp.data.imported) selected[s.name] = true
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Preview failed'
  } finally {
    loading.value = false
  }
}

async function handleImport() {
  if (!preview.value) return
  importing.value = true
  addError.value = null
  try {
    const names = Object.entries(selected).filter(([, v]) => v).map(([k]) => k)
    const resp = activePath
      ? await api.importServersFromPath({ path: activePath, preview: false, server_names: names })
      : await api.importServersFromJSON({ content: content.value, preview: false, server_names: names })
    if (!resp.success) {
      addError.value = resp.error || 'Import failed'
      return
    }
    emit('imported', resp.data?.summary?.imported ?? resp.data?.imported?.length ?? names.length, actuallyImported(resp.data, names))
  } catch (e) {
    addError.value = e instanceof Error ? e.message : 'Import failed'
  } finally {
    importing.value = false
  }
}
</script>
