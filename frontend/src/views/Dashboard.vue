<template>
  <div class="space-y-6">
    <!-- Page Header -->
    <div class="flex justify-between items-center">
      <div>
        <h1 class="text-3xl font-bold">Dashboard</h1>
        <p class="text-base-content/70 mt-1">MCPProxy Control Panel Overview</p>
      </div>
      <div class="flex items-center space-x-2">
        <div
          :class="[
            'badge',
            systemStore.isRunning ? 'badge-success' : 'badge-error'
          ]"
        >
          {{ systemStore.isRunning ? 'Running' : 'Stopped' }}
        </div>
        <span class="text-sm">{{ systemStore.listenAddr || 'Not running' }}</span>
      </div>
    </div>

    <!-- Stats Cards -->
    <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6">
      <!-- Servers Stats -->
      <div class="stats shadow bg-base-100">
        <div class="stat">
          <div class="stat-figure text-primary">
            <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2m-2-4h.01M17 16h.01" />
            </svg>
          </div>
          <div class="stat-title">Total Servers</div>
          <div class="stat-value">{{ serversStore.serverCount.total }}</div>
          <div class="stat-desc">{{ serversStore.serverCount.connected }} connected</div>
        </div>
      </div>

      <!-- Tools Stats -->
      <div class="stats shadow bg-base-100">
        <div class="stat">
          <div class="stat-figure text-secondary">
            <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
            </svg>
          </div>
          <div class="stat-title">Available Tools</div>
          <div class="stat-value">{{ serversStore.totalTools }}</div>
          <div class="stat-desc">across all servers</div>
        </div>
      </div>

      <!-- Enabled Servers -->
      <div class="stats shadow bg-base-100">
        <div class="stat">
          <div class="stat-figure text-success">
            <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
          </div>
          <div class="stat-title">Enabled</div>
          <div class="stat-value">{{ serversStore.serverCount.enabled }}</div>
          <div class="stat-desc">servers active</div>
        </div>
      </div>

      <!-- Quarantined Servers -->
      <div class="stats shadow bg-base-100">
        <div class="stat">
          <div class="stat-figure text-warning">
            <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.732-.833-2.5 0L3.732 16.5c-.77.833.192 2.5 1.732 2.5z" />
            </svg>
          </div>
          <div class="stat-title">Quarantined</div>
          <div class="stat-value">{{ serversStore.serverCount.quarantined }}</div>
          <div class="stat-desc">security review needed</div>
        </div>
      </div>
    </div>

    <!-- Diagnostics Panel -->
    <div class="space-y-6">
      <!-- Main Diagnostics Card -->
      <div class="card bg-base-100 shadow-md">
        <div class="card-body">
          <div class="flex items-center justify-between mb-4">
            <h2 class="card-title text-xl">System Diagnostics</h2>
            <div class="flex items-center space-x-2">
              <div class="badge badge-sm" :class="diagnosticsBadgeClass">
                {{ totalDiagnosticsCount }} {{ totalDiagnosticsCount === 1 ? 'issue' : 'issues' }}
              </div>
              <button
                v-if="dismissedDiagnostics.size > 0"
                @click="restoreAllDismissed"
                class="btn btn-xs btn-ghost"
                title="Restore dismissed issues"
              >
                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
                </svg>
              </button>
            </div>
          </div>

          <!-- Upstream Errors -->
          <div v-if="upstreamErrors.length > 0" class="collapse collapse-arrow border border-error mb-4">
            <input type="checkbox" class="peer" :checked="!collapsedSections.upstreamErrors" @change="toggleSection('upstreamErrors')" />
            <div class="collapse-title bg-error/10 text-error font-medium flex items-center justify-between">
              <div class="flex items-center space-x-2">
                <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                </svg>
                <span>Upstream Errors ({{ upstreamErrors.length }})</span>
              </div>
            </div>
            <div class="collapse-content bg-error/5">
              <div class="pt-4 space-y-3">
                <div
                  v-for="error in upstreamErrors"
                  :key="error.server"
                  class="flex items-start justify-between p-3 bg-base-100 rounded-lg border border-error/20"
                >
                  <div class="flex-1">
                    <div class="font-medium text-error">{{ error.server }}</div>
                    <div class="text-sm text-base-content/70 mt-1">{{ error.message }}</div>
                    <div class="text-xs text-base-content/50 mt-1">{{ error.timestamp }}</div>
                  </div>
                  <div class="flex items-center space-x-2 ml-4">
                    <router-link :to="`/servers/${error.server}`" class="btn btn-xs btn-outline btn-error">
                      Fix
                    </router-link>
                    <button @click="dismissError(error)" class="btn btn-xs btn-ghost" title="Dismiss">
                      <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                      </svg>
                    </button>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <!-- OAuth Required -->
          <div v-if="oauthRequired.length > 0" class="collapse collapse-arrow border border-warning mb-4">
            <input type="checkbox" class="peer" :checked="!collapsedSections.oauthRequired" @change="toggleSection('oauthRequired')" />
            <div class="collapse-title bg-warning/10 text-warning font-medium flex items-center justify-between">
              <div class="flex items-center space-x-2">
                <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.029 5.912c-.563-.097-1.159.026-1.563.43L10.5 17.25H8v2.25H5.5v2.25H3v-2.818c0-.597.237-1.17.659-1.591l6.499-6.499c.404-.404.527-1 .43-1.563A6 6 0 1121 9z" />
                </svg>
                <span>Authentication Required ({{ oauthRequired.length }})</span>
              </div>
            </div>
            <div class="collapse-content bg-warning/5">
              <div class="pt-4 space-y-3">
                <div
                  v-for="server in oauthRequired"
                  :key="server"
                  class="flex items-center justify-between p-3 bg-base-100 rounded-lg border border-warning/20"
                >
                  <div class="flex items-center space-x-3">
                    <svg class="w-5 h-5 text-warning" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.029 5.912c-.563-.097-1.159.026-1.563.43L10.5 17.25H8v2.25H5.5v2.25H3v-2.818c0-.597.237-1.17.659-1.591l6.499-6.499c.404-.404.527-1 .43-1.563A6 6 0 1121 9z" />
                    </svg>
                    <div>
                      <div class="font-medium">{{ server }}</div>
                      <div class="text-sm text-base-content/70">OAuth authentication needed</div>
                    </div>
                  </div>
                  <div class="flex items-center space-x-2">
                    <button @click="triggerOAuthLogin(server)" class="btn btn-xs btn-warning">
                      Login
                    </button>
                    <button @click="dismissOAuth(server)" class="btn btn-xs btn-ghost" title="Dismiss">
                      <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                      </svg>
                    </button>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <!-- Missing Secrets -->
          <div v-if="missingSecrets.length > 0" class="collapse collapse-arrow border border-warning mb-4">
            <input type="checkbox" class="peer" :checked="!collapsedSections.missingSecrets" @change="toggleSection('missingSecrets')" />
            <div class="collapse-title bg-warning/10 text-warning font-medium flex items-center justify-between">
              <div class="flex items-center space-x-2">
                <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
                </svg>
                <span>Missing Secrets ({{ missingSecrets.length }})</span>
              </div>
            </div>
            <div class="collapse-content bg-warning/5">
              <div class="pt-4 space-y-3">
                <div
                  v-for="secret in missingSecrets"
                  :key="secret.name"
                  class="flex items-center justify-between p-3 bg-base-100 rounded-lg border border-warning/20"
                >
                  <div class="flex items-center space-x-3">
                    <svg class="w-5 h-5 text-warning" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
                    </svg>
                    <div>
                      <div class="font-medium">{{ secret.name }}</div>
                      <div class="text-sm text-base-content/70 font-mono">{{ secret.reference }}</div>
                    </div>
                  </div>
                  <div class="flex items-center space-x-2">
                    <router-link to="/secrets" class="btn btn-xs btn-warning">
                      Set Value
                    </router-link>
                    <button @click="dismissSecret(secret)" class="btn btn-xs btn-ghost" title="Dismiss">
                      <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                      </svg>
                    </button>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <!-- Runtime Warnings -->
          <div v-if="runtimeWarnings.length > 0" class="collapse collapse-arrow border border-info mb-4">
            <input type="checkbox" class="peer" :checked="!collapsedSections.runtimeWarnings" @change="toggleSection('runtimeWarnings')" />
            <div class="collapse-title bg-info/10 text-info font-medium flex items-center justify-between">
              <div class="flex items-center space-x-2">
                <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                </svg>
                <span>Runtime Warnings ({{ runtimeWarnings.length }})</span>
              </div>
            </div>
            <div class="collapse-content bg-info/5">
              <div class="pt-4 space-y-3">
                <div
                  v-for="warning in runtimeWarnings"
                  :key="warning.id"
                  class="flex items-start justify-between p-3 bg-base-100 rounded-lg border border-info/20"
                >
                  <div class="flex-1">
                    <div class="font-medium text-info">{{ warning.category }}</div>
                    <div class="text-sm text-base-content/70 mt-1">{{ warning.message }}</div>
                    <div class="text-xs text-base-content/50 mt-1">{{ warning.timestamp }}</div>
                  </div>
                  <button @click="dismissWarning(warning)" class="btn btn-xs btn-ghost ml-4" title="Dismiss">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                    </svg>
                  </button>
                </div>
              </div>
            </div>
          </div>

          <!-- No Issues State -->
          <div v-if="totalDiagnosticsCount === 0" class="text-center py-12">
            <svg class="w-16 h-16 mx-auto mb-4 text-success opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            <h3 class="text-lg font-medium text-success mb-2">All Systems Operational</h3>
            <p class="text-base-content/60">No issues detected with your server configuration</p>
            <router-link to="/servers" class="btn btn-sm btn-outline btn-success mt-4">
              View Servers
            </router-link>
          </div>
        </div>
      </div>

      <!-- Tool Call History Placeholder -->
      <div class="card bg-base-100 shadow-md">
        <div class="card-body">
          <h2 class="card-title text-xl mb-4">Recent Tool Calls</h2>

          <div class="text-center py-12">
            <svg class="w-16 h-16 mx-auto mb-4 text-base-content/30" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
            </svg>
            <h3 class="text-lg font-medium text-base-content/60 mb-2">Tool Call History Coming Soon</h3>
            <p class="text-base-content/40">Tool call logging and history will be available after Phase 3 implementation</p>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, reactive, onMounted, onUnmounted } from 'vue'
import { useServersStore } from '@/stores/servers'
import { useSystemStore } from '@/stores/system'
import api from '@/services/api'

const serversStore = useServersStore()
const systemStore = useSystemStore()

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

// Lifecycle
onMounted(() => {
  // Load diagnostics immediately
  loadDiagnostics()

  // Set up auto-refresh every 30 seconds
  refreshInterval = setInterval(loadDiagnostics, 30000)

  // Listen for SSE events to refresh diagnostics
  const handleSSEUpdate = () => {
    setTimeout(loadDiagnostics, 1000) // Small delay to let backend process the change
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