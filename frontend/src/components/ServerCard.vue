<template>
  <div class="card bg-base-100 shadow-md hover:shadow-lg transition-shadow">
    <div class="card-body">
      <!-- Header -->
      <div class="flex justify-between items-start mb-4">
        <div class="flex-1 min-w-0 mr-2">
          <h3 class="card-title text-lg truncate">{{ server.name }}</h3>
          <p class="text-sm text-base-content/70 truncate">
            {{ server.protocol }} • {{ server.url || server.command || 'No endpoint' }}
          </p>
        </div>

        <!-- Status indicator using unified health status -->
        <!-- M-004: Add tooltip showing health.detail if present -->
        <div
          :class="[
            'badge badge-sm flex-shrink-0',
            statusBadgeClass,
            statusTooltip ? 'tooltip tooltip-left' : ''
          ]"
          :data-tip="statusTooltip"
        >
          {{ statusText }}
        </div>
      </div>

      <!-- Stats -->
      <div class="grid grid-cols-2 gap-4 mb-4">
        <div class="stat bg-base-200 rounded-lg p-3">
          <div class="stat-title text-xs">Tools</div>
          <div class="stat-value text-lg">{{ server.tool_count }}</div>
          <div v-if="server.tool_list_token_size" class="stat-desc text-xs">
            {{ server.tool_list_token_size.toLocaleString() }} tokens
          </div>
        </div>
        <div class="stat bg-base-200 rounded-lg p-3">
          <div class="stat-title text-xs">Status</div>
          <div class="stat-value text-lg">
            <div class="flex items-center space-x-1">
              <input
                type="checkbox"
                :checked="server.enabled"
                @change="toggleEnabled"
                class="toggle toggle-sm"
                :disabled="loading"
              />
              <span class="text-sm">{{ server.enabled ? 'Enabled' : 'Disabled' }}</span>
            </div>
          </div>
        </div>
      </div>

      <!-- Error message - suppressed when health.action conveys the issue (FR-018, FR-019) -->
      <div v-if="shouldShowError" class="alert alert-error alert-sm mb-4">
        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
        </svg>
        <span class="text-xs">{{ server.last_error }}</span>
      </div>

      <!-- Quarantine warning -->
      <div v-if="server.quarantined" class="alert alert-warning alert-sm mb-4">
        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.732-.833-2.5 0L3.732 16.5c-.77.833.192 2.5 1.732 2.5z" />
        </svg>
        <span class="text-xs">Server is quarantined</span>
      </div>

      <!-- Actions - uses unified health.action when available -->
      <div class="card-actions justify-end space-x-2">
        <!-- Primary action button based on health.action -->
        <button
          v-if="healthAction === 'approve'"
          @click="unquarantine"
          :disabled="loading"
          class="btn btn-sm btn-warning"
        >
          <span v-if="loading" class="loading loading-spinner loading-xs"></span>
          Approve
        </button>

        <button
          v-if="healthAction === 'enable'"
          @click="enableServer"
          :disabled="loading"
          class="btn btn-sm btn-primary"
        >
          <span v-if="loading" class="loading loading-spinner loading-xs"></span>
          Enable
        </button>

        <button
          v-if="healthAction === 'login'"
          @click="triggerOAuth"
          :disabled="loading"
          class="btn btn-sm btn-primary"
        >
          <span v-if="loading" class="loading loading-spinner loading-xs"></span>
          Login
        </button>

        <button
          v-if="healthAction === 'restart'"
          @click="restart"
          :disabled="loading"
          class="btn btn-sm btn-primary"
        >
          <span v-if="loading" class="loading loading-spinner loading-xs"></span>
          Restart
        </button>

        <router-link
          v-if="healthAction === 'view_logs'"
          :to="`/servers/${server.name}?tab=logs`"
          class="btn btn-sm btn-primary"
        >
          View Logs
        </router-link>

        <router-link
          v-if="healthAction === 'set_secret'"
          to="/secrets"
          class="btn btn-sm btn-primary"
        >
          Set Secret
        </router-link>

        <router-link
          v-if="healthAction === 'configure'"
          :to="`/servers/${server.name}?tab=config`"
          class="btn btn-sm btn-primary"
        >
          Configure
        </router-link>

        <!-- Logout button (only when connected with OAuth) -->
        <button
          v-if="canLogout"
          @click="triggerLogout"
          :disabled="loading"
          class="btn btn-sm btn-outline btn-warning"
        >
          <span v-if="loading" class="loading loading-spinner loading-xs"></span>
          Logout
        </button>

        <router-link
          :to="`/servers/${server.name}`"
          class="btn btn-sm btn-outline"
        >
          Details
        </router-link>

        <button
          @click="showDeleteConfirmation = true"
          :disabled="loading"
          class="btn btn-sm btn-error"
        >
          Delete
        </button>
      </div>
    </div>

    <!-- Delete Confirmation Modal -->
    <div v-if="showDeleteConfirmation" class="modal modal-open">
      <div class="modal-box">
        <h3 class="font-bold text-lg mb-4">Delete Server</h3>
        <p class="mb-4">
          Are you sure you want to delete the server <strong>{{ server.name }}</strong>?
        </p>
        <p class="text-sm text-base-content/70 mb-6">
          This action cannot be undone. The server will be removed from your configuration.
        </p>
        <div class="modal-action">
          <button
            @click="showDeleteConfirmation = false"
            :disabled="loading"
            class="btn btn-outline"
          >
            Cancel
          </button>
          <button
            @click="confirmDelete"
            :disabled="loading"
            class="btn btn-error"
          >
            <span v-if="loading" class="loading loading-spinner loading-xs"></span>
            Delete Server
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import type { Server } from '@/types'
import { useServersStore } from '@/stores/servers'
import { useSystemStore } from '@/stores/system'

interface Props {
  server: Server
}

const props = defineProps<Props>()

const serversStore = useServersStore()
const systemStore = useSystemStore()
const loading = ref(false)
const showDeleteConfirmation = ref(false)

const isHttpProtocol = computed(() => {
  return props.server.protocol === 'http' || props.server.protocol === 'streamable-http'
})

// Unified health status computed properties
const statusBadgeClass = computed(() => {
  const health = props.server.health
  if (health) {
    // Use admin_state for disabled/quarantined, otherwise use health level
    switch (health.admin_state) {
      case 'disabled':
        return 'badge-neutral' // gray
      case 'quarantined':
        return 'badge-secondary' // purple-ish
      default:
        // Use health level
        switch (health.level) {
          case 'healthy':
            return 'badge-success'
          case 'degraded':
            return 'badge-warning'
          case 'unhealthy':
            return 'badge-error'
          default:
            return 'badge-ghost'
        }
    }
  }
  // Fallback to legacy logic
  if (props.server.connected) return 'badge-success'
  if (props.server.connecting) return 'badge-warning'
  return 'badge-error'
})

const statusText = computed(() => {
  const health = props.server.health
  if (health) {
    return health.summary || health.level
  }
  // Fallback to legacy logic
  if (props.server.connected) return 'Connected'
  if (props.server.connecting) return 'Connecting'
  return 'Disconnected'
})

// M-004: Tooltip showing health.detail if present (for additional context)
const statusTooltip = computed(() => {
  const health = props.server.health
  if (health?.detail) {
    return health.detail
  }
  return ''
})

// Suggested action from health status
const healthAction = computed(() => {
  return props.server.health?.action || ''
})

// Determine if error message should be shown (FR-018, FR-019)
// Suppress verbose last_error when health.action already conveys the issue
const shouldShowError = computed(() => {
  // No error to show
  if (!props.server.last_error) return false

  // Actions where the button is sufficient - error is redundant (T043-T046)
  const actionsSuppressingError = ['login', 'set_secret', 'configure']
  if (actionsSuppressingError.includes(healthAction.value)) {
    return false
  }

  // Show error for other cases (restart, view_logs, or no action)
  return true
})

const canLogout = computed(() => {
  // Don't show Logout button if server is disabled
  if (!props.server.enabled) return false

  // Don't show Logout if user already explicitly logged out
  if (props.server.user_logged_out) return false

  if (!isHttpProtocol.value) return false

  const hasToken = props.server.authenticated === true
  if (!hasToken) return false

  // Show Logout when:
  // 1. Connected with valid token (normal case)
  // 2. Has error but token is still valid (user may want to clear token to re-authenticate)
  //
  // Don't show Logout when:
  // - Disconnected without error and token expired (show Login instead)
  // - Server is connecting (wait for connection to complete)

  if (props.server.connecting) return false

  // If connected, always show Logout (user can log out of working connection)
  if (props.server.connected) return true

  // If not connected but has error, check if it's an OAuth authentication error
  // If OAuth auth is required, show Login instead of Logout
  if (props.server.last_error) {
    // Don't show Logout if oauth_status says token is expired
    // In that case, Login is more appropriate
    if (props.server.oauth_status === 'expired') return false

    // Don't show Logout if the error indicates OAuth authentication is required
    // This means the stored token isn't valid, so Login is more appropriate
    const isOAuthRequired = props.server.last_error.includes('OAuth authentication required') ||
      props.server.last_error.includes('authorization') ||
      props.server.last_error.includes('401') ||
      props.server.last_error.includes('invalid_token')
    if (isOAuthRequired) return false

    return true
  }

  // Not connected, no error, has token - mcpproxy is likely trying to reconnect
  // Show Logout only if token is still valid (authenticated status)
  if (props.server.oauth_status === 'authenticated') return true

  return false
})

async function toggleEnabled() {
  loading.value = true
  try {
    if (props.server.enabled) {
      await serversStore.disableServer(props.server.name)
      systemStore.addToast({
        type: 'success',
        title: 'Server Disabled',
        message: `${props.server.name} has been disabled`,
      })
    } else {
      await serversStore.enableServer(props.server.name)
      systemStore.addToast({
        type: 'success',
        title: 'Server Enabled',
        message: `${props.server.name} has been enabled`,
      })
    }
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Operation Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    loading.value = false
  }
}

async function enableServer() {
  loading.value = true
  try {
    await serversStore.enableServer(props.server.name)
    systemStore.addToast({
      type: 'success',
      title: 'Server Enabled',
      message: `${props.server.name} has been enabled`,
    })
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Enable Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    loading.value = false
  }
}

async function restart() {
  loading.value = true
  try {
    await serversStore.restartServer(props.server.name)
    systemStore.addToast({
      type: 'success',
      title: 'Server Restarted',
      message: `${props.server.name} is restarting`,
    })
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Restart Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    loading.value = false
  }
}

async function triggerOAuth() {
  loading.value = true
  try {
    await serversStore.triggerOAuthLogin(props.server.name)
    systemStore.addToast({
      type: 'success',
      title: 'OAuth Login Triggered',
      message: `Check your browser for ${props.server.name} login`,
    })
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'OAuth Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    loading.value = false
  }
}

async function triggerLogout() {
  loading.value = true
  try {
    await serversStore.triggerOAuthLogout(props.server.name)
    systemStore.addToast({
      type: 'success',
      title: 'OAuth Logout Successful',
      message: `${props.server.name} has been logged out`,
    })
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Logout Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    loading.value = false
  }
}

async function unquarantine() {
  loading.value = true
  try {
    await serversStore.unquarantineServer(props.server.name)
    systemStore.addToast({
      type: 'success',
      title: 'Server Unquarantined',
      message: `${props.server.name} has been removed from quarantine`,
    })
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Unquarantine Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    loading.value = false
  }
}

async function confirmDelete() {
  loading.value = true
  try {
    await serversStore.deleteServer(props.server.name)
    systemStore.addToast({
      type: 'success',
      title: 'Server Deleted',
      message: `${props.server.name} has been deleted successfully`,
    })
    showDeleteConfirmation.value = false
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Delete Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    loading.value = false
  }
}
</script>