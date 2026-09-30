<template>
  <BaseDialog :open="open" :title="`Rename ${profile.title || profile.name}`" test-id="profile-rename-dialog" @close="emit('close')">
    <form class="space-y-3" @submit.prevent="submit">
      <ProfileImpact v-if="hasImpact" :profile-name="profile.name" :used-by="usedBy" heading="This moves:" />
      <p v-else class="text-sm opacity-70">Nothing points at this profile yet.</p>
      <div class="form-control">
        <label class="label" for="profile-new-name"><span class="label-text font-medium">New name</span></label>
        <input
          id="profile-new-name"
          v-model.trim="newName"
          class="input input-bordered input-sm w-full font-mono"
          :class="fieldError && 'input-error'"
          autocomplete="off"
          data-test="profile-rename-input"
        />
        <span v-if="fieldError" class="text-error text-xs mt-1" data-test="profile-rename-error">{{ fieldError }}</span>
      </div>
      <GuardRefusal v-if="guard" :refusal="guard" @navigate="emit('close')" />
      <div v-else-if="otherError" role="alert" class="alert alert-error text-sm">{{ otherError }}</div>
      <div class="modal-action">
        <button type="button" class="btn btn-ghost btn-sm" @click="emit('close')">Cancel</button>
        <button type="submit" class="btn btn-primary btn-sm" :disabled="busy || !newName || newName === profile.name" data-test="profile-rename-submit">
          <span v-if="busy" class="loading loading-spinner loading-xs" />
          Rename
        </button>
      </div>
    </form>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import BaseDialog from '@/components/BaseDialog.vue'
import GuardRefusal from '@/components/GuardRefusal.vue'
import ProfileImpact from '@/components/profiles/ProfileImpact.vue'
import api from '@/services/api'
import { useProfilesStore } from '@/stores/profiles'
import { describeError, isGuardRefusal } from '@/utils/profiles'
import type { ProfileUsedBy, ProfileView } from '@/types/api'
import type { ApiError } from '@/services/api'

// Spec 108-i I19: rename shows what it moves BEFORE the POST. The server rewrites
// every pin, binding, switchable_to entry and the anonymous_profile.
const props = defineProps<{ open: boolean; profile: ProfileView }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'renamed', newName: string): void }>()
const profiles = useProfilesStore()
const newName = ref('')
const busy = ref(false)
const error = ref<ApiError | null>(null)

watch(() => props.open, open => {
  if (!open) return
  newName.value = props.profile.name
  error.value = null
  busy.value = false
  // The impact list reads other profiles' switchable_to: make sure it is current.
  void profiles.fetchProfiles()
})

const usedBy = computed<ProfileUsedBy>(() => props.profile.used_by ?? { clients: [], tokens: [], anonymous_profile: false })
const referenced = computed(() => profiles.profiles.some(other => other.name !== props.profile.name && other.switchable_to?.includes(props.profile.name)))
const hasImpact = computed(() => usedBy.value.clients.length > 0 || usedBy.value.tokens.length > 0 || usedBy.value.anonymous_profile || referenced.value)
const guard = computed(() => (error.value && isGuardRefusal(error.value) ? error.value : null))
const fieldError = computed(() => (error.value?.field === 'new_name' || error.value?.field === 'name' ? error.value.message : ''))
const otherError = computed(() => (error.value && !guard.value && !fieldError.value ? describeError(error.value) : ''))

async function submit() {
  busy.value = true
  error.value = null
  try {
    const result = await api.renameProfile(props.profile.name, newName.value)
    emit('renamed', result.profile.name)
  } catch (err) {
    error.value = err as ApiError
  } finally {
    busy.value = false
  }
}
</script>
