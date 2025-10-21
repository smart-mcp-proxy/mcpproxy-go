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

        <!-- Status indicator -->
        <div
          :class="[
            'badge badge-sm flex-shrink-0',
            server.connected ? 'badge-success' :
            server.connecting ? 'badge-warning' :
            'badge-error'
          ]"
        >
          {{ server.connected ? 'Connected' : server.connecting ? 'Connecting' : 'Disconnected' }}
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

      <!-- Error message -->
      <div v-if="server.last_error" class="alert alert-error alert-sm mb-4">
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

      <!-- Actions -->
      <div class="card-actions justify-end space-x-2">
        <button
          v-if="server.quarantined"
          @click="unquarantine"
          :disabled="loading"
          class="btn btn-sm btn-warning"
        >
          <span v-if="loading" class="loading loading-spinner loading-xs"></span>
          Unquarantine
        </button>

        <button
          v-if="!server.connected && server.enabled"
          @click="restart"
          :disabled="loading"
          class="btn btn-sm btn-outline"
        >
          <span v-if="loading" class="loading loading-spinner loading-xs"></span>
          Restart
        </button>

        <button
          v-if="needsOAuth"
          @click="triggerOAuth"
          :disabled="loading"
          class="btn btn-sm btn-primary"
        >
          <span v-if="loading" class="loading loading-spinner loading-xs"></span>
          Login
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

const needsOAuth = computed(() => {
  // Check if server requires OAuth authentication
  const isHttpProtocol = props.server.protocol === 'http' || props.server.protocol === 'streamable-http'
  const notConnected = !props.server.connected
  const isEnabled = props.server.enabled

  // Check for OAuth-related errors in last_error
  const hasOAuthError = props.server.last_error && (
    props.server.last_error.includes('authorization') ||
    props.server.last_error.includes('OAuth') ||
    props.server.last_error.includes('401') ||
    props.server.last_error.includes('invalid_token') ||
    props.server.last_error.includes('Missing or invalid access token')
  )

  // Check if server has OAuth configuration
  const hasOAuthConfig = props.server.oauth !== null && props.server.oauth !== undefined

  // Check if server is authenticated
  const notAuthenticated = !props.server.authenticated

  return isHttpProtocol && notConnected && isEnabled && (hasOAuthError || (hasOAuthConfig && notAuthenticated))
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