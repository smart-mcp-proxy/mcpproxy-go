<template>
  <form data-test="manual-server-form" @submit.prevent="handleSubmit">
    <div class="form-control mb-4">
      <label class="label" :for="ids.name"><span class="label-text font-semibold">Name</span></label>
      <input :id="ids.name" v-model="name" type="text" class="input input-bordered" required autocomplete="off" data-test="manual-name-input" />
    </div>

    <fieldset class="form-control mb-4">
      <legend class="label"><span class="label-text font-semibold">Server Type</span></legend>
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
    </fieldset>

    <template v-if="protocol === 'http'">
      <div class="form-control mb-4">
        <label class="label" :for="ids.url"><span class="label-text font-semibold">URL</span></label>
        <input :id="ids.url" v-model="url" type="url" class="input input-bordered" placeholder="https://api.example.com/mcp" required data-test="manual-url-input" />
      </div>
      <div class="mb-4 space-y-2" role="group" :aria-labelledby="ids.headers">
        <span :id="ids.headers" class="label"><span class="label-text font-semibold">Headers</span></span>
        <div v-for="(h, i) in headerRows" :key="h.id" class="flex gap-2 items-start">
          <input v-model="h.name" type="text" class="input input-bordered input-sm flex-1" placeholder="Header name" :aria-label="`Header name ${i + 1}`" :data-test="`manual-header-name-${i}`" />
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
          <button type="button" class="btn btn-ghost btn-sm" :aria-label="`Remove header ${i + 1}`" data-test="manual-header-remove" @click="headerRows.splice(i, 1)">✕</button>
        </div>
        <button type="button" class="btn btn-ghost btn-sm" data-test="manual-header-add" @click="headerRows.push(newRow())">
          + Add header
        </button>
      </div>
    </template>

    <template v-else>
      <div class="form-control mb-4">
        <label class="label" :for="ids.command"><span class="label-text font-semibold">Command</span></label>
        <input :id="ids.command" v-model="command" type="text" class="input input-bordered" placeholder="npx" required data-test="manual-command-input" />
      </div>
      <div class="form-control mb-4">
        <label class="label" :for="ids.args"><span class="label-text font-semibold">Arguments (space-separated)</span></label>
        <input :id="ids.args" v-model="argsText" type="text" class="input input-bordered" placeholder="-y @modelcontextprotocol/server-filesystem /tmp" data-test="manual-args-input" />
      </div>
      <div class="mb-4 space-y-2" role="group" :aria-labelledby="ids.env">
        <span :id="ids.env" class="label"><span class="label-text font-semibold">Environment variables</span></span>
        <div v-for="(e, i) in envRows" :key="e.id" class="flex gap-2 items-start">
          <input v-model="e.name" type="text" class="input input-bordered input-sm flex-1" placeholder="VAR_NAME" :aria-label="`Environment variable name ${i + 1}`" :data-test="`manual-env-name-${i}`" />
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
          <button type="button" class="btn btn-ghost btn-sm" :aria-label="`Remove environment variable ${i + 1}`" data-test="manual-env-remove" @click="envRows.splice(i, 1)">✕</button>
        </div>
        <button type="button" class="btn btn-ghost btn-sm" data-test="manual-env-add" @click="envRows.push(newRow())">
          + Add variable
        </button>
      </div>
    </template>

    <div v-if="props.allowTrustModeSelection" class="form-control mb-4 pt-2" role="group" :aria-labelledby="ids.trust" data-test="manual-trust-mode">
      <div class="label">
        <span :id="ids.trust" class="label-text font-semibold">Trust mode</span>
        <span class="label-text-alt">Decides quarantine on add and tool-change approval</span>
      </div>
      <TrustModeSelector
        v-model="trustMode"
        name="manual-server-trust-mode"
        @confirmation-pending="trustModeConfirmationPending = $event"
      />
    </div>

    <div v-if="error" ref="errorEl" tabindex="-1" role="alert" class="alert alert-error text-sm mb-4" data-test="manual-error">{{ error }}</div>

    <button
      type="submit"
      class="btn btn-primary"
      :disabled="submitting || trustModeConfirmationPending"
      data-test="manual-submit"
    >
      {{ submitting ? 'Adding…' : 'Add to MCPProxy' }}
    </button>
  </form>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, nextTick, useId } from 'vue'
import { useRouter } from 'vue-router'
import api from '@/services/api'
import SecretToggle from '@/components/SecretToggle.vue'
import TrustModeSelector from '@/components/TrustModeSelector.vue'
import { resolveSecretFields, rollbackSecrets } from '@/composables/useSecretFields'
import { useServersStore } from '@/stores/servers'
import { serverDetailPath } from '@/utils/serverRoute'
import type { TrustMode } from '@/utils/trustMode'

const props = withDefaults(defineProps<{
  navigateAfterAdd?: boolean
  allowTrustModeSelection?: boolean
}>(), {
  navigateAfterAdd: true,
  allowTrustModeSelection: true,
})
const emit = defineEmits<{ added: [name: string] }>()

const router = useRouter()
const serversStore = useServersStore()

// Per-instance ids tie each visible label to its input (several forms can be
// mounted at once, e.g. the onboarding wizard beside this page).
const uid = useId()
const ids = {
  name: `${uid}-name`, url: `${uid}-url`, command: `${uid}-command`, args: `${uid}-args`,
  headers: `${uid}-headers`, env: `${uid}-env`, trust: `${uid}-trust`,
}
const errorEl = ref<HTMLElement | null>(null)

const name = ref('')
const protocol = ref<'stdio' | 'http'>('stdio')
const trustMode = ref<TrustMode>('manual')
const trustModeConfirmationPending = ref(false)
const url = ref('')
const command = ref('')
const argsText = ref('')
// Each row carries a stable `id` so :key survives removal of an earlier row;
// keying by index would hand a removed row's SecretToggle (and its revealed
// state) to the next row (review F3.1).
type KVRow = { id: number; name: string; value: string; mode: 'value' | 'secret' }
let rowSeq = 0
const newRow = (): KVRow => ({ id: ++rowSeq, name: '', value: '', mode: 'value' })
const headerRows = reactive<KVRow[]>([])
const envRows = reactive<KVRow[]>([])
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
  if (trustModeConfirmationPending.value) return
  error.value = null
  submitting.value = true
  // Populated only once resolveSecretFields has returned successfully, so
  // the catch block below never double-rolls-back refs that
  // resolveSecretFields already rolled back itself on a write failure.
  let writtenRefs: string[] = []
  try {
    const fields = [
      ...envRows.filter((r) => r.name.trim()).map((r) => ({ kind: 'env' as const, name: r.name.trim(), value: r.value, mode: r.mode })),
      ...headerRows.filter((r) => r.name.trim()).map((r) => ({ kind: 'header' as const, name: r.name.trim(), value: r.value, mode: r.mode })),
    ]
    const resolved = await resolveSecretFields(name.value, fields)
    writtenRefs = resolved.writtenRefs

    const serverData: Record<string, unknown> = {
      operation: 'add',
      name: name.value,
      protocol: protocol.value,
      enabled: true,
      trust_mode: trustMode.value,
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
    if (props.navigateAfterAdd) {
      void router.push(serverDetailPath(name.value))
    }
  } catch (e) {
    // The secret write (if any) succeeded but something after it failed —
    // don't leave an orphaned keyring entry behind, and let a retry reuse
    // the same ref name instead of computing a new -2-suffixed one.
    if (writtenRefs.length > 0) await rollbackSecrets(writtenRefs)
    error.value = e instanceof Error ? e.message : 'Failed to add server'
    // Move focus to the failure so keyboard and screen-reader users hear it.
    await nextTick()
    errorEl.value?.focus()
  } finally {
    submitting.value = false
  }
}
</script>
