<template>
  <div class="p-4 max-w-6xl mx-auto">
    <div class="flex justify-between items-center mb-6">
      <h1 class="text-2xl font-bold">Server Management</h1>
    </div>

    <div v-if="loading" class="flex justify-center py-8">
      <span class="loading loading-spinner loading-lg"></span>
    </div>

    <div v-else-if="servers.length === 0" class="text-base-content/50 py-8 text-center">
      No servers configured. Add servers in the configuration file.
    </div>

    <div v-else class="space-y-2">
      <div v-for="server in servers" :key="server.name"
           class="card bg-base-100 shadow-sm">
        <div class="card-body py-3 px-4 flex-row items-center justify-between">
          <div class="flex items-center gap-2 flex-wrap">
            <span class="font-medium">{{ server.name }}</span>
            <span v-if="server.shared" class="badge badge-sm badge-primary">shared</span>
            <span v-else class="badge badge-sm badge-ghost">private</span>
            <span class="badge badge-sm" :class="server.enabled ? 'badge-success' : 'badge-error'">
              {{ server.enabled ? 'enabled' : 'disabled' }}
            </span>
            <span v-if="server.quarantined" class="badge badge-sm badge-warning">quarantined</span>
            <span class="text-sm text-base-content/50">{{ server.protocol }}</span>
            <span v-if="server.url" class="text-xs text-base-content/40 truncate max-w-xs">{{ server.url }}</span>
          </div>
          <div class="flex gap-2 flex-shrink-0">
            <button
              class="btn btn-sm"
              :class="server.shared ? 'btn-warning' : 'btn-primary'"
              @click="toggleShared(server)"
              :disabled="togglingServer === server.name"
            >
              <span v-if="togglingServer === server.name" class="loading loading-spinner loading-xs"></span>
              {{ server.shared ? 'Make Private' : 'Share with Users' }}
            </button>
          </div>
        </div>
      </div>
    </div>

    <div v-if="error" class="alert alert-error mt-4">
      <span>{{ error }}</span>
      <button class="btn btn-ghost btn-xs" @click="error = ''">Dismiss</button>
    </div>

    <div v-if="successMsg" class="alert alert-success mt-4">
      <span>{{ successMsg }}</span>
      <button class="btn btn-ghost btn-xs" @click="successMsg = ''">Dismiss</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'

interface AdminServer {
  name: string
  url?: string
  protocol: string
  enabled: boolean
  quarantined: boolean
  shared: boolean
}

const loading = ref(true)
const error = ref('')
const successMsg = ref('')
const servers = ref<AdminServer[]>([])
const togglingServer = ref('')

async function fetchServers() {
  loading.value = true
  error.value = ''
  try {
    const res = await fetch('/api/v1/admin/servers', { credentials: 'include' })
    if (!res.ok) throw new Error(`HTTP ${res.status}: ${res.statusText}`)
    const data = await res.json()
    servers.value = Array.isArray(data) ? data : []
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to load servers'
  } finally {
    loading.value = false
  }
}

async function toggleShared(server: AdminServer) {
  togglingServer.value = server.name
  error.value = ''
  successMsg.value = ''
  try {
    const newShared = !server.shared
    const res = await fetch(`/api/v1/admin/servers/${encodeURIComponent(server.name)}/shared`, {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ shared: newShared }),
    })
    if (!res.ok) {
      const data = await res.json().catch(() => ({}))
      throw new Error(data.message || `HTTP ${res.status}`)
    }
    successMsg.value = `Server "${server.name}" is now ${newShared ? 'shared with all users' : 'private'}.`
    await fetchServers()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to update server'
  } finally {
    togglingServer.value = ''
  }
}

onMounted(() => {
  fetchServers()
})
</script>
