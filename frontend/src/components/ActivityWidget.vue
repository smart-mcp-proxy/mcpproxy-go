<template>
  <div class="card bg-base-100 shadow-md">
    <div class="card-body">
      <div class="flex items-center justify-between mb-4">
        <div>
          <h2 class="card-title text-lg">Recent Activity</h2>
          <p class="text-sm opacity-60">Activity across all servers</p>
        </div>
        <router-link to="/activity" class="btn btn-sm btn-ghost">
          View All
        </router-link>
      </div>

      <!-- Summary Stats Row -->
      <div v-if="summary" class="stats stats-horizontal bg-base-200 mb-4">
        <div class="stat py-2 px-4">
          <div class="stat-title text-xs">Today</div>
          <div class="stat-value text-lg">{{ summary.total_count }}</div>
        </div>
        <div class="stat py-2 px-4">
          <div class="stat-title text-xs">Success</div>
          <div class="stat-value text-lg text-success">{{ summary.success_count }}</div>
        </div>
        <div class="stat py-2 px-4">
          <div class="stat-title text-xs">Errors</div>
          <div class="stat-value text-lg text-error">{{ summary.error_count }}</div>
        </div>
      </div>

      <!-- Loading State -->
      <div v-if="loading" class="flex justify-center py-4">
        <span class="loading loading-spinner loading-sm"></span>
      </div>

      <!-- Error State -->
      <div v-else-if="error" class="alert alert-error alert-sm">
        <span class="text-sm">{{ error }}</span>
      </div>

      <!-- Empty State -->
      <div v-else-if="activities.length === 0" class="text-center py-4 text-base-content/60">
        <p class="text-sm">No activity yet</p>
      </div>

      <!-- Recent Activities List -->
      <div v-else class="space-y-2">
        <div
          v-for="activity in activities.slice(0, 5)"
          :key="activity.id"
          class="flex items-center justify-between p-2 bg-base-200 rounded-lg hover:bg-base-300 transition-colors cursor-pointer"
          @click="navigateToActivity(activity.id)"
        >
          <div class="flex items-center gap-3">
            <span class="text-lg">{{ getTypeIcon(activity.type) }}</span>
            <div>
              <div class="text-sm font-medium">
                <span v-if="activity.server_name">{{ activity.server_name }}</span>
                <span v-if="activity.tool_name" class="text-base-content/70">:{{ activity.tool_name }}</span>
              </div>
              <div class="text-xs text-base-content/60">{{ formatRelativeTime(activity.timestamp) }}</div>
            </div>
          </div>
          <div
            class="badge badge-sm"
            :class="getStatusBadgeClass(activity.status)"
          >
            {{ activity.status }}
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import api from '@/services/api'
import type { ActivityRecord, ActivitySummaryResponse } from '@/types/api'

const router = useRouter()

// State
const activities = ref<ActivityRecord[]>([])
const summary = ref<ActivitySummaryResponse | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)

// Load data
const loadData = async () => {
  loading.value = true
  error.value = null

  try {
    const [activitiesResponse, summaryResponse] = await Promise.all([
      api.getActivities({ limit: 5 }),
      api.getActivitySummary('24h')
    ])

    if (activitiesResponse.success && activitiesResponse.data) {
      activities.value = activitiesResponse.data.activities || []
    }

    if (summaryResponse.success && summaryResponse.data) {
      summary.value = summaryResponse.data
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to load activity'
  } finally {
    loading.value = false
  }
}

// Navigation
const navigateToActivity = (id: string) => {
  router.push('/activity')
}

// Format helpers
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

const getTypeIcon = (type: string): string => {
  const typeIcons: Record<string, string> = {
    'tool_call': '🔧',
    'policy_decision': '🛡️',
    'quarantine_change': '⚠️',
    'server_change': '🔄'
  }
  return typeIcons[type] || '📋'
}

const getStatusBadgeClass = (status: string): string => {
  const statusClasses: Record<string, string> = {
    'success': 'badge-success',
    'error': 'badge-error',
    'blocked': 'badge-warning'
  }
  return statusClasses[status] || 'badge-ghost'
}

// Lifecycle
onMounted(() => {
  loadData()
})
</script>
