<template>
  <dialog :open="show" class="modal">
    <div class="modal-box max-w-lg">
      <h3 class="font-bold text-lg mb-2">Connect MCPProxy to AI Agents</h3>
      <p class="text-sm opacity-70 mb-4">
        Register MCPProxy as an MCP server in your AI tools. This modifies the tool's config file (backup created automatically).
      </p>

      <!-- Loading state -->
      <div v-if="loading.initial" class="flex justify-center py-8">
        <span class="loading loading-spinner loading-md"></span>
      </div>

      <!-- Error state -->
      <div v-else-if="error" class="alert alert-error mb-4">
        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
        </svg>
        <span class="text-sm">{{ error }}</span>
      </div>

      <!-- Client list -->
      <div v-else class="space-y-2">
        <div
          v-for="client in clients"
          :key="client.id"
          class="flex items-center justify-between p-3 rounded-lg border border-base-300 hover:bg-base-200/50 transition-colors"
        >
          <div class="flex items-center gap-3 min-w-0 flex-1">
            <div class="w-8 h-8 flex items-center justify-center text-lg flex-shrink-0" :title="client.name">
              {{ clientIcon(client) }}
            </div>
            <div class="min-w-0 flex-1">
              <div class="font-medium text-sm truncate">{{ client.name }}</div>
              <div class="text-xs opacity-50 truncate" :title="client.config_path">{{ client.config_path }}</div>
            </div>
          </div>
          <div class="flex-shrink-0 ml-2">
            <span v-if="!client.supported" class="badge badge-ghost badge-sm">{{ client.reason || 'Not supported' }}</span>
            <span v-else-if="!client.exists" class="text-xs opacity-40">Config not found</span>
            <button
              v-else-if="client.connected"
              @click="disconnect(client.id)"
              class="btn btn-ghost btn-xs text-error"
              :disabled="loading.clients[client.id]"
            >
              <span v-if="loading.clients[client.id]" class="loading loading-spinner loading-xs"></span>
              <span v-else>Disconnect</span>
            </button>
            <button
              v-else
              @click="connect(client.id)"
              class="btn btn-primary btn-xs"
              :disabled="loading.clients[client.id]"
            >
              <span v-if="loading.clients[client.id]" class="loading loading-spinner loading-xs"></span>
              <span v-else>Connect</span>
            </button>
          </div>
        </div>

        <div v-if="clients.length === 0 && !loading.initial" class="text-center py-6 opacity-60">
          <p class="text-sm">No AI clients detected on this system.</p>
        </div>
      </div>

      <!-- Result message -->
      <div v-if="resultMessage" class="mt-3">
        <div class="alert alert-sm" :class="resultSuccess ? 'alert-success' : 'alert-error'">
          <span class="text-sm">{{ resultMessage }}</span>
        </div>
      </div>

      <div class="modal-action">
        <button
          @click="connectAll"
          class="btn btn-primary btn-sm"
          :disabled="allConnected || connectableClients.length === 0"
        >
          Connect All
        </button>
        <button @click="close" class="btn btn-ghost btn-sm">Close</button>
      </div>
    </div>
    <form method="dialog" class="modal-backdrop" @click.prevent="close"><button>close</button></form>
  </dialog>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch } from 'vue'
import api from '@/services/api'
import { useSystemStore } from '@/stores/system'
import type { ClientStatus } from '@/types'

interface Props {
  show: boolean
}

interface Emits {
  (e: 'close'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()
const systemStore = useSystemStore()

const clients = ref<ClientStatus[]>([])
const error = ref<string | null>(null)
const resultMessage = ref('')
const resultSuccess = ref(false)
const loading = reactive({
  initial: false,
  clients: {} as Record<string, boolean>,
})

const connectableClients = computed(() =>
  clients.value.filter(c => c.supported && c.exists && !c.connected)
)

const allConnected = computed(() =>
  connectableClients.value.length === 0
)

function clientIcon(client: ClientStatus): string {
  // Map client icons based on id/name
  const iconMap: Record<string, string> = {
    'claude-desktop': '\u2728',
    'claude-code': '\u{1F4BB}',
    'cursor': '\u{1F4DD}',
    'vscode': '\u{1F4D0}',
    'windsurf': '\u{1F3C4}',
    'zed': '\u26A1',
    'cline': '\u{1F916}',
    'continue': '\u27A1\uFE0F',
  }
  return iconMap[client.id] || client.icon || '\u{1F527}'
}

async function fetchClients() {
  loading.initial = true
  error.value = null

  try {
    const response = await api.getConnectStatus()
    if (response.success && response.data) {
      clients.value = Array.isArray(response.data) ? response.data : []
    } else {
      error.value = response.error || 'Failed to load client status'
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to connect to API'
  } finally {
    loading.initial = false
  }
}

async function connect(clientId: string) {
  loading.clients[clientId] = true
  resultMessage.value = ''

  try {
    const response = await api.connectClient(clientId)
    if (response.success && response.data) {
      resultMessage.value = response.data.message || `Connected to ${clientId}`
      resultSuccess.value = true
      // Update local state
      const client = clients.value.find(c => c.id === clientId)
      if (client) client.connected = true
      systemStore.addToast({
        type: 'success',
        title: 'Client Connected',
        message: `MCPProxy registered in ${clientId}`,
      })
    } else {
      resultMessage.value = response.error || 'Failed to connect'
      resultSuccess.value = false
    }
  } catch (err) {
    resultMessage.value = err instanceof Error ? err.message : 'Unknown error'
    resultSuccess.value = false
  } finally {
    loading.clients[clientId] = false
  }
}

async function disconnect(clientId: string) {
  loading.clients[clientId] = true
  resultMessage.value = ''

  try {
    const response = await api.disconnectClient(clientId)
    if (response.success && response.data) {
      resultMessage.value = response.data.message || `Disconnected from ${clientId}`
      resultSuccess.value = true
      // Update local state
      const client = clients.value.find(c => c.id === clientId)
      if (client) client.connected = false
      systemStore.addToast({
        type: 'info',
        title: 'Client Disconnected',
        message: `MCPProxy removed from ${clientId}`,
      })
    } else {
      resultMessage.value = response.error || 'Failed to disconnect'
      resultSuccess.value = false
    }
  } catch (err) {
    resultMessage.value = err instanceof Error ? err.message : 'Unknown error'
    resultSuccess.value = false
  } finally {
    loading.clients[clientId] = false
  }
}

async function connectAll() {
  for (const client of connectableClients.value) {
    await connect(client.id)
  }
}

function close() {
  resultMessage.value = ''
  emit('close')
}

// Fetch client list when modal opens
watch(() => props.show, (newVal) => {
  if (newVal) {
    fetchClients()
    resultMessage.value = ''
  }
})
</script>
