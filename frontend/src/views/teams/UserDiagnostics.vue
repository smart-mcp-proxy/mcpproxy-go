<template>
  <div class="space-y-6 max-w-6xl mx-auto">
    <!-- Page Header -->
    <div class="flex justify-between items-center">
      <div>
        <h1 class="text-2xl font-bold">Diagnostics</h1>
        <p class="text-base-content/70 mt-1">Server health for your accessible MCP servers</p>
      </div>
      <button @click="loadDiagnostics" class="btn btn-sm btn-ghost" :disabled="loading">
        <svg class="w-4 h-4" :class="{ 'animate-spin': loading }" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
        </svg>
        Refresh
      </button>
    </div>

    <!-- Summary Stats -->
    <div class="stats shadow bg-base-100 w-full">
      <div class="stat">
        <div class="stat-title">Total Servers</div>
        <div class="stat-value">{{ servers.length }}</div>
      </div>
      <div class="stat">
        <div class="stat-title">Healthy</div>
        <div class="stat-value text-success">{{ healthyCounts.healthy }}</div>
      </div>
      <div class="stat">
        <div class="stat-title">Degraded</div>
        <div class="stat-value text-warning">{{ healthyCounts.degraded }}</div>
      </div>
      <div class="stat">
        <div class="stat-title">Unhealthy</div>
        <div class="stat-value text-error">{{ healthyCounts.unhealthy }}</div>
      </div>
    </div>

    <!-- Loading -->
    <div v-if="loading && servers.length === 0" class="flex justify-center py-12">
      <span class="loading loading-spinner loading-lg"></span>
    </div>

    <!-- Error -->
    <div v-else-if="error" class="alert alert-error">
      <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <span>{{ error }}</span>
      <button class="btn btn-sm" @click="loadDiagnostics">Try Again</button>
    </div>

    <!-- Empty -->
    <div v-else-if="servers.length === 0" class="text-center py-12 text-base-content/60">
      <p class="text-lg font-medium">No servers found</p>
      <p class="text-sm mt-1">You don't have any accessible servers yet</p>
    </div>

    <!-- Server Health Cards -->
    <div v-else class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
      <div v-for="server in servers" :key="server.name" class="card bg-base-100 shadow-sm">
        <div class="card-body p-4">
          <div class="flex items-center justify-between mb-2">
            <h3 class="font-semibold truncate">{{ server.name }}</h3>
            <span class="badge badge-sm" :class="ownerBadgeClass(server.owner_type)">
              {{ server.owner_type }}
            </span>
          </div>

          <!-- Health Indicator -->
          <div class="flex items-center gap-2 mb-3">
            <div class="w-3 h-3 rounded-full" :class="healthDotClass(server.health_level)"></div>
            <span class="text-sm font-medium" :class="healthTextClass(server.health_level)">
              {{ capitalize(server.health_level) }}
            </span>
          </div>

          <!-- Health Summary -->
          <p v-if="server.health_summary" class="text-sm text-base-content/60 mb-2">
            {{ server.health_summary }}
          </p>

          <!-- Server Info -->
          <div class="flex flex-wrap gap-2 mt-auto">
            <span class="badge badge-outline badge-xs">{{ server.protocol }}</span>
            <span v-if="server.connected" class="badge badge-outline badge-xs badge-success">connected</span>
            <span v-else class="badge badge-outline badge-xs badge-error">disconnected</span>
            <span v-if="server.tool_count > 0" class="badge badge-outline badge-xs">{{ server.tool_count }} tools</span>
          </div>

          <!-- Health Detail / Action -->
          <div v-if="server.health_detail" class="mt-3 text-xs text-base-content/50">
            {{ server.health_detail }}
          </div>
          <div v-if="server.health_action" class="mt-2">
            <button class="btn btn-xs btn-outline btn-primary" @click="handleAction(server)">
              {{ actionLabel(server.health_action) }}
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'

interface DiagServer {
  name: string
  protocol: string
  enabled: boolean
  connected: boolean
  tool_count: number
  owner_type: 'personal' | 'shared'
  health_level: string
  health_summary: string
  health_detail: string
  health_action: string
}

const loading = ref(false)
const error = ref('')
const servers = ref<DiagServer[]>([])

const healthyCounts = computed(() => {
  const counts = { healthy: 0, degraded: 0, unhealthy: 0 }
  for (const s of servers.value) {
    if (s.health_level === 'healthy') counts.healthy++
    else if (s.health_level === 'degraded') counts.degraded++
    else counts.unhealthy++
  }
  return counts
})

function capitalize(s: string): string {
  return s ? s.charAt(0).toUpperCase() + s.slice(1) : ''
}

function healthDotClass(level: string): string {
  switch (level) {
    case 'healthy': return 'bg-success'
    case 'degraded': return 'bg-warning'
    case 'unhealthy': return 'bg-error'
    default: return 'bg-base-content/30'
  }
}

function healthTextClass(level: string): string {
  switch (level) {
    case 'healthy': return 'text-success'
    case 'degraded': return 'text-warning'
    case 'unhealthy': return 'text-error'
    default: return ''
  }
}

function ownerBadgeClass(type: string): string {
  return type === 'shared' ? 'badge-info' : 'badge-primary'
}

function actionLabel(action: string): string {
  switch (action) {
    case 'login': return 'Login'
    case 'restart': return 'Restart'
    case 'enable': return 'Enable'
    case 'approve': return 'Approve'
    case 'view_logs': return 'View Logs'
    case 'set_secret': return 'Set Secret'
    case 'configure': return 'Configure'
    default: return action
  }
}

async function handleAction(server: DiagServer) {
  try {
    if (server.health_action === 'login') {
      await fetch(`/api/v1/user/servers/${encodeURIComponent(server.name)}/login`, {
        method: 'POST',
        credentials: 'include',
      })
    } else if (server.health_action === 'restart') {
      await fetch(`/api/v1/user/servers/${encodeURIComponent(server.name)}/restart`, {
        method: 'POST',
        credentials: 'include',
      })
    } else if (server.health_action === 'enable') {
      await fetch(`/api/v1/user/servers/${encodeURIComponent(server.name)}/enable`, {
        method: 'POST',
        credentials: 'include',
      })
    }
    // Refresh after action
    setTimeout(loadDiagnostics, 1000)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Action failed'
  }
}

async function loadDiagnostics() {
  loading.value = true
  error.value = ''
  try {
    const res = await fetch('/api/v1/user/diagnostics', { credentials: 'include' })
    if (!res.ok) throw new Error(`HTTP ${res.status}: ${res.statusText}`)
    const data = await res.json()
    servers.value = (data.servers || []).map((s: any) => ({
      ...s,
      owner_type: s.ownership || 'shared',
      health_level: s.connected ? 'healthy' : (s.enabled ? 'unhealthy' : 'degraded'),
      health_summary: s.connected ? 'Connected' : (s.enabled ? 'Not connected' : 'Disabled'),
      health_detail: '',
      health_action: '',
    }))
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to load diagnostics'
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  loadDiagnostics()
})
</script>
