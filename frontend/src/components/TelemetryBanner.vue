<template>
  <!-- Inline variant: the wizard's Verify-step one-liner (Spec 109-b FR-044). -->
  <p
    v-if="variant === 'inline' && visible"
    class="mt-4 border-t border-base-300 pt-3 text-[11px] opacity-60 flex items-center gap-2"
    data-test="wizard-telemetry-notice"
    :data-mode="mode"
  >
    <span class="flex-1">
      <template v-if="mode === 'off_env' && state">
        <span data-test="telemetry-off-env">{{ telemetryOffLine(state) }}</span>
      </template>
      <template v-else>
        MCPProxy sends anonymous usage statistics to help improve the product. No personal data is collected.
      </template>
      <a href="https://mcpproxy.app/telemetry" target="_blank" rel="noopener noreferrer" class="link link-hover underline">Learn more</a>
    </span>
    <button
      class="btn btn-ghost btn-xs"
      data-test="wizard-telemetry-notice-dismiss"
      @click="dismiss"
    >Dismiss</button>
  </p>

  <!-- UX audit F14: a daisyUI alert is a GRID that flows in columns, so at 390px
       the message shared the row with the action buttons and rendered as a
       ~10-character column ~440px tall — over half the viewport, before any
       content. Below `sm` the alert stacks (`alert-vertical`) and the text child
       gets `min-w-0` so it may shrink inside its track. -->
  <div
    v-else-if="variant === 'banner' && visible"
    class="alert alert-vertical sm:alert-horizontal alert-info"
    data-test="telemetry-banner"
    :data-mode="mode"
  >
    <svg class="w-6 h-6 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
    </svg>
    <!-- Environment opt-out (Spec 109 FR-044a): say it is off and why. The
         setting is locked, so no disclosure line and no Manage link. -->
    <div v-if="mode === 'off_env' && state" class="flex-1 min-w-0">
      <span data-test="telemetry-off-env">{{ telemetryOffLine(state) }}</span>
      <a
        href="https://mcpproxy.app/telemetry"
        target="_blank"
        rel="noopener noreferrer"
        class="link link-hover underline"
      > Learn more</a>
    </div>
    <div v-else class="flex-1 min-w-0">
      <span>MCPProxy sends anonymous usage statistics to help improve the product. No personal data is collected. </span>
      <a
        href="https://mcpproxy.app/telemetry"
        target="_blank"
        rel="noopener noreferrer"
        class="link link-hover underline"
      >Learn more</a>
      <!-- Transparency note (MCP-2482): disclose the one-time opt-out signal. -->
      <p class="text-xs mt-1" data-test="telemetry-banner-disclosure">
        Disabling sends a single anonymous opt-out signal, then stops all telemetry.
      </p>
    </div>
    <div class="flex items-center gap-2 shrink-0">
      <RouterLink
        v-if="mode !== 'off_env'"
        to="/settings?focus=telemetry.enabled"
        class="btn btn-sm btn-ghost"
        data-test="telemetry-banner-settings-link"
        @click="dismiss"
      >
        Manage in Settings
      </RouterLink>
      <button class="btn btn-sm btn-ghost btn-square" @click="dismiss" aria-label="Dismiss" data-test="telemetry-banner-dismiss">
        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
        </svg>
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount } from 'vue'
import { RouterLink } from 'vue-router'
import { useOnboardingStore } from '@/stores/onboarding'
import { telemetryNoticeMode, telemetryOffLine } from '@/utils/telemetryState'

const props = withDefaults(defineProps<{ variant?: 'banner' | 'inline' }>(), { variant: 'banner' })

const onboarding = useOnboardingStore()

const state = computed(() => onboarding.telemetryState)
const mode = computed(() => telemetryNoticeMode(state.value))

// Spec 109-b FR-044: the page banner must not render while the wizard is open —
// the wizard offers this same notice inline in its Verify step (variant
// "inline"), and showing both would say the same thing twice while the wizard
// sits on top of everything else. Dismissal is the store's shared
// `telemetryNoticeDismissed` ref (backed by one localStorage key), not an
// independent per-component copy, so acting on either surface hides both
// immediately even though the banner and the wizard stay mounted together on
// Dashboard.vue and neither ever remounts.
//
// Spec 109 FR-044a: a user who turned telemetry off in their own config is not
// nagged (`hidden`); an environment opt-out is stated, not disclosed.
const visible = computed(() => {
  if (onboarding.telemetryNoticeDismissed) return false
  if (mode.value === 'hidden') return false
  return props.variant === 'inline' ? true : !onboarding.wizardOpen
})

function dismiss() {
  onboarding.dismissTelemetryNotice()
}

// The state is read on mount and again whenever the tab regains focus, so a
// change made elsewhere (config edit, Settings in another tab) is picked up
// without a full reload.
function refreshOnVisible() {
  if (document.visibilityState === 'hidden') return
  void onboarding.loadTelemetryState()
}

onMounted(() => {
  void onboarding.loadTelemetryState()
  document.addEventListener('visibilitychange', refreshOnVisible)
  window.addEventListener('focus', refreshOnVisible)
})

onBeforeUnmount(() => {
  document.removeEventListener('visibilitychange', refreshOnVisible)
  window.removeEventListener('focus', refreshOnVisible)
})
</script>
