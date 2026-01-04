<template>
  <div class="space-y-6">
    <!-- Page Header with Summary -->
    <div class="flex flex-wrap justify-between items-start gap-4">
      <div>
        <h1 class="text-3xl font-bold">Activity Log</h1>
        <p class="text-base-content/70 mt-1">Monitor and analyze all activity across your MCP servers</p>
      </div>
      <div class="flex items-center gap-4">
        <!-- Auto-refresh Toggle -->
        <div class="form-control">
          <label class="label cursor-pointer gap-2">
            <span class="label-text text-sm">Auto-refresh</span>
            <input type="checkbox" v-model="autoRefresh" class="toggle toggle-sm toggle-primary" />
          </label>
        </div>
        <!-- Connection Status -->
        <div class="flex items-center gap-2">
          <div class="badge" :class="systemStore.connected ? 'badge-success' : 'badge-error'">
            <span class="w-2 h-2 rounded-full mr-1" :class="systemStore.connected ? 'bg-success animate-pulse' : 'bg-error'"></span>
            {{ systemStore.connected ? 'Live' : 'Disconnected' }}
          </div>
        </div>
        <!-- Manual Refresh -->
        <button v-if="!autoRefresh" @click="loadActivities" class="btn btn-sm btn-ghost" :disabled="loading">
          <svg class="w-4 h-4" :class="{ 'animate-spin': loading }" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
          </svg>
        </button>
      </div>
    </div>

    <!-- Summary Stats -->
    <div v-if="summary" class="stats shadow bg-base-100 w-full">
      <div class="stat">
        <div class="stat-title">Total (24h)</div>
        <div class="stat-value text-2xl">{{ summary.total_count }}</div>
      </div>
      <div class="stat">
        <div class="stat-title">Success</div>
        <div class="stat-value text-2xl text-success">{{ summary.success_count }}</div>
      </div>
      <div class="stat">
        <div class="stat-title">Errors</div>
        <div class="stat-value text-2xl text-error">{{ summary.error_count }}</div>
      </div>
      <div class="stat">
        <div class="stat-title">Blocked</div>
        <div class="stat-value text-2xl text-warning">{{ summary.blocked_count }}</div>
      </div>
    </div>

    <!-- Filters -->
    <div class="card bg-base-100 shadow-md">
      <div class="card-body py-4">
        <div class="flex flex-wrap gap-4 items-end">
          <!-- Type Filter -->
          <div class="form-control min-w-[150px]">
            <label class="label py-1">
              <span class="label-text text-xs">Type</span>
            </label>
            <select v-model="filterType" class="select select-bordered select-sm">
              <option value="">All Types</option>
              <option value="tool_call">Tool Call</option>
              <option value="policy_decision">Policy Decision</option>
              <option value="quarantine_change">Quarantine Change</option>
              <option value="server_change">Server Change</option>
            </select>
          </div>

          <!-- Server Filter -->
          <div class="form-control min-w-[150px]">
            <label class="label py-1">
              <span class="label-text text-xs">Server</span>
            </label>
            <select v-model="filterServer" class="select select-bordered select-sm">
              <option value="">All Servers</option>
              <option v-for="server in availableServers" :key="server" :value="server">
                {{ server }}
              </option>
            </select>
          </div>

          <!-- Status Filter -->
          <div class="form-control min-w-[120px]">
            <label class="label py-1">
              <span class="label-text text-xs">Status</span>
            </label>
            <select v-model="filterStatus" class="select select-bordered select-sm">
              <option value="">All</option>
              <option value="success">Success</option>
              <option value="error">Error</option>
              <option value="blocked">Blocked</option>
            </select>
          </div>

          <!-- Date Range Filter -->
          <div class="form-control min-w-[160px]">
            <label class="label py-1">
              <span class="label-text text-xs">From</span>
            </label>
            <input
              type="datetime-local"
              v-model="filterStartDate"
              class="input input-bordered input-sm"
            />
          </div>
          <div class="form-control min-w-[160px]">
            <label class="label py-1">
              <span class="label-text text-xs">To</span>
            </label>
            <input
              type="datetime-local"
              v-model="filterEndDate"
              class="input input-bordered input-sm"
            />
          </div>

          <!-- Clear Filters -->
          <button
            v-if="hasActiveFilters"
            @click="clearFilters"
            class="btn btn-sm btn-ghost"
          >
            Clear Filters
          </button>

          <!-- Spacer -->
          <div class="flex-1"></div>

          <!-- Export Dropdown -->
          <div class="dropdown dropdown-end">
            <div tabindex="0" role="button" class="btn btn-sm btn-outline">
              <svg class="w-4 h-4 mr-1" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 10v6m0 0l-3-3m3 3l3-3m2 8H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
              </svg>
              Export
            </div>
            <ul tabindex="0" class="dropdown-content z-[1] menu p-2 shadow-lg bg-base-200 rounded-box w-40">
              <li><a @click="exportActivities('json')">Export as JSON</a></li>
              <li><a @click="exportActivities('csv')">Export as CSV</a></li>
            </ul>
          </div>
        </div>

        <!-- Active Filters Summary -->
        <div v-if="hasActiveFilters" class="flex flex-wrap gap-2 mt-2 pt-2 border-t border-base-300">
          <span class="text-xs text-base-content/60">Active filters:</span>
          <span v-if="filterType" class="badge badge-sm badge-outline">Type: {{ formatType(filterType) }}</span>
          <span v-if="filterServer" class="badge badge-sm badge-outline">Server: {{ filterServer }}</span>
          <span v-if="filterStatus" class="badge badge-sm badge-outline">Status: {{ filterStatus }}</span>
          <span v-if="filterStartDate" class="badge badge-sm badge-outline">From: {{ new Date(filterStartDate).toLocaleString() }}</span>
          <span v-if="filterEndDate" class="badge badge-sm badge-outline">To: {{ new Date(filterEndDate).toLocaleString() }}</span>
        </div>
      </div>
    </div>

    <!-- Activity Table -->
    <div class="card bg-base-100 shadow-md">
      <div class="card-body">
        <!-- Loading State -->
        <div v-if="loading && activities.length === 0" class="flex justify-center py-12">
          <span class="loading loading-spinner loading-lg"></span>
        </div>

        <!-- Error State -->
        <div v-else-if="error" class="alert alert-error">
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <span>{{ error }}</span>
          <button @click="loadActivities" class="btn btn-sm btn-ghost">Retry</button>
        </div>

        <!-- Empty State -->
        <div v-else-if="filteredActivities.length === 0" class="text-center py-12 text-base-content/60">
          <svg class="w-16 h-16 mx-auto mb-4 opacity-30" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2" />
          </svg>
          <p class="text-lg">{{ hasActiveFilters ? 'No matching activities' : 'No activity records found' }}</p>
          <p class="text-sm mt-1">{{ hasActiveFilters ? 'Try adjusting your filters' : 'Activity will appear here as tools are called and actions are taken' }}</p>
        </div>

        <!-- Activity Table -->
        <div v-else class="overflow-x-auto">
          <table class="table table-sm">
            <thead>
              <tr>
                <th>Time</th>
                <th>Type</th>
                <th>Server</th>
                <th>Details</th>
                <th>Status</th>
                <th>Duration</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="activity in paginatedActivities"
                :key="activity.id"
                class="hover cursor-pointer"
                :class="{ 'bg-base-200': selectedActivity?.id === activity.id }"
                @click="selectActivity(activity)"
              >
                <td>
                  <div class="text-sm">{{ formatTimestamp(activity.timestamp) }}</div>
                  <div class="text-xs text-base-content/60">{{ formatRelativeTime(activity.timestamp) }}</div>
                </td>
                <td>
                  <div class="flex items-center gap-2">
                    <span class="text-lg">{{ getTypeIcon(activity.type) }}</span>
                    <span class="text-sm">{{ formatType(activity.type) }}</span>
                  </div>
                </td>
                <td>
                  <router-link
                    v-if="activity.server_name"
                    :to="`/servers/${activity.server_name}`"
                    class="link link-hover font-medium"
                    @click.stop
                  >
                    {{ activity.server_name }}
                  </router-link>
                  <span v-else class="text-base-content/40">-</span>
                </td>
                <td>
                  <div class="max-w-xs truncate">
                    <code v-if="activity.tool_name" class="text-sm bg-base-200 px-2 py-1 rounded">
                      {{ activity.tool_name }}
                    </code>
                    <span v-else-if="activity.metadata?.action" class="text-sm">
                      {{ activity.metadata.action }}
                    </span>
                    <span v-else class="text-base-content/40">-</span>
                  </div>
                </td>
                <td>
                  <div
                    class="badge badge-sm"
                    :class="getStatusBadgeClass(activity.status)"
                  >
                    {{ formatStatus(activity.status) }}
                  </div>
                </td>
                <td>
                  <span v-if="activity.duration_ms !== undefined" class="text-sm">
                    {{ formatDuration(activity.duration_ms) }}
                  </span>
                  <span v-else class="text-base-content/40">-</span>
                </td>
                <td>
                  <button class="btn btn-xs btn-ghost" @click.stop="selectActivity(activity)">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7" />
                    </svg>
                  </button>
                </td>
              </tr>
            </tbody>
          </table>

          <!-- Pagination -->
          <div v-if="totalPages > 1" class="flex justify-between items-center mt-4 pt-4 border-t border-base-300">
            <div class="text-sm text-base-content/60">
              Showing {{ (currentPage - 1) * pageSize + 1 }}-{{ Math.min(currentPage * pageSize, filteredActivities.length) }} of {{ filteredActivities.length }}
            </div>
            <div class="join">
              <button
                @click="currentPage = 1"
                :disabled="currentPage === 1"
                class="join-item btn btn-sm"
              >
                «
              </button>
              <button
                @click="currentPage = Math.max(1, currentPage - 1)"
                :disabled="currentPage === 1"
                class="join-item btn btn-sm"
              >
                ‹
              </button>
              <button class="join-item btn btn-sm">
                {{ currentPage }} / {{ totalPages }}
              </button>
              <button
                @click="currentPage = Math.min(totalPages, currentPage + 1)"
                :disabled="currentPage === totalPages"
                class="join-item btn btn-sm"
              >
                ›
              </button>
              <button
                @click="currentPage = totalPages"
                :disabled="currentPage === totalPages"
                class="join-item btn btn-sm"
              >
                »
              </button>
            </div>
            <div class="form-control">
              <select v-model.number="pageSize" class="select select-bordered select-sm">
                <option :value="10">10 / page</option>
                <option :value="25">25 / page</option>
                <option :value="50">50 / page</option>
                <option :value="100">100 / page</option>
              </select>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Activity Detail Drawer -->
    <div class="drawer drawer-end">
      <input id="activity-detail-drawer" type="checkbox" class="drawer-toggle" v-model="showDetailDrawer" />
      <div class="drawer-side z-50">
        <label for="activity-detail-drawer" aria-label="close sidebar" class="drawer-overlay"></label>
        <div class="bg-base-100 w-[500px] min-h-full p-6">
          <div v-if="selectedActivity" class="space-y-4">
            <!-- Header -->
            <div class="flex justify-between items-start">
              <div>
                <h3 class="text-lg font-bold flex items-center gap-2">
                  <span class="text-2xl">{{ getTypeIcon(selectedActivity.type) }}</span>
                  {{ formatType(selectedActivity.type) }}
                </h3>
                <p class="text-sm text-base-content/60">{{ formatTimestamp(selectedActivity.timestamp) }}</p>
              </div>
              <button @click="closeDetailDrawer" class="btn btn-sm btn-circle btn-ghost">
                <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                </svg>
              </button>
            </div>

            <!-- Status Badge -->
            <div class="flex items-center gap-2">
              <span class="text-sm text-base-content/60">Status:</span>
              <div class="badge" :class="getStatusBadgeClass(selectedActivity.status)">
                {{ formatStatus(selectedActivity.status) }}
              </div>
            </div>

            <!-- Metadata -->
            <div class="space-y-3">
              <div v-if="selectedActivity.id" class="flex gap-2">
                <span class="text-sm text-base-content/60 w-24 shrink-0">ID:</span>
                <code class="text-xs bg-base-200 px-2 py-1 rounded break-all">{{ selectedActivity.id }}</code>
              </div>
              <div v-if="selectedActivity.server_name" class="flex gap-2">
                <span class="text-sm text-base-content/60 w-24 shrink-0">Server:</span>
                <router-link :to="`/servers/${selectedActivity.server_name}`" class="link link-primary text-sm">
                  {{ selectedActivity.server_name }}
                </router-link>
              </div>
              <div v-if="selectedActivity.tool_name" class="flex gap-2">
                <span class="text-sm text-base-content/60 w-24 shrink-0">Tool:</span>
                <code class="text-sm bg-base-200 px-2 py-1 rounded">{{ selectedActivity.tool_name }}</code>
              </div>
              <div v-if="selectedActivity.duration_ms !== undefined" class="flex gap-2">
                <span class="text-sm text-base-content/60 w-24 shrink-0">Duration:</span>
                <span class="text-sm">{{ formatDuration(selectedActivity.duration_ms) }}</span>
              </div>
              <div v-if="selectedActivity.session_id" class="flex gap-2">
                <span class="text-sm text-base-content/60 w-24 shrink-0">Session:</span>
                <code class="text-xs bg-base-200 px-2 py-1 rounded">{{ selectedActivity.session_id }}</code>
              </div>
              <div v-if="selectedActivity.source" class="flex gap-2">
                <span class="text-sm text-base-content/60 w-24 shrink-0">Source:</span>
                <span class="badge badge-sm badge-outline">{{ selectedActivity.source }}</span>
              </div>
            </div>

            <!-- Arguments (Request) -->
            <div v-if="selectedActivity.arguments && Object.keys(selectedActivity.arguments).length > 0">
              <h4 class="font-semibold mb-2 flex items-center gap-2">
                Request Arguments
                <span class="badge badge-sm badge-info">JSON</span>
              </h4>
              <JsonViewer :data="selectedActivity.arguments" max-height="12rem" />
            </div>

            <!-- Response -->
            <div v-if="selectedActivity.response">
              <h4 class="font-semibold mb-2 flex items-center gap-2">
                Response Body
                <span class="badge badge-sm badge-info">JSON</span>
                <span v-if="selectedActivity.response_truncated" class="badge badge-sm badge-warning">Truncated</span>
              </h4>
              <JsonViewer :data="parseResponseData(selectedActivity.response)" max-height="16rem" />
            </div>

            <!-- Error -->
            <div v-if="selectedActivity.error_message">
              <h4 class="font-semibold mb-2 text-error">Error Message</h4>
              <div class="alert alert-error">
                <span class="text-sm break-words">{{ selectedActivity.error_message }}</span>
              </div>
            </div>

            <!-- Intent (if present) -->
            <div v-if="selectedActivity.metadata?.intent">
              <h4 class="font-semibold mb-2">Intent Declaration</h4>
              <div class="bg-base-200 rounded p-3 space-y-2">
                <div v-if="selectedActivity.metadata.intent.operation_type" class="flex gap-2">
                  <span class="text-sm text-base-content/60">Operation:</span>
                  <span class="badge badge-sm" :class="getIntentBadgeClass(selectedActivity.metadata.intent.operation_type)">
                    {{ getIntentIcon(selectedActivity.metadata.intent.operation_type) }} {{ selectedActivity.metadata.intent.operation_type }}
                  </span>
                </div>
                <div v-if="selectedActivity.metadata.intent.data_sensitivity" class="flex gap-2">
                  <span class="text-sm text-base-content/60">Sensitivity:</span>
                  <span class="text-sm">{{ selectedActivity.metadata.intent.data_sensitivity }}</span>
                </div>
                <div v-if="selectedActivity.metadata.intent.reason" class="flex gap-2">
                  <span class="text-sm text-base-content/60">Reason:</span>
                  <span class="text-sm">{{ selectedActivity.metadata.intent.reason }}</span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useSystemStore } from '@/stores/system'
import api from '@/services/api'
import type { ActivityRecord, ActivitySummaryResponse } from '@/types/api'
import JsonViewer from '@/components/JsonViewer.vue'

const systemStore = useSystemStore()

// State
const activities = ref<ActivityRecord[]>([])
const summary = ref<ActivitySummaryResponse | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
const selectedActivity = ref<ActivityRecord | null>(null)
const showDetailDrawer = ref(false)
const autoRefresh = ref(true)

// Filters
const filterType = ref('')
const filterServer = ref('')
const filterStatus = ref('')
const filterStartDate = ref('')
const filterEndDate = ref('')

// Pagination
const currentPage = ref(1)
const pageSize = ref(25)

// Computed
const availableServers = computed(() => {
  const servers = new Set<string>()
  activities.value.forEach(a => {
    if (a.server_name) servers.add(a.server_name)
  })
  return Array.from(servers).sort()
})

const hasActiveFilters = computed(() => {
  return filterType.value || filterServer.value || filterStatus.value || filterStartDate.value || filterEndDate.value
})

const filteredActivities = computed(() => {
  let result = activities.value

  if (filterType.value) {
    result = result.filter(a => a.type === filterType.value)
  }
  if (filterServer.value) {
    result = result.filter(a => a.server_name === filterServer.value)
  }
  if (filterStatus.value) {
    result = result.filter(a => a.status === filterStatus.value)
  }
  if (filterStartDate.value) {
    const startTime = new Date(filterStartDate.value).getTime()
    result = result.filter(a => new Date(a.timestamp).getTime() >= startTime)
  }
  if (filterEndDate.value) {
    const endTime = new Date(filterEndDate.value).getTime()
    result = result.filter(a => new Date(a.timestamp).getTime() <= endTime)
  }

  return result
})

const totalPages = computed(() => Math.ceil(filteredActivities.value.length / pageSize.value))

const paginatedActivities = computed(() => {
  const start = (currentPage.value - 1) * pageSize.value
  return filteredActivities.value.slice(start, start + pageSize.value)
})

// Load activities
const loadActivities = async () => {
  loading.value = true
  error.value = null

  try {
    const [activitiesResponse, summaryResponse] = await Promise.all([
      api.getActivities({ limit: 200 }),
      api.getActivitySummary('24h')
    ])

    if (activitiesResponse.success && activitiesResponse.data) {
      activities.value = activitiesResponse.data.activities || []
    } else {
      error.value = activitiesResponse.error || 'Failed to load activities'
    }

    if (summaryResponse.success && summaryResponse.data) {
      summary.value = summaryResponse.data
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Unknown error'
  } finally {
    loading.value = false
  }
}

// Clear filters
const clearFilters = () => {
  filterType.value = ''
  filterServer.value = ''
  filterStatus.value = ''
  filterStartDate.value = ''
  filterEndDate.value = ''
  currentPage.value = 1
}

// Select activity for detail view
const selectActivity = (activity: ActivityRecord) => {
  selectedActivity.value = activity
  showDetailDrawer.value = true
}

const closeDetailDrawer = () => {
  showDetailDrawer.value = false
  selectedActivity.value = null
}

// Export activities
const exportActivities = (format: 'json' | 'csv') => {
  const url = api.getActivityExportUrl({
    format,
    type: filterType.value || undefined,
    server: filterServer.value || undefined,
    status: filterStatus.value || undefined,
  })
  window.open(url, '_blank')
}

// SSE event handlers - refresh from API when events arrive
// SSE payloads don't have 'id' field (generated by database), so we refresh from API
const handleActivityEvent = (event: CustomEvent) => {
  if (!autoRefresh.value) return

  const payload = event.detail
  // SSE events indicate new activity - refresh the list from API
  if (payload && (payload.server_name || payload.tool_name || payload.type)) {
    console.log('Activity event received, refreshing from API:', payload)
    loadActivities()
  }
}

const handleActivityCompleted = (event: CustomEvent) => {
  if (!autoRefresh.value) return

  const payload = event.detail
  // SSE completed events indicate activity finished - refresh from API
  if (payload && (payload.server_name || payload.tool_name || payload.status)) {
    console.log('Activity completed event received, refreshing from API:', payload)
    loadActivities()
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

const formatType = (type: string): string => {
  const typeLabels: Record<string, string> = {
    'tool_call': 'Tool Call',
    'policy_decision': 'Policy Decision',
    'quarantine_change': 'Quarantine Change',
    'server_change': 'Server Change'
  }
  return typeLabels[type] || type
}

const getTypeIcon = (type: string): string => {
  const typeIcons: Record<string, string> = {
    'tool_call': '🔧',
    'policy_decision': '🛡️',
    'quarantine_change': '⚠️',
    'server_change': '🔄'
  }
  return typeIcons[type] || '📋'
}

const formatStatus = (status: string): string => {
  const statusLabels: Record<string, string> = {
    'success': 'Success',
    'error': 'Error',
    'blocked': 'Blocked'
  }
  return statusLabels[status] || status
}

const getStatusBadgeClass = (status: string): string => {
  const statusClasses: Record<string, string> = {
    'success': 'badge-success',
    'error': 'badge-error',
    'blocked': 'badge-warning'
  }
  return statusClasses[status] || 'badge-ghost'
}

const formatDuration = (ms: number): string => {
  if (ms < 1000) return `${Math.round(ms)}ms`
  return `${(ms / 1000).toFixed(2)}s`
}

// Parse response data - try to parse as JSON, fallback to string
const parseResponseData = (response: string | object): unknown => {
  if (typeof response === 'object') return response
  try {
    return JSON.parse(response)
  } catch {
    return response
  }
}

const getIntentIcon = (operationType: string): string => {
  const icons: Record<string, string> = {
    'read': '📖',
    'write': '✏️',
    'destructive': '⚠️'
  }
  return icons[operationType] || '❓'
}

const getIntentBadgeClass = (operationType: string): string => {
  const classes: Record<string, string> = {
    'read': 'badge-info',
    'write': 'badge-warning',
    'destructive': 'badge-error'
  }
  return classes[operationType] || 'badge-ghost'
}

// Reset page when filters change
watch([filterType, filterServer, filterStatus, filterStartDate, filterEndDate], () => {
  currentPage.value = 1
})

// Keyboard handler for closing drawer
const handleKeydown = (event: KeyboardEvent) => {
  if (event.key === 'Escape' && showDetailDrawer.value) {
    closeDetailDrawer()
  }
}

// Lifecycle
onMounted(() => {
  loadActivities()

  // Listen for SSE activity events
  window.addEventListener('mcpproxy:activity', handleActivityEvent as EventListener)
  window.addEventListener('mcpproxy:activity-started', handleActivityEvent as EventListener)
  window.addEventListener('mcpproxy:activity-completed', handleActivityCompleted as EventListener)
  window.addEventListener('mcpproxy:activity-policy', handleActivityEvent as EventListener)
  window.addEventListener('keydown', handleKeydown)
})

onUnmounted(() => {
  window.removeEventListener('mcpproxy:activity', handleActivityEvent as EventListener)
  window.removeEventListener('mcpproxy:activity-started', handleActivityEvent as EventListener)
  window.removeEventListener('mcpproxy:activity-completed', handleActivityCompleted as EventListener)
  window.removeEventListener('mcpproxy:activity-policy', handleActivityEvent as EventListener)
  window.removeEventListener('keydown', handleKeydown)
})
</script>
