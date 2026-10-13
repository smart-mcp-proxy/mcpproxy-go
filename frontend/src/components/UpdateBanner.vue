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
        <template v-if="currentVersion"> — you are running {{ currentVersion }}</template
        ><!-- Spec 079 FR-002: the "N releases / M weeks behind" delta. The
             clause is rendered by the daemon (updatecheck.FormatBehindSummary)
             and printed verbatim, so this banner cannot word it differently
             from status/doctor/the tray — do not rebuild it from the numeric
             fields. Absent when the delta could not be resolved or the daemon
             predates it, and the sentence then reads exactly as before. -->
        <template v-if="behindSummary">, <span data-test="update-banner-behind">{{ behindSummary }}</span></template>.
      </span>
      <a
        v-if="releaseUrl"
        :href="releaseUrl"
        target="_blank"
        rel="noopener noreferrer"
        class="link link-hover underline"
        data-test="update-banner-release-link"
      >Release notes</a>
      <!-- Spec 079 US2 (FR-009): the exact one-line command for the detected
           install channel, copyable. Absent for channels without a safe
           command (dmg/windows-installer/docker/unknown). Spec 092 FR-020
           added `tarball` to the CHANNELS WITH a command (`mcpproxy update`),
           so it is no longer in that list. Nothing here needed changing: the
           branch keys off the presence of update_command, never the channel
           name. -->
      <div v-if="updateCommand" class="mt-2 flex items-center gap-2">
        <code
          class="rounded bg-base-200 px-2 py-1 font-mono text-sm text-base-content"
          data-test="update-banner-command"
        >{{ updateCommand }}</code>
        <button
          class="btn btn-xs btn-ghost"
          :aria-label="copied ? 'Copied' : 'Copy update command'"
          data-test="update-banner-copy"
          @click="copyCommand"
        >
          <svg v-if="!copied" class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
          </svg>
          <span v-else class="text-success">Copied</span>
        </button>
      </div>
      <!-- Guidance fallback for channels that intentionally have no safe
           command (dmg/windows-installer/docker/unknown), mirroring
           internal/updatecheck.GuidanceLine so the Web UI matches the status/
           doctor surfaces (FR-009/FR-010). The Release notes link above is
           the deep link, so the text references it generically. -->
      <div
        v-else-if="guidance"
        class="mt-2 text-sm"
        data-test="update-banner-guidance"
      >{{ guidance }}</div>
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

// localStorage can throw (blocked storage in embedded/private contexts);
// degrade to session-only dismissal instead of breaking component setup
// (precedent: stores/system.ts wraps storage access the same way).
function readDismissedVersion(): string {
  try {
    return localStorage.getItem(STORAGE_KEY) ?? ''
  } catch {
    return ''
  }
}

// Read eagerly (not in onMounted) so a dismissed version never flashes the
// banner on the first render.
const dismissedVersion = ref(readDismissedVersion())

const latestVersion = computed(() => systemStore.latestVersion)
// Spec 079 FR-002 — see the template comment; rendered, never re-derived.
const behindSummary = computed(() => systemStore.updateBehindSummary)
const currentVersion = computed(() => systemStore.version)
const releaseUrl = computed(() => systemStore.info?.update?.release_url ?? '')
// Spec 079 US2: channel-aware one-line update command (empty when the
// detected channel has no safe command, FR-009).
const updateCommand = computed(() => systemStore.updateCommand)
const installChannel = computed(() => systemStore.installChannel)

// Guidance for updates without a safe command — mirrors
// internal/updatecheck.GuidanceLine (keep the two in sync). Rendered only
// when no update_command was provided; command channels reach the default
// branch when the offered version is a prerelease (their command was
// suppressed backend-side). '' (older daemon) renders nothing.
const guidance = computed(() => {
  if (updateCommand.value) return ''
  switch (installChannel.value) {
    case 'dmg':
      return 'Download the latest DMG from the releases page.'
    case 'windows-installer':
      return 'Download the latest Windows installer from the releases page.'
    case 'docker':
      return 'Pull or rebuild the newer image for your deployment.'
    case '':
      return ''
    // Spec 092: `tarball` carries `mcpproxy update` for stable AND prerelease
    // offers alike, so it never reaches this switch — the `updateCommand`
    // guard above returns first. Listing it here would be misleading.
    default: // unknown, prerelease-suppressed command channels, …
      return 'Download the latest release from the releases page.'
  }
})

const copied = ref(false)
let copiedTimer: ReturnType<typeof setTimeout> | undefined

async function copyCommand() {
  try {
    await navigator.clipboard.writeText(updateCommand.value)
    copied.value = true
    if (copiedTimer) clearTimeout(copiedTimer)
    copiedTimer = setTimeout(() => {
      copied.value = false
    }, 2000)
  } catch {
    // Clipboard unavailable (insecure context / permissions) — the command
    // stays visible for manual selection; no error surface needed (FR-006).
  }
}

const visible = computed(
  () =>
    systemStore.updateAvailable &&
    // Spec 079 US3 (FR-019): the core stamps nudges_suppressed in CI /
    // non-interactive contexts — status/doctor/info keep the facts, the
    // banner stays quiet.
    !systemStore.updateNudgesSuppressed &&
    !!latestVersion.value &&
    latestVersion.value !== dismissedVersion.value
)

function dismiss() {
  dismissedVersion.value = latestVersion.value
  try {
    localStorage.setItem(STORAGE_KEY, latestVersion.value)
  } catch {
    // Storage unavailable — dismissal still holds for this session via the ref.
  }
}
</script>
