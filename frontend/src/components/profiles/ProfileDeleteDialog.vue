<template>
  <BaseDialog :open="open" :title="`Delete ${profile.title || profile.name}?`" test-id="profile-delete-dialog" @close="emit('close')">
    <ProfileImpact v-if="inUse" :profile-name="profile.name" :used-by="usedBy" heading="This profile is used by:" />
    <p v-else class="text-sm">Nothing points at this profile. Deleting it cannot be undone.</p>

    <div v-if="needsTarget" class="form-control">
      <label class="label" for="profile-delete-target"><span class="label-text font-medium">Move them to</span></label>
      <select id="profile-delete-target" v-model="target" class="select select-bordered select-sm w-full" :disabled="leave" data-test="profile-delete-target">
        <option value="" disabled>Choose a profile&hellip;</option>
        <option v-for="other in targets" :key="other.name" :value="other.name">{{ other.title || other.name }}</option>
      </select>
      <p v-if="!targets.length" class="text-xs text-warning mt-1">There is no other profile to move them to.</p>
    </div>
    <label v-if="needsTarget && !isAnonymous" class="label cursor-pointer justify-start gap-2">
      <input v-model="leave" type="checkbox" class="checkbox checkbox-sm" data-test="profile-delete-force" />
      <span class="label-text">Leave them without a profile (they will be denied all tools)</span>
    </label>
    <p v-if="isAnonymous" class="text-xs opacity-70">Anonymous callers move with the others; they cannot be left without a profile.</p>

    <GuardRefusal v-if="guard" :refusal="guard" @navigate="emit('close')" />
    <div v-else-if="error" role="alert" class="alert alert-error text-sm" data-test="profile-delete-error">{{ error }}</div>
    <template #actions>
      <button type="button" class="btn btn-ghost btn-sm" @click="emit('close')">Cancel</button>
      <button type="button" class="btn btn-error btn-sm" :disabled="busy || (needsTarget && !leave && !target)" data-test="profile-delete-confirm" @click="submit">
        <span v-if="busy" class="loading loading-spinner loading-xs" />
        Delete profile
      </button>
    </template>
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

// Spec 108-i I19 / 108-f F4. When anything points at the profile a "Move them
// to" target is REQUIRED (another profile: a locked client cannot hold an empty
// pin). `force` leaves pins dangling (deny-all); it is not offered for the
// anonymous_profile, which the server refuses to delete without a target.
const props = defineProps<{ open: boolean; profile: ProfileView }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'deleted'): void }>()
const profiles = useProfilesStore()
const target = ref('')
const leave = ref(false)
const busy = ref(false)
const apiError = ref<ApiError | null>(null)

watch(() => props.open, open => {
  if (!open) return
  target.value = ''
  leave.value = false
  busy.value = false
  apiError.value = null
  void profiles.fetchProfiles()
}, { immediate: true })

const usedBy = computed<ProfileUsedBy>(() => props.profile.used_by ?? { clients: [], tokens: [], anonymous_profile: false })
const isAnonymous = computed(() => usedBy.value.anonymous_profile)
const referenced = computed(() => profiles.profiles.some(other => other.name !== props.profile.name && other.switchable_to?.includes(props.profile.name)))
const needsTarget = computed(() => usedBy.value.clients.length > 0 || usedBy.value.tokens.length > 0 || isAnonymous.value)
const inUse = computed(() => needsTarget.value || referenced.value)
const targets = computed(() => profiles.profiles.filter(other => other.name !== props.profile.name))
const guard = computed(() => (apiError.value && isGuardRefusal(apiError.value) ? apiError.value : null))
const error = computed(() => (apiError.value && !guard.value ? describeError(apiError.value) : ''))

async function submit() {
  busy.value = true
  apiError.value = null
  try {
    const opts: { reassign_to?: string; force?: boolean } = {}
    if (needsTarget.value) {
      if (leave.value && !isAnonymous.value) opts.force = true
      else opts.reassign_to = target.value
    }
    await api.deleteProfile(props.profile.name, opts)
    emit('deleted')
  } catch (err) {
    apiError.value = err as ApiError
  } finally {
    busy.value = false
  }
}
</script>
