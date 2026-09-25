<template>
  <div data-test="import-servers-panel">
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
      <div v-for="(s, i) in preview.imported" :key="s.name" class="flex items-center gap-2 py-1">
        <input v-model="selected[s.name]" type="checkbox" class="checkbox checkbox-sm" :data-test="`import-select-${i}`" />
        <span class="font-mono text-sm">{{ s.name }}</span>
        <span class="text-xs text-base-content/60">{{ s.protocol }}</span>
      </div>
      <div v-if="addError" class="alert alert-error text-sm mt-3" data-test="import-add-error">{{ addError }}</div>
      <button type="button" class="btn btn-primary mt-3" :disabled="importing || selectedCount === 0" data-test="import-confirm-button" @click="handleImport">
        {{ importing ? 'Importing…' : `Import ${selectedCount} server(s)` }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import api, { type CanonicalConfigPath } from '@/services/api'
import type { ImportResponse } from '@/types'

const emit = defineEmits<{ imported: [count: number] }>()

const content = ref('')
const loading = ref(false)
const error = ref<string | null>(null)
const preview = ref<ImportResponse | null>(null)
const selected = reactive<Record<string, boolean>>({})
const addError = ref<string | null>(null)
const importing = ref(false)
const canonicalPaths = ref<CanonicalConfigPath[]>([])
let activePath = ''

const selectedCount = computed(() => Object.values(selected).filter(Boolean).length)

onMounted(async () => {
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
    emit('imported', names.length)
  } catch (e) {
    addError.value = e instanceof Error ? e.message : 'Import failed'
  } finally {
    importing.value = false
  }
}
</script>
