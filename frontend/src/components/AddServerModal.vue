<template>
  <dialog :open="show" class="modal">
    <div class="modal-box max-w-3xl">
      <h3 class="font-bold text-lg mb-4">Add New Server</h3>

      <!-- Tab Selection -->
      <div class="tabs tabs-boxed mb-4">
        <a
          :class="['tab', activeTab === 'manual' ? 'tab-active' : '']"
          @click="activeTab = 'manual'"
        >
          Manual
        </a>
        <a
          :class="['tab', activeTab === 'import' ? 'tab-active' : '']"
          @click="activeTab = 'import'"
        >
          Import
        </a>
      </div>

      <!-- Manual Tab -->
      <div v-if="activeTab === 'manual'">
        <form @submit.prevent="handleSubmit">
          <!-- Server Type Selection -->
          <div class="form-control mb-4">
            <label class="label">
              <span class="label-text font-semibold">Server Type</span>
            </label>
            <div class="flex gap-4">
              <label class="flex items-center space-x-2 cursor-pointer">
                <input
                  type="radio"
                  name="serverType"
                  value="stdio"
                  v-model="formData.type"
                  class="radio radio-primary"
                />
                <span>stdio (Local Command)</span>
              </label>
              <label class="flex items-center space-x-2 cursor-pointer">
                <input
                  type="radio"
                  name="serverType"
                  value="http"
                  v-model="formData.type"
                  class="radio radio-primary"
                />
                <span>HTTP/HTTPS (Remote)</span>
              </label>
            </div>
          </div>

          <!-- Common Fields -->
          <div class="form-control mb-4">
            <label class="label">
              <span class="label-text font-semibold">Server Name</span>
            </label>
            <input
              type="text"
              v-model="formData.name"
              placeholder="e.g., github-server"
              class="input input-bordered"
              required
            />
          </div>

          <!-- HTTP/HTTPS Fields -->
          <div v-if="formData.type === 'http'" class="space-y-4">
            <div class="form-control">
              <label class="label">
                <span class="label-text font-semibold">URL</span>
              </label>
              <input
                type="url"
                v-model="formData.url"
                placeholder="https://api.example.com/mcp"
                class="input input-bordered"
                required
              />
            </div>
          </div>

          <!-- stdio Fields -->
          <div v-if="formData.type === 'stdio'" class="space-y-4">
            <div class="form-control">
              <label class="label">
                <span class="label-text font-semibold">Command</span>
              </label>
              <select v-model="formData.command" class="select select-bordered" required>
                <option value="">Select command</option>
                <option value="npx">npx (Node.js)</option>
                <option value="uvx">uvx (Python)</option>
                <option value="node">node</option>
                <option value="python">python</option>
                <option value="custom">Custom command</option>
              </select>
            </div>

            <div v-if="formData.command === 'custom'" class="form-control">
              <label class="label">
                <span class="label-text font-semibold">Custom Command Path</span>
              </label>
              <input
                type="text"
                v-model="formData.customCommand"
                placeholder="/usr/local/bin/my-mcp-server"
                class="input input-bordered"
                required
              />
            </div>

            <div class="form-control">
              <label class="label">
                <span class="label-text font-semibold">Arguments</span>
                <div class="flex items-center gap-2">
                  <span class="label-text-alt">One per line</span>
                  <div class="tooltip tooltip-left" data-tip="You can use Secrets to store sensitive argument values">
                    <svg class="w-4 h-4 opacity-60" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                    </svg>
                  </div>
                </div>
              </label>
              <textarea
                v-model="formData.argsText"
                placeholder="@modelcontextprotocol/server-filesystem"
                class="textarea textarea-bordered h-24"
                rows="3"
              ></textarea>
            </div>

            <div class="form-control">
              <label class="label">
                <span class="label-text font-semibold">Environment Variables</span>
                <div class="flex items-center gap-2">
                  <span class="label-text-alt">KEY=value format, one per line</span>
                  <div class="tooltip tooltip-left" data-tip="You can use Secrets to store sensitive values like API keys">
                    <svg class="w-4 h-4 opacity-60" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                    </svg>
                  </div>
                </div>
              </label>
              <textarea
                v-model="formData.envText"
                placeholder="API_KEY=your-key&#10;DEBUG=true"
                class="textarea textarea-bordered h-24"
                rows="3"
              ></textarea>
            </div>

            <div class="form-control">
              <label class="label">
                <span class="label-text font-semibold">Working Directory (Optional)</span>
              </label>
              <input
                type="text"
                v-model="formData.workingDir"
                placeholder="/path/to/project"
                class="input input-bordered"
              />
            </div>
          </div>

          <!-- Toggles Section -->
          <div class="divider mt-6">Options</div>

          <div class="space-y-3">
            <!-- Enabled -->
            <div class="form-control">
              <label class="label cursor-pointer justify-start space-x-3">
                <input
                  type="checkbox"
                  v-model="formData.enabled"
                  class="toggle toggle-primary"
                />
                <span class="label-text font-semibold">Enabled</span>
                <div class="tooltip tooltip-right" data-tip="Start this server immediately after adding">
                  <svg class="w-4 h-4 opacity-60" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                  </svg>
                </div>
              </label>
            </div>

            <!-- Quarantined -->
            <div class="form-control">
              <label class="label cursor-pointer justify-start space-x-3">
                <input
                  type="checkbox"
                  v-model="formData.quarantined"
                  class="toggle toggle-warning"
                />
                <span class="label-text font-semibold">Quarantined</span>
                <div class="tooltip tooltip-right" data-tip="Prevent tool execution until security review is complete. Recommended for new servers.">
                  <svg class="w-4 h-4 opacity-60" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                  </svg>
                </div>
              </label>
            </div>

            <!-- Isolated (Docker) -->
            <div class="form-control">
              <label class="label cursor-pointer justify-start space-x-3">
                <input
                  type="checkbox"
                  v-model="formData.isolated"
                  class="toggle toggle-info"
                  :disabled="formData.type !== 'stdio'"
                />
                <span class="label-text font-semibold">Docker Isolation</span>
                <div class="tooltip tooltip-right" data-tip="Run stdio server in isolated Docker container for enhanced security (stdio only)">
                  <svg class="w-4 h-4 opacity-60" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                  </svg>
                </div>
              </label>
            </div>

            <!-- Idle on Inactivity -->
            <div class="form-control">
              <label class="label cursor-pointer justify-start space-x-3">
                <input
                  type="checkbox"
                  v-model="formData.idleOnInactivity"
                  class="toggle toggle-success"
                  disabled
                />
                <span class="label-text font-semibold opacity-50">Idle on Inactivity</span>
                <div class="tooltip tooltip-right" data-tip="Future feature: Automatically stop server after period of inactivity to save resources">
                  <svg class="w-4 h-4 opacity-60" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                  </svg>
                </div>
              </label>
              <span class="text-xs opacity-50 ml-12">Coming soon</span>
            </div>
          </div>

          <!-- Error Display -->
          <div v-if="error" class="alert alert-error mt-4">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            <span>{{ error }}</span>
          </div>

          <!-- Actions -->
          <div class="modal-action">
            <button type="button" @click="handleClose" class="btn btn-ghost">Cancel</button>
            <button type="submit" class="btn btn-primary" :disabled="loading">
              <span v-if="loading" class="loading loading-spinner loading-sm"></span>
              {{ loading ? 'Adding...' : 'Add Server' }}
            </button>
          </div>
        </form>
      </div>

      <!-- Import Tab -->
      <div v-if="activeTab === 'import'">
        <!-- Input Method Tabs -->
        <div class="flex gap-2 mb-4">
          <button
            :class="['btn btn-sm', importMode === 'file' ? 'btn-primary' : 'btn-outline']"
            @click="importMode = 'file'"
          >
            Upload File
          </button>
          <button
            :class="['btn btn-sm', importMode === 'paste' ? 'btn-primary' : 'btn-outline']"
            @click="importMode = 'paste'"
          >
            Paste Content
          </button>
        </div>

        <!-- File Upload -->
        <div v-if="importMode === 'file'" class="form-control mb-4">
          <label class="label">
            <span class="label-text font-semibold">Configuration File</span>
          </label>
          <input
            type="file"
            accept=".json,.toml"
            @change="handleFileSelect"
            class="file-input file-input-bordered w-full"
          />
          <label class="label">
            <span class="label-text-alt">Supports Claude Desktop, Claude Code, Cursor IDE, Codex CLI, and Gemini CLI configs</span>
          </label>

          <!-- Canonical Config Paths Hint Panel -->
          <div v-if="canonicalPaths.length > 0" class="mt-3 p-3 bg-base-200 rounded-lg">
            <div class="text-sm font-semibold mb-2 flex items-center gap-2">
              <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
              </svg>
              Quick Import - Found Configs
            </div>
            <div class="space-y-2">
              <div
                v-for="config in canonicalPaths"
                :key="config.path"
                class="flex items-center justify-between p-2 rounded"
                :class="config.exists ? 'bg-success/10 border border-success/30' : 'bg-base-300/50'"
              >
                <div class="flex-1 min-w-0">
                  <div class="flex items-center gap-2">
                    <span class="font-medium text-sm">{{ config.name }}</span>
                    <span
                      v-if="config.exists"
                      class="badge badge-success badge-xs"
                    >Found</span>
                    <span
                      v-else
                      class="badge badge-ghost badge-xs"
                    >Not found</span>
                  </div>
                  <div class="text-xs text-base-content/60 truncate" :title="config.path">
                    {{ config.path }}
                  </div>
                </div>
                <button
                  v-if="config.exists"
                  @click="importFromCanonicalPath(config)"
                  class="btn btn-primary btn-xs ml-2"
                  :disabled="importingCanonicalPath === config.path"
                >
                  <span v-if="importingCanonicalPath === config.path" class="loading loading-spinner loading-xs"></span>
                  <span v-else>Import</span>
                </button>
              </div>
            </div>
          </div>
        </div>

        <!-- Paste Content -->
        <div v-if="importMode === 'paste'" class="form-control mb-4">
          <label class="label">
            <span class="label-text font-semibold">Configuration Content</span>
          </label>
          <!-- Editor with line numbers -->
          <div :class="['flex border rounded-lg overflow-hidden h-48', validationError ? 'border-error' : 'border-base-300']">
            <!-- Line numbers gutter -->
            <div
              ref="lineNumbersRef"
              class="bg-base-200 text-base-content/50 text-right select-none py-2 px-2 font-mono text-sm overflow-hidden border-r border-base-300"
              style="min-width: 3rem;"
            >
              <div v-for="n in lineCount" :key="n" class="leading-[1.5rem]" :class="{'text-error font-bold': validationError?.line === n}">
                {{ n }}
              </div>
            </div>
            <!-- Textarea -->
            <textarea
              ref="textareaRef"
              v-model="importContent"
              placeholder='Paste JSON or TOML configuration here...

Example (Claude Desktop):
{
  "mcpServers": {
    "github": {
      "command": "uvx",
      "args": ["mcp-server-github"]
    }
  }
}'
              class="flex-1 bg-base-100 font-mono text-sm resize-none border-0 focus:outline-none py-2 px-3 leading-[1.5rem]"
              @input="debouncePreview"
              @scroll="syncScroll"
            ></textarea>
          </div>
          <!-- Validation Error Display -->
          <div v-if="validationError" class="mt-2 p-3 bg-error/10 border border-error/30 rounded-lg">
            <div class="flex items-start gap-2 text-error">
              <svg class="w-5 h-5 flex-shrink-0 mt-0.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
              </svg>
              <div>
                <div class="font-semibold">Invalid JSON Syntax</div>
                <div class="text-sm mt-1">{{ validationError.message }}</div>
                <div v-if="validationError.line" class="text-xs mt-1 opacity-70">
                  Line {{ validationError.line }}<span v-if="validationError.column">, Column {{ validationError.column }}</span>
                </div>
                <div v-if="validationError.hint" class="text-xs mt-2 text-warning">
                  <strong>Hint:</strong> {{ validationError.hint }}
                </div>
              </div>
            </div>
          </div>
        </div>

        <!-- Format Hint -->
        <div class="form-control mb-4">
          <label class="label">
            <span class="label-text font-semibold">Format (Optional)</span>
          </label>
          <select v-model="importFormat" class="select select-bordered select-sm">
            <option value="">Auto-detect</option>
            <option value="claude-desktop">Claude Desktop</option>
            <option value="claude-code">Claude Code</option>
            <option value="cursor">Cursor IDE</option>
            <option value="codex">Codex CLI (TOML)</option>
            <option value="gemini">Gemini CLI</option>
          </select>
        </div>

        <!-- Preview Loading -->
        <div v-if="previewLoading" class="flex justify-center py-4">
          <span class="loading loading-spinner loading-md"></span>
          <span class="ml-2">Loading preview...</span>
        </div>

        <!-- Preview Results -->
        <div v-if="previewResult && !previewLoading" class="space-y-4">
          <!-- Format Detection -->
          <div class="alert alert-info">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            <span>Detected format: <strong>{{ previewResult.format_name }}</strong></span>
          </div>

          <!-- Summary -->
          <div class="stats shadow w-full">
            <div class="stat">
              <div class="stat-title">Total</div>
              <div class="stat-value text-lg">{{ previewResult.summary.total }}</div>
            </div>
            <div class="stat">
              <div class="stat-title">Will Import</div>
              <div class="stat-value text-lg text-success">{{ previewResult.summary.imported }}</div>
            </div>
            <div v-if="previewResult.summary.skipped > 0" class="stat">
              <div class="stat-title">Skipped</div>
              <div class="stat-value text-lg text-warning">{{ previewResult.summary.skipped }}</div>
            </div>
          </div>

          <!-- Warnings -->
          <div v-if="previewResult.warnings?.length > 0" class="alert alert-warning">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
            </svg>
            <div>
              <div class="font-bold">Warnings</div>
              <ul class="text-sm mt-1">
                <li v-for="(warning, idx) in previewResult.warnings" :key="idx">{{ warning }}</li>
              </ul>
            </div>
          </div>

          <!-- Server Selection -->
          <div v-if="previewResult.imported.length > 0" class="space-y-2">
            <div class="flex justify-between items-center">
              <span class="font-semibold">Servers to Import</span>
              <label class="flex items-center gap-2 cursor-pointer">
                <input
                  type="checkbox"
                  class="checkbox checkbox-sm"
                  :checked="allServersSelected"
                  @change="toggleAllServers"
                />
                <span class="text-sm">Select All</span>
              </label>
            </div>
            <div class="max-h-64 overflow-y-auto space-y-2">
              <div
                v-for="server in previewResult.imported"
                :key="server.name"
                class="flex items-center gap-3 p-3 bg-base-200 rounded-lg"
              >
                <input
                  type="checkbox"
                  class="checkbox checkbox-primary"
                  :checked="selectedServers.has(server.name)"
                  @change="toggleServer(server.name)"
                />
                <div class="flex-1">
                  <div class="font-medium">{{ server.name }}</div>
                  <div class="text-sm opacity-70">
                    <span class="badge badge-sm mr-1">{{ server.protocol }}</span>
                    <span v-if="server.command">{{ server.command }} {{ server.args?.join(' ') }}</span>
                    <span v-else-if="server.url">{{ server.url }}</span>
                  </div>
                  <div v-if="server.warnings?.length" class="text-xs text-warning mt-1">
                    {{ server.warnings.join(', ') }}
                  </div>
                </div>
              </div>
            </div>
          </div>

          <!-- Skipped Servers -->
          <div v-if="previewResult.skipped?.length > 0" class="collapse collapse-arrow bg-base-200">
            <input type="checkbox" />
            <div class="collapse-title font-medium">
              Skipped Servers ({{ previewResult.skipped.length }})
            </div>
            <div class="collapse-content">
              <div v-for="server in previewResult.skipped" :key="server.name" class="py-2 border-b border-base-300 last:border-0">
                <div class="font-medium">{{ server.name }}</div>
                <div class="text-sm text-warning">{{ server.reason }}</div>
              </div>
            </div>
          </div>
        </div>

        <!-- Error Display -->
        <div v-if="importError" class="alert alert-error mt-4">
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <span>{{ importError }}</span>
        </div>

        <!-- Critical Warnings Display -->
        <div v-if="selectedServersWithCriticalWarnings.length > 0" class="alert alert-error mt-4">
          <svg class="w-5 h-5 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
          </svg>
          <div>
            <div class="font-bold">Cannot import servers with critical errors</div>
            <ul class="text-sm mt-1 list-disc list-inside">
              <li v-for="server in selectedServersWithCriticalWarnings" :key="server.name">
                <strong>{{ server.name }}:</strong> {{ server.warnings?.filter(w => /missing (command|url) field/i.test(w)).join(', ') }}
              </li>
            </ul>
            <div class="text-sm mt-2">Deselect these servers or fix the configuration before importing.</div>
          </div>
        </div>

        <!-- Actions -->
        <div class="modal-action">
          <button type="button" @click="handleClose" class="btn btn-ghost">Cancel</button>
          <button
            @click="handleImport"
            class="btn btn-primary"
            :disabled="importLoading || !canImport"
          >
            <span v-if="importLoading" class="loading loading-spinner loading-sm"></span>
            {{ importLoading ? 'Importing...' : `Import ${selectedServers.size} Server${selectedServers.size !== 1 ? 's' : ''}` }}
          </button>
        </div>
      </div>
    </div>
    <form method="dialog" class="modal-backdrop" @click="handleClose">
      <button>close</button>
    </form>
  </dialog>
</template>

<script setup lang="ts">
import { ref, reactive, watch, computed, onMounted } from 'vue'
import { useServersStore } from '@/stores/servers'
import { useSystemStore } from '@/stores/system'
import api, { type CanonicalConfigPath } from '@/services/api'
import type { ImportResponse, ImportedServer } from '@/types'

interface Props {
  show: boolean
}

interface Emits {
  (e: 'close'): void
  (e: 'added'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const serversStore = useServersStore()
const systemStore = useSystemStore()

// Tab state
const activeTab = ref<'manual' | 'import'>('manual')

// Manual form state
const formData = reactive({
  type: 'stdio' as 'stdio' | 'http',
  name: '',
  url: '',
  command: '',
  customCommand: '',
  argsText: '',
  envText: '',
  workingDir: '',
  enabled: true,
  quarantined: true,
  isolated: false,
  idleOnInactivity: false
})

const loading = ref(false)
const error = ref('')

// Import state
const importMode = ref<'file' | 'paste'>('file')
const importContent = ref('')
const importFormat = ref('')
const importFile = ref<File | null>(null)
const previewLoading = ref(false)
const previewResult = ref<ImportResponse | null>(null)
const importError = ref('')
const importLoading = ref(false)
const selectedServers = ref<Set<string>>(new Set())
const validationError = ref<{ message: string; line?: number; column?: number; hint?: string } | null>(null)

// Canonical config paths for quick import
const canonicalPaths = ref<CanonicalConfigPath[]>([])
const importingCanonicalPath = ref<string | null>(null)

// Editor refs for line numbers
const textareaRef = ref<HTMLTextAreaElement | null>(null)
const lineNumbersRef = ref<HTMLDivElement | null>(null)

// Debounce timer for preview
let previewTimer: ReturnType<typeof setTimeout> | null = null

// Computed
const lineCount = computed(() => {
  if (!importContent.value) return 10 // Show at least 10 lines for placeholder
  return Math.max(importContent.value.split('\n').length, 10)
})

const allServersSelected = computed(() => {
  if (!previewResult.value?.imported.length) return false
  return previewResult.value.imported.every(s => selectedServers.value.has(s.name))
})

// Critical warnings that indicate the server won't work
const criticalWarningPatterns = [
  /missing command field/i,
  /missing url field/i,
]

// Check if a server has critical warnings that should block import
function hasCriticalWarnings(server: ImportedServer): boolean {
  if (!server.warnings?.length) return false
  return server.warnings.some(warning =>
    criticalWarningPatterns.some(pattern => pattern.test(warning))
  )
}

// Get selected servers that have critical warnings
const selectedServersWithCriticalWarnings = computed(() => {
  if (!previewResult.value?.imported) return []
  return previewResult.value.imported.filter(s =>
    selectedServers.value.has(s.name) && hasCriticalWarnings(s)
  )
})

const canImport = computed(() => {
  // Must have preview result and selected servers
  if (!previewResult.value || selectedServers.value.size === 0) return false
  // Cannot import if any selected server has critical warnings
  return selectedServersWithCriticalWarnings.value.length === 0
})

// Watchers
watch(() => formData.type, (newType) => {
  if (newType !== 'stdio') {
    formData.isolated = false
  }
})

watch(() => props.show, (newShow) => {
  if (newShow) {
    // Reset to manual tab when opening
    activeTab.value = 'manual'
  }
})

// Watch for import content changes to trigger preview
watch(importContent, () => {
  if (importMode.value === 'paste' && importContent.value.trim()) {
    debouncePreview()
  }
})

watch(importFormat, () => {
  if (importContent.value.trim() || importFile.value) {
    triggerPreview()
  }
})

// Methods - Manual form
function parseArgs(): string[] {
  if (!formData.argsText.trim()) return []
  return formData.argsText.split('\n').map(line => line.trim()).filter(line => line)
}

function parseEnv(): Record<string, string> {
  if (!formData.envText.trim()) return {}
  const env: Record<string, string> = {}
  formData.envText.split('\n').forEach(line => {
    const trimmed = line.trim()
    if (!trimmed) return
    const [key, ...valueParts] = trimmed.split('=')
    if (key && valueParts.length > 0) {
      env[key.trim()] = valueParts.join('=').trim()
    }
  })
  return env
}

// Content validation
function validateContent(content: string): { valid: boolean; error?: { message: string; line?: number; column?: number; hint?: string } } {
  const trimmed = content.trim()
  if (!trimmed) {
    return { valid: true }
  }

  // Check if it looks like TOML (starts with [ or has = without :)
  const looksLikeToml = trimmed.startsWith('[') || (trimmed.includes('=') && !trimmed.includes(':'))
  if (looksLikeToml) {
    // TOML validation is harder client-side, let the server handle it
    return { valid: true }
  }

  // Try to parse as JSON
  try {
    JSON.parse(trimmed)
    return { valid: true }
  } catch (e) {
    if (e instanceof SyntaxError) {
      const errorMsg = e.message
      let line: number | undefined
      let column: number | undefined
      let hint: string | undefined

      // Extract position from error message (varies by browser)
      // Chrome: "... at position N"
      // Firefox: "... at line N column M"
      const posMatch = errorMsg.match(/position (\d+)/)
      const lineColMatch = errorMsg.match(/line (\d+) column (\d+)/)

      if (lineColMatch) {
        line = parseInt(lineColMatch[1], 10)
        column = parseInt(lineColMatch[2], 10)
      } else if (posMatch) {
        // Convert position to line/column
        const pos = parseInt(posMatch[1], 10)
        const lines = trimmed.substring(0, pos).split('\n')
        line = lines.length
        column = lines[lines.length - 1].length + 1
      }

      // Provide helpful hints for common errors
      if (errorMsg.includes('Unexpected token') || errorMsg.includes('Expected')) {
        // Check for trailing comma
        if (trimmed.match(/,\s*[}\]]/)) {
          hint = 'Check for trailing commas before closing braces or brackets (e.g., "value",} should be "value"})'
        }
        // Check for unescaped backslash
        else if (trimmed.includes('\\') && !trimmed.includes('\\\\') && !trimmed.match(/\\[nrt"\\\/bfu]/)) {
          hint = 'Check for unescaped backslashes. In JSON, backslashes must be escaped as \\\\ (e.g., "C:\\\\" instead of "C:\\")'
        }
        // Check for single quotes
        else if (trimmed.includes("'")) {
          hint = 'JSON requires double quotes for strings. Replace single quotes with double quotes.'
        }
      }

      // Clean up the error message for display
      let cleanMessage = errorMsg
        .replace(/^JSON\.parse: /, '')
        .replace(/^Unexpected token/, 'Unexpected character')
        .replace(/ in JSON at position \d+$/, '')

      return {
        valid: false,
        error: {
          message: cleanMessage,
          line,
          column,
          hint
        }
      }
    }
    return {
      valid: false,
      error: { message: 'Invalid content format' }
    }
  }
}

async function handleSubmit() {
  error.value = ''
  loading.value = true

  try {
    const command = formData.command === 'custom' ? formData.customCommand : formData.command
    const args = parseArgs()
    const env = parseEnv()

    const serverData: any = {
      operation: 'add',
      name: formData.name,
      protocol: formData.type,
      enabled: formData.enabled,
      quarantined: formData.quarantined
    }

    if (formData.type === 'http') {
      serverData.url = formData.url
    } else {
      serverData.command = command
      if (args.length > 0) {
        serverData.args_json = JSON.stringify(args)
      }
      if (Object.keys(env).length > 0) {
        serverData.env_json = JSON.stringify(env)
      }
      if (formData.workingDir) {
        serverData.working_dir = formData.workingDir
      }
      if (formData.isolated) {
        serverData.isolation_json = JSON.stringify({ enabled: true })
      }
    }

    await serversStore.addServer(serverData)

    systemStore.addToast({
      type: 'success',
      title: 'Server Added',
      message: `${formData.name} has been added successfully`
    })

    emit('added')
    handleClose()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to add server'
  } finally {
    loading.value = false
  }
}

// Methods - Import
function handleFileSelect(event: Event) {
  const input = event.target as HTMLInputElement
  if (input.files && input.files.length > 0) {
    importFile.value = input.files[0]
    triggerPreview()
  }
}

function debouncePreview() {
  if (previewTimer) {
    clearTimeout(previewTimer)
  }
  previewTimer = setTimeout(() => {
    triggerPreview()
  }, 500)
}

// Sync scroll between textarea and line numbers
function syncScroll() {
  if (textareaRef.value && lineNumbersRef.value) {
    lineNumbersRef.value.scrollTop = textareaRef.value.scrollTop
  }
}

async function triggerPreview() {
  importError.value = ''
  previewResult.value = null
  selectedServers.value.clear()
  validationError.value = null

  if (importMode.value === 'file' && importFile.value) {
    await previewFromFile()
  } else if (importMode.value === 'paste' && importContent.value.trim()) {
    await previewFromContent()
  }
}

async function previewFromFile() {
  if (!importFile.value) return

  previewLoading.value = true
  try {
    const response = await api.importServersFromFile(importFile.value, {
      format: importFormat.value || undefined,
      preview: true
    })

    if (response.success && response.data) {
      previewResult.value = response.data
      // Select all servers by default
      response.data.imported.forEach((s: ImportedServer) => selectedServers.value.add(s.name))
    } else {
      importError.value = response.error || 'Failed to preview import'
    }
  } catch (err) {
    importError.value = err instanceof Error ? err.message : 'Failed to preview import'
  } finally {
    previewLoading.value = false
  }
}

async function previewFromContent() {
  if (!importContent.value.trim()) return

  // Validate content first (client-side)
  const validation = validateContent(importContent.value)
  if (!validation.valid) {
    validationError.value = validation.error || { message: 'Invalid content' }
    previewResult.value = null
    return
  }

  // Clear validation error if content is valid
  validationError.value = null

  previewLoading.value = true
  try {
    const response = await api.importServersFromJSON({
      content: importContent.value,
      format: importFormat.value || undefined,
      preview: true
    })

    if (response.success && response.data) {
      previewResult.value = response.data
      // Select all servers by default
      response.data.imported.forEach((s: ImportedServer) => selectedServers.value.add(s.name))
    } else {
      importError.value = response.error || 'Failed to preview import'
    }
  } catch (err) {
    importError.value = err instanceof Error ? err.message : 'Failed to preview import'
  } finally {
    previewLoading.value = false
  }
}

function toggleServer(name: string) {
  if (selectedServers.value.has(name)) {
    selectedServers.value.delete(name)
  } else {
    selectedServers.value.add(name)
  }
}

function toggleAllServers() {
  if (allServersSelected.value) {
    selectedServers.value.clear()
  } else {
    previewResult.value?.imported.forEach(s => selectedServers.value.add(s.name))
  }
}

async function handleImport() {
  if (!previewResult.value || selectedServers.value.size === 0) return

  importLoading.value = true
  importError.value = ''

  try {
    const serverNames = Array.from(selectedServers.value)

    let response
    if (importMode.value === 'file' && importFile.value) {
      response = await api.importServersFromFile(importFile.value, {
        format: importFormat.value || undefined,
        server_names: serverNames,
        preview: false
      })
    } else {
      response = await api.importServersFromJSON({
        content: importContent.value,
        format: importFormat.value || undefined,
        server_names: serverNames,
        preview: false
      })
    }

    if (response.success && response.data) {
      const count = response.data.summary.imported
      systemStore.addToast({
        type: 'success',
        title: 'Import Successful',
        message: `${count} server${count !== 1 ? 's' : ''} imported successfully`
      })

      emit('added')
      handleClose()
    } else {
      importError.value = response.error || 'Failed to import servers'
    }
  } catch (err) {
    importError.value = err instanceof Error ? err.message : 'Failed to import servers'
  } finally {
    importLoading.value = false
  }
}

// Load canonical config paths for quick import hints
async function loadCanonicalPaths() {
  try {
    const response = await api.getCanonicalConfigPaths()
    if (response.success && response.data) {
      // Sort by existence (found configs first), then by name
      canonicalPaths.value = response.data.paths.sort((a, b) => {
        if (a.exists !== b.exists) return a.exists ? -1 : 1
        return a.name.localeCompare(b.name)
      })
    }
  } catch (err) {
    console.error('Failed to load canonical config paths:', err)
  }
}

// Import from a canonical config path
async function importFromCanonicalPath(config: CanonicalConfigPath) {
  importingCanonicalPath.value = config.path
  importError.value = ''

  try {
    // First, preview the import
    const previewResponse = await api.importServersFromPath({
      path: config.path,
      format: config.format,
      preview: true
    })

    if (!previewResponse.success || !previewResponse.data) {
      importError.value = previewResponse.error || 'Failed to preview import'
      return
    }

    // Show preview result
    previewResult.value = previewResponse.data

    // Select all servers by default
    selectedServers.value.clear()
    previewResponse.data.imported.forEach(s => selectedServers.value.add(s.name))
  } catch (err) {
    importError.value = err instanceof Error ? err.message : 'Failed to import from config'
  } finally {
    importingCanonicalPath.value = null
  }
}

// Load canonical paths when component mounts or when switching to import mode
watch(() => props.show, (newVal) => {
  if (newVal) {
    loadCanonicalPaths()
  }
})

function handleClose() {
  // Reset manual form
  formData.type = 'stdio'
  formData.name = ''
  formData.url = ''
  formData.command = ''
  formData.customCommand = ''
  formData.argsText = ''
  formData.envText = ''
  formData.workingDir = ''
  formData.enabled = true
  formData.quarantined = true
  formData.isolated = false
  formData.idleOnInactivity = false
  error.value = ''

  // Reset import state
  importMode.value = 'file'
  importContent.value = ''
  importFormat.value = ''
  importFile.value = null
  previewResult.value = null
  importError.value = ''
  validationError.value = null
  selectedServers.value.clear()

  // Reset tab
  activeTab.value = 'manual'

  emit('close')
}
</script>
