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
import { resolveSecretFields, rollbackSecrets } from '@/composables/useSecretFields'
import { useServersStore } from '@/stores/servers'
import { serverDetailPath } from '@/utils/serverRoute'

const emit = defineEmits<{ added: [name: string] }>()

const content = ref('')
const loading = ref(false)
const error = ref<string | null>(null)
const preview = ref<ImportedServer | null>(null)
// The exact raw text that produced the current `preview` — captured
// separately from `content` (which keeps changing as the user types) so
// Add always re-parses the same input the preview was computed from, even
// if a debounced re-preview for newer text hasn't landed yet.
const previewRawContent = ref('')
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
    const resp = await api.importServersFromJSON({ content: raw, preview: true, allow_paste_fallback: true })
    if (!resp.success || !resp.data || resp.data.imported.length === 0) {
      error.value = resp.error || 'Could not detect a server from this input'
      preview.value = null
      return
    }
    format.value = resp.data.format
    preview.value = resp.data.imported[0]
    previewRawContent.value = raw
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
  // Populated only once resolveSecretFields has returned successfully, so
  // the catch block below never double-rolls-back refs that
  // resolveSecretFields already rolled back itself on a write failure.
  let writtenRefs: string[] = []
  try {
    const p = preview.value
    const fields = [
      ...(p.env || []).map((f) => ({ kind: 'env' as const, name: f.name, value: values[`env:${f.name}`] || '', mode: modes[`env:${f.name}`] || 'value' })),
      ...(p.headers || []).map((f) => ({ kind: 'header' as const, name: f.name, value: values[`header:${f.name}`] || '', mode: modes[`header:${f.name}`] || 'value' })),
    ]
    const resolved = await resolveSecretFields(p.name, fields)
    writtenRefs = resolved.writtenRefs

    // Apply (preview=false) against the ORIGINAL raw content, never against
    // `p.url`/`p.command`/`p.args` — those are the preview's redacted
    // values (a credential embedded directly in a URL query param or an
    // argv flag renders as `••••23 (16 chars)`), and baking that masked
    // placeholder into the real server config would leave it permanently
    // unable to connect with no way to recover the original secret. The
    // backend re-parses this same content server-side and adds the server
    // with the true, unredacted values; env_override/header_override carry
    // the user's SecretToggle edits (plain value or a keyring ref) across,
    // since those never appeared in the preview at all.
    // Deliberately no `format` hint here: detection is a pure function of
    // content, so re-detecting the identical previewRawContent reproduces
    // the exact same format preview already showed. (format.value from a
    // JSON/TOML preview can be e.g. "claude_desktop", which parseFormat
    // does not accept as a hint string — passing it back as a hint would
    // 400 the apply for those inputs. Re-detection avoids that mismatch
    // entirely.)
    const resp = await api.importServersFromJSON({
      content: previewRawContent.value,
      preview: false,
      server_names: [p.name],
      env_override: Object.keys(resolved.env).length > 0 ? resolved.env : undefined,
      header_override: Object.keys(resolved.headers).length > 0 ? resolved.headers : undefined,
      allow_paste_fallback: true,
    })
    if (!resp.success || !resp.data || resp.data.imported.length === 0) {
      throw new Error(resp.error || 'Failed to add server')
    }

    await serversStore.fetchServers()
    emit('added', p.name)
    void router.push(serverDetailPath(p.name))
  } catch (e) {
    // The secret write (if any) succeeded but something after it failed —
    // don't leave an orphaned keyring entry behind, and let a retry reuse
    // the same ref name instead of computing a new -2-suffixed one.
    if (writtenRefs.length > 0) await rollbackSecrets(writtenRefs)
    addError.value = e instanceof Error ? e.message : 'Failed to add server'
  } finally {
    adding.value = false
  }
}
</script>
