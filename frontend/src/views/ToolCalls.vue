<template>
  <div class="space-y-6">
    <!-- Page Header -->
    <div class="flex justify-between items-center">
      <div>
        <h1 class="text-3xl font-bold">Tool Call History</h1>
        <p class="text-base-content/70 mt-1">Browse and analyze tool call execution history</p>
      </div>
    </div>

    <!-- Filters -->
    <div class="card bg-base-100 shadow-md">
      <div class="card-body">
        <div class="flex flex-wrap gap-4">
          <div class="form-control flex-1 min-w-[200px]">
            <label class="label">
              <span class="label-text">Filter by Server</span>
            </label>
            <select v-model="filterServer" class="select select-bordered select-sm">
              <option value="">All Servers</option>
              <option v-for="server in availableServers" :key="server" :value="server">
                {{ server }}
              </option>
            </select>
          </div>

          <div class="form-control flex-1 min-w-[200px]">
            <label class="label">
              <span class="label-text">Filter by Tool</span>
            </label>
            <input
              v-model="filterTool"
              type="text"
              placeholder="Tool name..."
              class="input input-bordered input-sm"
            />
          </div>

          <div class="form-control flex-1 min-w-[200px]">
            <label class="label">
              <span class="label-text">Status</span>
            </label>
            <select v-model="filterStatus" class="select select-bordered select-sm">
              <option value="">All</option>
              <option value="success">Success</option>
              <option value="error">Error</option>
            </select>
          </div>

          <div class="form-control">
            <label class="label">
              <span class="label-text">&nbsp;</span>
            </label>
            <button @click="clearFilters" class="btn btn-sm btn-ghost">
              Clear Filters
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Tool Calls Table -->
    <div class="card bg-base-100 shadow-md">
      <div class="card-body">
        <div v-if="loading" class="flex justify-center py-12">
          <span class="loading loading-spinner loading-lg"></span>
        </div>

        <div v-else-if="error" class="alert alert-error">
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <span>{{ error }}</span>
        </div>

        <div v-else-if="filteredToolCalls.length === 0" class="text-center py-12 text-base-content/60">
          <svg class="w-16 h-16 mx-auto mb-4 opacity-30" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2" />
          </svg>
          <p class="text-lg">No tool calls found</p>
          <p class="text-sm mt-1">Try adjusting your filters or wait for servers to execute tools</p>
        </div>

        <div v-else class="overflow-x-auto">
          <table class="table">
            <thead>
              <tr>
                <th>Timestamp</th>
                <th>Server</th>
                <th>Tool</th>
                <th>Status</th>
                <th>Duration</th>
                <th>Tokens</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              <template v-for="call in paginatedToolCalls" :key="call.id">
                <!-- Main row -->
                <tr>
                  <td>
                    <div class="text-sm">{{ formatTimestamp(call.timestamp) }}</div>
                    <div class="text-xs text-base-content/60">{{ formatRelativeTime(call.timestamp) }}</div>
                  </td>
                  <td>
                    <router-link
                      :to="`/servers/${call.server_name}`"
                      class="link link-hover font-medium"
                    >
                      {{ call.server_name }}
                    </router-link>
                  </td>
                  <td>
                    <code class="text-sm bg-base-200 px-2 py-1 rounded">{{ call.tool_name }}</code>
                  </td>
                  <td>
                    <div
                      class="badge"
                      :class="call.error ? 'badge-error' : 'badge-success'"
                    >
                      {{ call.error ? 'Error' : 'Success' }}
                    </div>
                  </td>
                  <td>
                    <span class="text-sm">{{ formatDuration(call.duration) }}</span>
                  </td>
                  <td>
                    <div v-if="call.metrics" class="text-sm">
                      <div class="flex items-center gap-1">
                        <span class="font-mono text-xs" :title="`Input: ${call.metrics.input_tokens}, Output: ${call.metrics.output_tokens}`">
                          {{ call.metrics.total_tokens }}
                        </span>
                        <span class="text-xs text-base-content/60">tokens</span>
                      </div>
                      <div class="flex items-center gap-1 mt-0.5">
                        <span class="text-xs text-base-content/50">
                          {{ call.metrics.model }}
                        </span>
                        <div v-if="call.metrics.was_truncated" class="badge badge-warning badge-xs" :title="`Saved ${call.metrics.truncated_tokens || 0} tokens`">
                          Truncated
                        </div>
                      </div>
                    </div>
                    <span v-else class="text-xs text-base-content/40">—</span>
                  </td>
                  <td>
                    <div class="flex gap-2">
                      <button
                        @click="toggleDetails(call.id)"
                        class="btn btn-xs btn-ghost"
                        :title="expandedCalls.has(call.id) ? 'Collapse' : 'Expand'"
                      >
                        <svg
                          class="w-4 h-4 transition-transform"
                          :class="{ 'rotate-180': expandedCalls.has(call.id) }"
                          fill="none"
                          stroke="currentColor"
                          viewBox="0 0 24 24"
                        >
                          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
                        </svg>
                      </button>
                      <button
                        @click="openReplayModal(call)"
                        class="btn btn-xs btn-primary"
                        title="Replay tool call"
                      >
                        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
                        </svg>
                      </button>
                      <button
                        @click="copyCLICommand(call)"
                        class="btn btn-xs btn-ghost"
                        title="Copy as CLI command"
                      >
                        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
                        </svg>
                      </button>
                    </div>
                  </td>
                </tr>

                <!-- Expandable detail row (immediately follows main row) -->
                <tr v-show="expandedCalls.has(call.id)">
                  <td colspan="7" class="bg-base-200">
                    <div class="p-4 space-y-4">
                      <!-- Arguments -->
                      <div>
                        <h4 class="font-semibold mb-2">Arguments:</h4>
                        <JsonViewer :data="call.arguments" max-height="15rem" />
                      </div>

                      <!-- Response or Error -->
                      <div v-if="call.error">
                        <h4 class="font-semibold mb-2 text-error">Error:</h4>
                        <div class="alert alert-error">
                          <span class="font-mono text-sm break-words">{{ call.error }}</span>
                        </div>
                      </div>
                      <div v-else-if="call.response">
                        <h4 class="font-semibold mb-2 text-success">Response:</h4>
                        <JsonViewer :data="call.response" max-height="24rem" />
                      </div>

                      <!-- Metadata -->
                      <div class="text-xs text-base-content/60 space-y-1">
                        <div class="break-all"><strong>Call ID:</strong> {{ call.id }}</div>
                        <div class="break-all"><strong>Server ID:</strong> {{ call.server_id }}</div>
                        <div v-if="call.request_id" class="break-all"><strong>Request ID:</strong> {{ call.request_id }}</div>
                        <div class="break-all"><strong>Config Path:</strong> {{ call.config_path }}</div>
                        <div v-if="call.metrics" class="pt-2 border-t border-base-300 mt-2">
                          <div class="font-semibold text-sm text-base-content/80 mb-1">Token Usage:</div>
                          <div><strong>Input Tokens:</strong> {{ call.metrics.input_tokens.toLocaleString() }}</div>
                          <div><strong>Output Tokens:</strong> {{ call.metrics.output_tokens.toLocaleString() }}</div>
                          <div><strong>Total Tokens:</strong> {{ call.metrics.total_tokens.toLocaleString() }}</div>
                          <div><strong>Model:</strong> {{ call.metrics.model }}</div>
                          <div><strong>Encoding:</strong> {{ call.metrics.encoding }}</div>
                          <div v-if="call.metrics.was_truncated" class="mt-2 pt-2 border-t border-base-300">
                            <div class="font-semibold text-sm text-warning mb-1 flex items-center gap-2">
                              <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
                                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M2.458 12C3.732 7.943 7.523 5 12 5c4.478 0 8.268 2.943 9.542 7-1.274 4.057-5.064 7-9.542 7-4.477 0-8.268-2.943-9.542-7z" />
                              </svg>
                              Response Truncation:
                            </div>
                            <div><strong>Truncated Tokens:</strong> {{ (call.metrics.truncated_tokens || 0).toLocaleString() }}</div>
                            <div class="text-success"><strong>Tokens Saved:</strong> {{ (call.metrics.truncated_tokens || 0).toLocaleString() }}</div>
                          </div>
                        </div>
                      </div>
                    </div>
                  </td>
                </tr>
              </template>
            </tbody>
          </table>

          <!-- Pagination -->
          <div v-if="filteredToolCalls.length > itemsPerPage" class="flex justify-center mt-6">
            <div class="join">
              <button
                @click="currentPage = Math.max(1, currentPage - 1)"
                :disabled="currentPage === 1"
                class="join-item btn btn-sm"
              >
                «
              </button>
              <button class="join-item btn btn-sm">
                Page {{ currentPage }} of {{ totalPages }}
              </button>
              <button
                @click="currentPage = Math.min(totalPages, currentPage + 1)"
                :disabled="currentPage === totalPages"
                class="join-item btn btn-sm"
              >
                »
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Replay Modal -->
    <dialog ref="replayModal" class="modal">
      <div class="modal-box max-w-4xl">
        <h3 class="font-bold text-lg mb-4">Replay Tool Call</h3>

        <div v-if="replayingCall" class="space-y-4">
          <!-- Call Info -->
          <div class="bg-base-200 p-3 rounded">
            <div class="text-sm space-y-1">
              <div><strong>Server:</strong> {{ replayingCall.server_name }}</div>
              <div><strong>Tool:</strong> <code class="bg-base-300 px-2 py-1 rounded">{{ replayingCall.tool_name }}</code></div>
              <div><strong>Original Call ID:</strong> {{ replayingCall.id }}</div>
            </div>
          </div>

          <!-- Arguments Editor -->
          <div>
            <label class="label">
              <span class="label-text font-semibold">Edit Arguments (JSON)</span>
            </label>
            <vue-monaco-editor
              v-model:value="replayArgsJson"
              language="json"
              theme="vs-dark"
              :options="editorOptions"
              height="300px"
            />
          </div>

          <!-- Validation Error -->
          <div v-if="replayValidationError" class="alert alert-error">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            <span>{{ replayValidationError }}</span>
          </div>

          <!-- Replay Result -->
          <div v-if="replayResult" class="space-y-2">
            <div v-if="replayResult.success" class="alert alert-success">
              <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7" />
              </svg>
              <span>Tool call replayed successfully! New call ID: {{ replayResult.new_call_id }}</span>
            </div>

            <!-- Response Preview -->
            <div v-if="replayResult.new_tool_call">
              <h4 class="font-semibold mb-2">Response:</h4>
              <pre class="bg-base-300 p-3 rounded text-xs overflow-x-auto max-h-60">{{ JSON.stringify(replayResult.new_tool_call.response || replayResult.new_tool_call.error, null, 2) }}</pre>
            </div>
          </div>
        </div>

        <!-- Actions -->
        <div class="modal-action">
          <button
            v-if="!replayResult"
            @click="executeReplay"
            :disabled="replaying"
            class="btn btn-primary"
          >
            <span v-if="replaying" class="loading loading-spinner loading-sm"></span>
            <span v-else>Replay Tool Call</span>
          </button>
          <button
            v-if="replayResult?.success"
            @click="closeReplayModal(); loadToolCalls()"
            class="btn btn-success"
          >
            Close & Refresh
          </button>
          <button
            @click="closeReplayModal"
            class="btn"
            :class="{ 'btn-ghost': !replayResult }"
          >
            {{ replayResult ? 'Close' : 'Cancel' }}
          </button>
        </div>
      </div>
      <form method="dialog" class="modal-backdrop">
        <button @click="closeReplayModal">close</button>
      </form>
    </dialog>

    <!-- Hints Panel (Bottom of Page) -->
    <CollapsibleHintsPanel :hints="toolCallsHints" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useSystemStore } from '@/stores/system'
import api from '@/services/api'
import type { ToolCallRecord } from '@/types'
import { VueMonacoEditor } from '@guolao/vue-monaco-editor'
import JsonViewer from '@/components/JsonViewer.vue'
import CollapsibleHintsPanel from '@/components/CollapsibleHintsPanel.vue'
import type { Hint } from '@/components/CollapsibleHintsPanel.vue'

const systemStore = useSystemStore()

// State
const toolCalls = ref<ToolCallRecord[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const expandedCalls = ref(new Set<string>())

// Filters
const filterServer = ref('')
const filterTool = ref('')
const filterStatus = ref('')

// Pagination
const currentPage = ref(1)
const itemsPerPage = 20

// Replay modal state
const replayModal = ref<HTMLDialogElement>()
const replayingCall = ref<ToolCallRecord | null>(null)
const replayArgsJson = ref('')
const replayValidationError = ref('')
const replayResult = ref<any>(null)
const replaying = ref(false)

// Monaco editor options
const editorOptions = {
  minimap: { enabled: false },
  lineNumbers: 'on' as 'on',
  scrollBeyondLastLine: false,
  wordWrap: 'on' as 'on',
  automaticLayout: true
}

// Load tool calls
const loadToolCalls = async () => {
  loading.value = true
  error.value = null

  try {
    const response = await api.getToolCalls({ limit: 100 })
    if (response.success && response.data) {
      toolCalls.value = response.data.tool_calls || []
    } else {
      error.value = response.error || 'Failed to load tool calls'
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Unknown error'
  } finally {
    loading.value = false
  }
}

// Computed properties
const availableServers = computed(() => {
  const servers = new Set<string>()
  toolCalls.value.forEach(call => servers.add(call.server_name))
  return Array.from(servers).sort()
})

const filteredToolCalls = computed(() => {
  let filtered = toolCalls.value

  if (filterServer.value) {
    filtered = filtered.filter(call => call.server_name === filterServer.value)
  }

  if (filterTool.value) {
    const searchTerm = filterTool.value.toLowerCase()
    filtered = filtered.filter(call => call.tool_name.toLowerCase().includes(searchTerm))
  }

  if (filterStatus.value === 'success') {
    filtered = filtered.filter(call => !call.error)
  } else if (filterStatus.value === 'error') {
    filtered = filtered.filter(call => !!call.error)
  }

  return filtered
})

const totalPages = computed(() => Math.ceil(filteredToolCalls.value.length / itemsPerPage))

const paginatedToolCalls = computed(() => {
  const start = (currentPage.value - 1) * itemsPerPage
  const end = start + itemsPerPage
  return filteredToolCalls.value.slice(start, end)
})

// Actions
const clearFilters = () => {
  filterServer.value = ''
  filterTool.value = ''
  filterStatus.value = ''
  currentPage.value = 1
}

const toggleDetails = (callId: string) => {
  const newSet = new Set(expandedCalls.value)
  if (newSet.has(callId)) {
    newSet.delete(callId)
  } else {
    newSet.add(callId)
  }
  expandedCalls.value = newSet
}

const copyCLICommand = (call: ToolCallRecord) => {
  const argsJson = JSON.stringify(call.arguments)
  const command = `mcpproxy call tool --tool-name="${call.server_name}:${call.tool_name}" --json_args='${argsJson}'`

  navigator.clipboard.writeText(command).then(() => {
    systemStore.addToast({
      type: 'success',
      title: 'Copied!',
      message: 'CLI command copied to clipboard'
    })
  }).catch(() => {
    systemStore.addToast({
      type: 'error',
      title: 'Copy Failed',
      message: 'Failed to copy command to clipboard'
    })
  })
}

// Replay methods
const openReplayModal = (call: ToolCallRecord) => {
  replayingCall.value = call
  replayArgsJson.value = JSON.stringify(call.arguments, null, 2)
  replayValidationError.value = ''
  replayResult.value = null
  replaying.value = false
  replayModal.value?.showModal()
}

const closeReplayModal = () => {
  replayModal.value?.close()
  replayingCall.value = null
}

const executeReplay = async () => {
  if (!replayingCall.value) return

  replaying.value = true
  replayValidationError.value = ''

  try {
    const args = JSON.parse(replayArgsJson.value)
    const response = await api.replayToolCall(replayingCall.value.id, args)

    if (response.success && response.data) {
      replayResult.value = response.data
      systemStore.addToast({
        type: 'success',
        title: 'Tool Call Replayed',
        message: `Successfully replayed tool call. New ID: ${response.data.new_call_id}`
      })
    } else {
      replayValidationError.value = response.error || 'Failed to replay tool call'
    }
  } catch (error: any) {
    replayValidationError.value = error.message || 'Invalid JSON or replay failed'
  } finally {
    replaying.value = false
  }
}

// Format helpers
const formatTimestamp = (timestamp: string): string => {
  return new Date(timestamp).toLocaleString()
}

const formatRelativeTime = (timestamp: string): string => {
  const now = Date.now()
  const time = new Date(timestamp).getTime()
  const diff = now - time

  if (diff < 1000) return 'Just now'
  if (diff < 60000) return `${Math.floor(diff / 1000)}s ago`
  if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`
  if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`
  return `${Math.floor(diff / 86400000)}d ago`
}

const formatDuration = (nanoseconds: number): string => {
  const ms = nanoseconds / 1000000
  if (ms < 1000) return `${Math.round(ms)}ms`
  return `${(ms / 1000).toFixed(2)}s`
}

// Tool calls hints
const toolCallsHints = computed<Hint[]>(() => {
  return [
    {
      icon: '📊',
      title: 'Understanding Tool Call History',
      description: 'Track and analyze tool execution performance',
      sections: [
        {
          title: 'What you can see',
          list: [
            'Complete execution history with timestamps',
            'Success/error status for each call',
            'Token usage and cost metrics',
            'Request and response payloads',
            'Duration and performance stats'
          ]
        },
        {
          title: 'Export tool call data',
          text: 'Get tool call history via API:',
          codeBlock: {
            language: 'bash',
            code: `# Get recent tool calls\ncurl -H "X-API-Key: your-api-key" \\\n  "http://127.0.0.1:8080/api/v1/tool-calls?limit=100" | jq`
          }
        }
      ]
    },
    {
      icon: '🔁',
      title: 'Replay Tool Calls',
      description: 'Re-execute previous tool calls for testing',
      sections: [
        {
          title: 'How to replay',
          list: [
            'Click the replay button on any tool call row',
            'Edit the arguments in the JSON editor',
            'Execute to create a new tool call with modified args',
            'Compare responses to debug issues'
          ]
        },
        {
          title: 'Replay via CLI',
          text: 'Copy tool call as CLI command:',
          codeBlock: {
            language: 'bash',
            code: `# Use the copy button to get CLI command\nmcpproxy call tool --tool-name=server:tool \\\n  --json_args='{"arg":"value"}'`
          }
        }
      ]
    },
    {
      icon: '💡',
      title: 'Token Optimization',
      description: 'Understand token usage and savings',
      sections: [
        {
          title: 'Token metrics',
          list: [
            'Input tokens: Size of tool arguments and context',
            'Output tokens: Size of tool response',
            'Truncated tokens: Tokens saved by response limits',
            'Model: AI model used for token counting'
          ]
        },
        {
          title: 'Response truncation',
          text: 'MCPProxy automatically truncates large responses to save tokens. Look for the "Truncated" badge to see token savings.'
        }
      ]
    }
  ]
})

// Lifecycle
onMounted(() => {
  loadToolCalls()
})
</script>