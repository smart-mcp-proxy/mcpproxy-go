<template>
  <dialog ref="nativeDialogEl" class="modal" data-test="auth-error-modal">
    <div
      ref="dialogRef"
      class="modal-box max-w-2xl"
      role="dialog"
      aria-modal="true"
      aria-labelledby="auth-error-modal-title"
    >
      <h3 id="auth-error-modal-title" class="font-bold text-lg text-error mb-4">
        🔒 Authentication Required
      </h3>

      <div class="mb-6">
        <p class="mb-4">
          The API key is invalid or missing. You need an API key to access the MCPProxy web interface.
        </p>

        <div class="alert alert-info mb-4">
          <div class="flex-1">
            <h4 class="font-semibold mb-2">How to get the API key:</h4>
            <!-- Audit F28: headless and server installs have no tray, so the
                 tray route can't be the only one. The config file and the CLI
                 work everywhere. -->
            <ol class="list-decimal list-inside space-y-1 text-sm">
              <li><strong>From the CLI:</strong> run <code class="bg-base-200 px-1 rounded">mcpproxy status</code> — its "Web UI" line is a ready-to-open URL with the key embedded (<code class="bg-base-200 px-1 rounded">mcpproxy status --web-url</code> prints just that URL), or read <code class="bg-base-200 px-1 rounded">api_key</code> in <code class="bg-base-200 px-1 rounded">~/.mcpproxy/mcp_config.json</code></li>
              <li><strong>From logs:</strong> the key is logged in full only by the start that generated it — later starts log a masked prefix</li>
              <!-- The label is NOT an OS split. "Open Web UI in Browser" is
                   the Swift app bundle (native/macos, shipped in the DMG);
                   "Open Web Control Panel" is the Go tray
                   (internal/tray/tray.go:594), whose build tag is
                   `!nogui && !headless && !linux` — so it ships on Windows AND
                   on macOS via the darwin tarball and Homebrew
                   (`bin.install "mcpproxy-tray" if OS.mac?`). Naming one of
                   them "Windows:" would put a fresh false statement on the
                   screen this modal exists to make honest. Both trays fetch
                   the URL from /api/v1/info over the socket, which answers
                   with an admin context, so both get the key appended. -->
              <li><strong>Using the tray</strong> (desktop installs): click the MCPProxy tray icon and choose "Open Web UI in Browser" or "Open Web Control Panel" (the wording depends on which tray build you have) — either opens an already-authenticated window</li>
            </ol>
          </div>
        </div>
      </div>

      <!-- Manual API Key Entry. Audit F28: the field was labelled "(optional)"
           while being the only way in. -->
      <div class="form-control mb-6">
        <label class="label">
          <span class="label-text font-semibold">API key <span class="text-error">(required)</span></span>
        </label>
        <div class="input-group">
          <input
            v-model="apiKeyInput"
            type="password"
            placeholder="Enter your API key..."
            class="input input-bordered flex-1"
            :class="{ 'input-error': inputError }"
            @keyup.enter="handleSetAPIKey"
            @input="clearInputError"
          />
          <button
            class="btn btn-primary"
            :disabled="!apiKeyInput.trim() || isValidating"
            @click="handleSetAPIKey"
          >
            <span v-if="isValidating" class="loading loading-spinner loading-sm"></span>
            {{ isValidating ? 'Validating...' : 'Set Key' }}
          </button>
        </div>
        <div v-if="inputError" class="label">
          <span class="label-text-alt text-error">{{ inputError }}</span>
        </div>
      </div>

      <!-- Current API Key Status -->
      <div class="mb-6">
        <div class="stats stats-vertical lg:stats-horizontal shadow">
          <div class="stat">
            <div class="stat-title">Current API Key</div>
            <div class="stat-value text-sm font-mono">
              {{ currentAPIKeyPreview }}
            </div>
            <div class="stat-desc">{{ currentAPIKeyStatus }}</div>
          </div>
        </div>
      </div>

      <!-- Action Buttons -->
      <div class="modal-action">
        <button class="btn btn-ghost" @click="handleRefresh">
          <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4 mr-2" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
          </svg>
          Refresh & Retry
        </button>
        <!-- Audit F28: "Continue Without Auth" led to a shell of zero-valued
             tiles, so it must not sit at equal weight beside the real way in.
             Demoted to a link, and honest about where it goes. -->
        <button
          v-if="canClose"
          class="btn btn-ghost btn-sm text-base-content/60"
          data-test="auth-dismiss"
          title="The UI cannot load data without a key — pages will render empty"
          @click="handleClose"
        >
          Dismiss (pages stay empty)
        </button>
      </div>
    </div>
    <!-- Deliberately no `<form method="dialog" class="modal-backdrop">` click
         catcher here (contrast with AddSecretModal/AddServerModal/ConnectModal/
         OnboardingWizard): clicking outside must not dismiss this modal, same
         as before the FR-055 top-layer migration. The dim backdrop itself
         still renders — `showModal()` promotes this dialog to the browser's
         top layer with its native `::backdrop`, which daisyUI's `.modal`
         styles independently of that click-catcher form. -->
  </dialog>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import api from '@/services/api'
import { useModalA11y } from '@/composables/useModalA11y'
import { useDialogOpen } from '@/composables/useDialogOpen'

interface Props {
  // Spec 107 T088 (App.vue): bound as `authModal.show || undefined` so the
  // stubbed component in tests never renders a literal `show="false"`
  // attribute — accept the omitted case here too.
  show?: boolean
  canClose?: boolean
  lastError?: string
}

interface Emits {
  (e: 'close'): void
  (e: 'authenticated'): void
  // `verified` says whether the reloaded key actually authenticated. App.vue
  // only invalidates views when it did (#1065).
  (e: 'refresh', verified: boolean): void
}

const props = withDefaults(defineProps<Props>(), {
  canClose: false
})

const emit = defineEmits<Emits>()

// Spec 109 FR-055 (review round 6, finding 3): promote to the browser's top
// layer via showModal(), same as AddSecretModal/AddServerModal/ConnectModal/
// OnboardingWizard, so a background 401 raised while one of those is open
// paints above it instead of underneath its backdrop. Escape and the Tab trap
// route through handleClose below, which already gates on `canClose` — so
// Escape is a no-op while canClose is false, matching the Dismiss button's
// own gating rather than adding a second copy of it here.
const { dialogRef } = useModalA11y(() => props.show, () => handleClose())
const { dialogEl: nativeDialogEl } = useDialogOpen(() => props.show, () => handleClose())

// State
const apiKeyInput = ref('')
const inputError = ref('')
const isValidating = ref(false)

// Computed
const currentAPIKeyPreview = computed(() => {
  return api.hasAPIKey() ? api.getAPIKeyPreview() : 'none'
})

const currentAPIKeyStatus = computed(() => {
  if (!api.hasAPIKey()) {
    return 'No API key set'
  }
  if (props.lastError?.includes('401') || props.lastError?.includes('403')) {
    return 'Invalid or expired'
  }
  return 'Set but validation failed'
})

// Methods
function clearInputError() {
  inputError.value = ''
}

async function handleSetAPIKey() {
  if (!apiKeyInput.value.trim()) {
    inputError.value = 'Please enter an API key'
    return
  }

  isValidating.value = true
  inputError.value = ''

  try {
    // Set the API key
    api.setAPIKey(apiKeyInput.value.trim())

    // Validate it
    const isValid = await api.validateAPIKey()

    if (isValid) {
      console.log('API key validation successful')
      apiKeyInput.value = ''
      emit('authenticated')
    } else {
      inputError.value = 'Invalid API key - please check and try again'
      // Don't clear the invalid key from localStorage yet in case user wants to retry
    }
  } catch (error) {
    console.error('API key validation error:', error)
    inputError.value = error instanceof Error ? error.message : 'Validation failed'
  } finally {
    isValidating.value = false
  }
}

async function handleRefresh() {
  // Reinitialize API key from URL/localStorage, then VERIFY it before telling
  // the app auth is repaired. Without the verification this path could only
  // assert recovery, so it could not safely invalidate the views holding stale
  // auth errors -- and #1065's stale red panel survived on this path.
  isValidating.value = true
  inputError.value = ''
  try {
    api.reinitializeAPIKey()
    const isValid = await api.validateAPIKey()
    emit('refresh', isValid)
    if (!isValid) {
      inputError.value = 'No valid API key found — enter one above'
    }
  } catch (error) {
    console.error('API key refresh error:', error)
    inputError.value = error instanceof Error ? error.message : 'Refresh failed'
    emit('refresh', false)
  } finally {
    isValidating.value = false
  }
}

function handleClose() {
  if (props.canClose) {
    emit('close')
  }
}

// Reset the form each time the modal opens. Previously this lived in
// onMounted, which was equivalent while the modal used `v-if="show"` —
// closing and reopening destroyed and recreated the component, so
// onMounted fired again on every open. The FR-055 top-layer migration
// (review round 6, finding 3) keeps the <dialog> permanently mounted and
// toggles it via showModal()/close() instead, so onMounted now fires only
// once ever; without this watch, a stale error or leftover key from a
// previous attempt would still be sitting there the next time the modal
// is shown.
watch(
  () => props.show,
  (open) => {
    if (!open) return
    apiKeyInput.value = ''
    inputError.value = ''
  },
  { immediate: true }
)
</script>

<style scoped>
code {
  font-family: 'Courier New', monospace;
  font-size: 0.875rem;
}
</style>
