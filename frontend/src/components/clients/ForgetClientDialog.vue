<template>
  <BaseDialog :open="open" :title="`Forget ${client.display_name}?`" test-id="forget-client-dialog" @close="close">
    <template v-if="!result">
      <p class="text-sm">
        This revokes the client credential <code>{{ client.token_name || `client-${client.id}` }}</code>. The client can no longer call MCPProxy until you connect it again.
      </p>
      <label v-if="client.kind === 'supported'" class="label cursor-pointer justify-start gap-2">
        <input v-model="disconnect" type="checkbox" class="checkbox checkbox-sm" data-test="forget-disconnect" />
        <span class="label-text">Also remove MCPProxy from {{ client.display_name }}'s config</span>
      </label>
      <div v-if="error" role="alert" class="alert alert-error text-sm" data-test="forget-error">{{ error }}</div>
    </template>
    <div v-else role="status" aria-live="polite" class="alert text-sm" :class="result.disconnect_error ? 'alert-warning' : 'alert-success'" data-test="forget-result">
      <span v-if="result.disconnect_error">Credential revoked; the config entry could not be removed: {{ result.disconnect_error }}</span>
      <span v-else-if="result.disconnected">Credential revoked and MCPProxy removed from {{ client.display_name }}'s config.</span>
      <span v-else>Credential revoked.</span>
    </div>
    <template #actions>
      <template v-if="!result">
        <button type="button" class="btn btn-ghost btn-sm" @click="close">Cancel</button>
        <button type="button" class="btn btn-error btn-sm" :disabled="busy" data-test="forget-confirm" @click="confirm">
          <span v-if="busy" class="loading loading-spinner loading-xs" />
          Forget client
        </button>
      </template>
      <button v-else type="button" class="btn btn-primary btn-sm" data-test="forget-close" @click="close">Close</button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import BaseDialog from '@/components/BaseDialog.vue'
import api from '@/services/api'
import { describeError } from '@/utils/profiles'
import type { ClientPresence, ForgetClientResponse } from '@/types/api'

// Spec 108-i I13: forgetting revokes the credential. For a supported client the
// config entry can be removed in the same step; the revocation never waits for
// that file write, so a failure to edit the file is reported beside a success.
const props = defineProps<{ open: boolean; client: ClientPresence }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'done'): void }>()
const disconnect = ref(true)
const busy = ref(false)
const error = ref('')
const result = ref<ForgetClientResponse | null>(null)

watch(() => props.open, open => {
  if (open) { disconnect.value = props.client.kind === 'supported'; busy.value = false; error.value = ''; result.value = null }
})

async function confirm() {
  busy.value = true
  error.value = ''
  try {
    result.value = await api.forgetClient(props.client.id, { disconnect: props.client.kind === 'supported' && disconnect.value })
    emit('done')
  } catch (err) {
    error.value = describeError(err)
  } finally {
    busy.value = false
  }
}

function close() { emit('close') }
</script>
