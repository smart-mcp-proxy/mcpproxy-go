<template>
  <BaseDialog :open="open" :title="`Assign ${profiles.titleFor(profileName)} to a client`" test-id="assign-client-dialog" @close="emit('close')">
    <p v-if="!eligible.length" class="text-sm opacity-70" data-test="assign-none">
      No client has a client credential yet. Connect a client from the Clients page first.
    </p>
    <form v-else class="space-y-3" @submit.prevent="submit">
      <div class="form-control">
        <label class="label" for="assign-client"><span class="label-text font-medium">Client</span></label>
        <select id="assign-client" v-model="clientId" class="select select-bordered select-sm w-full" data-test="assign-client-select">
          <option value="" disabled>Choose a client&hellip;</option>
          <option v-for="client in eligible" :key="client.id" :value="client.id">{{ client.display_name }} &mdash; {{ client.profile ? profiles.titleFor(client.profile) : 'All servers' }}</option>
        </select>
      </div>
      <GuardRefusal v-if="refusal && isGuardRefusal(refusal)" :refusal="refusal as any" @navigate="emit('close')" />
      <div v-else-if="refusal" role="alert" class="alert alert-error text-sm">{{ describeError(refusal) }}</div>
      <p v-if="done" role="status" aria-live="polite" class="text-sm text-success" data-test="assign-done">{{ done }}</p>
      <div class="modal-action">
        <button type="button" class="btn btn-ghost btn-sm" @click="emit('close')">Close</button>
        <button type="submit" class="btn btn-primary btn-sm" :disabled="!clientId || bindings.busy[clientId]" data-test="assign-submit">Assign</button>
      </div>
    </form>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import BaseDialog from '@/components/BaseDialog.vue'
import GuardRefusal from '@/components/GuardRefusal.vue'
import { useClientsStore } from '@/stores/clients'
import { useClientBindingsStore } from '@/stores/clientBindings'
import { useProfilesStore } from '@/stores/profiles'
import { describeError, isGuardRefusal } from '@/utils/profiles'

// Spec 108-i I17/I18: "Assign to client..." on a profile. Moves one client with
// a client credential onto the profile (PUT binding without a mode, which keeps
// the credential's mode). A refusal stays in this dialog.
const props = defineProps<{ open: boolean; profileName: string }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'assigned'): void }>()
const profiles = useProfilesStore()
const clients = useClientsStore()
const bindings = useClientBindingsStore()
const clientId = ref('')
const done = ref('')
const refusal = computed(() => (clientId.value ? bindings.rowErrors[clientId.value] : undefined))
const eligible = computed(() => clients.clients.filter(client => client.credential_state === 'client'))

watch(() => props.open, open => {
  if (!open) return
  clientId.value = ''
  done.value = ''
  if (!clients.clients.length) void clients.refreshPresence()
})

async function submit() {
  done.value = ''
  const moved = await bindings.setBinding(clientId.value, { profile: props.profileName })
  if (moved) {
    done.value = `${moved.display_name} now uses ${profiles.titleFor(props.profileName)}.`
    emit('assigned')
  }
}
</script>
