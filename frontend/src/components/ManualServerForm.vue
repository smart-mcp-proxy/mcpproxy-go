<template>
  <form data-test="manual-server-form" @submit.prevent="handleSubmit">
    <div class="form-control mb-4">
      <label class="label"><span class="label-text font-semibold">Name</span></label>
      <input v-model="name" type="text" class="input input-bordered" required data-test="manual-name-input" />
    </div>

    <div class="form-control mb-4">
      <label class="label"><span class="label-text font-semibold">Server Type</span></label>
      <div class="flex gap-4">
        <label class="flex items-center gap-2 cursor-pointer">
          <input v-model="protocol" type="radio" value="stdio" class="radio radio-primary" data-test="manual-type-stdio" />
          <span>stdio (Local Command)</span>
        </label>
        <label class="flex items-center gap-2 cursor-pointer">
          <input v-model="protocol" type="radio" value="http" class="radio radio-primary" data-test="manual-type-http" />
          <span>HTTP/HTTPS (Remote)</span>
        </label>
      </div>
    </div>

    <template v-if="protocol === 'http'">
      <div class="form-control mb-4">
        <label class="label"><span class="label-text font-semibold">URL</span></label>
        <input v-model="url" type="url" class="input input-bordered" placeholder="https://api.example.com/mcp" required data-test="manual-url-input" />
      </div>
      <div class="mb-4 space-y-2">
        <label class="label"><span class="label-text font-semibold">Headers</span></label>
        <div v-for="(h, i) in headerRows" :key="i" class="flex gap-2 items-start">
          <input v-model="h.name" type="text" class="input input-bordered input-sm flex-1" placeholder="Header name" :data-test="`manual-header-name-${i}`" />
          <SecretToggle
            class="flex-[2]"
            :name="h.name || `header-${i}`"
            kind="header"
            :model-value="h.value"
            :mode="h.mode"
            :keyring-available="keyringAvailable"
            :keyring-reason="keyringReason"
            @update:model-value="(v) => (h.value = v)"
            @update:mode="(m) => (h.mode = m)"
          />
          <button type="button" class="btn btn-ghost btn-sm" data-test="manual-header-remove" @click="headerRows.splice(i, 1)">✕</button>
        </div>
        <button type="button" class="btn btn-ghost btn-sm" data-test="manual-header-add" @click="headerRows.push({ name: '', value: '', mode: 'value' })">
          + Add header
        </button>
      </div>
    </template>

    <template v-else>
      <div class="form-control mb-4">
        <label class="label"><span class="label-text font-semibold">Command</span></label>
        <input v-model="command" type="text" class="input input-bordered" placeholder="npx" required data-test="manual-command-input" />
      </div>
      <div class="form-control mb-4">
        <label class="label"><span class="label-text font-semibold">Arguments (space-separated)</span></label>
        <input v-model="argsText" type="text" class="input input-bordered" placeholder="-y @modelcontextprotocol/server-filesystem /tmp" data-test="manual-args-input" />
      </div>
      <div class="mb-4 space-y-2">
        <label class="label"><span class="label-text font-semibold">Environment variables</span></label>
        <div v-for="(e, i) in envRows" :key="i" class="flex gap-2 items-start">
          <input v-model="e.name" type="text" class="input input-bordered input-sm flex-1" placeholder="VAR_NAME" :data-test="`manual-env-name-${i}`" />
          <SecretToggle
            class="flex-[2]"
            :name="e.name || `env-${i}`"
            kind="env"
            :model-value="e.value"
            :mode="e.mode"
            :keyring-available="keyringAvailable"
            :keyring-reason="keyringReason"
            @update:model-value="(v) => (e.value = v)"
            @update:mode="(m) => (e.mode = m)"
          />
          <button type="button" class="btn btn-ghost btn-sm" data-test="manual-env-remove" @click="envRows.splice(i, 1)">✕</button>
        </div>
        <button type="button" class="btn btn-ghost btn-sm" data-test="manual-env-add" @click="envRows.push({ name: '', value: '', mode: 'value' })">
          + Add variable
        </button>
      </div>
    </template>

    <div v-if="error" class="alert alert-error text-sm mb-4" data-test="manual-error">{{ error }}</div>

    <button type="submit" class="btn btn-primary" :disabled="submitting" data-test="manual-submit">
      {{ submitting ? 'Adding…' : 'Add to MCPProxy' }}
    </button>
  </form>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import api from '@/services/api'
import SecretToggle from '@/components/SecretToggle.vue'
import { resolveSecretFields } from '@/composables/useSecretFields'
import { useServersStore } from '@/stores/servers'
import { serverDetailPath } from '@/utils/serverRoute'

const emit = defineEmits<{ added: [name: string] }>()

const router = useRouter()
const serversStore = useServersStore()

const name = ref('')
const protocol = ref<'stdio' | 'http'>('stdio')
const url = ref('')
const command = ref('')
const argsText = ref('')
const headerRows = reactive<{ name: string; value: string; mode: 'value' | 'secret' }[]>([])
const envRows = reactive<{ name: string; value: string; mode: 'value' | 'secret' }[]>([])
const error = ref<string | null>(null)
const submitting = ref(false)
const keyringAvailable = ref(true)
const keyringReason = ref('')

onMounted(async () => {
  const resp = await api.getConfigSecrets()
  if (resp.success && resp.data) {
    keyringAvailable.value = resp.data.keyring_available
    keyringReason.value = resp.data.keyring_reason || ''
  }
})

function parseArgs(): string[] {
  return argsText.value.trim() === '' ? [] : argsText.value.trim().split(/\s+/)
}

async function handleSubmit() {
  error.value = null
  submitting.value = true
  try {
    const fields = [
      ...envRows.filter((r) => r.name.trim()).map((r) => ({ kind: 'env' as const, name: r.name.trim(), value: r.value, mode: r.mode })),
      ...headerRows.filter((r) => r.name.trim()).map((r) => ({ kind: 'header' as const, name: r.name.trim(), value: r.value, mode: r.mode })),
    ]
    const resolved = await resolveSecretFields(name.value, fields)

    const serverData: Record<string, unknown> = {
      operation: 'add',
      name: name.value,
      protocol: protocol.value,
      enabled: true,
    }
    if (protocol.value === 'http') {
      serverData.url = url.value
      if (Object.keys(resolved.headers).length > 0) serverData.headers_json = JSON.stringify(resolved.headers)
    } else {
      serverData.command = command.value
      const args = parseArgs()
      if (args.length > 0) serverData.args_json = JSON.stringify(args)
      if (Object.keys(resolved.env).length > 0) serverData.env_json = JSON.stringify(resolved.env)
    }

    await serversStore.addServer(serverData)
    emit('added', name.value)
    void router.push(serverDetailPath(name.value))
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to add server'
  } finally {
    submitting.value = false
  }
}
</script>
