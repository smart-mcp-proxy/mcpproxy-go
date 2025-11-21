<template>
  <div class="space-y-6">
    <!-- Page Header -->
    <div class="flex justify-between items-center">
      <div>
        <h1 class="text-3xl font-bold">MCP Sessions</h1>
        <p class="text-base-content/70 mt-1">Monitor active and recent MCP client sessions</p>
      </div>
      <button @click="loadSessions" class="btn btn-sm btn-ghost" :disabled="loading">
        <svg class="w-4 h-4" :class="{ 'animate-spin': loading }" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
        </svg>
        Refresh
      </button>
    </div>

    <!-- Sessions Table -->
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

        <div v-else-if="sessions.length === 0" class="text-center py-12 text-base-content/60">
          <svg class="w-16 h-16 mx-auto mb-4 opacity-30" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z" />
          </svg>
          <p class="text-lg">No sessions found</p>
          <p class="text-sm mt-1">Sessions will appear here when MCP clients connect</p>
        </div>

        <div v-else class="overflow-x-auto">
          <table class="table">
            <thead>
              <tr>
                <th>Session ID</th>
                <th>Client</th>
                <th>Status</th>
                <th>Capabilities</th>
                <th>Tool Calls</th>
                <th>Tokens</th>
                <th>Started</th>
                <th>Last Active</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="session in sessions" :key="session.id">
                <td>
                  <code class="text-xs bg-base-200 px-2 py-1 rounded" :title="session.id">
                    {{ session.id.substring(0, 12) }}...
                  </code>
                </td>
                <td>
                  <div class="font-medium">{{ session.client_name || 'Unknown' }}</div>
                  <div v-if="session.client_version" class="text-xs text-base-content/60">
                    v{{ session.client_version }}
                  </div>
                </td>
                <td>
                  <div
                    class="badge"
                    :class="session.status === 'active' ? 'badge-success' : 'badge-neutral'"
                  >
                    {{ session.status === 'active' ? 'Active' : 'Closed' }}
                  </div>
                </td>
                <td>
                  <div class="flex flex-wrap gap-1">
                    <span v-if="session.has_roots" class="badge badge-sm badge-info" title="Client supports roots capability">
                      Roots
                    </span>
                    <span v-if="session.has_sampling" class="badge badge-sm badge-info" title="Client supports sampling capability">
                      Sampling
                    </span>
                    <span
                      v-if="session.experimental && session.experimental.length > 0"
                      class="badge badge-sm badge-warning"
                      :title="`Experimental features: ${session.experimental.join(', ')}`"
                    >
                      Experimental ({{ session.experimental.length }})
                    </span>
                    <span v-if="!session.has_roots && !session.has_sampling && (!session.experimental || session.experimental.length === 0)" class="text-xs text-base-content/40">
                      None
                    </span>
                  </div>
                </td>
                <td>
                  <span class="font-mono">{{ session.tool_call_count }}</span>
                </td>
                <td>
                  <span class="font-mono text-sm" :title="`Total tokens used in this session`">
                    {{ session.total_tokens.toLocaleString() }}
                  </span>
                </td>
                <td>
                  <div class="text-sm">{{ formatTimestamp(session.start_time) }}</div>
                  <div class="text-xs text-base-content/60">{{ formatRelativeTime(session.start_time) }}</div>
                </td>
                <td>
                  <div class="text-sm">{{ formatTimestamp(session.last_activity) }}</div>
                  <div class="text-xs text-base-content/60">{{ formatRelativeTime(session.last_activity) }}</div>
                </td>
                <td>
                  <router-link
                    :to="{ name: 'tool-calls', query: { session: session.id } }"
                    class="btn btn-xs btn-primary"
                    title="View tool calls for this session"
                  >
                    View Calls
                  </router-link>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <!-- Info footer -->
        <div v-if="sessions.length > 0" class="text-sm text-base-content/60 mt-4 text-center">
          Showing {{ sessions.length }} most recent sessions
        </div>
      </div>
    </div>

    <!-- Session Info Panel -->
    <div class="card bg-base-100 shadow-md">
      <div class="card-body">
        <h2 class="card-title text-lg">About MCP Sessions</h2>
        <div class="prose prose-sm max-w-none">
          <p>
            MCP sessions represent individual connections from AI clients (like Claude Code) to MCPProxy.
            Each session tracks:
          </p>
          <ul class="text-sm space-y-1 mt-2">
            <li><strong>Tool Calls:</strong> Number of tool invocations made during the session</li>
            <li><strong>Token Usage:</strong> Total tokens consumed across all tool calls</li>
            <li><strong>Duration:</strong> Time from connection to disconnection</li>
          </ul>
          <p class="mt-2 text-xs text-base-content/60">
            Sessions are retained for the 100 most recent connections.
          </p>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import api from '@/services/api'
import type { MCPSession } from '@/types'

// State
const sessions = ref<MCPSession[]>([])
const loading = ref(false)
const error = ref<string | null>(null)

// Polling interval for active session updates
let pollInterval: ReturnType<typeof setInterval> | null = null

// Load sessions from API
const loadSessions = async () => {
  loading.value = true
  error.value = null

  try {
    const response = await api.getSessions(10)
    if (response.success && response.data) {
      sessions.value = response.data.sessions || []
    } else {
      error.value = response.error || 'Failed to load sessions'
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Unknown error'
  } finally {
    loading.value = false
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

// Lifecycle
onMounted(() => {
  loadSessions()

  // Poll for updates every 30 seconds (as per spec)
  pollInterval = setInterval(loadSessions, 30000)
})

onUnmounted(() => {
  if (pollInterval) {
    clearInterval(pollInterval)
  }
})
</script>
