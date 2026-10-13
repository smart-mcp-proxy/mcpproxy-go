<template>
  <div class="rounded-lg border border-base-300 p-3 space-y-2" data-test="setting-row-anonymous_profile" :class="highlight && 'ring-2 ring-primary ring-offset-2'">
    <div data-test="settings-anonymous-profile">
      <label class="font-medium text-sm" for="anonymous-profile-select">Anonymous callers</label>
      <p class="text-xs text-base-content/70 mt-1" data-test="anonymous-profile-explanation">
        <template v-if="requireMcpAuth">Anonymous callers are refused because authentication is required. This setting applies only if you turn authentication off.</template>
        <template v-else>Callers that send no credential get this profile. If any client is bound to a profile, anonymous callers must not reach more than it, or they are denied everything (binding guard).</template>
      </p>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      <select
        id="anonymous-profile-select"
        ref="selectEl"
        v-model="selected"
        class="select select-bordered select-sm"
        :disabled="profiles.loading && !profiles.loaded"
        data-test="anonymous-profile-select"
      >
        <option value="">Unconfined &mdash; all servers</option>
        <option v-for="profile in profiles.profiles" :key="profile.name" :value="profile.name">{{ profile.title || profile.name }}{{ profile.title ? ` (${profile.name})` : '' }}</option>
      </select>
      <button type="button" class="btn btn-primary btn-sm" :disabled="saving || !dirty" data-test="anonymous-profile-save" @click="save">
        <span v-if="saving" class="loading loading-spinner loading-xs" />
        Save
      </button>
      <router-link v-if="profiles.loaded && !profiles.hasProfiles" class="link text-sm" to="/profiles?create=1" data-test="anonymous-profile-create-link">Create a profile first</router-link>
    </div>
    <GuardRefusal v-if="refusal" :refusal="refusal" />
    <p v-else-if="error" role="alert" class="text-sm text-error" data-test="anonymous-profile-error">{{ error }}</p>
    <p v-else-if="savedMessage" role="status" aria-live="polite" class="text-sm text-success" data-test="anonymous-profile-saved">{{ savedMessage }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import GuardRefusal from '@/components/GuardRefusal.vue'
import api from '@/services/api'
import { useProfilesStore } from '@/stores/profiles'
import { useSystemStore } from '@/stores/system'
import { describeError, isGuardRefusal } from '@/utils/profiles'
import type { ApiError } from '@/services/api'

// Spec 108-i I21 / T098a: "Anonymous callers" on Settings -> Security. The
// options are dynamic (the profiles), so it is its own component rather than a
// fields.ts entry. A 409 is the FR-008a guard: the change would leave a bound
// client reachable without authentication, and nothing was written.
defineProps<{ requireMcpAuth: boolean }>()
const route = useRoute()
const profiles = useProfilesStore()
const system = useSystemStore()
const selected = ref('')
const saving = ref(false)
const error = ref('')
const savedMessage = ref('')
const refusal = ref<ApiError | null>(null)
const highlight = ref(false)
const selectEl = ref<HTMLSelectElement | null>(null)

const dirty = computed(() => selected.value !== profiles.anonymousProfile)

// `?focus=anonymous_profile&value=<p>` (a guard refusal's fix button) preselects
// the profile and focuses the control. It never saves: the operator sees the
// change and confirms it with Save.
async function applyDeepLink() {
  if (route?.query.focus !== 'anonymous_profile') return
  const value = route.query.value
  if (typeof value === 'string') selected.value = value
  await nextTick()
  selectEl.value?.focus()
  selectEl.value?.scrollIntoView?.({ block: 'center' })
  highlight.value = true
  setTimeout(() => { highlight.value = false }, 2500)
}

async function load() {
  await profiles.fetchProfiles()
  selected.value = profiles.anonymousProfile
  await applyDeepLink()
}

async function save() {
  saving.value = true
  error.value = ''
  savedMessage.value = ''
  refusal.value = null
  try {
    await api.setAnonymousProfile(selected.value)
    await profiles.fetchProfiles()
    savedMessage.value = selected.value ? `Anonymous callers now get ${profiles.titleFor(selected.value)}.` : 'Anonymous callers are unconfined.'
    system.addToast({ type: 'success', title: 'Settings saved', message: 'anonymous_profile' })
  } catch (err) {
    if (isGuardRefusal(err)) refusal.value = err
    else error.value = describeError(err, 'Failed to save')
  } finally {
    saving.value = false
  }
}

onMounted(load)
watch(() => route?.query.value, () => { void applyDeepLink() })
</script>
