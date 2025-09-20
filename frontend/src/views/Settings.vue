<template>
  <div class="space-y-6">
    <!-- Page Header -->
    <div class="flex justify-between items-center">
      <div>
        <h1 class="text-3xl font-bold">Settings</h1>
        <p class="text-base-content/70 mt-1">Configure MCPProxy preferences and server management</p>
      </div>
      <div class="flex items-center space-x-2">
        <div class="badge badge-outline">{{ serverCount }} servers</div>
        <button
          class="btn btn-sm btn-outline"
          @click="refreshSettings"
          :disabled="loading"
        >
          <svg v-if="!loading" class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
          </svg>
          <span v-if="loading" class="loading loading-spinner loading-sm"></span>
          Refresh
        </button>
      </div>
    </div>

    <!-- Settings Tabs -->
    <div class="tabs tabs-lifted">
      <button
        v-for="tab in tabs"
        :key="tab.id"
        :class="['tab tab-lg', { 'tab-active': activeTab === tab.id }]"
        @click="activeTab = tab.id"
      >
        {{ tab.label }}
      </button>
    </div>

    <!-- General Settings -->
    <div v-if="activeTab === 'general'" class="space-y-6">
      <div class="card bg-base-100 shadow-md">
        <div class="card-body">
          <h2 class="card-title">System Configuration</h2>

          <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
            <div class="form-control">
              <label class="label">
                <span class="label-text">Server Listen Address</span>
                <span class="label-text-alt">Current: {{ systemInfo.listen || ':8080' }}</span>
              </label>
              <input
                v-model="settings.listen"
                type="text"
                placeholder=":8080"
                class="input input-bordered"
              />
            </div>

            <div class="form-control">
              <label class="label">
                <span class="label-text">Data Directory</span>
                <span class="label-text-alt">Current: {{ systemInfo.dataDir || '~/.mcpproxy' }}</span>
              </label>
              <input
                v-model="settings.dataDir"
                type="text"
                placeholder="~/.mcpproxy"
                class="input input-bordered"
              />
            </div>

            <div class="form-control">
              <label class="label">
                <span class="label-text">Top K Results</span>
                <span class="label-text-alt">Number of top results to return</span>
              </label>
              <input
                v-model.number="settings.topK"
                type="number"
                min="1"
                max="50"
                class="input input-bordered"
              />
            </div>

            <div class="form-control">
              <label class="label">
                <span class="label-text">Tools Limit</span>
                <span class="label-text-alt">Maximum tools per server</span>
              </label>
              <input
                v-model.number="settings.toolsLimit"
                type="number"
                min="1"
                max="1000"
                class="input input-bordered"
              />
            </div>

            <div class="form-control">
              <label class="label">
                <span class="label-text">Tool Response Limit</span>
                <span class="label-text-alt">Maximum response size in characters</span>
              </label>
              <input
                v-model.number="settings.toolResponseLimit"
                type="number"
                min="1000"
                max="100000"
                class="input input-bordered"
              />
            </div>

            <div class="form-control">
              <label class="label cursor-pointer">
                <span class="label-text">Enable System Tray</span>
                <input
                  v-model="settings.enableTray"
                  type="checkbox"
                  class="toggle toggle-primary"
                />
              </label>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Server Management -->
    <div v-if="activeTab === 'servers'" class="space-y-6">
      <div class="card bg-base-100 shadow-md">
        <div class="card-body">
          <div class="flex justify-between items-center mb-4">
            <h2 class="card-title">Server Management</h2>
            <button class="btn btn-primary btn-sm" @click="showAddServer = true">
              <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 6v6m0 0v6m0-6h6m-6 0H6" />
              </svg>
              Add Server
            </button>
          </div>

          <div class="overflow-x-auto">
            <table class="table table-zebra w-full">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Type</th>
                  <th>Status</th>
                  <th>Tools</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="server in servers" :key="server.name">
                  <td>
                    <div class="flex items-center space-x-2">
                      <span class="font-medium">{{ server.name }}</span>
                      <div v-if="server.quarantined" class="badge badge-warning badge-xs">Quarantined</div>
                    </div>
                  </td>
                  <td>
                    <div class="badge badge-outline">{{ server.protocol || 'stdio' }}</div>
                  </td>
                  <td>
                    <div :class="['badge', server.connected ? 'badge-success' : server.enabled ? 'badge-warning' : 'badge-error']">
                      {{ server.connected ? 'Connected' : server.enabled ? 'Connecting' : 'Disabled' }}
                    </div>
                  </td>
                  <td>{{ server.tool_count || 0 }}</td>
                  <td>
                    <div class="flex items-center space-x-2">
                      <button
                        :class="['btn btn-xs', server.enabled ? 'btn-warning' : 'btn-success']"
                        @click="toggleServer(server)"
                      >
                        {{ server.enabled ? 'Disable' : 'Enable' }}
                      </button>
                      <button
                        v-if="server.quarantined"
                        class="btn btn-xs btn-info"
                        @click="unquarantineServer(server)"
                      >
                        Unquarantine
                      </button>
                      <button
                        class="btn btn-xs btn-error"
                        @click="removeServer(server)"
                      >
                        Remove
                      </button>
                    </div>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>
    </div>

    <!-- Logging Settings -->
    <div v-if="activeTab === 'logging'" class="space-y-6">
      <div class="card bg-base-100 shadow-md">
        <div class="card-body">
          <h2 class="card-title">Logging Configuration</h2>

          <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
            <div class="form-control">
              <label class="label">
                <span class="label-text">Log Level</span>
                <span class="label-text-alt">Current verbosity level</span>
              </label>
              <select v-model="settings.logLevel" class="select select-bordered">
                <option value="error">Error</option>
                <option value="warn">Warning</option>
                <option value="info">Info</option>
                <option value="debug">Debug</option>
                <option value="trace">Trace</option>
              </select>
            </div>

            <div class="form-control">
              <label class="label">
                <span class="label-text">Log Directory</span>
                <span class="label-text-alt">Where logs are stored</span>
              </label>
              <input
                v-model="settings.logDir"
                type="text"
                placeholder="Auto-detected"
                class="input input-bordered"
              />
            </div>

            <div class="form-control">
              <label class="label cursor-pointer">
                <span class="label-text">Enable File Logging</span>
                <input
                  v-model="settings.enableFileLogging"
                  type="checkbox"
                  class="toggle toggle-primary"
                />
              </label>
            </div>

            <div class="form-control">
              <label class="label cursor-pointer">
                <span class="label-text">Enable Console Logging</span>
                <input
                  v-model="settings.enableConsoleLogging"
                  type="checkbox"
                  class="toggle toggle-primary"
                />
              </label>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- System Information -->
    <div v-if="activeTab === 'system'" class="space-y-6">
      <div class="card bg-base-100 shadow-md">
        <div class="card-body">
          <h2 class="card-title">System Information</h2>

          <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
            <div class="stat">
              <div class="stat-title">MCPProxy Version</div>
              <div class="stat-value text-lg">{{ systemInfo.version || 'v0.1.0' }}</div>
            </div>

            <div class="stat">
              <div class="stat-title">Server Status</div>
              <div class="stat-value text-lg">{{ systemInfo.status || 'Running' }}</div>
            </div>

            <div class="stat">
              <div class="stat-title">Listen Address</div>
              <div class="stat-value text-lg">{{ systemInfo.listen || ':8080' }}</div>
            </div>

            <div class="stat">
              <div class="stat-title">Data Directory</div>
              <div class="stat-value text-sm">{{ systemInfo.dataDir || '~/.mcpproxy' }}</div>
            </div>

            <div class="stat">
              <div class="stat-title">Log Directory</div>
              <div class="stat-value text-sm">{{ systemInfo.logDir || 'Auto' }}</div>
            </div>

            <div class="stat">
              <div class="stat-title">Config Path</div>
              <div class="stat-value text-sm">{{ systemInfo.configPath || 'Default' }}</div>
            </div>
          </div>
        </div>
      </div>

      <div class="card bg-base-100 shadow-md">
        <div class="card-body">
          <h3 class="card-title">Actions</h3>
          <div class="flex flex-wrap gap-2">
            <button class="btn btn-outline" @click="reloadConfig">
              Reload Configuration
            </button>
            <button class="btn btn-outline" @click="openLogDirectory">
              Open Log Directory
            </button>
            <button class="btn btn-outline" @click="openConfigFile">
              Open Config File
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Save/Reset Actions -->
    <div class="card bg-base-100 shadow-md">
      <div class="card-body">
        <div class="flex justify-between items-center">
          <div class="text-sm text-base-content/70">
            Changes are saved automatically to your configuration file.
          </div>
          <div class="flex items-center space-x-2">
            <button
              class="btn btn-outline"
              @click="resetSettings"
              :disabled="saving"
            >
              Reset to Defaults
            </button>
            <button
              class="btn btn-primary"
              @click="saveSettings"
              :disabled="saving"
            >
              <span v-if="saving" class="loading loading-spinner loading-sm"></span>
              Save Settings
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Add Server Modal -->
    <div v-if="showAddServer" class="modal modal-open">
      <div class="modal-box">
        <h3 class="font-bold text-lg mb-4">Add New Server</h3>

        <div class="space-y-4">
          <div class="form-control">
            <label class="label">
              <span class="label-text">Server Name</span>
            </label>
            <input
              v-model="newServer.name"
              type="text"
              placeholder="my-server"
              class="input input-bordered"
            />
          </div>

          <div class="form-control">
            <label class="label">
              <span class="label-text">Protocol</span>
            </label>
            <select v-model="newServer.protocol" class="select select-bordered">
              <option value="stdio">STDIO</option>
              <option value="http">HTTP</option>
            </select>
          </div>

          <div v-if="newServer.protocol === 'http'" class="form-control">
            <label class="label">
              <span class="label-text">URL</span>
            </label>
            <input
              v-model="newServer.url"
              type="url"
              placeholder="https://api.example.com/mcp"
              class="input input-bordered"
            />
          </div>

          <div v-if="newServer.protocol === 'stdio'" class="space-y-4">
            <div class="form-control">
              <label class="label">
                <span class="label-text">Command</span>
              </label>
              <input
                v-model="newServer.command"
                type="text"
                placeholder="npx"
                class="input input-bordered"
              />
            </div>

            <div class="form-control">
              <label class="label">
                <span class="label-text">Arguments (one per line)</span>
              </label>
              <textarea
                v-model="newServer.args"
                placeholder="@modelcontextprotocol/server-filesystem&#10;/path/to/directory"
                class="textarea textarea-bordered"
                rows="3"
              ></textarea>
            </div>

            <div class="form-control">
              <label class="label">
                <span class="label-text">Working Directory (optional)</span>
              </label>
              <input
                v-model="newServer.workingDir"
                type="text"
                placeholder="/path/to/directory"
                class="input input-bordered"
              />
            </div>
          </div>
        </div>

        <div class="modal-action">
          <button class="btn" @click="showAddServer = false">Cancel</button>
          <button
            class="btn btn-primary"
            @click="addServer"
            :disabled="!canAddServer"
          >
            Add Server
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import type { Server } from '@/types/api'

// Active tab state
const activeTab = ref('general')

// Tab configuration
const tabs = [
  { id: 'general', label: 'General' },
  { id: 'servers', label: 'Servers' },
  { id: 'logging', label: 'Logging' },
  { id: 'system', label: 'System' }
]

// Loading states
const loading = ref(false)
const saving = ref(false)

// System information (mock data for now)
const systemInfo = ref({
  version: 'v0.1.0',
  status: 'Running',
  listen: ':8080',
  dataDir: '~/.mcpproxy',
  logDir: 'Auto',
  configPath: 'Default'
})

// Settings data
const settings = ref({
  listen: ':8080',
  dataDir: '~/.mcpproxy',
  topK: 5,
  toolsLimit: 15,
  toolResponseLimit: 20000,
  enableTray: true,
  logLevel: 'info',
  logDir: '',
  enableFileLogging: true,
  enableConsoleLogging: true
})

// Server management
const servers = ref<Server[]>([])
const showAddServer = ref(false)
const newServer = ref({
  name: '',
  protocol: 'stdio',
  url: '',
  command: '',
  args: '',
  workingDir: ''
})

// Computed properties
const serverCount = computed(() => servers.value.length)

const canAddServer = computed(() => {
  if (!newServer.value.name.trim()) return false

  if (newServer.value.protocol === 'http') {
    return !!newServer.value.url.trim()
  } else {
    return !!newServer.value.command.trim()
  }
})

// Methods
async function refreshSettings() {
  loading.value = true
  try {
    // TODO: Implement actual API calls
    await new Promise(resolve => setTimeout(resolve, 1000))
  } catch (error) {
    console.error('Failed to refresh settings:', error)
  } finally {
    loading.value = false
  }
}

async function saveSettings() {
  saving.value = true
  try {
    // TODO: Implement actual API calls
    await new Promise(resolve => setTimeout(resolve, 1000))
  } catch (error) {
    console.error('Failed to save settings:', error)
  } finally {
    saving.value = false
  }
}

function resetSettings() {
  settings.value = {
    listen: ':8080',
    dataDir: '~/.mcpproxy',
    topK: 5,
    toolsLimit: 15,
    toolResponseLimit: 20000,
    enableTray: true,
    logLevel: 'info',
    logDir: '',
    enableFileLogging: true,
    enableConsoleLogging: true
  }
}

function toggleServer(server: Server) {
  // TODO: Implement server toggle
  console.log('Toggle server:', server.name)
}

function unquarantineServer(server: Server) {
  // TODO: Implement unquarantine
  console.log('Unquarantine server:', server.name)
}

function removeServer(server: Server) {
  // TODO: Implement server removal
  console.log('Remove server:', server.name)
}

function addServer() {
  // TODO: Implement server addition
  console.log('Add server:', newServer.value)
  showAddServer.value = false

  // Reset form
  newServer.value = {
    name: '',
    protocol: 'stdio',
    url: '',
    command: '',
    args: '',
    workingDir: ''
  }
}

function reloadConfig() {
  // TODO: Implement config reload
  console.log('Reload config')
}

function openLogDirectory() {
  // TODO: Implement open log directory
  console.log('Open log directory')
}

function openConfigFile() {
  // TODO: Implement open config file
  console.log('Open config file')
}

// Initialize component
onMounted(() => {
  refreshSettings()
})
</script>