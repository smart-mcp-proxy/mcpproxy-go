<template>
  <div class="space-y-3" :data-test="`client-binding-controls-${client.id}`" @click.stop>
    <dl class="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-1 text-sm">
      <div class="flex gap-2"><dt class="opacity-70">Credential</dt><dd>{{ credentialLabel(client.credential_state) }}<template v-if="client.token_name"> (<code class="text-xs">{{ client.token_name }}</code>)</template></dd></div>
      <div v-if="client.expires_at" class="flex gap-2"><dt class="opacity-70">Expires</dt><dd :data-test="`client-expires-${client.id}`">{{ formatDateTimeShort(client.expires_at) }}</dd></div>
      <div v-if="client.profile" class="flex gap-2"><dt class="opacity-70">Profile</dt><dd>{{ profiles.titleFor(client.profile) }} <span class="opacity-70">({{ client.profile_mode === 'locked' ? 'locked' : 'switchable' }})</span></dd></div>
    </dl>

    <div v-if="client.rotation_pending" class="alert alert-info text-sm flex flex-wrap items-center gap-2" :data-test="`client-rotation-pending-${client.id}`" role="status">
      <span class="badge badge-sm badge-info">Rotation pending</span>
      <span class="flex-1">Both secrets work for 24 h.</span>
      <button type="button" class="btn btn-xs" :disabled="bindings.busy[client.id]" :data-test="`client-finalize-${client.id}`" @click="finalize">Finalize now</button>
    </div>

    <div class="flex flex-wrap gap-2">
      <button type="button" class="btn btn-xs btn-outline" :data-test="`client-explain-${client.id}`" @click="explainOpen = true">Explain access&hellip;</button>
      <button v-if="client.credential_state === 'client'" type="button" class="btn btn-xs btn-outline" :data-test="`client-rotate-${client.id}`" @click="startRotate">Rotate credential&hellip;</button>
      <button v-if="hasRecord" type="button" class="btn btn-xs btn-outline btn-error" :data-test="`client-forget-${client.id}`" @click="forgetOpen = true">Forget client&hellip;</button>
    </div>
    <p v-if="actionResult" role="status" aria-live="polite" class="text-sm" :data-test="`client-action-result-${client.id}`">{{ actionResult }}</p>
    <p v-if="actionError" role="alert" class="text-sm text-error" :data-test="`client-action-error-${client.id}`">{{ actionError }}</p>

    <AccessExplainer :open="explainOpen" :subject="{ kind: 'client', name: client.id }" @close="explainOpen = false" />
    <ForgetClientDialog :open="forgetOpen" :client="client" @close="forgetOpen = false" @done="emit('changed')" />

    <BaseDialog :open="rotateOpen" :title="`Rotate ${client.display_name} credential`" test-id="rotate-client-dialog" @close="closeRotate">
      <template v-if="isCustom">
        <p class="text-sm">A new credential is issued and shown once. The old one keeps working for 24 hours, or until you finalize the rotation.</p>
      </template>
      <template v-else>
        <p v-if="rotatePreview" class="text-sm">The client's config is rewritten with a new credential. <code class="text-xs break-all">{{ rotatePreview.display_path || rotatePreview.config_path }}</code></p>
        <pre v-if="rotatePreview" class="text-[11px] font-mono whitespace-pre-wrap break-all rounded bg-base-300/60 border-l-2 border-success px-2 py-1.5" data-test="rotate-preview-entry">{{ rotatePreview.entry_text }}</pre>
        <span v-else-if="!rotateError" class="loading loading-spinner loading-sm" />
      </template>
      <div v-if="rotateError" role="alert" class="alert alert-error text-sm" data-test="rotate-error">{{ rotateError }}</div>
      <template #actions>
        <button type="button" class="btn btn-ghost btn-sm" @click="closeRotate">Cancel</button>
        <button type="button" class="btn btn-primary btn-sm" :disabled="rotateBusy || (!isCustom && !rotatePreview)" data-test="rotate-confirm" @click="confirmRotate">
          <span v-if="rotateBusy" class="loading loading-spinner loading-xs" />
          Rotate
        </button>
      </template>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import BaseDialog from '@/components/BaseDialog.vue'
import AccessExplainer from '@/components/AccessExplainer.vue'
import ForgetClientDialog from '@/components/clients/ForgetClientDialog.vue'
import api from '@/services/api'
import { useProfilesStore } from '@/stores/profiles'
import { useClientBindingsStore } from '@/stores/clientBindings'
import { credentialLabel, describeError } from '@/utils/profiles'
import { formatDateTimeShort } from '@/utils/datetime'
import type { ClientPresence, ConnectPreview } from '@/types/api'

// Spec 108-i I6/I13: the credential controls of an expanded Clients row -
// credential details, Explain access, staged Rotate, Finalize and Forget.
const props = defineProps<{ client: ClientPresence }>()
const emit = defineEmits<{
  (e: 'changed'): void
  (e: 'credential', payload: { credential: string; snippet?: { generic_http: string; header_name: string } | null; title: string }): void
}>()
const profiles = useProfilesStore()
const bindings = useClientBindingsStore()

const explainOpen = ref(false)
const forgetOpen = ref(false)
const rotateOpen = ref(false)
const rotatePreview = ref<ConnectPreview | null>(null)
const rotateError = ref('')
const rotateBusy = ref(false)
const actionResult = ref('')
const actionError = ref('')

const isCustom = computed(() => props.client.kind === 'custom')
const hasRecord = computed(() => Boolean(props.client.token_name) || ['client', 'revoked', 'expired'].includes(props.client.credential_state ?? ''))

async function startRotate() {
  rotateOpen.value = true
  rotateError.value = ''
  rotatePreview.value = null
  actionResult.value = ''
  actionError.value = ''
  if (isCustom.value) return
  // A supported client shows the change first; the preview's precondition token
  // then binds the rotate call, so a config edited in between is refused.
  try {
    const response = await api.getConnectPreview(props.client.id)
    if (response.success && response.data) rotatePreview.value = response.data
    else rotateError.value = response.error || 'Failed to load the preview'
  } catch (err) {
    rotateError.value = describeError(err)
  }
}

function closeRotate() {
  rotateOpen.value = false
  rotatePreview.value = null
}

async function confirmRotate() {
  rotateBusy.value = true
  rotateError.value = ''
  try {
    const result = await api.rotateClient(props.client.id, isCustom.value ? {} : { precondition_token: rotatePreview.value?.precondition_token })
    closeRotate()
    if (isCustom.value && result.credential) {
      emit('credential', { credential: result.credential, snippet: result.snippet ?? null, title: `New credential for ${props.client.display_name}` })
    } else if (result.rotation?.state === 'rolled_back') {
      actionResult.value = 'Rolled back (the client config was not changed).'
    } else {
      actionResult.value = 'Rotation finalized.'
    }
    emit('changed')
  } catch (err) {
    rotateError.value = describeError(err)
  } finally {
    rotateBusy.value = false
  }
}

async function finalize() {
  actionError.value = ''
  actionResult.value = ''
  if (await bindings.finalizeRotation(props.client.id)) {
    actionResult.value = 'Rotation finalized.'
    emit('changed')
  } else {
    actionError.value = describeError(bindings.rowErrors[props.client.id])
  }
}
</script>
