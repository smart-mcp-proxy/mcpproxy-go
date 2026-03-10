<template>
  <div class="p-4 max-w-6xl mx-auto">
    <div class="flex justify-between items-center mb-6">
      <h1 class="text-2xl font-bold">My Servers</h1>
      <button class="btn btn-primary btn-sm" @click="showAddModal = true">
        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
        </svg>
        Add Personal Server
      </button>
    </div>

    <!-- Personal Servers -->
    <div class="mb-8">
      <h2 class="text-lg font-semibold mb-3">Personal Servers</h2>
      <div v-if="loading" class="flex justify-center py-8">
        <span class="loading loading-spinner loading-lg"></span>
      </div>
      <div v-else-if="servers.personal.length === 0" class="text-base-content/50 py-8 text-center">
        No personal servers yet. Click "Add Personal Server" to get started.
      </div>
      <div v-else class="overflow-x-auto">
        <table class="table table-sm w-full">
          <thead>
            <tr class="text-xs uppercase text-base-content/50">
              <th>Server</th>
              <th>Protocol</th>
              <th>Endpoint</th>
              <th>Status</th>
              <th>Tools</th>
              <th class="text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="server in servers.personal" :key="server.name"
                class="hover:bg-base-200/50 cursor-pointer transition-colors"
                @click="navigateToDetail(server)">
              <td class="font-medium">{{ server.name }}</td>
              <td>
                <span class="badge badge-ghost badge-xs">{{ server.protocol }}</span>
              </td>
              <td class="text-xs text-base-content/50 truncate max-w-xs">
                {{ server.url || server.command || '\u2014' }}
              </td>
              <td>
                <span class="badge badge-xs" :class="healthBadgeClass(server)">
                  {{ healthLabel(server) }}
                </span>
              </td>
              <td class="text-xs text-base-content/60">
                {{ server.tool_count != null ? server.tool_count : '\u2014' }}
              </td>
              <td class="text-right" @click.stop>
                <div class="dropdown dropdown-end">
                  <label tabindex="0" class="btn btn-ghost btn-xs btn-square">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 5v.01M12 12v.01M12 19v.01" />
                    </svg>
                  </label>
                  <ul tabindex="0" class="dropdown-content z-[1] menu p-1 shadow-lg bg-base-100 rounded-lg w-40 border border-base-300">
                    <li><a @click="toggleServer(server)" :disabled="togglingServer === server.name">
                      {{ server.enabled ? 'Disable' : 'Enable' }}
                    </a></li>
                    <li class="border-t border-base-200 mt-1 pt-1"><a class="text-error" @click="confirmRemoveServer(server.name)" :disabled="removingServer === server.name">
                      Remove
                    </a></li>
                  </ul>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- Shared Servers -->
    <div>
      <h2 class="text-lg font-semibold mb-3">Shared Servers</h2>
      <div v-if="servers.shared.length === 0" class="rounded-lg border border-base-300 bg-base-200/30 py-8 text-center">
        <svg class="w-10 h-10 mx-auto mb-3 text-base-content/30" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2" />
        </svg>
        <p class="text-base-content/50 text-sm">No shared servers available.</p>
        <p class="text-base-content/40 text-xs mt-1">Your administrator can share servers from the Server Management page.</p>
      </div>
      <div v-else class="overflow-x-auto">
        <table class="table table-sm w-full">
          <thead>
            <tr class="text-xs uppercase text-base-content/50">
              <th>Server</th>
              <th>Protocol</th>
              <th>Endpoint</th>
              <th>Status</th>
              <th>Tools</th>
              <th class="text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="server in servers.shared" :key="server.name"
                class="hover:bg-base-200/50 cursor-pointer transition-colors"
                :class="{ 'opacity-50': isSharedDisabled(server) }"
                @click="navigateToDetail(server)">
              <td class="font-medium">
                {{ server.name }}
                <span class="badge badge-info badge-xs ml-1">shared</span>
              </td>
              <td>
                <span class="badge badge-ghost badge-xs">{{ server.protocol }}</span>
              </td>
              <td class="text-xs text-base-content/50 truncate max-w-xs">
                {{ server.url || server.command || '\u2014' }}
              </td>
              <td>
                <span class="badge badge-xs" :class="healthBadgeClass(server)">
                  {{ isSharedDisabled(server) ? 'disabled by you' : healthLabel(server) }}
                </span>
              </td>
              <td class="text-xs text-base-content/60">
                {{ server.tool_count != null ? server.tool_count : '\u2014' }}
              </td>
              <td class="text-right" @click.stop>
                <button class="btn btn-ghost btn-xs" @click="toggleSharedServer(server)" :disabled="togglingServer === server.name">
                  <span v-if="togglingServer === server.name" class="loading loading-spinner loading-xs"></span>
                  {{ isSharedDisabled(server) ? 'Enable' : 'Disable' }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- Error Alert -->
    <div v-if="error" class="alert alert-error mt-4">
      <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <span>{{ error }}</span>
      <button class="btn btn-ghost btn-xs" @click="error = ''">Dismiss</button>
    </div>

    <!-- Add Server Modal -->
    <dialog class="modal" :class="{ 'modal-open': showAddModal }">
      <div class="modal-box">
        <h3 class="font-bold text-lg mb-4">Add Personal Server</h3>
        <form @submit.prevent="addServer">
          <div class="form-control mb-3">
            <label class="label"><span class="label-text">Name</span></label>
            <input v-model="newServer.name" type="text" class="input input-bordered" required placeholder="my-server" />
          </div>
          <div class="form-control mb-3">
            <label class="label"><span class="label-text">Protocol</span></label>
            <select v-model="newServer.protocol" class="select select-bordered">
              <option value="http">HTTP</option>
              <option value="sse">SSE</option>
              <option value="streamable-http">Streamable HTTP</option>
              <option value="stdio">stdio</option>
            </select>
          </div>
          <div v-if="newServer.protocol !== 'stdio'" class="form-control mb-3">
            <label class="label"><span class="label-text">URL</span></label>
            <input v-model="newServer.url" type="text" class="input input-bordered" placeholder="https://..." required />
          </div>
          <div v-if="newServer.protocol === 'stdio'" class="form-control mb-3">
            <label class="label"><span class="label-text">Command</span></label>
            <input v-model="newServer.command" type="text" class="input input-bordered" placeholder="npx" required />
          </div>
          <div v-if="newServer.protocol === 'stdio'" class="form-control mb-3">
            <label class="label"><span class="label-text">Arguments (one per line)</span></label>
            <textarea v-model="newServer.args" class="textarea textarea-bordered" placeholder="@modelcontextprotocol/server-filesystem&#10;/path/to/dir" rows="3"></textarea>
          </div>
          <div v-if="addError" class="alert alert-error mb-3 text-sm">{{ addError }}</div>
          <div class="modal-action">
            <button type="button" class="btn" @click="closeAddModal">Cancel</button>
            <button type="submit" class="btn btn-primary" :disabled="adding">
              <span v-if="adding" class="loading loading-spinner loading-xs"></span>
              {{ adding ? 'Adding...' : 'Add Server' }}
            </button>
          </div>
        </form>
      </div>
      <form method="dialog" class="modal-backdrop" @click="closeAddModal"></form>
    </dialog>

    <!-- Remove Confirmation Modal -->
    <dialog class="modal" :class="{ 'modal-open': !!serverToRemove }">
      <div class="modal-box">
        <h3 class="font-bold text-lg">Remove Server</h3>
        <p class="py-4">Are you sure you want to remove <strong>{{ serverToRemove }}</strong>? This action cannot be undone.</p>
        <div class="modal-action">
          <button class="btn" @click="serverToRemove = ''">Cancel</button>
          <button class="btn btn-error" @click="removeServer" :disabled="removingServer === serverToRemove">
            <span v-if="removingServer === serverToRemove" class="loading loading-spinner loading-xs"></span>
            Remove
          </button>
        </div>
      </div>
      <form method="dialog" class="modal-backdrop" @click="serverToRemove = ''"></form>
    </dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted, computed } from 'vue'
import { useRouter } from 'vue-router'

interface UserServer {
  name: string
  url?: string
  command?: string
  protocol: string
  enabled: boolean
  connected: boolean
  owner_type: 'personal' | 'shared'
  user_enabled?: boolean | null
  tool_count?: number
  health?: {
    level: string
    summary: string
  }
}

const router = useRouter()
const loading = ref(true)
const error = ref('')
const allServers = ref<UserServer[]>([])
const showAddModal = ref(false)
const adding = ref(false)
const addError = ref('')
const togglingServer = ref('')
const removingServer = ref('')
const serverToRemove = ref('')

const newServer = reactive({
  name: '',
  url: '',
  protocol: 'http',
  command: '',
  args: '',
})

const servers = computed(() => ({
  personal: allServers.value.filter(s => s.owner_type === 'personal'),
  shared: allServers.value.filter(s => s.owner_type === 'shared'),
}))

function healthBadgeClass(server: UserServer): string {
  if (!server.health) {
    return server.enabled ? (server.connected ? 'badge-success' : 'badge-warning') : 'badge-ghost'
  }
  switch (server.health.level) {
    case 'healthy': return 'badge-success'
    case 'degraded': return 'badge-warning'
    case 'unhealthy': return 'badge-error'
    default: return 'badge-ghost'
  }
}

function healthLabel(server: UserServer): string {
  if (!server.health) {
    return server.enabled ? (server.connected ? 'connected' : 'disconnected') : 'disabled'
  }
  return server.health.level
}

function navigateToDetail(server: UserServer) {
  router.push('/servers/' + encodeURIComponent(server.name))
}

async function fetchServers() {
  loading.value = true
  error.value = ''
  try {
    const res = await fetch('/api/v1/user/servers', { credentials: 'include' })
    if (!res.ok) throw new Error(`HTTP ${res.status}: ${res.statusText}`)
    const data = await res.json()
    const personal = (data.personal || []).map((s: any) => ({ ...s, owner_type: 'personal' }))
    const shared = (data.shared || []).map((s: any) => ({ ...s, owner_type: 'shared', user_enabled: s.user_enabled ?? null }))
    allServers.value = [...personal, ...shared]
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to load servers'
  } finally {
    loading.value = false
  }
}

function isSharedDisabled(server: UserServer): boolean {
  return server.user_enabled === false
}

async function toggleSharedServer(server: UserServer) {
  togglingServer.value = server.name
  error.value = ''
  try {
    const newEnabled = isSharedDisabled(server) ? true : false
    const res = await fetch(`/api/v1/user/servers/${encodeURIComponent(server.name)}/enable`, {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ enabled: newEnabled }),
    })
    if (!res.ok) {
      const data = await res.json().catch(() => ({}))
      throw new Error(data.message || `HTTP ${res.status}`)
    }
    await fetchServers()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to update server'
  } finally {
    togglingServer.value = ''
  }
}

async function toggleServer(server: UserServer) {
  togglingServer.value = server.name
  error.value = ''
  try {
    const action = server.enabled ? 'disable' : 'enable'
    const res = await fetch(`/api/v1/user/servers/${encodeURIComponent(server.name)}/${action}`, {
      method: 'POST',
      credentials: 'include',
    })
    if (!res.ok) {
      const data = await res.json().catch(() => ({}))
      throw new Error(data.error || `HTTP ${res.status}`)
    }
    await fetchServers()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to update server'
  } finally {
    togglingServer.value = ''
  }
}

function confirmRemoveServer(name: string) {
  serverToRemove.value = name
}

async function removeServer() {
  const name = serverToRemove.value
  if (!name) return
  removingServer.value = name
  error.value = ''
  try {
    const res = await fetch(`/api/v1/user/servers/${encodeURIComponent(name)}`, {
      method: 'DELETE',
      credentials: 'include',
    })
    if (!res.ok) {
      const data = await res.json().catch(() => ({}))
      throw new Error(data.error || `HTTP ${res.status}`)
    }
    serverToRemove.value = ''
    await fetchServers()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to remove server'
  } finally {
    removingServer.value = ''
  }
}

async function addServer() {
  adding.value = true
  addError.value = ''
  try {
    const body: Record<string, unknown> = {
      name: newServer.name,
      protocol: newServer.protocol,
      enabled: true,
    }
    if (newServer.protocol === 'stdio') {
      body.command = newServer.command
      if (newServer.args.trim()) {
        body.args = newServer.args.trim().split('\n').map(a => a.trim()).filter(Boolean)
      }
    } else {
      body.url = newServer.url
    }
    const res = await fetch('/api/v1/user/servers', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    if (!res.ok) {
      const data = await res.json().catch(() => ({}))
      throw new Error(data.error || `HTTP ${res.status}`)
    }
    closeAddModal()
    await fetchServers()
  } catch (err) {
    addError.value = err instanceof Error ? err.message : 'Failed to add server'
  } finally {
    adding.value = false
  }
}

function closeAddModal() {
  showAddModal.value = false
  addError.value = ''
  newServer.name = ''
  newServer.url = ''
  newServer.protocol = 'http'
  newServer.command = ''
  newServer.args = ''
}

onMounted(() => {
  fetchServers()
})
</script>
