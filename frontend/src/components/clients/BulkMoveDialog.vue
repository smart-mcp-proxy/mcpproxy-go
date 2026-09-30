<template>
  <BaseDialog :open="open" title="Move clients" test-id="bulk-move-dialog" @close="emit('close')">
    <form v-if="!result" class="space-y-3" @submit.prevent="submit">
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <div class="form-control">
          <label class="label" for="bulk-from"><span class="label-text font-medium">From</span></label>
          <select id="bulk-from" v-model="from" class="select select-bordered select-sm w-full" data-test="bulk-from">
            <option value="">All servers</option>
            <option v-for="profile in profiles.profiles" :key="profile.name" :value="profile.name">{{ profile.title || profile.name }}</option>
          </select>
        </div>
        <div class="form-control">
          <label class="label" for="bulk-to"><span class="label-text font-medium">To</span></label>
          <select id="bulk-to" v-model="to" class="select select-bordered select-sm w-full" data-test="bulk-to">
            <option value="">All servers</option>
            <option v-for="profile in profiles.profiles" :key="profile.name" :value="profile.name">{{ profile.title || profile.name }}</option>
          </select>
        </div>
      </div>
      <fieldset class="form-control">
        <legend class="label-text font-medium mb-1">Mode</legend>
        <label class="label cursor-pointer justify-start gap-2"><input v-model="mode" type="radio" class="radio radio-sm" value="" data-test="bulk-mode-keep" /><span class="label-text">Keep each client's mode</span></label>
        <label class="label cursor-pointer justify-start gap-2"><input v-model="mode" type="radio" class="radio radio-sm" value="locked" :disabled="!to" data-test="bulk-mode-locked" /><span class="label-text">Lock</span></label>
        <label class="label cursor-pointer justify-start gap-2"><input v-model="mode" type="radio" class="radio radio-sm" value="switchable" data-test="bulk-mode-switchable" /><span class="label-text">Switchable</span></label>
      </fieldset>
      <p class="text-sm" data-test="bulk-preview-line" aria-live="polite">{{ previewLine }}</p>
      <div v-if="error" role="alert" class="alert alert-error text-sm" data-test="bulk-error">{{ error }}</div>
      <div class="modal-action">
        <button type="button" class="btn btn-ghost btn-sm" @click="emit('close')">Cancel</button>
        <button type="submit" class="btn btn-primary btn-sm" :disabled="busy || from === to || affected.length === 0" data-test="bulk-submit">
          <span v-if="busy" class="loading loading-spinner loading-xs" />
          Move {{ affected.length }} client{{ affected.length === 1 ? '' : 's' }}
        </button>
      </div>
    </form>
    <div v-else class="space-y-3" role="status" aria-live="polite" data-test="bulk-result">
      <p class="text-sm">
        <strong>{{ result.moved.length }}</strong> moved<template v-if="result.moved.length">: {{ result.moved.join(', ') }}</template>.
      </p>
      <div v-if="result.skipped.length" class="space-y-2">
        <p class="text-sm font-medium">Skipped</p>
        <ul class="space-y-2" data-test="bulk-skipped">
          <li v-for="item in result.skipped" :key="item.client_id" class="text-sm" :data-test="`bulk-skipped-${item.client_id}`">
            <span class="font-medium">{{ item.client_id }}</span>:
            <GuardRefusal v-if="item.code === 'binding_bypassable_without_auth'" class="mt-1" :refusal="{ error: item.error, fixes: [{ kind: 'require_mcp_auth' }] }" @navigate="emit('close')" />
            <span v-else>{{ skipText(item) }}</span>
          </li>
        </ul>
      </div>
      <div class="modal-action"><button type="button" class="btn btn-primary btn-sm" data-test="bulk-close" @click="emit('close')">Close</button></div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import BaseDialog from '@/components/BaseDialog.vue'
import GuardRefusal from '@/components/GuardRefusal.vue'
import api from '@/services/api'
import { useProfilesStore } from '@/stores/profiles'
import { describeError } from '@/utils/profiles'
import type { BulkAssignResponse, ClientPresence } from '@/types/api'

// Spec 108-i I14: "Move all clients using X to Y". The guard and the credential
// precondition apply per client on the server; a client it refuses is listed
// under Skipped with the same code the single operation returns.
const props = defineProps<{ open: boolean; clients: ClientPresence[]; initialFrom?: string }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'done'): void }>()
const profiles = useProfilesStore()
const from = ref('')
const to = ref('')
const mode = ref<'' | 'locked' | 'switchable'>('')
const busy = ref(false)
const error = ref('')
const result = ref<BulkAssignResponse | null>(null)

watch(() => props.open, open => {
  if (!open) return
  from.value = props.initialFrom ?? ''
  to.value = ''
  mode.value = ''
  busy.value = false
  error.value = ''
  result.value = null
  if (!profiles.loaded) void profiles.fetchProfiles()
})

// A switch to All servers can only be switchable.
watch(to, value => { if (!value && mode.value === 'locked') mode.value = '' })

const affected = computed(() => props.clients.filter(client => client.credential_state === 'client' && (client.profile ?? '') === from.value))
const previewLine = computed(() => {
  const name = from.value ? profiles.titleFor(from.value) : 'All servers'
  const n = affected.value.length
  return `${n} client${n === 1 ? ' uses' : 's use'} ${name}`
})

function skipText(item: { code: string; error?: string }): string {
  if (item.code === 'no_client_credential') return 'no client credential'
  return item.error || item.code.replaceAll('_', ' ')
}

async function submit() {
  busy.value = true
  error.value = ''
  try {
    const body: Parameters<typeof api.bulkAssignClients>[0] = { from_profile: from.value, to_profile: to.value }
    if (mode.value) body.mode = mode.value
    result.value = await api.bulkAssignClients(body)
    emit('done')
  } catch (err) {
    error.value = describeError(err)
  } finally {
    busy.value = false
  }
}
</script>
