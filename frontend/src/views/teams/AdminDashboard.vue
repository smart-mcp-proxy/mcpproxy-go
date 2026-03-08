<template>
  <div class="space-y-6 max-w-6xl mx-auto">
    <!-- Page Header -->
    <div class="flex justify-between items-center">
      <div>
        <h1 class="text-2xl font-bold">Admin Dashboard</h1>
        <p class="text-base-content/70 mt-1">Team overview and system health</p>
      </div>
      <button @click="loadAll" class="btn btn-sm btn-ghost" :disabled="loading">
        <svg class="w-4 h-4" :class="{ 'animate-spin': loading }" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
        </svg>
        Refresh
      </button>
    </div>

    <!-- Stats Cards -->
    <div class="stats shadow bg-base-100 w-full">
      <div class="stat">
        <div class="stat-figure text-primary">
          <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 20h5v-2a3 3 0 00-5.356-1.857M17 20H7m10 0v-2c0-.656-.126-1.283-.356-1.857M7 20H2v-2a3 3 0 015.356-1.857M7 20v-2c0-.656.126-1.283.356-1.857m0 0a5.002 5.002 0 019.288 0M15 7a3 3 0 11-6 0 3 3 0 016 0z" />
          </svg>
        </div>
        <div class="stat-title">Total Users</div>
        <div class="stat-value text-primary">{{ stats.totalUsers }}</div>
        <div class="stat-desc">{{ stats.activeUsers }} active</div>
      </div>

      <div class="stat">
        <div class="stat-figure text-secondary">
          <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 10V3L4 14h7v7l9-11h-7z" />
          </svg>
        </div>
        <div class="stat-title">Active Sessions</div>
        <div class="stat-value text-secondary">{{ stats.activeSessions }}</div>
        <div class="stat-desc">Current connections</div>
      </div>

      <div class="stat">
        <div class="stat-figure text-accent">
          <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2" />
          </svg>
        </div>
        <div class="stat-title">Total Servers</div>
        <div class="stat-value text-accent">{{ stats.totalServers }}</div>
        <div class="stat-desc">{{ stats.healthyServers }} healthy</div>
      </div>

      <div class="stat">
        <div class="stat-figure text-info">
          <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2" />
          </svg>
        </div>
        <div class="stat-title">Tool Calls (24h)</div>
        <div class="stat-value text-info">{{ stats.toolCalls24h }}</div>
        <div class="stat-desc">{{ stats.errorRate24h }}% error rate</div>
      </div>
    </div>

    <!-- Loading -->
    <div v-if="loading && !hasData" class="flex justify-center py-12">
      <span class="loading loading-spinner loading-lg"></span>
    </div>

    <!-- Error -->
    <div v-if="error" class="alert alert-error">
      <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <span>{{ error }}</span>
    </div>

    <div v-if="hasData" class="grid grid-cols-1 lg:grid-cols-2 gap-6">
      <!-- Recent Users -->
      <div class="card bg-base-100 shadow-sm">
        <div class="card-body">
          <div class="flex items-center justify-between mb-3">
            <h2 class="card-title text-lg">Recent Users</h2>
            <router-link to="/admin/users" class="btn btn-xs btn-ghost">View All</router-link>
          </div>
          <div v-if="recentUsers.length === 0" class="text-center py-4 text-base-content/60 text-sm">
            No users yet
          </div>
          <div v-else class="space-y-2">
            <div v-for="user in recentUsers" :key="user.id" class="flex items-center justify-between py-2 border-b border-base-200 last:border-0">
              <div>
                <div class="font-medium text-sm">{{ user.display_name || user.email }}</div>
                <div class="text-xs text-base-content/50">{{ user.email }}</div>
              </div>
              <div class="flex items-center gap-2">
                <span class="badge badge-xs" :class="user.role === 'admin' ? 'badge-primary' : 'badge-ghost'">
                  {{ user.role }}
                </span>
                <span class="text-xs text-base-content/50">
                  {{ user.last_login_at ? formatRelativeTime(user.last_login_at) : 'Never' }}
                </span>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Recent Activity -->
      <div class="card bg-base-100 shadow-sm">
        <div class="card-body">
          <div class="flex items-center justify-between mb-3">
            <h2 class="card-title text-lg">Recent Activity</h2>
            <router-link to="/activity" class="btn btn-xs btn-ghost">View All</router-link>
          </div>
          <div v-if="recentActivity.length === 0" class="text-center py-4 text-base-content/60 text-sm">
            No recent activity
          </div>
          <div v-else class="space-y-2">
            <div v-for="activity in recentActivity" :key="activity.id" class="flex items-center justify-between py-2 border-b border-base-200 last:border-0">
              <div>
                <div class="text-sm">
                  <code class="text-xs">{{ activity.tool_name || activity.type }}</code>
                  <span v-if="activity.server_name" class="text-base-content/50 ml-1">on {{ activity.server_name }}</span>
                </div>
                <div class="text-xs text-base-content/50">{{ activity.user_email || 'system' }}</div>
              </div>
              <div class="flex items-center gap-2">
                <span class="badge badge-xs" :class="activity.status === 'success' ? 'badge-success' : activity.status === 'error' ? 'badge-error' : 'badge-ghost'">
                  {{ activity.status }}
                </span>
                <span class="text-xs text-base-content/50">{{ formatRelativeTime(activity.timestamp) }}</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'

interface DashboardUser {
  id: string
  email: string
  display_name: string
  role: string
  last_login_at: string
}

interface DashboardActivity {
  id: string
  type: string
  tool_name?: string
  server_name?: string
  status: string
  timestamp: string
  user_email?: string
}

const loading = ref(false)
const error = ref('')
const recentUsers = ref<DashboardUser[]>([])
const recentActivity = ref<DashboardActivity[]>([])
let refreshInterval: ReturnType<typeof setInterval> | null = null

const stats = reactive({
  totalUsers: 0,
  activeUsers: 0,
  activeSessions: 0,
  totalServers: 0,
  healthyServers: 0,
  toolCalls24h: 0,
  errorRate24h: 0,
})

const hasData = computed(() => stats.totalUsers > 0 || recentUsers.value.length > 0 || recentActivity.value.length > 0)

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

async function loadAll() {
  loading.value = true
  error.value = ''
  try {
    const res = await fetch('/api/v1/admin/dashboard', { credentials: 'include' })
    if (!res.ok) throw new Error(`HTTP ${res.status}: ${res.statusText}`)
    const data = await res.json()

    stats.totalUsers = data.total_users || 0
    stats.activeUsers = data.active_users || 0
    stats.activeSessions = data.active_sessions || 0
    stats.totalServers = data.total_servers || 0
    stats.healthyServers = data.healthy_servers || 0
    stats.toolCalls24h = data.tool_calls_24h || 0
    stats.errorRate24h = data.error_rate_24h || 0
    recentUsers.value = data.recent_users || []
    recentActivity.value = data.recent_activity || []
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to load dashboard data'
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  loadAll()
  refreshInterval = setInterval(loadAll, 30000)
})

onUnmounted(() => {
  if (refreshInterval) {
    clearInterval(refreshInterval)
    refreshInterval = null
  }
})
</script>
