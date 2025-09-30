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

    <!-- Tokenizer Settings -->
    <div v-if="activeTab === 'tokenizer'" class="space-y-6">
      <div class="card bg-base-100 shadow-md">
        <div class="card-body">
          <h2 class="card-title">Token Counting Configuration</h2>
          <p class="text-base-content/70 mb-4">
            Configure token counting for LLM cost estimation and usage tracking.
          </p>

          <div class="form-control">
            <label class="label cursor-pointer">
              <span class="label-text">Enable Token Counting</span>
              <input
                type="checkbox"
                class="toggle toggle-primary"
                checked
                disabled
                title="Token counting is always enabled (read-only for now)"
              />
            </label>
            <label class="label">
              <span class="label-text-alt">Token counting helps track API usage and estimate costs</span>
            </label>
          </div>

          <div class="form-control">
            <label class="label">
              <span class="label-text">Default Model</span>
              <span class="label-text-alt">Model used for token estimation</span>
            </label>
            <select class="select select-bordered" disabled>
              <option selected>gpt-4</option>
              <option>gpt-4o</option>
              <option>gpt-3.5-turbo</option>
              <option>claude-3-5-sonnet</option>
              <option>claude-3-opus</option>
            </select>
            <label class="label">
              <span class="label-text-alt">Current: gpt-4 (configuration editing coming soon)</span>
            </label>
          </div>

          <div class="form-control">
            <label class="label">
              <span class="label-text">Encoding</span>
              <span class="label-text-alt">Tokenization encoding method</span>
            </label>
            <select class="select select-bordered" disabled>
              <option selected>cl100k_base (GPT-4, GPT-3.5)</option>
              <option>o200k_base (GPT-4o)</option>
              <option>p50k_base (Codex)</option>
              <option>r50k_base (GPT-3)</option>
            </select>
            <label class="label">
              <span class="label-text-alt">Current: cl100k_base</span>
            </label>
          </div>

          <div class="alert alert-info mt-4">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            <div class="text-sm">
              <p><strong>Note:</strong> Token counting is currently read-only in the UI.</p>
              <p class="mt-1">To customize settings, edit the <code class="bg-base-300 px-1 rounded">tokenizer</code> section in your config file:</p>
              <pre class="bg-base-300 p-2 rounded mt-2 text-xs">
{
  "tokenizer": {
    "enabled": true,
    "default_model": "gpt-4",
    "encoding": "cl100k_base"
  }
}</pre>
            </div>
          </div>

          <div class="alert">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            <div class="text-sm">
              <p><strong>Supported Models:</strong></p>
              <ul class="list-disc list-inside mt-1 space-y-1">
                <li>GPT-4, GPT-4 Turbo, GPT-3.5 Turbo</li>
                <li>GPT-4o (o200k_base encoding)</li>
                <li>Claude 3.5, Claude 3 (approximation using cl100k_base)</li>
                <li>Codex models (p50k_base)</li>
              </ul>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Configuration Editor -->
    <div v-if="activeTab === 'configuration'" class="space-y-6">
      <div class="card bg-base-100 shadow-md">
        <div class="card-body">
          <div class="flex justify-between items-center mb-4">
            <div>
              <h2 class="card-title">Configuration Editor</h2>
              <p class="text-sm text-base-content/70 mt-1">
                Edit your MCPProxy configuration directly. Changes require restart for some settings.
              </p>
            </div>
            <div class="flex items-center space-x-2">
              <div v-if="configStatus" :class="['badge', configStatus.valid ? 'badge-success' : 'badge-error']">
                {{ configStatus.valid ? '✓ Valid' : '✗ Invalid' }}
              </div>
              <button
                class="btn btn-sm btn-outline"
                @click="loadConfig"
                :disabled="loadingConfig"
              >
                <span v-if="loadingConfig" class="loading loading-spinner loading-xs"></span>
                <span v-else>Reload</span>
              </button>
            </div>
          </div>

          <!-- Monaco Editor -->
          <div class="border border-base-300 rounded-lg overflow-hidden" style="height: 600px;">
            <vue-monaco-editor
              v-model:value="configJson"
              language="json"
              theme="vs-dark"
              :options="editorOptions"
              @mount="handleEditorMount"
              @change="handleConfigChange"
            />
          </div>

          <!-- Validation Errors -->
          <div v-if="configErrors.length > 0" class="alert alert-error mt-4">
            <svg xmlns="http://www.w3.org/2000/svg" class="stroke-current shrink-0 h-6 w-6" fill="none" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            <div>
              <h3 class="font-bold">Validation Errors</h3>
              <ul class="list-disc list-inside text-sm">
                <li v-for="(error, index) in configErrors" :key="index">
                  <span class="font-mono">{{ error.field }}</span>: {{ error.message }}
                </li>
              </ul>
            </div>
          </div>

          <!-- Apply Configuration -->
          <div class="flex justify-between items-center mt-4">
            <div class="text-sm text-base-content/70">
              <span v-if="applyResult && applyResult.requires_restart" class="text-warning">
                ⚠️ {{ applyResult.restart_reason }}
              </span>
              <span v-else-if="applyResult && applyResult.applied_immediately" class="text-success">
                ✓ Configuration applied successfully
              </span>
            </div>
            <div class="flex items-center space-x-2">
              <button
                class="btn btn-outline"
                @click="validateConfig"
                :disabled="validatingConfig || !configJson"
              >
                <span v-if="validatingConfig" class="loading loading-spinner loading-sm"></span>
                Validate
              </button>
              <button
                class="btn btn-primary"
                @click="applyConfig"
                :disabled="applyingConfig || configErrors.length > 0 || !configJson"
              >
                <span v-if="applyingConfig" class="loading loading-spinner loading-sm"></span>
                Apply Configuration
              </button>
            </div>
          </div>
        </div>
      </div>

      <!-- Configuration Info -->
      <div class="card bg-base-100 shadow-md">
        <div class="card-body">
          <h3 class="card-title text-sm">Configuration Tips</h3>
          <div class="text-sm text-base-content/70 space-y-2">
            <p>• Use <kbd class="kbd kbd-xs">Ctrl+Space</kbd> for autocomplete</p>
            <p>• Use <kbd class="kbd kbd-xs">Ctrl+F</kbd> to search in the configuration</p>
            <p>• Invalid JSON will be highlighted with red squiggles</p>
            <p>• <span class="font-semibold">Hot-reloadable</span>: server changes, limits, logging</p>
            <p>• <span class="font-semibold">Requires restart</span>: listen address, data directory, API key, TLS</p>
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
import { VueMonacoEditor } from '@guolao/vue-monaco-editor'
import type { Server } from '@/types/api'
import { useServersStore } from '@/stores/servers'
import { useSystemStore } from '@/stores/system'
import api from '@/services/api'

// Store references
const serversStore = useServersStore()
const systemStore = useSystemStore()

// Active tab state
const activeTab = ref('general')

// Tab configuration
const tabs = [
  { id: 'general', label: 'General' },
  { id: 'servers', label: 'Servers' },
  { id: 'logging', label: 'Logging' },
  { id: 'tokenizer', label: 'Tokenizer' },
  { id: 'configuration', label: 'Configuration' },
  { id: 'system', label: 'System' }
]

// Loading states
const loading = ref(false)
const saving = ref(false)

// System information - get from store
const systemInfo = computed(() => ({
  version: 'v0.1.0',
  status: systemStore.isRunning ? 'Running' : 'Stopped',
  listen: systemStore.listenAddr || ':8080',
  dataDir: '~/.mcpproxy',
  logDir: 'Auto',
  configPath: 'Default'
}))

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

// Server management - use store data
const servers = computed(() => serversStore.servers)
const showAddServer = ref(false)
const newServer = ref({
  name: '',
  protocol: 'stdio',
  url: '',
  command: '',
  args: '',
  workingDir: ''
})

// Configuration editor state
const configJson = ref('')
const loadingConfig = ref(false)
const validatingConfig = ref(false)
const applyingConfig = ref(false)
const configStatus = ref<{ valid: boolean } | null>(null)
const configErrors = ref<Array<{ field: string; message: string }>>([])
const applyResult = ref<{
  success: boolean
  applied_immediately: boolean
  requires_restart: boolean
  restart_reason?: string
  changed_fields?: string[]
} | null>(null)
const editorInstance = ref<any>(null)

// Monaco editor options
const editorOptions = {
  automaticLayout: true,
  formatOnType: true,
  formatOnPaste: true,
  minimap: { enabled: false },
  scrollBeyondLastLine: false,
  fontSize: 14,
  tabSize: 2,
  wordWrap: 'on' as 'on',
  lineNumbers: 'on' as 'on',
  glyphMargin: true,
  folding: true,
  lineDecorationsWidth: 10,
  lineNumbersMinChars: 3
}

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
    // Refresh servers data from API
    await serversStore.fetchServers()
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

async function toggleServer(server: Server) {
  try {
    if (server.enabled) {
      await serversStore.disableServer(server.name)
    } else {
      await serversStore.enableServer(server.name)
    }
    // Refresh data after toggle
    await serversStore.fetchServers()
  } catch (error) {
    console.error('Failed to toggle server:', error)
  }
}

function unquarantineServer(server: Server) {
  // TODO: Implement unquarantine API
  console.log('Unquarantine server:', server.name)
}

function removeServer(server: Server) {
  // TODO: Implement server removal API
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

// Configuration editor methods
function handleEditorMount(editor: any) {
  editorInstance.value = editor
}

function handleConfigChange() {
  // Reset validation state on change
  configErrors.value = []
  configStatus.value = null
  applyResult.value = null

  // Try to parse JSON to check syntax
  try {
    JSON.parse(configJson.value)
    configStatus.value = { valid: true }
  } catch (e) {
    configStatus.value = { valid: false }
  }
}

async function loadConfig() {
  loadingConfig.value = true
  configErrors.value = []
  applyResult.value = null

  try {
    const response = await api.getConfig()
    if (response.success && response.data) {
      configJson.value = JSON.stringify(response.data.config, null, 2)
      configStatus.value = { valid: true }
    } else {
      configErrors.value = [{ field: 'general', message: response.error || 'Failed to load configuration' }]
    }
  } catch (error: any) {
    console.error('Failed to load config:', error)
    configErrors.value = [{ field: 'general', message: error.message || 'Failed to load configuration' }]
  } finally {
    loadingConfig.value = false
  }
}

async function validateConfig() {
  validatingConfig.value = true
  configErrors.value = []

  try {
    // Parse JSON first
    const config = JSON.parse(configJson.value)

    // Call validation endpoint
    const response = await api.validateConfig(config)
    if (response.success && response.data) {
      configErrors.value = response.data.errors || []
      configStatus.value = { valid: response.data.valid }
      if (response.data.valid) {
        console.log('Configuration validated successfully')
      }
    } else {
      configErrors.value = [{ field: 'general', message: response.error || 'Validation failed' }]
      configStatus.value = { valid: false }
    }
  } catch (error: any) {
    configErrors.value = [{ field: 'json', message: error.message || 'Invalid JSON syntax' }]
    configStatus.value = { valid: false }
  } finally {
    validatingConfig.value = false
  }
}

async function applyConfig() {
  applyingConfig.value = true
  configErrors.value = []
  applyResult.value = null

  try {
    // Parse JSON first
    const config = JSON.parse(configJson.value)

    // Call apply configuration endpoint
    const response = await api.applyConfig(config)
    if (response.success && response.data) {
      applyResult.value = response.data
      if (response.data.applied_immediately) {
        // Refresh UI data to reflect changes
        await refreshSettings()
      }
      console.log('Configuration applied successfully:', response.data)
    } else {
      configErrors.value = [{ field: 'apply', message: response.error || 'Failed to apply configuration' }]
    }
  } catch (error: any) {
    configErrors.value = [{ field: 'apply', message: error.message || 'Failed to apply configuration' }]
  } finally {
    applyingConfig.value = false
  }
}

// Initialize component
onMounted(() => {
  refreshSettings()
  loadConfig() // Load configuration when component mounts
})
</script>