<template>
  <div class="space-y-6" data-test="tools-page">
    <!-- Page Header -->
    <div class="flex flex-wrap justify-between items-start gap-4">
      <div>
        <h1 class="text-3xl font-bold">Tools</h1>
        <p class="text-base-content/70 mt-1">Monitor and edit all individual tools</p>
      </div>
      <div class="flex items-center gap-3">
        <div v-if="stats" class="badge badge-outline badge-lg">
          {{ stats.total }} tools
        </div>
        <button @click="loadTools" class="btn btn-sm btn-ghost" :disabled="loading">
          <svg class="w-4 h-4" :class="{ 'animate-spin': loading }" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
          </svg>
        </button>
      </div>
    </div>

    <!-- Summary Stat Cards -->
    <div v-if="stats" class="stats shadow bg-base-100 w-full">
      <button
        type="button"
        :class="['stat text-left transition-colors cursor-pointer hover:bg-base-200/60', activeStatCard === 'total' ? 'bg-base-200 ring-2 ring-inset ring-primary/40' : '']"
        data-test="stat-total"
        @click="selectStatCard('total')"
      >
        <div class="stat-title">Total</div>
        <div class="stat-value text-2xl">{{ stats.total }}</div>
      </button>
      <button
        type="button"
        :class="['stat text-left transition-colors cursor-pointer hover:bg-base-200/60', activeStatCard === 'enabled' ? 'bg-base-200 ring-2 ring-inset ring-primary/40' : '']"
        data-test="stat-enabled"
        @click="selectStatCard('enabled')"
      >
        <div class="stat-title">Enabled</div>
        <div class="stat-value text-2xl text-success">{{ stats.enabled }}</div>
      </button>
      <button
        type="button"
        :class="['stat text-left transition-colors cursor-pointer hover:bg-base-200/60', activeStatCard === 'disabled' ? 'bg-base-200 ring-2 ring-inset ring-primary/40' : '']"
        data-test="stat-disabled"
        @click="selectStatCard('disabled')"
      >
        <div class="stat-title">Disabled</div>
        <div class="stat-value text-2xl text-warning">{{ stats.disabled }}</div>
      </button>
      <button
        type="button"
        :class="['stat text-left transition-colors cursor-pointer hover:bg-base-200/60', activeStatCard === 'pending' ? 'bg-base-200 ring-2 ring-inset ring-primary/40' : '']"
        data-test="stat-pending"
        @click="selectStatCard('pending')"
      >
        <div class="stat-title">Pending Approval</div>
        <div class="stat-value text-2xl" :class="stats.pending_approval > 0 ? 'text-error' : ''">
          {{ stats.pending_approval }}
        </div>
      </button>
    </div>

    <!-- Partial-error banner -->
    <div v-if="partial && failedServers.length > 0" class="alert alert-warning">
      <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <div>
        <div class="font-medium">Partial results — some servers could not be read</div>
        <div class="text-sm">Failed servers: {{ failedServers.join(', ') }}</div>
      </div>
    </div>

    <!-- Filters -->
    <div class="card bg-base-100 shadow-md">
      <div class="card-body py-4">
        <div class="flex flex-wrap gap-4 items-end">
          <!-- Search -->
          <div class="form-control flex-1 min-w-[200px]">
            <label class="label py-1">
              <span class="label-text text-xs">Search</span>
            </label>
            <div class="relative">
              <input
                v-model="searchQuery"
                type="text"
                placeholder="Search by name, description, or server..."
                class="input input-bordered input-sm w-full pl-8"
                data-test="tools-search"
              />
              <svg class="absolute left-2.5 top-2 w-4 h-4 text-base-content/40" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
              </svg>
            </div>
          </div>

          <!-- Server filter -->
          <div class="form-control min-w-[140px]">
            <label class="label py-1">
              <span class="label-text text-xs">Server</span>
            </label>
            <select v-model="filterServer" class="select select-bordered select-sm" data-test="filter-server">
              <option value="">All Servers</option>
              <option v-for="srv in availableServers" :key="srv" :value="srv">{{ srv }}</option>
            </select>
          </div>

          <!-- Status filter -->
          <div class="form-control min-w-[130px]">
            <label class="label py-1">
              <span class="label-text text-xs">Status</span>
            </label>
            <select v-model="filterStatus" class="select select-bordered select-sm" data-test="filter-status">
              <option value="">All</option>
              <option value="enabled">Enabled</option>
              <option value="disabled">Disabled</option>
              <option value="config_denied">Config Denied</option>
            </select>
          </div>

          <!-- Risk filter -->
          <div class="form-control min-w-[120px]">
            <label class="label py-1">
              <span class="label-text text-xs">Risk</span>
            </label>
            <select v-model="filterRisk" class="select select-bordered select-sm" data-test="filter-risk">
              <option value="">All</option>
              <option value="read">Read</option>
              <option value="write">Write</option>
              <option value="destructive">Destructive</option>
            </select>
          </div>

          <!-- Approval filter -->
          <div class="form-control min-w-[120px]">
            <label class="label py-1">
              <span class="label-text text-xs">Approval</span>
            </label>
            <select v-model="filterApproval" class="select select-bordered select-sm" data-test="filter-approval">
              <option value="">All</option>
              <option value="awaiting">Awaiting approval</option>
              <option value="approved">Approved</option>
              <option value="pending">Pending</option>
              <option value="changed">Changed</option>
            </select>
          </div>

          <!-- Clear Filters -->
          <button v-if="hasActiveFilters" @click="clearFilters" class="btn btn-sm btn-ghost">
            Clear Filters
          </button>
        </div>

        <!-- Active filter chips -->
        <div v-if="hasActiveFilters" class="flex flex-wrap gap-2 mt-2 pt-2 border-t border-base-300">
          <span class="text-xs text-base-content/60">Active filters:</span>
          <span v-if="searchQuery" class="badge badge-sm badge-outline">Search: {{ searchQuery }}</span>
          <span v-if="filterServer" class="badge badge-sm badge-outline">Server: {{ filterServer }}</span>
          <span v-if="filterStatus" class="badge badge-sm badge-outline">Status: {{ filterStatus }}</span>
          <span v-if="filterRisk" class="badge badge-sm badge-outline">Risk: {{ filterRisk }}</span>
          <span v-if="filterApproval" class="badge badge-sm badge-outline">Approval: {{ filterApproval }}</span>
        </div>
      </div>
    </div>

    <!-- Batch action bar -->
    <div v-if="selectedKeys.size > 0" class="alert shadow-md" data-test="tools-batch-bar">
      <div class="flex items-center gap-3 w-full flex-wrap">
        <span class="font-medium">{{ selectedKeys.size }} tool{{ selectedKeys.size === 1 ? '' : 's' }} selected</span>
        <button
          @click="batchEnable(true)"
          :disabled="batchLoading"
          class="btn btn-sm btn-success"
          data-test="batch-enable"
        >
          <span v-if="batchLoading" class="loading loading-spinner loading-xs"></span>
          Enable selected
        </button>
        <button
          @click="batchEnable(false)"
          :disabled="batchLoading"
          class="btn btn-sm btn-warning"
          data-test="batch-disable"
        >
          <span v-if="batchLoading" class="loading loading-spinner loading-xs"></span>
          Disable selected
        </button>
        <button @click="selectedKeys.clear()" class="btn btn-sm btn-ghost ml-auto">
          Clear selection
        </button>
      </div>
    </div>

    <!-- Batch result summary -->
    <div v-if="batchResult" class="alert" :class="batchResult.failed > 0 ? 'alert-warning' : 'alert-success'">
      <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <div class="flex-1">
        <div class="font-medium">Batch action complete: {{ batchResult.succeeded }} succeeded, {{ batchResult.failed }} failed</div>
        <div v-if="batchResult.failedTools.length > 0" class="text-sm mt-1">
          Failed: {{ batchResult.failedTools.join(', ') }}
        </div>
      </div>
      <button @click="batchResult = null" class="btn btn-sm btn-ghost">Dismiss</button>
    </div>

    <!-- Table card -->
    <div class="card bg-base-100 shadow-md">
      <div class="card-body p-0">
        <!-- Loading -->
        <div v-if="loading && allTools.length === 0" class="flex justify-center py-12">
          <span class="loading loading-spinner loading-lg"></span>
        </div>

        <!-- Error -->
        <div v-else-if="error" class="alert alert-error m-4">
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <span>{{ error }}</span>
          <button @click="loadTools" class="btn btn-sm btn-ghost">Retry</button>
        </div>

        <!-- Empty state -->
        <div v-else-if="filteredTools.length === 0 && !loading" class="text-center py-12 text-base-content/60">
          <svg class="w-16 h-16 mx-auto mb-4 opacity-30" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
          </svg>
          <p class="text-lg">
            {{ hasActiveFilters ? 'No matching tools' : 'No tools available' }}
          </p>
          <p class="text-sm mt-1">
            {{ hasActiveFilters ? 'Try adjusting your filters or search query' : 'Connect MCP servers to see their tools here.' }}
          </p>
          <div class="mt-4 space-x-2">
            <button v-if="hasActiveFilters" @click="clearFilters" class="btn btn-outline btn-sm">Clear Filters</button>
            <router-link v-else to="/servers" class="btn btn-primary btn-sm">Manage Servers</router-link>
          </div>
        </div>

        <!-- Table -->
        <div v-else class="overflow-x-auto">
          <table class="table table-sm" data-test="tools-table">
            <thead>
              <tr>
                <th class="w-10">
                  <input
                    type="checkbox"
                    class="checkbox checkbox-sm"
                    :checked="allPageSelected"
                    :indeterminate="somePageSelected && !allPageSelected"
                    @change="toggleSelectAll"
                    data-test="tools-select-all"
                  />
                </th>
                <th class="cursor-pointer hover:bg-base-200 select-none" @click="sortBy('name')">
                  Tool {{ getSortIndicator('name') }}
                </th>
                <th class="cursor-pointer hover:bg-base-200 select-none" @click="sortBy('server_name')">
                  Server {{ getSortIndicator('server_name') }}
                </th>
                <th>Description</th>
                <th class="cursor-pointer hover:bg-base-200 select-none" @click="sortBy('risk')">
                  Risk {{ getSortIndicator('risk') }}
                </th>
                <th class="cursor-pointer hover:bg-base-200 select-none" @click="sortBy('approval_status')">
                  Approval {{ getSortIndicator('approval_status') }}
                </th>
                <th class="cursor-pointer hover:bg-base-200 select-none" @click="sortBy('enabled')">
                  Enabled {{ getSortIndicator('enabled') }}
                </th>
                <th class="cursor-pointer hover:bg-base-200 select-none" @click="sortBy('usage')">
                  Usage {{ getSortIndicator('usage') }}
                </th>
                <th class="cursor-pointer hover:bg-base-200 select-none" @click="sortBy('last_used')">
                  Last Used {{ getSortIndicator('last_used') }}
                </th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="tool in paginatedTools"
                :key="toolKey(tool)"
                class="hover cursor-pointer"
                :class="{ 'bg-primary/5': selectedKeys.has(toolKey(tool)) }"
                @click="openDetail(tool)"
                data-test="tool-row"
              >
                <td @click.stop>
                  <input
                    type="checkbox"
                    class="checkbox checkbox-sm"
                    :checked="selectedKeys.has(toolKey(tool))"
                    @change="toggleSelect(tool)"
                  />
                </td>
                <td>
                  <code class="text-xs bg-base-200 px-1.5 py-0.5 rounded">{{ tool.name }}</code>
                </td>
                <td>
                  <router-link
                    :to="`/servers/${tool.server_name}`"
                    class="link link-primary text-sm font-medium"
                    @click.stop
                  >
                    {{ tool.server_name }}
                  </router-link>
                </td>
                <td>
                  <div class="max-w-xs truncate text-sm text-base-content/70">
                    {{ tool.description || '—' }}
                  </div>
                </td>
                <td>
                  <span class="badge badge-sm" :class="getRiskBadgeClass(tool)">
                    {{ getRiskLabel(tool) }}
                  </span>
                </td>
                <td>
                  <span v-if="tool.approval_status" class="badge badge-sm" :class="getApprovalBadgeClass(tool.approval_status)">
                    {{ tool.approval_status }}
                  </span>
                  <span v-else class="text-base-content/30 text-xs">—</span>
                </td>
                <td>
                  <span v-if="tool.config_denied" class="badge badge-sm badge-error">config-denied</span>
                  <span v-else-if="tool.disabled" class="badge badge-sm badge-warning">disabled</span>
                  <span v-else class="badge badge-sm badge-success">enabled</span>
                </td>
                <td class="text-sm text-right">
                  {{ tool.usage || 0 }}
                </td>
                <td class="text-sm text-base-content/60">
                  <span v-if="tool.last_used">{{ formatRelativeTime(tool.last_used) }}</span>
                  <span v-else class="text-base-content/30">never</span>
                </td>
              </tr>
            </tbody>
          </table>

          <!-- Pagination -->
          <div v-if="totalPages > 1" class="flex justify-between items-center px-4 py-3 border-t border-base-300">
            <div class="text-sm text-base-content/60">
              Showing {{ (currentPage - 1) * pageSize + 1 }}–{{ Math.min(currentPage * pageSize, sortedTools.length) }} of {{ sortedTools.length }}
            </div>
            <div class="join">
              <button @click="currentPage = 1" :disabled="currentPage === 1" class="join-item btn btn-sm">«</button>
              <button @click="currentPage = Math.max(1, currentPage - 1)" :disabled="currentPage === 1" class="join-item btn btn-sm">‹</button>
              <button class="join-item btn btn-sm">{{ currentPage }} / {{ totalPages }}</button>
              <button @click="currentPage = Math.min(totalPages, currentPage + 1)" :disabled="currentPage === totalPages" class="join-item btn btn-sm">›</button>
              <button @click="currentPage = totalPages" :disabled="currentPage === totalPages" class="join-item btn btn-sm">»</button>
            </div>
            <div class="form-control">
              <select v-model.number="pageSize" class="select select-bordered select-sm">
                <option :value="25">25 / page</option>
                <option :value="50">50 / page</option>
                <option :value="100">100 / page</option>
              </select>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Tool Detail Modal -->
    <div v-if="selectedTool" class="modal modal-open" @click.self="selectedTool = null">
      <div class="modal-box max-w-3xl">
        <div class="flex justify-between items-start mb-4">
          <div>
            <h3 class="font-bold text-lg">
              <code class="text-base bg-base-200 px-2 py-1 rounded">{{ selectedTool.name }}</code>
            </h3>
            <div class="flex items-center gap-2 mt-2">
              <router-link :to="`/servers/${selectedTool.server_name}`" class="link link-primary text-sm">
                {{ selectedTool.server_name }}
              </router-link>
              <span class="badge badge-sm" :class="getRiskBadgeClass(selectedTool)">{{ getRiskLabel(selectedTool) }}</span>
              <span v-if="selectedTool.config_denied" class="badge badge-sm badge-error">config-denied</span>
              <span v-else-if="selectedTool.disabled" class="badge badge-sm badge-warning">disabled</span>
              <span v-else class="badge badge-sm badge-success">enabled</span>
            </div>
          </div>
          <button class="btn btn-sm btn-circle btn-ghost" @click="selectedTool = null">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        <div class="space-y-4">
          <div v-if="selectedTool.description">
            <h4 class="text-sm font-semibold mb-1 text-base-content/70">Description</h4>
            <p class="text-sm">{{ selectedTool.description }}</p>
          </div>

          <div class="flex gap-6 text-sm">
            <div>
              <span class="text-base-content/60">Usage (30d):</span>
              <span class="ml-1 font-medium">{{ selectedTool.usage || 0 }}</span>
            </div>
            <div v-if="selectedTool.last_used">
              <span class="text-base-content/60">Last used:</span>
              <span class="ml-1">{{ formatRelativeTime(selectedTool.last_used) }}</span>
            </div>
            <div v-if="selectedTool.approval_status">
              <span class="text-base-content/60">Approval:</span>
              <span class="badge badge-sm ml-1" :class="getApprovalBadgeClass(selectedTool.approval_status)">
                {{ selectedTool.approval_status }}
              </span>
            </div>
          </div>

          <div v-if="selectedTool.annotations && Object.keys(selectedTool.annotations).length > 0">
            <h4 class="text-sm font-semibold mb-1 text-base-content/70">Annotations</h4>
            <div class="flex flex-wrap gap-2">
              <span v-if="selectedTool.annotations.readOnlyHint" class="badge badge-sm badge-info">readOnly</span>
              <span v-if="selectedTool.annotations.destructiveHint" class="badge badge-sm badge-error">destructive</span>
              <span v-if="selectedTool.annotations.idempotentHint" class="badge badge-sm badge-ghost">idempotent</span>
              <span v-if="selectedTool.annotations.openWorldHint" class="badge badge-sm badge-ghost">openWorld</span>
            </div>
          </div>

          <div v-if="selectedToolSchema">
            <h4 class="text-sm font-semibold mb-1 flex items-center gap-2 text-base-content/70">
              Input Schema
              <span class="badge badge-sm badge-ghost">JSON</span>
            </h4>
            <div class="mockup-code max-h-64 overflow-y-auto">
              <pre class="text-xs"><code>{{ JSON.stringify(selectedToolSchema, null, 2) }}</code></pre>
            </div>
          </div>
        </div>

        <div class="modal-action">
          <button class="btn btn-sm" @click="selectedTool = null">Close</button>
        </div>
      </div>
    </div>

    <!-- Hints Panel -->
    <CollapsibleHintsPanel :hints="toolsHints" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import CollapsibleHintsPanel from '@/components/CollapsibleHintsPanel.vue'
import type { Hint } from '@/components/CollapsibleHintsPanel.vue'
import type { GlobalTool, GlobalToolsStats } from '@/types/api'
import api from '@/services/api'

// ---- State ----
const allTools = ref<GlobalTool[]>([])
const stats = ref<GlobalToolsStats | null>(null)
const partial = ref(false)
const failedServers = ref<string[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const selectedTool = ref<GlobalTool | null>(null)

// ---- Filters ----
const searchQuery = ref('')
const filterServer = ref('')
const filterStatus = ref('')
const filterRisk = ref('')
const filterApproval = ref('')

// Debounce search
let searchTimer: ReturnType<typeof setTimeout> | null = null
watch(searchQuery, () => {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => { currentPage.value = 1 }, 300)
})

// ---- Sort ----
type SortCol = 'name' | 'server_name' | 'risk' | 'approval_status' | 'enabled' | 'usage' | 'last_used'
const sortColumn = ref<SortCol>('name')
const sortDirection = ref<'asc' | 'desc'>('asc')

function sortBy(col: SortCol) {
  if (sortColumn.value === col) {
    sortDirection.value = sortDirection.value === 'asc' ? 'desc' : 'asc'
  } else {
    sortColumn.value = col
    sortDirection.value = col === 'usage' || col === 'last_used' ? 'desc' : 'asc'
  }
}

function getSortIndicator(col: SortCol): string {
  if (sortColumn.value !== col) return ''
  return sortDirection.value === 'asc' ? '↑' : '↓'
}

// ---- Pagination ----
const currentPage = ref(1)
const pageSize = ref(25)

// ---- Selection ----
const selectedKeys = ref(new Set<string>())

function toolKey(tool: GlobalTool): string {
  return `${tool.server_name}\x00${tool.name}`
}

const allPageSelected = computed(() =>
  paginatedTools.value.length > 0 && paginatedTools.value.every(t => selectedKeys.value.has(toolKey(t)))
)

const somePageSelected = computed(() =>
  paginatedTools.value.some(t => selectedKeys.value.has(toolKey(t)))
)

function toggleSelectAll() {
  if (allPageSelected.value) {
    paginatedTools.value.forEach(t => selectedKeys.value.delete(toolKey(t)))
  } else {
    paginatedTools.value.forEach(t => selectedKeys.value.add(toolKey(t)))
  }
}

function toggleSelect(tool: GlobalTool) {
  const k = toolKey(tool)
  if (selectedKeys.value.has(k)) {
    selectedKeys.value.delete(k)
  } else {
    selectedKeys.value.add(k)
  }
}

// ---- Batch actions ----
const batchLoading = ref(false)
const batchResult = ref<{ succeeded: number; failed: number; failedTools: string[] } | null>(null)

async function batchEnable(enabled: boolean) {
  if (batchLoading.value || selectedKeys.value.size === 0) return
  batchLoading.value = true
  batchResult.value = null

  const targets = allTools.value.filter(t => selectedKeys.value.has(toolKey(t)))
  let succeeded = 0
  let failed = 0
  const failedTools: string[] = []

  for (const tool of targets) {
    try {
      const resp = await api.setToolEnabled(tool.server_name, tool.name, enabled)
      if (resp.success) {
        succeeded++
        // Update local state immediately so row reflects change
        tool.disabled = !enabled
      } else {
        failed++
        failedTools.push(`${tool.server_name}:${tool.name} (${resp.error || 'failed'})`)
      }
    } catch (err) {
      failed++
      failedTools.push(`${tool.server_name}:${tool.name}`)
    }
  }

  batchResult.value = { succeeded, failed, failedTools }
  selectedKeys.value.clear()
  batchLoading.value = false

  // Refresh to get authoritative server state
  await loadTools()
}

// ---- Computed: available filter options ----
const availableServers = computed(() => {
  const s = new Set<string>()
  allTools.value.forEach(t => s.add(t.server_name))
  return Array.from(s).sort()
})

const hasActiveFilters = computed(() =>
  !!searchQuery.value || !!filterServer.value || !!filterStatus.value || !!filterRisk.value || !!filterApproval.value
)

// Clickable stat cards (parity with Servers page): each card drives the
// status/approval filter and toggles off when its active card is clicked again.
type StatCard = 'total' | 'enabled' | 'disabled' | 'pending'

const activeStatCard = computed<StatCard>(() => {
  if (filterApproval.value === 'awaiting' || filterApproval.value === 'pending') return 'pending'
  if (filterStatus.value === 'enabled') return 'enabled'
  if (filterStatus.value === 'disabled') return 'disabled'
  if (!filterStatus.value && !filterApproval.value) return 'total'
  return 'total'
})

function selectStatCard(card: StatCard) {
  // Toggle: re-clicking the active card resets to the unfiltered "total" view.
  if (card === 'total' || activeStatCard.value === card) {
    filterStatus.value = ''
    filterApproval.value = ''
    return
  }
  if (card === 'pending') {
    filterStatus.value = ''
    filterApproval.value = 'awaiting'
  } else {
    filterApproval.value = ''
    filterStatus.value = card // 'enabled' | 'disabled'
  }
}

// ---- Computed: risk derivation ----
function getRisk(tool: GlobalTool): 'read' | 'write' | 'destructive' {
  if (tool.annotations?.destructiveHint) return 'destructive'
  if (tool.annotations?.readOnlyHint) return 'read'
  return 'write'
}

function getRiskLabel(tool: GlobalTool): string {
  return getRisk(tool)
}

function getRiskBadgeClass(tool: GlobalTool): string {
  const r = getRisk(tool)
  if (r === 'destructive') return 'badge-error'
  if (r === 'read') return 'badge-success'
  return 'badge-warning'
}

function getApprovalBadgeClass(status: string): string {
  if (status === 'approved') return 'badge-success'
  if (status === 'pending') return 'badge-warning'
  if (status === 'changed') return 'badge-error'
  return 'badge-ghost'
}

// ---- Computed: filtering ----
const filteredTools = computed(() => {
  let tools = allTools.value

  if (searchQuery.value) {
    const q = searchQuery.value.toLowerCase()
    tools = tools.filter(t =>
      t.name.toLowerCase().includes(q) ||
      (t.description || '').toLowerCase().includes(q) ||
      t.server_name.toLowerCase().includes(q)
    )
  }

  if (filterServer.value) {
    tools = tools.filter(t => t.server_name === filterServer.value)
  }

  if (filterStatus.value === 'enabled') {
    tools = tools.filter(t => !t.disabled && !t.config_denied)
  } else if (filterStatus.value === 'disabled') {
    tools = tools.filter(t => t.disabled && !t.config_denied)
  } else if (filterStatus.value === 'config_denied') {
    tools = tools.filter(t => t.config_denied)
  }

  if (filterRisk.value) {
    tools = tools.filter(t => getRisk(t) === filterRisk.value)
  }

  if (filterApproval.value === 'awaiting') {
    // "Awaiting approval" mirrors the Pending Approval stat, which counts both
    // brand-new (pending) and rug-pull (changed) tools.
    tools = tools.filter(t => t.approval_status === 'pending' || t.approval_status === 'changed')
  } else if (filterApproval.value) {
    tools = tools.filter(t => t.approval_status === filterApproval.value)
  }

  return tools
})

// ---- Computed: sorting ----
const sortedTools = computed(() => {
  const list = [...filteredTools.value]
  const col = sortColumn.value
  const dir = sortDirection.value

  list.sort((a, b) => {
    let av: string | number
    let bv: string | number

    switch (col) {
      case 'name':
        av = a.name; bv = b.name; break
      case 'server_name':
        av = a.server_name; bv = b.server_name; break
      case 'risk':
        av = getRisk(a); bv = getRisk(b); break
      case 'approval_status':
        av = a.approval_status || ''; bv = b.approval_status || ''; break
      case 'enabled': {
        // sort: enabled first in asc
        const ae = (!a.disabled && !a.config_denied) ? 1 : 0
        const be = (!b.disabled && !b.config_denied) ? 1 : 0
        av = ae; bv = be; break
      }
      case 'usage':
        av = a.usage || 0; bv = b.usage || 0; break
      case 'last_used':
        av = a.last_used ? new Date(a.last_used).getTime() : 0
        bv = b.last_used ? new Date(b.last_used).getTime() : 0
        break
      default:
        av = ''; bv = ''
    }

    if (typeof av === 'number' && typeof bv === 'number') {
      return dir === 'asc' ? av - bv : bv - av
    }
    const as = String(av); const bs = String(bv)
    return dir === 'asc' ? as.localeCompare(bs) : bs.localeCompare(as)
  })

  return list
})

// ---- Computed: pagination ----
const totalPages = computed(() => Math.ceil(sortedTools.value.length / pageSize.value))

const paginatedTools = computed(() => {
  const start = (currentPage.value - 1) * pageSize.value
  return sortedTools.value.slice(start, start + pageSize.value)
})

// ---- Computed: modal schema ----
const selectedToolSchema = computed(() => {
  if (!selectedTool.value) return null
  const t = selectedTool.value as any
  return t.input_schema || t.schema || null
})

// ---- Methods ----
async function loadTools() {
  loading.value = true
  error.value = null

  try {
    const resp = await api.getGlobalTools()
    if (resp.success && resp.data) {
      allTools.value = resp.data.tools || []
      stats.value = resp.data.stats
      partial.value = resp.data.partial || false
      failedServers.value = resp.data.failed_servers || []
    } else {
      error.value = resp.error || 'Failed to load tools'
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Unknown error'
  } finally {
    loading.value = false
  }
}

function openDetail(tool: GlobalTool) {
  selectedTool.value = tool
}

function clearFilters() {
  searchQuery.value = ''
  filterServer.value = ''
  filterStatus.value = ''
  filterRisk.value = ''
  filterApproval.value = ''
  currentPage.value = 1
}

function formatRelativeTime(ts: string): string {
  const diff = Date.now() - new Date(ts).getTime()
  if (diff < 60_000) return 'just now'
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`
  if (diff < 30 * 86_400_000) return `${Math.floor(diff / 86_400_000)}d ago`
  return new Date(ts).toLocaleDateString()
}

// Reset page when filters/sort change
watch([filterServer, filterStatus, filterRisk, filterApproval, sortColumn, sortDirection], () => {
  currentPage.value = 1
})

// Also reset when pageSize changes
watch(pageSize, () => { currentPage.value = 1 })

// ---- Hints ----
const toolsHints = computed<Hint[]>(() => [
  {
    icon: '🔍',
    title: 'Global Tools Overview',
    description: 'See every tool across all configured MCP servers in one place',
    sections: [
      {
        title: 'Audit and cleanup',
        list: [
          'Search by tool name, description, or server',
          'Filter by status, risk level, or approval state',
          'Sort any column to find stale or unused tools',
          'Select multiple tools for batch enable/disable',
        ],
      },
      {
        title: 'List all tools via CLI',
        codeBlock: {
          language: 'bash',
          code: '# List all tools across all servers\nmcpproxy tools list\n\n# Filter by status\nmcpproxy tools list --status disabled\n\n# Disable tools in bulk\nmcpproxy tools disable server1:tool_a server2:tool_b',
        },
      },
    ],
  },
])

// ---- Lifecycle ----
onMounted(() => {
  loadTools()
})
</script>
