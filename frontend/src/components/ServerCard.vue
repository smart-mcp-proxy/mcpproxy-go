<template>
  <div class="card bg-base-100 shadow-md hover:shadow-lg transition-shadow">
    <div class="card-body">
      <!-- Header -->
      <div class="flex justify-between items-start mb-4">
        <div>
          <h3 class="card-title text-lg">{{ server.name }}</h3>
          <p class="text-sm text-base-content/70">
            {{ server.protocol }} • {{ server.url || server.command || 'No endpoint' }}
          </p>
        </div>

        <!-- Status indicator -->
        <div
          :class="[
            'badge',
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

const needsOAuth = computed(() => {
  // Simplified check - in reality, you'd check if the server supports OAuth
  // and if authentication is required
  return (props.server.protocol === 'http' || props.server.protocol === 'streamable-http') &&
         !props.server.connected &&
         props.server.enabled &&
         props.server.last_error?.includes('authorization')
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
</script>