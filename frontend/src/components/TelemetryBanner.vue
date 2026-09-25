<template>
  <!-- UX audit F14: a daisyUI alert is a GRID that flows in columns, so at 390px
       the message shared the row with the action buttons and rendered as a
       ~10-character column ~440px tall — over half the viewport, before any
       content. Below `sm` the alert stacks (`alert-vertical`) and the text child
       gets `min-w-0` so it may shrink inside its track. -->
  <div v-if="visible" class="alert alert-vertical sm:alert-horizontal alert-info" data-test="telemetry-banner">
    <svg class="w-6 h-6 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
    </svg>
    <div class="flex-1 min-w-0">
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
import { ref, computed, onMounted } from 'vue'
import { RouterLink } from 'vue-router'
import { useOnboardingStore, TELEMETRY_BANNER_STORAGE_KEY } from '@/stores/onboarding'

const onboarding = useOnboardingStore()
const dismissed = ref(false)

onMounted(() => {
  dismissed.value = !!localStorage.getItem(TELEMETRY_BANNER_STORAGE_KEY)
})

// Spec 109-b FR-044: the notice must not render while the wizard is open —
// it already offers its own one-line version in the Verify step (see
// OnboardingWizard.vue), and showing both would say the same thing twice
// while the wizard sits on top of everything else. Dismissal is one shared
// localStorage key, so acting on either surface silences both for good.
const visible = computed(() => !dismissed.value && !onboarding.wizardOpen)

function dismiss() {
  dismissed.value = true
  localStorage.setItem(TELEMETRY_BANNER_STORAGE_KEY, 'true')
}
</script>
