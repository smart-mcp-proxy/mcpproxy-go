<template>
  <div class="space-y-6">
    <!-- Loading State -->
    <div v-if="loading" class="text-center py-12">
      <span class="loading loading-spinner loading-lg"></span>
      <p class="mt-4">Loading server details...</p>
    </div>

    <!-- Error State -->
    <div v-else-if="error" class="alert alert-error">
      <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <div>
        <h3 class="font-bold">Failed to load server details</h3>
        <div class="text-sm">{{ error }}</div>
      </div>
      <button @click="loadServerDetails" class="btn btn-sm">
        Try Again
      </button>
    </div>

    <!-- Server Not Found -->
    <div v-else-if="!server" class="text-center py-12">
      <svg class="w-16 h-16 mx-auto mb-4 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2" />
      </svg>
      <h3 class="text-xl font-semibold mb-2">Server not found</h3>
      <p class="text-base-content/70 mb-4">
        The server "{{ serverName }}" was not found.
      </p>
      <router-link to="/servers" class="btn btn-primary">
        Back to Servers
      </router-link>
    </div>

    <!-- Server Details -->
    <div v-else>
      <!-- Header -->
      <div class="flex flex-col lg:flex-row lg:justify-between lg:items-start gap-4">
        <div>
          <div class="breadcrumbs text-sm mb-2">
            <ul>
              <li><router-link to="/servers">Servers</router-link></li>
              <li>{{ server.name }}</li>
            </ul>
          </div>
          <h1 class="text-3xl font-bold">{{ server.name }}</h1>
          <p class="text-base-content/70 mt-1">{{ server.protocol }} • {{ server.url || server.command || 'No endpoint' }}</p>
        </div>

        <div class="flex items-center space-x-2">
          <div
            :class="[
              'badge badge-lg',
              server.connected ? 'badge-success' :
              server.connecting ? 'badge-warning' :
              'badge-error'
            ]"
          >
            {{ server.connected ? 'Connected' : server.connecting ? 'Connecting' : 'Disconnected' }}
          </div>
          <div class="dropdown dropdown-end">
            <div tabindex="0" role="button" class="btn btn-outline">
              Actions
              <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
              </svg>
            </div>
            <ul tabindex="0" class="dropdown-content menu bg-base-100 rounded-box z-[1] w-52 p-2 shadow">
              <li>
                <button @click="toggleEnabled" :disabled="actionLoading">
                  <span v-if="actionLoading" class="loading loading-spinner loading-xs"></span>
                  {{ server.enabled ? 'Disable' : 'Enable' }}
                </button>
              </li>
              <li v-if="server.enabled">
                <button @click="restartServer" :disabled="actionLoading">
                  <span v-if="actionLoading" class="loading loading-spinner loading-xs"></span>
                  Restart
                </button>
              </li>
              <li v-if="needsOAuth">
                <button @click="triggerOAuth" :disabled="actionLoading">
                  <span v-if="actionLoading" class="loading loading-spinner loading-xs"></span>
                  OAuth Login
                </button>
              </li>
              <li>
                <button @click="server.quarantined ? unquarantineServer() : quarantineServer()" :disabled="actionLoading">
                  <span v-if="actionLoading" class="loading loading-spinner loading-xs"></span>
                  {{ server.quarantined ? 'Unquarantine' : 'Quarantine' }}
                </button>
              </li>
              <li>
                <button @click="refreshData" :disabled="actionLoading">
                  <span v-if="actionLoading" class="loading loading-spinner loading-xs"></span>
                  Refresh
                </button>
              </li>
            </ul>
          </div>
        </div>
      </div>

      <!-- Status Cards -->
      <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6">
        <div class="stats shadow bg-base-100">
          <div class="stat">
            <div class="stat-figure text-primary">
              <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
              </svg>
            </div>
            <div class="stat-title">Tools</div>
            <div class="stat-value">{{ serverTools.length }}</div>
            <div class="stat-desc">available tools</div>
          </div>
        </div>

        <div class="stats shadow bg-base-100">
          <div class="stat">
            <div class="stat-figure text-secondary">
              <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
              </svg>
            </div>
            <div class="stat-title">Status</div>
            <div class="stat-value text-sm">{{ server.enabled ? 'Enabled' : 'Disabled' }}</div>
            <div class="stat-desc">{{ server.quarantined ? 'Quarantined' : 'Active' }}</div>
          </div>
        </div>

        <div class="stats shadow bg-base-100">
          <div class="stat">
            <div class="stat-figure text-info">
              <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 10V3L4 14h7v7l9-11h-7z" />
              </svg>
            </div>
            <div class="stat-title">Protocol</div>
            <div class="stat-value text-sm">{{ server.protocol }}</div>
            <div class="stat-desc">communication type</div>
          </div>
        </div>

        <div class="stats shadow bg-base-100">
          <div class="stat">
            <div class="stat-figure text-warning">
              <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
              </svg>
            </div>
            <div class="stat-title">Connection</div>
            <div class="stat-value text-sm">
              {{ server.connected ? 'Online' : server.connecting ? 'Connecting' : 'Offline' }}
            </div>
            <div class="stat-desc">current state</div>
          </div>
        </div>
      </div>

      <!-- Alerts -->
      <div class="space-y-4">
        <div v-if="server.last_error" class="alert alert-error">
          <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <div>
            <h3 class="font-bold">Server Error</h3>
            <div class="text-sm">{{ server.last_error }}</div>
          </div>
        </div>

        <div v-if="server.quarantined" class="alert alert-warning">
          <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.732-.833-2.5 0L3.732 16.5c-.77.833.192 2.5 1.732 2.5z" />
          </svg>
          <div>
            <h3 class="font-bold">Security Quarantine</h3>
            <div class="text-sm">This server is quarantined and requires manual approval before tools can be executed.</div>
          </div>
          <button @click="unquarantineServer" :disabled="actionLoading" class="btn btn-sm btn-warning">
            <span v-if="actionLoading" class="loading loading-spinner loading-xs"></span>
            Unquarantine
          </button>
        </div>
      </div>

      <!-- Tabs -->
      <div class="tabs tabs-bordered">
        <button
          :class="['tab tab-lg', activeTab === 'tools' ? 'tab-active' : '']"
          @click="activeTab = 'tools'"
        >
          Tools ({{ serverTools.length }})
        </button>
        <button
          :class="['tab tab-lg', activeTab === 'logs' ? 'tab-active' : '']"
          @click="activeTab = 'logs'"
        >
          Logs
        </button>
        <button
          :class="['tab tab-lg', activeTab === 'config' ? 'tab-active' : '']"
          @click="activeTab = 'config'"
        >
          Configuration
        </button>
      </div>

      <!-- Tab Content -->
      <div class="mt-6">
        <!-- Tools Tab -->
        <div v-if="activeTab === 'tools'">
          <div v-if="toolsLoading" class="text-center py-8">
            <span class="loading loading-spinner loading-lg"></span>
            <p class="mt-2">Loading tools...</p>
          </div>

          <div v-else-if="toolsError" class="alert alert-error">
            <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            <span>{{ toolsError }}</span>
            <button @click="loadTools" class="btn btn-sm">Retry</button>
          </div>

          <div v-else-if="serverTools.length === 0" class="text-center py-8">
            <svg class="w-16 h-16 mx-auto mb-4 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
            </svg>
            <h3 class="text-xl font-semibold mb-2">No tools available</h3>
            <p class="text-base-content/70">
              {{ server.connected ? 'This server has no tools available.' : 'Server must be connected to view tools.' }}
            </p>
          </div>

          <div v-else class="space-y-4">
            <div class="flex justify-between items-center">
              <div>
                <h3 class="text-lg font-semibold">Available Tools</h3>
                <p class="text-base-content/70">Tools provided by {{ server.name }}</p>
              </div>
              <div class="form-control">
                <input
                  v-model="toolSearch"
                  type="text"
                  placeholder="Search tools..."
                  class="input input-bordered input-sm w-64"
                />
              </div>
            </div>

            <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
              <div
                v-for="tool in filteredTools"
                :key="tool.name"
                class="card bg-base-100 shadow-md"
              >
                <div class="card-body">
                  <h4 class="card-title text-lg">{{ tool.name }}</h4>
                  <p class="text-sm text-base-content/70">
                    {{ tool.description || 'No description available' }}
                  </p>
                  <AnnotationBadges
                    v-if="tool.annotations"
                    :annotations="tool.annotations"
                    class="mt-2"
                  />
                  <div v-if="tool.input_schema" class="card-actions justify-end mt-4">
                    <button
                      class="btn btn-sm btn-outline"
                      @click="viewToolSchema(tool)"
                    >
                      View Schema
                    </button>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>

        <!-- Logs Tab -->
        <div v-if="activeTab === 'logs'">
          <div class="flex justify-between items-center mb-4">
            <div>
              <h3 class="text-lg font-semibold">Server Logs</h3>
              <p class="text-base-content/70">Recent log entries for {{ server.name }}</p>
            </div>
            <div class="flex items-center space-x-2">
              <select v-model="logTail" class="select select-bordered select-sm">
                <option :value="50">Last 50 lines</option>
                <option :value="100">Last 100 lines</option>
                <option :value="200">Last 200 lines</option>
                <option :value="500">Last 500 lines</option>
              </select>
              <button @click="loadLogs" class="btn btn-sm btn-outline" :disabled="logsLoading">
                <span v-if="logsLoading" class="loading loading-spinner loading-xs"></span>
                Refresh
              </button>
            </div>
          </div>

          <div v-if="logsLoading" class="text-center py-8">
            <span class="loading loading-spinner loading-lg"></span>
            <p class="mt-2">Loading logs...</p>
          </div>

          <div v-else-if="logsError" class="alert alert-error">
            <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            <span>{{ logsError }}</span>
            <button @click="loadLogs" class="btn btn-sm">Retry</button>
          </div>

          <div v-else-if="serverLogs.length === 0" class="text-center py-8">
            <svg class="w-16 h-16 mx-auto mb-4 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
            </svg>
            <h3 class="text-xl font-semibold mb-2">No logs available</h3>
            <p class="text-base-content/70">No log entries found for this server.</p>
          </div>

          <div v-else class="mockup-code max-h-96 overflow-y-auto">
            <pre v-for="(line, index) in serverLogs" :key="index" class="text-xs"><code>{{ line }}</code></pre>
          </div>
        </div>

        <!-- Configuration Tab -->
        <div v-if="activeTab === 'config'">
          <div class="space-y-6">
            <div>
              <h3 class="text-lg font-semibold mb-4">Server Configuration</h3>
              <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
                <div class="space-y-4">
                  <div>
                    <label class="label">
                      <span class="label-text font-medium">Name</span>
                    </label>
                    <input :value="server.name" readonly class="input input-bordered w-full" />
                  </div>
                  <div>
                    <label class="label">
                      <span class="label-text font-medium">Protocol</span>
                    </label>
                    <input :value="server.protocol" readonly class="input input-bordered w-full" />
                  </div>
                  <div v-if="server.url">
                    <label class="label">
                      <span class="label-text font-medium">URL</span>
                    </label>
                    <input :value="server.url" readonly class="input input-bordered w-full" />
                  </div>
                  <div v-if="server.command">
                    <label class="label">
                      <span class="label-text font-medium">Command</span>
                    </label>
                    <input :value="server.command" readonly class="input input-bordered w-full" />
                  </div>
                </div>
                <div class="space-y-4">
                  <div class="form-control">
                    <label class="label">
                      <span class="label-text font-medium">Enabled</span>
                    </label>
                    <input
                      type="checkbox"
                      :checked="server.enabled"
                      @change="toggleEnabled"
                      class="toggle"
                      :disabled="actionLoading"
                    />
                  </div>
                  <div class="form-control">
                    <label class="label">
                      <span class="label-text font-medium">Quarantined</span>
                    </label>
                    <input
                      type="checkbox"
                      :checked="server.quarantined"
                      readonly
                      class="toggle"
                      disabled
                    />
                  </div>
                  <div>
                    <label class="label">
                      <span class="label-text font-medium">Tools Count</span>
                    </label>
                    <input :value="server.tool_count" readonly class="input input-bordered w-full" />
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Tool Schema Modal -->
    <div v-if="selectedToolSchema" class="modal modal-open">
      <div class="modal-box max-w-4xl">
        <h3 class="font-bold text-lg mb-4">{{ selectedToolSchema.name }} - Input Schema</h3>
        <div class="mockup-code">
          <pre><code>{{ JSON.stringify(selectedToolSchema.input_schema, null, 2) }}</code></pre>
        </div>
        <div class="modal-action">
          <button class="btn" @click="selectedToolSchema = null">Close</button>
        </div>
      </div>
    </div>

    <!-- Hints Panel (Bottom of Page) -->
    <CollapsibleHintsPanel :hints="serverDetailHints" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useServersStore } from '@/stores/servers'
import { useSystemStore } from '@/stores/system'
import CollapsibleHintsPanel from '@/components/CollapsibleHintsPanel.vue'
import AnnotationBadges from '@/components/AnnotationBadges.vue'
import type { Hint } from '@/components/CollapsibleHintsPanel.vue'
import type { Server, Tool } from '@/types'
import api from '@/services/api'

interface Props {
  serverName: string
}

const props = defineProps<Props>()
const route = useRoute()

const serversStore = useServersStore()
const systemStore = useSystemStore()

// State
const loading = ref(true)
const error = ref<string | null>(null)
const server = ref<Server | null>(null)
const activeTab = ref<'tools' | 'logs' | 'config'>('tools')
const actionLoading = ref(false)

// Tools
const serverTools = ref<Tool[]>([])
const toolsLoading = ref(false)
const toolsError = ref<string | null>(null)
const toolSearch = ref('')
const selectedToolSchema = ref<Tool | null>(null)

// Logs
const serverLogs = ref<string[]>([])
const logsLoading = ref(false)
const logsError = ref<string | null>(null)
const logTail = ref(100)

// Computed
const needsOAuth = computed(() => {
  return server.value &&
         (server.value.protocol === 'http' || server.value.protocol === 'streamable-http') &&
         !server.value.connected &&
         server.value.enabled &&
         server.value.last_error?.includes('authorization')
})

const filteredTools = computed(() => {
  if (!toolSearch.value) return serverTools.value

  const query = toolSearch.value.toLowerCase()
  return serverTools.value.filter(tool =>
    tool.name.toLowerCase().includes(query) ||
    tool.description?.toLowerCase().includes(query)
  )
})

// Methods
async function loadServerDetails() {
  loading.value = true
  error.value = null

  try {
    await serversStore.fetchServers()
    server.value = serversStore.servers.find(s => s.name === props.serverName) || null

    if (!server.value) {
      error.value = `Server "${props.serverName}" not found`
      return
    }

    // Load tools and logs in parallel
    await Promise.all([
      loadTools(),
      loadLogs()
    ])
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to load server details'
  } finally {
    loading.value = false
  }
}

async function loadTools() {
  if (!server.value) return

  toolsLoading.value = true
  toolsError.value = null

  try {
    const response = await api.getServerTools(server.value.name)
    if (response.success && response.data) {
      serverTools.value = response.data.tools || []
    } else {
      toolsError.value = response.error || 'Failed to load tools'
    }
  } catch (err) {
    toolsError.value = err instanceof Error ? err.message : 'Failed to load tools'
  } finally {
    toolsLoading.value = false
  }
}

async function loadLogs() {
  if (!server.value) return

  logsLoading.value = true
  logsError.value = null

  try {
    const response = await api.getServerLogs(server.value.name, logTail.value)
    if (response.success && response.data) {
      serverLogs.value = response.data.logs || []
    } else {
      logsError.value = response.error || 'Failed to load logs'
    }
  } catch (err) {
    logsError.value = err instanceof Error ? err.message : 'Failed to load logs'
  } finally {
    logsLoading.value = false
  }
}

async function toggleEnabled() {
  if (!server.value) return

  actionLoading.value = true
  try {
    if (server.value.enabled) {
      await serversStore.disableServer(server.value.name)
      systemStore.addToast({
        type: 'success',
        title: 'Server Disabled',
        message: `${server.value.name} has been disabled`,
      })
    } else {
      await serversStore.enableServer(server.value.name)
      systemStore.addToast({
        type: 'success',
        title: 'Server Enabled',
        message: `${server.value.name} has been enabled`,
      })
    }
    // Update local server reference
    await serversStore.fetchServers()
    server.value = serversStore.servers.find(s => s.name === props.serverName) || null
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Operation Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    actionLoading.value = false
  }
}

async function restartServer() {
  if (!server.value) return

  actionLoading.value = true
  try {
    await serversStore.restartServer(server.value.name)
    systemStore.addToast({
      type: 'success',
      title: 'Server Restarted',
      message: `${server.value.name} is restarting`,
    })
    // Refresh server data after restart
    setTimeout(async () => {
      await serversStore.fetchServers()
      server.value = serversStore.servers.find(s => s.name === props.serverName) || null
    }, 2000)
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Restart Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    actionLoading.value = false
  }
}

async function triggerOAuth() {
  if (!server.value) return

  actionLoading.value = true
  try {
    await serversStore.triggerOAuthLogin(server.value.name)
    systemStore.addToast({
      type: 'success',
      title: 'OAuth Login Triggered',
      message: `Check your browser for ${server.value.name} login`,
    })
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'OAuth Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    actionLoading.value = false
  }
}

async function quarantineServer() {
  if (!server.value) return

  actionLoading.value = true
  try {
    await serversStore.quarantineServer(server.value.name)
    systemStore.addToast({
      type: 'success',
      title: 'Server Quarantined',
      message: `${server.value.name} has been quarantined`,
    })
    // Update local server reference
    await serversStore.fetchServers()
    server.value = serversStore.servers.find(s => s.name === props.serverName) || null
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Quarantine Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    actionLoading.value = false
  }
}

async function unquarantineServer() {
  if (!server.value) return

  actionLoading.value = true
  try {
    await serversStore.unquarantineServer(server.value.name)
    systemStore.addToast({
      type: 'success',
      title: 'Server Unquarantined',
      message: `${server.value.name} has been removed from quarantine`,
    })
    // Update local server reference
    await serversStore.fetchServers()
    server.value = serversStore.servers.find(s => s.name === props.serverName) || null
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Unquarantine Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    actionLoading.value = false
  }
}

async function refreshData() {
  await loadServerDetails()
}

function viewToolSchema(tool: Tool) {
  selectedToolSchema.value = tool
}

// Server detail hints
const serverDetailHints = computed<Hint[]>(() => {
  const hints: Hint[] = [
    {
      icon: '🔧',
      title: 'Server Management',
      description: 'Control and monitor this MCP server',
      sections: [
        {
          title: 'Enable/Disable server',
          codeBlock: {
            language: 'bash',
            code: `# Disable server\nmcpproxy call tool --tool-name=upstream_servers \\\n  --json_args='{"operation":"update","name":"${props.serverName}","enabled":false}'\n\n# Enable server\nmcpproxy call tool --tool-name=upstream_servers \\\n  --json_args='{"operation":"update","name":"${props.serverName}","enabled":true}'`
          }
        },
        {
          title: 'View server logs',
          codeBlock: {
            language: 'bash',
            code: `# Real-time logs for this server\ntail -f ~/.mcpproxy/logs/server-${props.serverName}.log`
          }
        }
      ]
    },
    {
      icon: '🛠️',
      title: 'Working with Tools',
      description: 'Use tools provided by this server',
      sections: [
        {
          title: 'List all tools',
          codeBlock: {
            language: 'bash',
            code: `# List tools from this server\nmcpproxy tools list --server=${props.serverName}`
          }
        },
        {
          title: 'Call a tool',
          text: 'Tools are prefixed with server name:',
          codeBlock: {
            language: 'bash',
            code: `# Call tool from this server\nmcpproxy call tool --tool-name=${props.serverName}:tool-name \\\n  --json_args='{"arg1":"value1"}'`
          }
        }
      ]
    },
    {
      icon: '💡',
      title: 'Troubleshooting',
      description: 'Common issues and solutions',
      sections: [
        {
          title: 'Connection issues',
          list: [
            'Check if server is enabled in configuration',
            'Review server logs for error messages',
            'Verify network connectivity for remote servers',
            'Check authentication credentials for OAuth servers'
          ]
        },
        {
          title: 'OAuth authentication',
          text: 'If server requires OAuth login:',
          codeBlock: {
            language: 'bash',
            code: `# Trigger OAuth login\nmcpproxy auth login --server=${props.serverName}`
          }
        }
      ]
    }
  ]

  return hints
})

// Watch for log tail changes
watch(logTail, () => {
  loadLogs()
})

// Load data on mount
onMounted(() => {
  loadServerDetails()
})
</script>