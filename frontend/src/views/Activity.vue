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
          <!-- Type Filter (Multi-select dropdown) -->
          <div class="form-control min-w-[180px]">
            <label class="label py-1">
              <span class="label-text text-xs">Type</span>
            </label>
            <div class="dropdown dropdown-bottom">
              <div
                tabindex="0"
                role="button"
                class="select select-bordered select-sm w-full text-left flex items-center justify-between"
              >
                <span v-if="selectedTypes.length === 0">All Types</span>
                <span v-else-if="selectedTypes.length === activityTypes.length">All Types</span>
                <span v-else class="truncate">{{ selectedTypes.length }} selected</span>
                <svg class="w-4 h-4 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
                </svg>
              </div>
              <ul tabindex="0" class="dropdown-content z-[10] menu p-2 shadow-lg bg-base-200 rounded-box w-56">
                <li class="menu-title flex flex-row justify-between items-center">
                  <span>Event Types</span>
                  <button
                    v-if="selectedTypes.length > 0"
                    @click.stop="clearTypeFilter"
                    class="btn btn-xs btn-ghost"
                  >
                    Clear
                  </button>
                </li>
                <li v-for="type in activityTypes" :key="type.value">
                  <label class="label cursor-pointer justify-start gap-2 py-1">
                    <input
                      type="checkbox"
                      :checked="selectedTypes.includes(type.value)"
                      @change="toggleTypeFilter(type.value)"
                      class="checkbox checkbox-sm"
                    />
                    <span class="text-lg">{{ type.icon }}</span>
                    <span>{{ type.label }}</span>
                  </label>
                </li>
              </ul>
            </div>
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

          <!-- Session Filter -->
          <div class="form-control min-w-[180px]">
            <label class="label py-1">
              <span class="label-text text-xs">Session</span>
            </label>
            <select v-model="filterSession" class="select select-bordered select-sm">
              <option value="">All Sessions</option>
              <option v-for="session in availableSessions" :key="session.id" :value="session.id">
                {{ session.label }}
              </option>
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
          <span
            v-for="type in selectedTypes"
            :key="type"
            class="badge badge-sm badge-outline gap-1 cursor-pointer hover:badge-error"
            @click="toggleTypeFilter(type)"
          >
            {{ getTypeIcon(type) }} {{ formatType(type) }}
            <svg class="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </span>
          <span v-if="filterServer" class="badge badge-sm badge-outline">Server: {{ filterServer }}</span>
          <span v-if="filterStatus" class="badge badge-sm badge-outline">Status: {{ filterStatus }}</span>
          <span v-if="filterSession" class="badge badge-sm badge-outline">Session: {{ getSessionLabel(filterSession) }}</span>
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
                <th class="cursor-pointer hover:bg-base-200" @click="sortBy('timestamp')">
                  Time {{ getSortIndicator('timestamp') }}
                </th>
                <th class="cursor-pointer hover:bg-base-200" @click="sortBy('type')">
                  Type {{ getSortIndicator('type') }}
                </th>
                <th class="cursor-pointer hover:bg-base-200" @click="sortBy('server_name')">
                  Server {{ getSortIndicator('server_name') }}
                </th>
                <th>Details</th>
                <th>Intent</th>
                <th class="cursor-pointer hover:bg-base-200" @click="sortBy('status')">
                  Status {{ getSortIndicator('status') }}
                </th>
                <th class="cursor-pointer hover:bg-base-200" @click="sortBy('duration_ms')">
                  Duration {{ getSortIndicator('duration_ms') }}
                </th>
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
                <!-- Intent column (Spec 024: US5) -->
                <td>
                  <div
                    v-if="activity.metadata?.intent?.operation_type"
                    class="tooltip tooltip-top"
                    :data-tip="activity.metadata?.intent?.reason || 'No reason provided'"
                  >
                    <span class="badge badge-sm gap-1" :class="getIntentBadgeClass(activity.metadata.intent.operation_type)">
                      {{ getIntentIcon(activity.metadata.intent.operation_type) }}
                      {{ activity.metadata.intent.operation_type }}
                    </span>
                  </div>
                  <span v-else class="text-base-content/40">-</span>
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
              Showing {{ (currentPage - 1) * pageSize + 1 }}-{{ Math.min(currentPage * pageSize, sortedActivities.length) }} of {{ sortedActivities.length }}
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

            <!-- Policy Decision Details (for blocked activities) -->
            <div v-if="selectedActivity.type === 'policy_decision' || selectedActivity.status === 'blocked'">
              <h4 class="font-semibold mb-2 text-warning flex items-center gap-2">
                <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
                </svg>
                Policy Decision
              </h4>
              <div class="alert alert-warning">
                <div class="flex flex-col gap-2 w-full">
                  <div class="flex items-center gap-2">
                    <span class="font-semibold">Decision:</span>
                    <span class="badge badge-warning">{{ selectedActivity.metadata?.decision || selectedActivity.status || 'Blocked' }}</span>
                  </div>
                  <div v-if="selectedActivity.metadata?.reason" class="flex flex-col gap-1">
                    <span class="font-semibold">Reason:</span>
                    <span class="text-sm">{{ selectedActivity.metadata.reason }}</span>
                  </div>
                  <div v-else-if="selectedActivity.metadata?.policy_rule" class="flex flex-col gap-1">
                    <span class="font-semibold">Policy Rule:</span>
                    <span class="text-sm">{{ selectedActivity.metadata.policy_rule }}</span>
                  </div>
                  <div v-else class="text-sm italic">
                    Tool call was blocked by security policy
                  </div>
                </div>
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

            <!-- Additional Metadata (for debugging/detailed view) -->
            <div v-if="hasAdditionalMetadata(selectedActivity)">
              <h4 class="font-semibold mb-2 flex items-center gap-2">
                Additional Details
                <span class="badge badge-sm badge-ghost">JSON</span>
              </h4>
              <JsonViewer :data="getAdditionalMetadata(selectedActivity)" max-height="12rem" />
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
const selectedTypes = ref<string[]>([])
const filterServer = ref('')
const filterSession = ref('')
const filterStatus = ref('')
const filterStartDate = ref('')
const filterEndDate = ref('')

// Activity types configuration (Spec 024: includes new types)
const activityTypes = [
  { value: 'tool_call', label: 'Tool Call', icon: '🔧' },
  { value: 'system_start', label: 'System Start', icon: '🚀' },
  { value: 'system_stop', label: 'System Stop', icon: '🛑' },
  { value: 'internal_tool_call', label: 'Internal Tool Call', icon: '⚙️' },
  { value: 'config_change', label: 'Config Change', icon: '⚡' },
  { value: 'policy_decision', label: 'Policy Decision', icon: '🛡️' },
  { value: 'quarantine_change', label: 'Quarantine Change', icon: '⚠️' },
  { value: 'server_change', label: 'Server Change', icon: '🔄' },
]

// Pagination
const currentPage = ref(1)
const pageSize = ref(25)

// Sorting (Spec 024: US6)
type SortColumn = 'timestamp' | 'type' | 'server_name' | 'status' | 'duration_ms'
type SortDirection = 'asc' | 'desc'
const sortColumn = ref<SortColumn>('timestamp')
const sortDirection = ref<SortDirection>('desc') // Default: newest first

// Computed
const availableServers = computed(() => {
  const servers = new Set<string>()
  activities.value.forEach(a => {
    if (a.server_name) servers.add(a.server_name)
  })
  return Array.from(servers).sort()
})

// Available sessions with client name and session_id suffix (Spec 024)
interface SessionOption {
  id: string
  label: string
  clientName?: string
}
const availableSessions = computed((): SessionOption[] => {
  const sessionsMap = new Map<string, { clientName?: string }>()
  activities.value.forEach(a => {
    if (a.session_id && !sessionsMap.has(a.session_id)) {
      // Try to get client name from metadata or any available source
      const clientName = a.metadata?.client_name as string | undefined
      sessionsMap.set(a.session_id, { clientName })
    }
  })

  return Array.from(sessionsMap.entries())
    .map(([sessionId, info]) => {
      // Format: "ClientName ...12345" or "...12345" if no client name
      const suffix = sessionId.slice(-5)
      const label = info.clientName
        ? `${info.clientName} ...${suffix}`
        : `...${suffix}`
      return { id: sessionId, label, clientName: info.clientName }
    })
    .sort((a, b) => a.label.localeCompare(b.label))
})

// Get session label by ID for display in Active Filters
const getSessionLabel = (sessionId: string): string => {
  const session = availableSessions.value.find(s => s.id === sessionId)
  return session?.label || `...${sessionId.slice(-5)}`
}

const hasActiveFilters = computed(() => {
  return selectedTypes.value.length > 0 || filterServer.value || filterSession.value || filterStatus.value || filterStartDate.value || filterEndDate.value
})

const filteredActivities = computed(() => {
  let result = activities.value

  // Multi-type filter (Spec 024): OR logic - show activities matching ANY selected type
  if (selectedTypes.value.length > 0) {
    result = result.filter(a => selectedTypes.value.includes(a.type))
  }
  if (filterServer.value) {
    result = result.filter(a => a.server_name === filterServer.value)
  }
  // Session filter (Spec 024)
  if (filterSession.value) {
    result = result.filter(a => a.session_id === filterSession.value)
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

// Sorted activities (Spec 024: US6)
const sortedActivities = computed(() => {
  const result = [...filteredActivities.value]
  const col = sortColumn.value
  const dir = sortDirection.value

  result.sort((a, b) => {
    let aVal: string | number | undefined
    let bVal: string | number | undefined

    if (col === 'timestamp') {
      aVal = new Date(a.timestamp).getTime()
      bVal = new Date(b.timestamp).getTime()
    } else if (col === 'duration_ms') {
      aVal = a.duration_ms ?? 0
      bVal = b.duration_ms ?? 0
    } else {
      aVal = a[col] ?? ''
      bVal = b[col] ?? ''
    }

    if (typeof aVal === 'string' && typeof bVal === 'string') {
      return dir === 'asc' ? aVal.localeCompare(bVal) : bVal.localeCompare(aVal)
    }
    return dir === 'asc' ? (aVal as number) - (bVal as number) : (bVal as number) - (aVal as number)
  })

  return result
})

const totalPages = computed(() => Math.ceil(sortedActivities.value.length / pageSize.value))

const paginatedActivities = computed(() => {
  const start = (currentPage.value - 1) * pageSize.value
  return sortedActivities.value.slice(start, start + pageSize.value)
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
  selectedTypes.value = []
  filterServer.value = ''
  filterSession.value = ''
  filterStatus.value = ''
  filterStartDate.value = ''
  filterEndDate.value = ''
  currentPage.value = 1
}

// Toggle type filter (Spec 024: multi-select support)
const toggleTypeFilter = (type: string) => {
  const index = selectedTypes.value.indexOf(type)
  if (index >= 0) {
    selectedTypes.value.splice(index, 1)
  } else {
    selectedTypes.value.push(type)
  }
}

// Clear type filter only
const clearTypeFilter = () => {
  selectedTypes.value = []
}

// Sort by column (Spec 024: US6)
const sortBy = (column: SortColumn) => {
  if (sortColumn.value === column) {
    // Toggle direction if same column
    sortDirection.value = sortDirection.value === 'asc' ? 'desc' : 'asc'
  } else {
    // New column - default to descending for timestamp/duration, ascending for others
    sortColumn.value = column
    sortDirection.value = column === 'timestamp' || column === 'duration_ms' ? 'desc' : 'asc'
  }
}

// Get sort indicator for column header
const getSortIndicator = (column: SortColumn): string => {
  if (sortColumn.value !== column) return ''
  return sortDirection.value === 'asc' ? '↑' : '↓'
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
    // Spec 024: Pass comma-separated types for multi-type filter
    type: selectedTypes.value.length > 0 ? selectedTypes.value.join(',') : undefined,
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
  // Spec 024: Include new activity types
  const typeLabels: Record<string, string> = {
    'tool_call': 'Tool Call',
    'system_start': 'System Start',
    'system_stop': 'System Stop',
    'internal_tool_call': 'Internal Tool Call',
    'config_change': 'Config Change',
    'policy_decision': 'Policy Decision',
    'quarantine_change': 'Quarantine Change',
    'server_change': 'Server Change'
  }
  return typeLabels[type] || type
}

const getTypeIcon = (type: string): string => {
  // Spec 024: Include new activity types
  const typeIcons: Record<string, string> = {
    'tool_call': '🔧',
    'system_start': '🚀',
    'system_stop': '🛑',
    'internal_tool_call': '⚙️',
    'config_change': '⚡',
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

// Check if there's additional metadata beyond what we show in dedicated sections
const hasAdditionalMetadata = (activity: ActivityRecord): boolean => {
  if (!activity.metadata) return false

  // Filter out fields we already show in dedicated sections
  const shownFields = ['intent', 'decision', 'reason', 'policy_rule']
  const additionalKeys = Object.keys(activity.metadata).filter(k => !shownFields.includes(k))

  return additionalKeys.length > 0
}

// Get metadata excluding fields already shown in dedicated sections
const getAdditionalMetadata = (activity: ActivityRecord): Record<string, unknown> => {
  if (!activity.metadata) return {}

  const shownFields = ['intent', 'decision', 'reason', 'policy_rule']
  const result: Record<string, unknown> = {}

  for (const [key, value] of Object.entries(activity.metadata)) {
    if (!shownFields.includes(key)) {
      result[key] = value
    }
  }

  return result
}

// Reset page when filters change
watch([selectedTypes, filterServer, filterStatus, filterStartDate, filterEndDate], () => {
  currentPage.value = 1
}, { deep: true })

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
