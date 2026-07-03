<template>
  <!-- Spec 079 FR-005: dismissible, non-modal upgrade nudge. Dismissal is
       persisted per latest version (localStorage), so the same version never
       re-nags but a newer release shows the banner again. When update
       checking is disabled (update_check.enabled=false) the /api/v1/info
       response carries no update object, so the banner is naturally absent. -->
  <div v-if="visible" class="alert alert-info" data-test="update-banner">
    <svg class="w-6 h-6 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16v2a2 2 0 002 2h12a2 2 0 002-2v-2M12 4v12m0 0l-4-4m4 4l4-4" />
    </svg>
    <div class="flex-1">
      <span>
        Update available: <span class="font-semibold">{{ latestVersion }}</span>
        <template v-if="currentVersion"> — you are running {{ currentVersion }}</template>.
      </span>
      <a
        v-if="releaseUrl"
        :href="releaseUrl"
        target="_blank"
        rel="noopener noreferrer"
        class="link link-hover underline"
        data-test="update-banner-release-link"
      >Release notes</a>
    </div>
    <button
      class="btn btn-sm btn-ghost btn-square"
      aria-label="Dismiss"
      data-test="update-banner-dismiss"
      @click="dismiss"
    >
      <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
      </svg>
    </button>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useSystemStore } from '@/stores/system'

// Per-version dismissal key (FR-005): stores the latest_version the user
// dismissed. A strictly different (newer) latest version no longer matches,
// so the banner reappears for it.
const STORAGE_KEY = 'update-banner-dismissed-version'

const systemStore = useSystemStore()
// Read eagerly (not in onMounted) so a dismissed version never flashes the
// banner on the first render.
const dismissedVersion = ref(localStorage.getItem(STORAGE_KEY) ?? '')

const latestVersion = computed(() => systemStore.latestVersion)
const currentVersion = computed(() => systemStore.version)
const releaseUrl = computed(() => systemStore.info?.update?.release_url ?? '')

const visible = computed(
  () =>
    systemStore.updateAvailable &&
    !!latestVersion.value &&
    latestVersion.value !== dismissedVersion.value
)

function dismiss() {
  dismissedVersion.value = latestVersion.value
  localStorage.setItem(STORAGE_KEY, latestVersion.value)
}
</script>
