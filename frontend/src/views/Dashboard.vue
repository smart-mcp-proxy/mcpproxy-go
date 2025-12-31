<template>
  <div class="space-y-6">
    <!-- System Diagnostics -->
    <div
      v-if="totalDiagnosticsCount > 0"
      class="alert"
      :class="{
        'alert-error': upstreamErrors.length > 0,
        'alert-warning': upstreamErrors.length === 0 && (oauthRequired.length > 0 || missingSecrets.length > 0),
        'alert-info': upstreamErrors.length === 0 && oauthRequired.length === 0 && missingSecrets.length === 0
      }"
    >
      <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.732-.833-2.5 0L3.732 16.5c-.77.833.192 2.5 1.732 2.5z" />
      </svg>
      <div class="flex-1">
        <h3 class="font-bold">System Diagnostics</h3>
        <div class="text-sm">
          <span class="badge badge-sm mr-2">{{ upstreamErrors.length }} Error{{ upstreamErrors.length !== 1 ? 's' : '' }}</span>
          <span v-if="upstreamErrors.length > 0">{{ upstreamErrors[0].server }}: {{ upstreamErrors[0].message }}</span>
          <span v-else-if="oauthRequired.length > 0">{{ oauthRequired.length }} server{{ oauthRequired.length !== 1 ? 's' : '' }} need authentication</span>
          <span v-else-if="missingSecrets.length > 0">{{ missingSecrets.length }} missing secret{{ missingSecrets.length !== 1 ? 's' : '' }}</span>
        </div>
      </div>
      <button class="btn btn-sm" @click="showDiagnosticsDetail = true">
        Fix
      </button>
      <button v-if="totalDiagnosticsCount > 1" class="btn btn-ghost btn-sm" @click="showDiagnosticsDetail = true">
        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
        </svg>
      </button>
    </div>

    <!-- Servers Needing Attention Banner (using unified health status) -->
    <div
      v-if="serversNeedingAttention.length > 0"
      class="alert alert-warning"
    >
      <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.732-.833-2.5 0L3.732 16.5c-.77.833.192 2.5 1.732 2.5z" />
      </svg>
      <div class="flex-1">
        <h3 class="font-bold">{{ serversNeedingAttention.length }} server{{ serversNeedingAttention.length !== 1 ? 's' : '' }} need{{ serversNeedingAttention.length === 1 ? 's' : '' }} attention</h3>
        <div class="text-sm space-y-1 mt-1">
          <div v-for="server in serversNeedingAttention.slice(0, 3)" :key="server.name" class="flex items-center gap-2">
            <span :class="server.health?.level === 'unhealthy' ? 'text-error' : 'text-warning'">●</span>
            <span class="font-medium">{{ server.name }}</span>
            <span class="opacity-70">{{ server.health?.summary }}</span>
            <button
              v-if="server.health?.action === 'login'"
              @click="triggerServerAction(server.name, 'oauth_login')"
              class="btn btn-xs btn-primary"
            >
              Login
            </button>
            <button
              v-if="server.health?.action === 'restart'"
              @click="triggerServerAction(server.name, 'restart')"
              class="btn btn-xs btn-primary"
            >
              Restart
            </button>
            <button
              v-if="server.health?.action === 'enable'"
              @click="triggerServerAction(server.name, 'enable')"
              class="btn btn-xs btn-primary"
            >
              Enable
            </button>
          </div>
          <div v-if="serversNeedingAttention.length > 3" class="text-xs opacity-60">
            ... and {{ serversNeedingAttention.length - 3 }} more
          </div>
        </div>
      </div>
      <router-link to="/servers" class="btn btn-sm">
        View All Servers
      </router-link>
    </div>

    <!-- Token Savings and Distribution -->
    <div v-if="tokenSavingsData" class="grid grid-cols-1 lg:grid-cols-2 gap-6">
      <!-- Token Savings -->
      <div class="card bg-base-100 shadow-md">
        <div class="card-body">
          <h2 class="card-title text-lg">Token Savings</h2>

          <div class="grid grid-cols-3 gap-4 mt-4">
            <div>
              <div class="text-sm opacity-60">Tokens Saved</div>
              <div class="text-3xl font-bold text-success">{{ formatNumber(tokenSavingsData.saved_tokens) }}</div>
              <div class="text-xs opacity-60">{{ tokenSavingsData.saved_tokens_percentage.toFixed(1) }}% reduction</div>
            </div>
            <div>
              <div class="text-sm opacity-60">Full Tool List Size</div>
              <div class="text-2xl font-bold">{{ formatNumber(tokenSavingsData.total_server_tool_list_size) }}</div>
              <div class="text-xs opacity-60">All upstream servers</div>
            </div>
            <div>
              <div class="text-sm opacity-60">Typical Query Result</div>
              <div class="text-2xl font-bold">{{ formatNumber(tokenSavingsData.average_query_result_size) }}</div>
              <div class="text-xs opacity-60">BM25 search size</div>
            </div>
          </div>
        </div>
      </div>

      <!-- Token Distribution -->
      <div class="card bg-base-100 shadow-md">
        <div class="card-body">
          <h2 class="card-title text-lg">Token Distribution</h2>
          <p class="text-sm opacity-60">Per-server tool list size breakdown</p>

          <!-- Pie Chart -->
          <div class="flex items-center justify-center py-4">
            <div class="w-64 h-64">
              <TokenPieChart v-if="pieChartSegments.length > 0" :data="pieChartSegments" />
            </div>
          </div>

          <!-- Legend -->
          <div class="mt-4 space-y-2 max-h-40 overflow-y-auto">
            <div
              v-for="(segment, index) in pieChartSegments"
              :key="index"
              class="flex items-center justify-between text-sm"
            >
              <div class="flex items-center space-x-2">
                <div class="w-3 h-3 rounded" :style="{ backgroundColor: segment.color }"></div>
                <span class="truncate">{{ segment.name }}</span>
              </div>
              <div class="flex items-center space-x-2">
                <span class="font-mono text-xs">{{ formatNumber(segment.value) }}</span>
                <span class="text-xs opacity-60">({{ segment.percentage.toFixed(1) }}%)</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>


    <!-- Activity Widget -->
    <ActivityWidget />

    <!-- Recent Sessions -->
    <div class="card bg-base-100 shadow-md">
      <div class="card-body">
        <div class="flex items-center justify-between mb-4">
          <div>
            <h2 class="card-title text-lg">Recent Sessions</h2>
            <p class="text-sm opacity-60">MCP client connections</p>
          </div>
          <router-link to="/sessions" class="btn btn-sm btn-ghost">
            View All →
          </router-link>
        </div>

        <div v-if="sessionsLoading" class="flex justify-center py-4">
          <span class="loading loading-spinner loading-sm"></span>
        </div>

        <div v-else-if="sessionsError" class="alert alert-error alert-sm">
          <span>{{ sessionsError }}</span>
        </div>

        <div v-else-if="recentSessions.length === 0" class="text-center py-4 text-base-content/60">
          <p class="text-sm">No sessions yet</p>
        </div>

        <div v-else class="overflow-x-auto">
          <table class="table table-sm">
            <thead>
              <tr>
                <th>Client</th>
                <th>Status</th>
                <th>Capabilities</th>
                <th>Tool Calls</th>
                <th>Tokens</th>
                <th>Started</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="session in recentSessions" :key="session.id" class="hover">
                <td>
                  <div class="font-medium text-sm">{{ session.client_name || 'Unknown' }}</div>
                  <div v-if="session.client_version" class="text-xs text-base-content/60">
                    v{{ session.client_version }}
                  </div>
                </td>
                <td>
                  <div
                    class="badge badge-sm"
                    :class="session.status === 'active' ? 'badge-success' : 'badge-ghost'"
                  >
                    {{ session.status === 'active' ? 'Active' : 'Closed' }}
                  </div>
                </td>
                <td>
                  <div class="flex flex-wrap gap-1">
                    <span v-if="session.has_roots" class="badge badge-xs badge-info" title="Roots">R</span>
                    <span v-if="session.has_sampling" class="badge badge-xs badge-info" title="Sampling">S</span>
                    <span v-if="session.experimental && session.experimental.length > 0" class="badge badge-xs badge-warning" :title="`Experimental: ${session.experimental.join(', ')}`">E</span>
                  </div>
                </td>
                <td class="text-center">{{ session.tool_call_count || 0 }}</td>
                <td class="text-right font-mono text-xs">{{ formatNumber(session.total_tokens || 0) }}</td>
                <td>
                  <span class="text-xs">{{ formatRelativeTime(session.start_time) }}</span>
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
      </div>
    </div>

    <!-- Recent Tool Calls -->
    <div class="card bg-base-100 shadow-md">
      <div class="card-body">
        <div class="flex items-center justify-between mb-4">
          <div>
            <h2 class="card-title text-lg">Recent Tool Calls</h2>
            <p v-if="tokenStats.totalTokens > 0" class="text-sm opacity-60">
              Total usage: <span class="font-bold">{{ formatNumber(tokenStats.totalTokens) }}</span> tokens
              ({{ recentToolCalls.length }} calls, avg {{ formatNumber(tokenStats.avgTokensPerCall) }}/call)
            </p>
          </div>
          <router-link to="/tool-calls" class="btn btn-sm btn-ghost">
            View All →
          </router-link>
        </div>

        <div v-if="toolCallsLoading" class="flex justify-center py-8">
          <span class="loading loading-spinner loading-md"></span>
        </div>

        <div v-else-if="toolCallsError" class="alert alert-error">
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <span>{{ toolCallsError }}</span>
        </div>

        <div v-else-if="recentToolCalls.length === 0" class="text-center py-8 text-base-content/60">
          <svg class="w-12 h-12 mx-auto mb-3 opacity-30" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2" />
          </svg>
          <p>No tool calls yet</p>
          <p class="text-sm mt-1">Tool calls will appear here once servers start executing tools</p>
        </div>

        <div v-else class="overflow-x-auto">
          <table class="table table-sm">
            <thead>
              <tr>
                <th>Time</th>
                <th>Server</th>
                <th>Tool</th>
                <th>Status</th>
                <th>Duration</th>
                <th class="text-right">Tokens</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="call in recentToolCalls" :key="call.id" class="hover">
                <td>
                  <span class="text-xs" :title="call.timestamp">
                    {{ formatRelativeTime(call.timestamp) }}
                  </span>
                </td>
                <td>
                  <router-link
                    :to="`/servers/${call.server_name}`"
                    class="link link-hover text-sm"
                  >
                    {{ call.server_name }}
                  </router-link>
                </td>
                <td>
                  <code class="text-xs">{{ call.tool_name }}</code>
                </td>
                <td>
                  <div
                    class="badge badge-sm"
                    :class="call.error ? 'badge-error' : 'badge-success'"
                  >
                    {{ call.error ? 'Error' : 'Success' }}
                  </div>
                </td>
                <td>
                  <span class="text-xs text-base-content/70">
                    {{ formatDuration(call.duration) }}
                  </span>
                </td>
                <td class="text-right">
                  <span v-if="call.metrics?.total_tokens" class="text-xs font-mono">
                    {{ formatNumber(call.metrics.total_tokens) }}
                  </span>
                  <span v-else class="text-xs opacity-40">-</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <!-- Hints Panel (Bottom of Page) -->
    <CollapsibleHintsPanel :hints="dashboardHints" />
  </div>
</template>

<script setup lang="ts">
import { computed, ref, reactive, onMounted, onUnmounted } from 'vue'
import { useServersStore } from '@/stores/servers'
import { useSystemStore } from '@/stores/system'
import api from '@/services/api'
import CollapsibleHintsPanel from '@/components/CollapsibleHintsPanel.vue'
import TokenPieChart from '@/components/TokenPieChart.vue'
import ActivityWidget from '@/components/ActivityWidget.vue'
import type { Hint } from '@/components/CollapsibleHintsPanel.vue'

const serversStore = useServersStore()
const systemStore = useSystemStore()

// Show diagnostics detail modal
const showDiagnosticsDetail = ref(false)

// Collapsed sections state
const collapsedSections = reactive({
  upstreamErrors: false,
  oauthRequired: false,
  missingSecrets: false,
  runtimeWarnings: false
})

// Dismissed diagnostics
const dismissedDiagnostics = ref(new Set<string>())

// Load dismissed items from localStorage
const STORAGE_KEY = 'mcpproxy-dismissed-diagnostics'
const loadDismissedDiagnostics = () => {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored) {
      const items = JSON.parse(stored) as string[]
      dismissedDiagnostics.value = new Set(items)
    }
  } catch (error) {
    console.warn('Failed to load dismissed diagnostics from localStorage:', error)
  }
}

// Save dismissed items to localStorage
const saveDismissedDiagnostics = () => {
  try {
    const items = Array.from(dismissedDiagnostics.value)
    localStorage.setItem(STORAGE_KEY, JSON.stringify(items))
  } catch (error) {
    console.warn('Failed to save dismissed diagnostics to localStorage:', error)
  }
}

// Load dismissed diagnostics on init
loadDismissedDiagnostics()

// Diagnostics data
const diagnosticsData = ref<any>(null)
const diagnosticsLoading = ref(false)
const diagnosticsError = ref<string | null>(null)

// Auto-refresh interval
let refreshInterval: ReturnType<typeof setInterval> | null = null

// Load diagnostics from API
const loadDiagnostics = async () => {
  diagnosticsLoading.value = true
  diagnosticsError.value = null

  try {
    const response = await api.getDiagnostics()
    if (response.success && response.data) {
      diagnosticsData.value = response.data
    } else {
      diagnosticsError.value = response.error || 'Failed to load diagnostics'
    }
  } catch (error) {
    diagnosticsError.value = error instanceof Error ? error.message : 'Unknown error'
  } finally {
    diagnosticsLoading.value = false
  }
}

// Computed diagnostics with dismiss filtering
const upstreamErrors = computed(() => {
  if (!diagnosticsData.value?.upstream_errors) return []

  return diagnosticsData.value.upstream_errors.filter((error: any) => {
    const errorKey = `error_${error.server}`
    return !dismissedDiagnostics.value.has(errorKey)
  }).map((error: any) => ({
    server: error.server || 'Unknown',
    message: error.message,
    timestamp: new Date(error.timestamp).toLocaleString()
  }))
})

const oauthRequired = computed(() => {
  if (!diagnosticsData.value?.oauth_required) return []

  return diagnosticsData.value.oauth_required.filter((server: string) => {
    const oauthKey = `oauth_${server}`
    return !dismissedDiagnostics.value.has(oauthKey)
  })
})

const missingSecrets = computed(() => {
  if (!diagnosticsData.value?.missing_secrets) return []

  return diagnosticsData.value.missing_secrets.filter((secret: any) => {
    const secretKey = `secret_${secret.name}`
    return !dismissedDiagnostics.value.has(secretKey)
  })
})

const runtimeWarnings = computed(() => {
  if (!diagnosticsData.value?.runtime_warnings) return []

  return diagnosticsData.value.runtime_warnings.filter((warning: any) => {
    const warningKey = `warning_${warning.title}_${warning.timestamp}`
    return !dismissedDiagnostics.value.has(warningKey)
  }).map((warning: any) => ({
    id: `${warning.title}_${warning.timestamp}`,
    category: warning.category,
    message: warning.message,
    timestamp: new Date(warning.timestamp).toLocaleString()
  }))
})

const totalDiagnosticsCount = computed(() => {
  return upstreamErrors.value.length +
         oauthRequired.value.length +
         missingSecrets.value.length +
         runtimeWarnings.value.length
})

const diagnosticsBadgeClass = computed(() => {
  if (totalDiagnosticsCount.value === 0) return 'badge-success'
  if (upstreamErrors.value.length > 0) return 'badge-error'
  if (oauthRequired.value.length > 0 || missingSecrets.value.length > 0) return 'badge-warning'
  return 'badge-info'
})

// Servers needing attention (unhealthy or degraded health level, excluding admin states)
const serversNeedingAttention = computed(() => {
  return serversStore.servers.filter(server => {
    // I-004: Defensive null check for backward compatibility
    if (!server.health) {
      console.warn(`Server ${server.name} missing health field`)
      return false
    }
    // Skip servers with admin states (disabled, quarantined)
    if (server.health.admin_state === 'disabled' || server.health.admin_state === 'quarantined') {
      return false
    }
    // Include servers with unhealthy or degraded health level
    return server.health.level === 'unhealthy' || server.health.level === 'degraded'
  })
})

const lastUpdateTime = computed(() => {
  if (!systemStore.status?.timestamp) return 'Never'

  const now = Date.now()
  const timestamp = systemStore.status.timestamp * 1000 // Convert to milliseconds
  const diff = now - timestamp

  if (diff < 1000) return 'Just now'
  if (diff < 60000) return `${Math.floor(diff / 1000)}s ago`
  if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`

  return new Date(timestamp).toLocaleTimeString()
})

// Methods
const toggleSection = (section: keyof typeof collapsedSections) => {
  collapsedSections[section] = !collapsedSections[section]
}

const dismissError = (error: any) => {
  const key = `error_${error.server}`
  dismissedDiagnostics.value.add(key)
  saveDismissedDiagnostics()
}

const dismissOAuth = (server: string) => {
  const key = `oauth_${server}`
  dismissedDiagnostics.value.add(key)
  saveDismissedDiagnostics()
}

const dismissSecret = (secret: any) => {
  const key = `secret_${secret.name}`
  dismissedDiagnostics.value.add(key)
  saveDismissedDiagnostics()
}

const dismissWarning = (warning: any) => {
  const key = `warning_${warning.id}`
  dismissedDiagnostics.value.add(key)
  saveDismissedDiagnostics()
}

const restoreAllDismissed = () => {
  dismissedDiagnostics.value.clear()
  saveDismissedDiagnostics()
}

const triggerOAuthLogin = async (server: string) => {
  try {
    await serversStore.triggerOAuthLogin(server)
    systemStore.addToast({
      type: 'success',
      title: 'OAuth Login',
      message: `OAuth login initiated for ${server}`
    })
    // Refresh diagnostics after OAuth attempt
    setTimeout(loadDiagnostics, 2000)
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'OAuth Login Failed',
      message: `Failed to initiate OAuth login: ${error instanceof Error ? error.message : 'Unknown error'}`
    })
  }
}

// Trigger server action based on health.action
const triggerServerAction = async (serverName: string, action: string) => {
  try {
    switch (action) {
      case 'oauth_login':
        await serversStore.triggerOAuthLogin(serverName)
        systemStore.addToast({
          type: 'success',
          title: 'OAuth Login',
          message: `OAuth login initiated for ${serverName}`
        })
        break
      case 'restart':
        await serversStore.restartServer(serverName)
        systemStore.addToast({
          type: 'success',
          title: 'Server Restarted',
          message: `${serverName} is restarting`
        })
        break
      case 'enable':
        await serversStore.enableServer(serverName)
        systemStore.addToast({
          type: 'success',
          title: 'Server Enabled',
          message: `${serverName} has been enabled`
        })
        break
      default:
        console.warn(`Unknown action: ${action}`)
    }
    // Refresh after action
    setTimeout(() => {
      loadDiagnostics()
      serversStore.fetchServers()
    }, 1000)
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Action Failed',
      message: error instanceof Error ? error.message : 'Unknown error'
    })
  }
}

// Token Savings Data
const tokenSavingsData = ref<any>(null)
const tokenSavingsLoading = ref(false)
const tokenSavingsError = ref<string | null>(null)

// Tool Calls History
const recentToolCalls = ref<any[]>([])
const toolCallsLoading = ref(false)
const toolCallsError = ref<string | null>(null)

// Recent Sessions
const recentSessions = ref<any[]>([])
const sessionsLoading = ref(false)
const sessionsError = ref<string | null>(null)

// Load token savings data
const loadTokenSavings = async () => {
  tokenSavingsLoading.value = true
  tokenSavingsError.value = null

  try {
    const response = await api.getTokenStats()
    if (response.success && response.data) {
      tokenSavingsData.value = response.data
    } else {
      tokenSavingsError.value = response.error || 'Failed to load token savings'
    }
  } catch (error) {
    tokenSavingsError.value = error instanceof Error ? error.message : 'Unknown error'
  } finally {
    tokenSavingsLoading.value = false
  }
}

// Load recent tool calls
const loadToolCalls = async () => {
  toolCallsLoading.value = true
  toolCallsError.value = null

  try {
    const response = await api.getToolCalls({ limit: 10 })
    if (response.success && response.data) {
      recentToolCalls.value = response.data.tool_calls || []
    } else {
      toolCallsError.value = response.error || 'Failed to load tool calls'
    }
  } catch (error) {
    toolCallsError.value = error instanceof Error ? error.message : 'Unknown error'
  } finally {
    toolCallsLoading.value = false
  }
}

// Load recent sessions
const loadSessions = async () => {
  sessionsLoading.value = true
  sessionsError.value = null

  try {
    const response = await api.getSessions(5)
    if (response.success && response.data) {
      recentSessions.value = response.data.sessions || []
    } else {
      sessionsError.value = response.error || 'Failed to load sessions'
    }
  } catch (error) {
    sessionsError.value = error instanceof Error ? error.message : 'Unknown error'
  } finally {
    sessionsLoading.value = false
  }
}

// Format duration from nanoseconds
const formatDuration = (nanoseconds: number): string => {
  const ms = nanoseconds / 1000000
  if (ms < 1000) return `${Math.round(ms)}ms`
  return `${(ms / 1000).toFixed(2)}s`
}

// Format relative time
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

// Format number with K, M suffixes
const formatNumber = (num: number): string => {
  if (num >= 1000000) return `${(num / 1000000).toFixed(1)}M`
  if (num >= 1000) return `${(num / 1000).toFixed(1)}K`
  return num.toString()
}

// Pie chart colors
const pieChartColors = [
  '#3b82f6',  // blue
  '#10b981',  // green
  '#f59e0b',  // orange
  '#ec4899',  // pink
  '#8b5cf6',  // purple
  '#06b6d4',  // cyan
  '#ef4444',  // red
  '#14b8a6',  // teal
  '#f97316',  // orange-600
  '#a855f7',  // purple-500
  '#6366f1',  // indigo
  '#84cc16',  // lime
  '#f43f5e',  // rose
  '#0ea5e9',  // sky
  '#22c55e',  // green-500
  '#eab308'   // yellow
]

// Compute pie chart segments
const pieChartSegments = computed(() => {
  if (!tokenSavingsData.value?.per_server_tool_list_sizes) return []

  const sizes = tokenSavingsData.value.per_server_tool_list_sizes
  const entries = Object.entries(sizes).sort((a, b) => (b[1] as number) - (a[1] as number))
  const total = entries.reduce((sum, [, value]) => sum + (value as number), 0)

  let offset = 0
  return entries.map(([name, value], index) => {
    const numValue = value as number
    const percentage = (numValue / total) * 100
    const segment = {
      name,
      value: numValue,
      percentage,
      offset,
      color: pieChartColors[index % pieChartColors.length]
    }
    offset += percentage
    return segment
  })
})

// Token statistics from recent tool calls
const tokenStats = computed(() => {
  let totalTokens = 0
  let inputTokens = 0
  let outputTokens = 0
  let callsWithMetrics = 0
  const modelCounts: Record<string, number> = {}

  for (const call of recentToolCalls.value) {
    if (call.metrics) {
      totalTokens += call.metrics.total_tokens || 0
      inputTokens += call.metrics.input_tokens || 0
      outputTokens += call.metrics.output_tokens || 0
      callsWithMetrics++

      const model = call.metrics.model || 'unknown'
      modelCounts[model] = (modelCounts[model] || 0) + 1
    }
  }

  // Find most used model
  let mostUsedModel = ''
  let maxCount = 0
  for (const [model, count] of Object.entries(modelCounts)) {
    if (count > maxCount) {
      maxCount = count
      mostUsedModel = model
    }
  }

  const avgTokensPerCall = callsWithMetrics > 0
    ? Math.round(totalTokens / callsWithMetrics)
    : 0

  return {
    totalTokens,
    inputTokens,
    outputTokens,
    avgTokensPerCall,
    mostUsedModel,
    callsWithMetrics
  }
})

// Dashboard hints
const dashboardHints = computed<Hint[]>(() => {
  const hints: Hint[] = []

  // Add hint if there are upstream errors
  if (upstreamErrors.value.length > 0) {
    hints.push({
      icon: '🔧',
      title: 'Fix Upstream Errors with CLI',
      description: 'Use these commands to diagnose and fix server connection issues',
      sections: [
        {
          title: 'Check server logs',
          codeBlock: {
            language: 'bash',
            code: `# View logs for specific server\ntail -f ~/.mcpproxy/logs/server-${upstreamErrors.value[0].server}.log\n\n# View main log\ntail -f ~/.mcpproxy/logs/main.log`
          }
        },
        {
          title: 'Restart server connection',
          codeBlock: {
            language: 'bash',
            code: `# Disable and re-enable server\nmcpproxy call tool --tool-name=upstream_servers \\\n  --json_args='{"operation":"update","name":"${upstreamErrors.value[0].server}","enabled":false}'\n\nmcpproxy call tool --tool-name=upstream_servers \\\n  --json_args='{"operation":"update","name":"${upstreamErrors.value[0].server}","enabled":true}'`
          }
        }
      ]
    })
  }

  // Add hint if there are missing secrets
  if (missingSecrets.value.length > 0) {
    hints.push({
      icon: '🔐',
      title: 'Set Missing Secrets',
      description: 'Add secrets to your system keyring',
      sections: [
        {
          title: 'Store secret using CLI',
          codeBlock: {
            language: 'bash',
            code: `# Add secret to keyring\nmcpproxy secrets set ${missingSecrets.value[0].name}`
          }
        },
        {
          title: 'Or use environment variable',
          text: 'You can also set environment variables instead of keyring secrets:',
          codeBlock: {
            language: 'bash',
            code: `export ${missingSecrets.value[0].name}="your-secret-value"`
          }
        }
      ]
    })
  }

  // Add hint if there are OAuth required
  if (oauthRequired.value.length > 0) {
    hints.push({
      icon: '🔑',
      title: 'Authenticate OAuth Servers',
      description: 'Complete OAuth authentication for these servers',
      sections: [
        {
          title: 'Login via CLI',
          codeBlock: {
            language: 'bash',
            code: `# Authenticate with OAuth\nmcpproxy auth login --server=${oauthRequired.value[0]}`
          }
        },
        {
          title: 'Check authentication status',
          codeBlock: {
            language: 'bash',
            code: `# View authentication status\nmcpproxy auth status`
          }
        }
      ]
    })
  }

  // Always show general CLI hints
  hints.push({
    icon: '💡',
    title: 'CLI Commands for Managing MCPProxy',
    description: 'Useful commands for working with MCPProxy',
    sections: [
      {
        title: 'View all servers',
        codeBlock: {
          language: 'bash',
          code: `# List all upstream servers\nmcpproxy upstream list`
        }
      },
      {
        title: 'Search for tools',
        codeBlock: {
          language: 'bash',
          code: `# Search across all server tools\nmcpproxy tools search "your query"\n\n# List tools from specific server\nmcpproxy tools list --server=server-name`
        }
      },
      {
        title: 'Call a tool directly',
        codeBlock: {
          language: 'bash',
          code: `# Execute a tool\nmcpproxy call tool --tool-name=server:tool-name \\\n  --json_args='{"arg1":"value1"}'`
        }
      }
    ]
  })

  // LLM Agent hints
  hints.push({
    icon: '🤖',
    title: 'Use MCPProxy with LLM Agents',
    description: 'Connect Claude or other LLM agents to MCPProxy',
    sections: [
      {
        title: 'Example LLM prompts',
        list: [
          'Search for tools related to GitHub issues across all my MCP servers',
          'List all available MCP servers and their connection status',
          'Add a new MCP server from npm package @modelcontextprotocol/server-filesystem',
          'Show me statistics about which tools are being used most frequently'
        ]
      },
      {
        title: 'Configure Claude Desktop',
        text: 'Add MCPProxy to your Claude Desktop config:',
        codeBlock: {
          language: 'json',
          code: `{
  "mcpServers": {
    "mcpproxy": {
      "command": "mcpproxy",
      "args": ["serve"],
      "env": {}
    }
  }
}`
        }
      }
    ]
  })

  return hints
})

// Lifecycle
onMounted(() => {
  // Load diagnostics immediately
  loadDiagnostics()
  // Load token savings immediately
  loadTokenSavings()
  // Load tool calls immediately
  loadToolCalls()
  // Load sessions immediately
  loadSessions()

  // Set up auto-refresh every 30 seconds
  refreshInterval = setInterval(() => {
    loadDiagnostics()
    loadTokenSavings()
    loadToolCalls()
    loadSessions()
  }, 30000)

  // Listen for SSE events to refresh diagnostics
  const handleSSEUpdate = () => {
    setTimeout(() => {
      loadDiagnostics()
      loadToolCalls()
    }, 1000) // Small delay to let backend process the change
  }

  // Listen to system store events
  systemStore.connectEventSource()

  // Refresh when servers change
  serversStore.fetchServers()
})

onUnmounted(() => {
  // Clean up interval
  if (refreshInterval) {
    clearInterval(refreshInterval)
    refreshInterval = null
  }
})
</script>