<template>
  <BaseDialog :open="open" title="Add other client" test-id="custom-client-dialog" @close="emit('close')">
    <p class="text-sm opacity-70">
      For a client MCPProxy cannot configure itself. It gets its own credential, bound to a profile, that you paste into the client's MCP configuration.
      The credential never carries the admin API key.
    </p>
    <form class="space-y-3" @submit.prevent="submit">
      <div class="form-control">
        <label class="label" for="custom-client-id"><span class="label-text font-medium">Client id</span></label>
        <input
          id="custom-client-id"
          v-model.trim="form.id"
          class="input input-bordered input-sm w-full"
          :class="fieldError('id') && 'input-error'"
          placeholder="dev-laptop"
          maxlength="56"
          autocomplete="off"
          data-test="custom-client-id"
        />
        <span v-if="fieldError('id')" class="text-error text-xs mt-1" data-test="custom-client-id-error">{{ fieldError('id') }}</span>
        <span v-else class="text-xs opacity-60 mt-1">Lower-case letters, digits, '-' or '_'; start with a letter or digit; at most 56 characters.</span>
      </div>
      <div class="form-control">
        <label class="label" for="custom-client-name"><span class="label-text font-medium">Display name (optional)</span></label>
        <input id="custom-client-name" v-model="form.display_name" class="input input-bordered input-sm w-full" data-test="custom-client-name" />
      </div>
      <div class="form-control">
        <label class="label" for="custom-client-profile"><span class="label-text font-medium">Profile</span></label>
        <select id="custom-client-profile" v-model="form.profile" class="select select-bordered select-sm w-full" data-test="custom-client-profile" @change="onProfileChange">
          <option value="">All servers</option>
          <option v-for="profile in profiles.profiles" :key="profile.name" :value="profile.name">{{ profile.title || profile.name }}{{ profile.title ? ` (${profile.name})` : '' }}</option>
        </select>
      </div>
      <fieldset class="form-control">
        <legend class="label-text font-medium mb-1">Mode</legend>
        <label class="label cursor-pointer justify-start gap-2"><input v-model="form.mode" type="radio" class="radio radio-sm" value="locked" :disabled="!form.profile" data-test="custom-client-mode-locked" /><span class="label-text">Locked &mdash; the client cannot switch profile</span></label>
        <label class="label cursor-pointer justify-start gap-2"><input v-model="form.mode" type="radio" class="radio radio-sm" value="switchable" data-test="custom-client-mode-switchable" /><span class="label-text">Switchable</span></label>
      </fieldset>
      <div class="form-control">
        <label class="label" for="custom-client-expiry"><span class="label-text font-medium">Expires in</span></label>
        <select id="custom-client-expiry" v-model="form.expires_in" class="select select-bordered select-sm w-full" data-test="custom-client-expiry">
          <option value="30d">30 days</option>
          <option value="90d">90 days</option>
          <option value="180d">180 days</option>
          <option value="365d">365 days</option>
        </select>
      </div>
      <GuardRefusal v-if="guard" :refusal="guard" @navigate="emit('close')" />
      <div v-else-if="conflict" role="alert" class="alert alert-error text-sm flex-col items-start" data-test="custom-client-conflict">
        <span>{{ conflict.message }}</span>
        <span class="text-xs">Revoke or delete token <code>{{ conflict.conflicting_token }}</code>, then add the client again.</span>
      </div>
      <div v-else-if="generalError" role="alert" class="alert alert-error text-sm" data-test="custom-client-error">{{ generalError }}</div>
      <div class="modal-action">
        <button type="button" class="btn btn-ghost btn-sm" @click="emit('close')">Cancel</button>
        <button type="submit" class="btn btn-primary btn-sm" :disabled="busy || !form.id" data-test="custom-client-submit">
          <span v-if="busy" class="loading loading-spinner loading-xs" />
          Add client
        </button>
      </div>
    </form>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import BaseDialog from '@/components/BaseDialog.vue'
import GuardRefusal from '@/components/GuardRefusal.vue'
import api from '@/services/api'
import { useProfilesStore } from '@/stores/profiles'
import { describeError, isGuardRefusal } from '@/utils/profiles'
import type { CustomClientResponse } from '@/types/api'
import type { ApiError } from '@/services/api'

// Spec 108-i I12: "Add other client...". The server validates the id (FR-021)
// and its 400 `field: "id"` text is shown under the input. The credential the
// response carries is handed to the parent, which shows it once.
const props = defineProps<{ open: boolean; initialProfile?: string }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'created', result: CustomClientResponse): void }>()
const profiles = useProfilesStore()
const busy = ref(false)
const error = ref<ApiError | null>(null)
const form = reactive({ id: '', display_name: '', profile: '', mode: 'switchable' as 'locked' | 'switchable', expires_in: '365d' })

watch(() => props.open, open => {
  if (!open) return
  Object.assign(form, { id: '', display_name: '', profile: props.initialProfile ?? '', mode: props.initialProfile ? 'locked' : 'switchable', expires_in: '365d' })
  error.value = null
  if (!profiles.loaded) void profiles.fetchProfiles()
})

// A profile defaults the mode to Locked; All servers can only be switchable.
function onProfileChange() { form.mode = form.profile ? 'locked' : 'switchable' }

const guard = computed(() => (error.value && isGuardRefusal(error.value) ? error.value : null))
const conflict = computed(() => (error.value?.conflicting_token ? error.value : null))
function fieldError(field: string): string {
  return error.value?.field === field ? error.value.message : ''
}
const generalError = computed(() => (error.value && error.value.field !== 'id' ? describeError(error.value) : ''))

async function submit() {
  busy.value = true
  error.value = null
  try {
    const body: Parameters<typeof api.createCustomClient>[0] = { id: form.id, expires_in: form.expires_in }
    if (form.display_name.trim()) body.display_name = form.display_name.trim()
    if (form.profile) { body.profile = form.profile; body.mode = form.mode }
    const result = await api.createCustomClient(body)
    emit('created', result)
  } catch (err) {
    error.value = err as ApiError
  } finally {
    busy.value = false
  }
}
</script>
