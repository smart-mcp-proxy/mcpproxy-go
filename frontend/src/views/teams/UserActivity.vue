<template>
  <div class="space-y-6 max-w-6xl mx-auto">
    <!-- Page Header -->
    <div class="flex flex-wrap justify-between items-start gap-4">
      <div>
        <h1 class="text-2xl font-bold">My Activity</h1>
        <p class="text-base-content/70 mt-1">Tool calls and activity for your sessions</p>
      </div>
      <div class="flex items-center gap-2">
        <button @click="loadActivities" class="btn btn-sm btn-ghost" :disabled="loading">
          <svg class="w-4 h-4" :class="{ 'animate-spin': loading }" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
          </svg>
          Refresh
        </button>
      </div>
    </div>

    <!-- Filters -->
    <div class="flex flex-wrap gap-3 items-center">
      <div class="form-control">
        <select v-model="filters.server" class="select select-bordered select-sm" @change="resetAndLoad">
          <option value="">All Servers</option>
          <option v-for="s in serverNames" :key="s" :value="s">{{ s }}</option>
        </select>
      </div>
      <div class="form-control">
        <select v-model="filters.status" class="select select-bordered select-sm" @change="resetAndLoad">
          <option value="">All Statuses</option>
          <option value="success">Success</option>
          <option value="error">Error</option>
        </select>
      </div>
      <div class="form-control">
        <select v-model="filters.type" class="select select-bordered select-sm" @change="resetAndLoad">
          <option value="">All Types</option>
          <option value="tool_call">Tool Calls</option>
          <option value="connection">Connections</option>
          <option value="auth">Authentication</option>
        </select>
      </div>
      <div v-if="hasActiveFilters" class="ml-2">
        <button class="btn btn-ghost btn-xs" @click="clearFilters">Clear Filters</button>
      </div>
    </div>

    <!-- Loading -->
    <div v-if="loading && activities.length === 0" class="flex justify-center py-12">
      <span class="loading loading-spinner loading-lg"></span>
    </div>

    <!-- Error -->
    <div v-else-if="error" class="alert alert-error">
      <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <span>{{ error }}</span>
      <button class="btn btn-sm" @click="loadActivities">Try Again</button>
    </div>

    <!-- Empty State -->
    <div v-else-if="activities.length === 0" class="text-center py-12 text-base-content/60">
      <svg class="w-16 h-16 mx-auto mb-4 opacity-30" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2" />
      </svg>
      <p class="text-lg font-medium">No activity yet</p>
      <p class="text-sm mt-1">Activity will appear here once you start using tools</p>
    </div>

    <!-- Activity Table -->
    <div v-else class="card bg-base-100 shadow-sm">
      <div class="overflow-x-auto">
        <table class="table table-sm">
          <thead>
            <tr>
              <th>Time</th>
              <th>Tool</th>
              <th>Server</th>
              <th>Status</th>
              <th class="text-right">Duration</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="activity in activities" :key="activity.id" class="hover cursor-pointer" @click="showDetail(activity)">
              <td>
                <span class="text-xs" :title="activity.timestamp">
                  {{ formatRelativeTime(activity.timestamp) }}
                </span>
              </td>
              <td>
                <code class="text-xs">{{ activity.tool_name || activity.type }}</code>
              </td>
              <td>
                <span class="text-sm">{{ activity.server_name || '-' }}</span>
              </td>
              <td>
                <span class="badge badge-sm" :class="activity.status === 'success' ? 'badge-success' : activity.status === 'error' ? 'badge-error' : 'badge-ghost'">
                  {{ activity.status }}
                </span>
              </td>
              <td class="text-right">
                <span class="text-xs text-base-content/70">
                  {{ activity.duration_ms ? `${activity.duration_ms}ms` : '-' }}
                </span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- Pagination -->
      <div class="flex justify-between items-center p-4 border-t border-base-300">
        <div class="text-sm text-base-content/60">
          Showing {{ activities.length }} of {{ totalCount }} activities
        </div>
        <div class="join">
          <button class="join-item btn btn-sm" :disabled="currentPage <= 1" @click="goToPage(currentPage - 1)">
            Previous
          </button>
          <button class="join-item btn btn-sm btn-active">{{ currentPage }}</button>
          <button class="join-item btn btn-sm" :disabled="!hasMore" @click="goToPage(currentPage + 1)">
            Next
          </button>
        </div>
      </div>
    </div>

    <!-- Activity Detail Modal -->
    <dialog class="modal" :class="{ 'modal-open': !!selectedActivity }">
      <div class="modal-box max-w-2xl">
        <h3 class="font-bold text-lg mb-4">Activity Details</h3>
        <div v-if="selectedActivity" class="space-y-3">
          <div class="grid grid-cols-2 gap-3 text-sm">
            <div>
              <span class="text-base-content/50">Type</span>
              <p class="font-medium">{{ selectedActivity.type }}</p>
            </div>
            <div>
              <span class="text-base-content/50">Status</span>
              <p>
                <span class="badge badge-sm" :class="selectedActivity.status === 'success' ? 'badge-success' : 'badge-error'">
                  {{ selectedActivity.status }}
                </span>
              </p>
            </div>
            <div>
              <span class="text-base-content/50">Server</span>
              <p class="font-medium">{{ selectedActivity.server_name || '-' }}</p>
            </div>
            <div>
              <span class="text-base-content/50">Tool</span>
              <p class="font-medium">{{ selectedActivity.tool_name || '-' }}</p>
            </div>
            <div>
              <span class="text-base-content/50">Time</span>
              <p>{{ new Date(selectedActivity.timestamp).toLocaleString() }}</p>
            </div>
            <div>
              <span class="text-base-content/50">Duration</span>
              <p>{{ selectedActivity.duration_ms ? `${selectedActivity.duration_ms}ms` : '-' }}</p>
            </div>
          </div>
          <div v-if="selectedActivity.error" class="mt-4">
            <span class="text-base-content/50 text-sm">Error</span>
            <pre class="bg-base-200 p-3 rounded-lg text-xs mt-1 overflow-x-auto">{{ selectedActivity.error }}</pre>
          </div>
        </div>
        <div class="modal-action">
          <button class="btn" @click="selectedActivity = null">Close</button>
        </div>
      </div>
      <form method="dialog" class="modal-backdrop" @click="selectedActivity = null"></form>
    </dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'

interface Activity {
  id: string
  type: string
  tool_name?: string
  server_name?: string
  status: string
  timestamp: string
  duration_ms?: number
  error?: string
}

const loading = ref(false)
const error = ref('')
const activities = ref<Activity[]>([])
const totalCount = ref(0)
const currentPage = ref(1)
const pageSize = 25
const selectedActivity = ref<Activity | null>(null)
const serverNames = ref<string[]>([])

const filters = reactive({
  server: '',
  status: '',
  type: '',
})

const hasActiveFilters = computed(() => !!(filters.server || filters.status || filters.type))
const hasMore = computed(() => activities.value.length < totalCount.value)

function formatRelativeTime(timestamp: string): string {
  const now = Date.now()
  const time = new Date(timestamp).getTime()
  const diff = now - time
  if (diff < 1000) return 'Just now'
  if (diff < 60000) return `${Math.floor(diff / 1000)}s ago`
  if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`
  if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`
  return `${Math.floor(diff / 86400000)}d ago`
}

async function loadActivities() {
  loading.value = true
  error.value = ''
  try {
    const params = new URLSearchParams()
    params.set('limit', pageSize.toString())
    params.set('offset', ((currentPage.value - 1) * pageSize).toString())
    if (filters.server) params.set('server', filters.server)
    if (filters.status) params.set('status', filters.status)
    if (filters.type) params.set('type', filters.type)

    const res = await fetch(`/api/v1/user/activity?${params}`, { credentials: 'include' })
    if (!res.ok) throw new Error(`HTTP ${res.status}: ${res.statusText}`)
    const data = await res.json()
    activities.value = data.items || []
    totalCount.value = data.total || activities.value.length
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to load activity'
  } finally {
    loading.value = false
  }
}

async function loadServerNames() {
  try {
    const res = await fetch('/api/v1/user/servers', { credentials: 'include' })
    if (res.ok) {
      const data = await res.json()
      const personal = (data.personal || []).map((s: { name: string }) => s.name)
      const shared = (data.shared || []).map((s: { name: string }) => s.name)
      serverNames.value = [...personal, ...shared]
    }
  } catch {
    // Non-critical, ignore
  }
}

function resetAndLoad() {
  currentPage.value = 1
  loadActivities()
}

function clearFilters() {
  filters.server = ''
  filters.status = ''
  filters.type = ''
  resetAndLoad()
}

function goToPage(page: number) {
  currentPage.value = page
  loadActivities()
}

function showDetail(activity: Activity) {
  selectedActivity.value = activity
}

onMounted(() => {
  loadActivities()
  loadServerNames()
})
</script>
