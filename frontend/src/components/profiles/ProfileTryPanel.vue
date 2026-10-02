<template>
  <section class="space-y-2 rounded-box border border-base-300 p-3" aria-labelledby="profile-try-heading" data-test="profile-try">
    <div class="flex flex-wrap items-center gap-2">
      <h2 id="profile-try-heading" class="font-semibold">Try it</h2>
      <span class="badge badge-sm" :class="dirty ? 'badge-warning' : 'badge-ghost'" data-test="profile-try-source">{{ dirty ? 'Uses your unsaved edits' : 'Uses the saved profile' }}</span>
    </div>
    <p class="text-xs opacity-70">Runs a real tool search under this profile as it is in the form. Nothing is saved.</p>
    <form class="flex flex-wrap items-end gap-2" @submit.prevent="run">
      <div class="form-control flex-1 min-w-[12rem]">
        <label class="label py-0" for="profile-try-query"><span class="label-text text-xs font-medium">Search query</span></label>
        <input id="profile-try-query" v-model.trim="query" class="input input-bordered input-sm w-full" placeholder="create issue" data-test="profile-try-query" />
      </div>
      <button type="submit" class="btn btn-sm btn-outline" :disabled="busy || !query" data-test="profile-try-run">
        <span v-if="busy" class="loading loading-spinner loading-xs" />
        Try
      </button>
    </form>
    <div aria-live="polite" class="space-y-2" data-test="profile-try-results">
      <p v-if="error" role="alert" class="text-sm text-error">{{ error }}</p>
      <template v-if="result">
        <p class="text-sm" data-test="profile-try-hidden-count">Hidden by profile: {{ result.hidden_by_profile }}</p>
        <ul class="text-sm space-y-1" data-test="profile-try-list">
          <li v-for="(row, index) in rows" :key="row.key || index" class="flex flex-col">
            <code class="text-xs">{{ row.key || '(unnamed tool)' }}</code>
            <span v-if="row.description" class="text-xs opacity-70 line-clamp-2">{{ row.description }}</span>
          </li>
          <li v-if="!rows.length" class="opacity-70">No visible tool matches.</li>
        </ul>
        <div v-if="result.hidden.length" data-test="profile-try-hidden">
          <p class="text-xs font-medium">Hidden</p>
          <ul class="text-xs space-y-0.5">
            <li v-for="item in result.hidden" :key="`${item.server}:${item.tool}`"><code>{{ item.server }}:{{ item.tool }}</code> &mdash; {{ reasonText(item.reason) }}</li>
          </ul>
          <p v-if="result.hidden_truncated" class="text-xs opacity-70">The list is truncated.</p>
        </div>
      </template>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import api from '@/services/api'
import { describeError, reasonText, tryHitRow } from '@/utils/profiles'
import type { ProfileConfig, TryProfileResponse } from '@/types/api'

// Spec 108-i I18: Try it posts the CURRENT DRAFT to POST /profiles/try, so an
// operator can see what a change would hide before saving it. `dirty` says
// whether that draft differs from the saved profile (fix-usertest-web T153).
const props = defineProps<{ draft: () => ProfileConfig; dirty: boolean }>()
const query = ref('')
const busy = ref(false)
const error = ref('')
const result = ref<TryProfileResponse | null>(null)
const rows = computed(() => (result.value?.results ?? []).map(item => tryHitRow(item as Record<string, unknown>)))

async function run() {
  busy.value = true
  error.value = ''
  try {
    result.value = await api.tryProfile({ profile: props.draft(), query: query.value })
  } catch (err) {
    result.value = null
    error.value = describeError(err)
  } finally {
    busy.value = false
  }
}
</script>
