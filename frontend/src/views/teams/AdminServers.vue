<template>
  <div class="p-4 max-w-7xl mx-auto">
    <div class="flex justify-between items-center mb-6">
      <div>
        <h1 class="text-2xl font-bold">Server Management</h1>
        <p class="text-sm text-base-content/60 mt-1">Manage upstream MCP servers. Shared servers are available to all users.</p>
      </div>
    </div>

    <!-- Stats row -->
    <div class="grid grid-cols-4 gap-3 mb-6">
      <div class="stat bg-base-100 rounded-lg shadow-sm p-3">
        <div class="stat-title text-xs">Total</div>
        <div class="stat-value text-lg">{{ servers.length }}</div>
      </div>
      <div class="stat bg-base-100 rounded-lg shadow-sm p-3">
        <div class="stat-title text-xs">Connected</div>
        <div class="stat-value text-lg text-success">{{ connectedCount }}</div>
      </div>
      <div class="stat bg-base-100 rounded-lg shadow-sm p-3">
        <div class="stat-title text-xs">Shared</div>
        <div class="stat-value text-lg text-info">{{ sharedCount }}</div>
      </div>
      <div class="stat bg-base-100 rounded-lg shadow-sm p-3">
        <div class="stat-title text-xs">Disabled</div>
        <div class="stat-value text-lg text-base-content/40">{{ disabledCount }}</div>
      </div>
    </div>

    <!-- Loading -->
    <div v-if="loading" class="flex justify-center py-8">
      <span class="loading loading-spinner loading-lg"></span>
    </div>

    <template v-else>
      <!-- Search/filter bar -->
      <div class="flex gap-2 mb-4">
        <input v-model="searchQuery" type="text" placeholder="Filter servers..." class="input input-bordered input-sm flex-1" />
        <select v-model="statusFilter" class="select select-bordered select-sm">
          <option value="">All Status</option>
          <option value="enabled">Enabled</option>
          <option value="disabled">Disabled</option>
        </select>
        <select v-model="shareFilter" class="select select-bordered select-sm">
          <option value="">All</option>
          <option value="shared">Shared</option>
          <option value="private">Private</option>
        </select>
      </div>

      <!-- Empty state -->
      <div v-if="servers.length === 0" class="text-base-content/50 py-8 text-center">
        No servers configured. Add servers in the configuration file.
      </div>

      <!-- Server table -->
      <div v-else class="overflow-x-auto">
        <table class="table table-sm w-full">
          <thead>
            <tr class="text-xs uppercase text-base-content/50">
              <th>Server</th>
              <th>Protocol</th>
              <th>Endpoint</th>
              <th>Status</th>
              <th>Sharing</th>
              <th class="text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="server in filteredServers" :key="server.name"
                class="hover:bg-base-200/50 cursor-pointer transition-colors"
                @click="navigateToDetail(server)">
              <td class="font-medium">
                {{ server.name }}
              </td>
              <td>
                <span class="badge badge-ghost badge-xs">{{ server.protocol }}</span>
              </td>
              <td class="text-xs text-base-content/50 truncate max-w-xs">
                {{ server.url || server.command || '\u2014' }}
              </td>
              <td>
                <span class="badge badge-xs" :class="statusBadge(server)">
                  {{ statusLabel(server) }}
                </span>
              </td>
              <td>
                <span v-if="server.shared" class="badge badge-info badge-xs">shared</span>
                <span v-else class="badge badge-ghost badge-xs">private</span>
              </td>
              <td class="text-right" @click.stop>
                <div class="dropdown dropdown-end">
                  <label tabindex="0" class="btn btn-ghost btn-xs btn-square">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 5v.01M12 12v.01M12 19v.01" />
                    </svg>
                  </label>
                  <ul tabindex="0" class="dropdown-content z-[1] menu p-1 shadow-lg bg-base-100 rounded-lg w-48 border border-base-300">
                    <li><a @click="toggleEnabled(server)">
                      {{ server.enabled ? 'Disable' : 'Enable' }}
                    </a></li>
                    <li><a @click="restartServer(server)" :class="{ 'opacity-50': !server.enabled }">
                      Restart
                    </a></li>
                    <li class="border-t border-base-200 mt-1 pt-1"><a @click="toggleShared(server)">
                      {{ server.shared ? 'Make Private' : 'Share with Users' }}
                    </a></li>
                  </ul>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <!-- Error alert -->
    <div v-if="error" class="alert alert-error mt-4">
      <span>{{ error }}</span>
      <button class="btn btn-ghost btn-xs" @click="error = ''">Dismiss</button>
    </div>

    <!-- Success toast -->
    <div v-if="successMsg" class="toast toast-end toast-bottom">
      <div class="alert alert-success">
        <span>{{ successMsg }}</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'

interface AdminServer {
  name: string
  url?: string
  command?: string
  protocol: string
  enabled: boolean
  connected: boolean
  quarantined: boolean
  shared: boolean
  health?: {
    level: string
    admin_state: string
    summary: string
  }
}

const router = useRouter()
const loading = ref(true)
const error = ref('')
const successMsg = ref('')
const servers = ref<AdminServer[]>([])
const searchQuery = ref('')
const statusFilter = ref('')
const shareFilter = ref('')

const connectedCount = computed(() => servers.value.filter(s => s.enabled && s.connected).length)
const sharedCount = computed(() => servers.value.filter(s => s.shared).length)
const disabledCount = computed(() => servers.value.filter(s => !s.enabled).length)

const filteredServers = computed(() => {
  let result = servers.value

  if (searchQuery.value) {
    const q = searchQuery.value.toLowerCase()
    result = result.filter(s =>
      s.name.toLowerCase().includes(q) ||
      (s.url && s.url.toLowerCase().includes(q)) ||
      (s.command && s.command.toLowerCase().includes(q)) ||
      s.protocol.toLowerCase().includes(q)
    )
  }

  if (statusFilter.value === 'enabled') {
    result = result.filter(s => s.enabled)
  } else if (statusFilter.value === 'disabled') {
    result = result.filter(s => !s.enabled)
  }

  if (shareFilter.value === 'shared') {
    result = result.filter(s => s.shared)
  } else if (shareFilter.value === 'private') {
    result = result.filter(s => !s.shared)
  }

  return result
})

function statusBadge(server: AdminServer): string {
  if (server.quarantined) return 'badge-error'
  if (!server.enabled) return 'badge-ghost'
  if (server.health) {
    switch (server.health.level) {
      case 'healthy': return 'badge-success'
      case 'degraded': return 'badge-warning'
      case 'unhealthy': return 'badge-error'
    }
  }
  return server.connected ? 'badge-success' : 'badge-warning'
}

function statusLabel(server: AdminServer): string {
  if (server.quarantined) return 'quarantined'
  if (!server.enabled) return 'disabled'
  if (server.health) return server.health.level
  return server.connected ? 'connected' : 'disconnected'
}

function navigateToDetail(server: AdminServer) {
  router.push('/servers/' + encodeURIComponent(server.name))
}

async function fetchServers() {
  loading.value = true
  error.value = ''
  try {
    const res = await fetch('/api/v1/admin/servers', { credentials: 'include' })
    if (!res.ok) throw new Error(`HTTP ${res.status}: ${res.statusText}`)
    const data = await res.json()
    // Handle both response formats: array or object with servers key
    if (Array.isArray(data)) {
      servers.value = data
    } else if (data && Array.isArray(data.servers)) {
      servers.value = data.servers
    } else {
      servers.value = []
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to load servers'
  } finally {
    loading.value = false
  }
}

async function toggleEnabled(server: AdminServer) {
  error.value = ''
  successMsg.value = ''
  try {
    const action = server.enabled ? 'disable' : 'enable'
    const res = await fetch(`/api/v1/admin/servers/${encodeURIComponent(server.name)}/${action}`, {
      method: 'POST',
      credentials: 'include',
    })
    if (!res.ok) {
      const data = await res.json().catch(() => ({}))
      throw new Error(data.message || data.error || `HTTP ${res.status}`)
    }
    successMsg.value = `Server "${server.name}" ${server.enabled ? 'disabled' : 'enabled'}.`
    await fetchServers()
    clearSuccessAfterDelay()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to update server'
  }
}

async function restartServer(server: AdminServer) {
  if (!server.enabled) return
  error.value = ''
  successMsg.value = ''
  try {
    const res = await fetch(`/api/v1/admin/servers/${encodeURIComponent(server.name)}/restart`, {
      method: 'POST',
      credentials: 'include',
    })
    if (!res.ok) {
      const data = await res.json().catch(() => ({}))
      throw new Error(data.message || data.error || `HTTP ${res.status}`)
    }
    successMsg.value = `Server "${server.name}" restarted.`
    await fetchServers()
    clearSuccessAfterDelay()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to restart server'
  }
}

async function toggleShared(server: AdminServer) {
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
      throw new Error(data.message || data.error || `HTTP ${res.status}`)
    }
    successMsg.value = `Server "${server.name}" is now ${newShared ? 'shared with all users' : 'private'}.`
    await fetchServers()
    clearSuccessAfterDelay()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to update server'
  }
}

function clearSuccessAfterDelay() {
  setTimeout(() => {
    successMsg.value = ''
  }, 3000)
}

onMounted(() => {
  fetchServers()
})
</script>
