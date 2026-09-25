<template>
  <div data-test="paste-server">
    <label class="label">
      <span class="label-text font-semibold">Paste a URL, a command line, or a JSON/TOML config</span>
    </label>
    <textarea
      v-model="content"
      rows="4"
      class="textarea textarea-bordered w-full font-mono text-sm"
      placeholder="https://api.example.com/mcp&#10;or: npx -y @modelcontextprotocol/server-filesystem /tmp&#10;or: { &quot;mcpServers&quot;: { ... } }"
      data-test="paste-textarea"
    />

    <div v-if="loading" class="flex items-center gap-2 py-3 text-sm text-base-content/70" data-test="paste-loading">
      <span class="loading loading-spinner loading-xs" /> Detecting…
    </div>
    <div v-else-if="error" class="alert alert-error text-sm mt-3" data-test="paste-error">{{ error }}</div>

    <div v-else-if="preview" class="mt-4 space-y-3" data-test="paste-preview">
      <div class="flex items-center gap-2 flex-wrap">
        <span class="badge badge-outline" data-test="paste-format">{{ formatLabel }}</span>
        <span v-for="tag in preview.tags || []" :key="tag" class="badge badge-ghost" :data-test="`paste-tag-${tag}`">
          {{ tag }}
        </span>
      </div>
      <p class="text-sm font-mono text-base-content/80 break-all" data-test="paste-summary">{{ preview.summary }}</p>

      <div v-if="(preview.env?.length || 0) + (preview.headers?.length || 0) > 0" class="space-y-2">
        <SecretToggle
          v-for="f in envFields"
          :key="`env-${f.name}`"
          :name="f.name"
          kind="env"
          :model-value="values[`env:${f.name}`] || ''"
          :mode="modes[`env:${f.name}`] || (f.secret_like ? 'secret' : 'value')"
          :keyring-available="keyringAvailable"
          :keyring-reason="keyringReason"
          @update:model-value="(v) => (values[`env:${f.name}`] = v)"
          @update:mode="(m) => (modes[`env:${f.name}`] = m)"
        />
        <SecretToggle
          v-for="f in headerFields"
          :key="`header-${f.name}`"
          :name="f.name"
          kind="header"
          :model-value="values[`header:${f.name}`] || ''"
          :mode="modes[`header:${f.name}`] || (f.secret_like ? 'secret' : 'value')"
          :keyring-available="keyringAvailable"
          :keyring-reason="keyringReason"
          @update:model-value="(v) => (values[`header:${f.name}`] = v)"
          @update:mode="(m) => (modes[`header:${f.name}`] = m)"
        />
      </div>

      <div v-if="addError" class="alert alert-error text-sm" data-test="paste-add-error">{{ addError }}</div>

      <button type="button" class="btn btn-primary" :disabled="adding" data-test="paste-add-button" @click="handleAdd">
        {{ adding ? 'Adding…' : 'Add to MCPProxy' }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import api from '@/services/api'
import type { ImportedServer } from '@/types'
import SecretToggle from '@/components/SecretToggle.vue'
import { resolveSecretFields } from '@/composables/useSecretFields'
import { useServersStore } from '@/stores/servers'
import { serverDetailPath } from '@/utils/serverRoute'

const emit = defineEmits<{ added: [name: string] }>()

const content = ref('')
const loading = ref(false)
const error = ref<string | null>(null)
const preview = ref<ImportedServer | null>(null)
const format = ref('')
const values = reactive<Record<string, string>>({})
const modes = reactive<Record<string, 'value' | 'secret'>>({})
const addError = ref<string | null>(null)
const adding = ref(false)
const keyringAvailable = ref(true)
const keyringReason = ref('')

const router = useRouter()
const serversStore = useServersStore()

const formatLabel = computed(() => {
  switch (format.value) {
    case 'url':
      return 'Remote URL'
    case 'command':
      return 'Command line'
    default:
      return format.value || 'Config'
  }
})

const envFields = computed(() => preview.value?.env || [])
const headerFields = computed(() => preview.value?.headers || [])

let debounceTimer: ReturnType<typeof setTimeout> | null = null

async function runPreview() {
  const raw = content.value.trim()
  if (!raw) {
    preview.value = null
    error.value = null
    return
  }
  loading.value = true
  error.value = null
  try {
    const resp = await api.importServersFromJSON({ content: raw, preview: true })
    if (!resp.success || !resp.data || resp.data.imported.length === 0) {
      error.value = resp.error || 'Could not detect a server from this input'
      preview.value = null
      return
    }
    format.value = resp.data.format
    preview.value = resp.data.imported[0]
    for (const key of Object.keys(values)) delete values[key]
    for (const key of Object.keys(modes)) delete modes[key]
    for (const f of preview.value.env || []) modes[`env:${f.name}`] = f.secret_like ? 'secret' : 'value'
    for (const f of preview.value.headers || []) modes[`header:${f.name}`] = f.secret_like ? 'secret' : 'value'
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Preview failed'
    preview.value = null
  } finally {
    loading.value = false
  }
}

watch(content, () => {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(runPreview, 400)
})

onMounted(async () => {
  const secretsResp = await api.getConfigSecrets()
  if (secretsResp.success && secretsResp.data) {
    keyringAvailable.value = secretsResp.data.keyring_available
    keyringReason.value = secretsResp.data.keyring_reason || ''
  }
})

async function handleAdd() {
  if (!preview.value) return
  adding.value = true
  addError.value = null
  try {
    const p = preview.value
    const fields = [
      ...(p.env || []).map((f) => ({ kind: 'env' as const, name: f.name, value: values[`env:${f.name}`] || '', mode: modes[`env:${f.name}`] || 'value' })),
      ...(p.headers || []).map((f) => ({ kind: 'header' as const, name: f.name, value: values[`header:${f.name}`] || '', mode: modes[`header:${f.name}`] || 'value' })),
    ]
    const resolved = await resolveSecretFields(p.name, fields)

    const serverData: Record<string, unknown> = {
      operation: 'add',
      name: p.name,
      protocol: p.protocol,
      enabled: true,
    }
    if (p.url) {
      serverData.url = p.url
      if (Object.keys(resolved.headers).length > 0) serverData.headers_json = JSON.stringify(resolved.headers)
    } else if (p.command) {
      serverData.command = p.command
      if (p.args && p.args.length > 0) serverData.args_json = JSON.stringify(p.args)
      if (Object.keys(resolved.env).length > 0) serverData.env_json = JSON.stringify(resolved.env)
    }

    await serversStore.addServer(serverData)
    emit('added', p.name)
    void router.push(serverDetailPath(p.name))
  } catch (e) {
    addError.value = e instanceof Error ? e.message : 'Failed to add server'
  } finally {
    adding.value = false
  }
}
</script>
