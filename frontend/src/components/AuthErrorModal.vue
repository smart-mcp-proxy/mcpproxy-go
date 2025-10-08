<template>
  <div v-if="show" class="modal modal-open">
    <div class="modal-box max-w-2xl">
      <h3 class="font-bold text-lg text-error mb-4">
        🔒 Authentication Required
      </h3>

      <div class="mb-6">
        <p class="mb-4">
          The API key is invalid or missing. You need an API key to access the MCPProxy web interface.
        </p>

        <div class="alert alert-info mb-4">
          <div class="flex-1">
            <h4 class="font-semibold mb-2">How to get the API key:</h4>
            <ol class="list-decimal list-inside space-y-1 text-sm">
              <li><strong>Using Tray:</strong> Right-click the MCPProxy tray icon and select "Open Web UI"</li>
              <li><strong>From Logs:</strong> Check mcpproxy startup logs for the API key, then add <code class="bg-base-200 px-1 rounded">?apikey=YOUR_KEY</code> to the URL</li>
              <li><strong>Manual Entry:</strong> Enter your API key below if you have it</li>
            </ol>
          </div>
        </div>
      </div>

      <!-- Manual API Key Entry -->
      <div class="form-control mb-6">
        <label class="label">
          <span class="label-text font-semibold">Enter API Key (optional)</span>
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
        <button v-if="canClose" class="btn btn-outline" @click="handleClose">
          Continue Without Auth
        </button>
      </div>
    </div>

    <!-- Backdrop (clicking outside won't close to prevent accidental dismissal) -->
    <div class="modal-backdrop bg-black bg-opacity-50"></div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import api from '@/services/api'

interface Props {
  show: boolean
  canClose?: boolean
  lastError?: string
}

interface Emits {
  (e: 'close'): void
  (e: 'authenticated'): void
  (e: 'refresh'): void
}

const props = withDefaults(defineProps<Props>(), {
  canClose: false
})

const emit = defineEmits<Emits>()

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

function handleRefresh() {
  // Reinitialize API key from URL/localStorage
  api.reinitializeAPIKey()
  emit('refresh')
}

function handleClose() {
  if (props.canClose) {
    emit('close')
  }
}

// Initialize
onMounted(() => {
  // Clear any previous input when modal opens
  apiKeyInput.value = ''
  inputError.value = ''
})
</script>

<style scoped>
.modal-backdrop {
  backdrop-filter: blur(2px);
}

code {
  font-family: 'Courier New', monospace;
  font-size: 0.875rem;
}
</style>