<template>
  <!-- Spec 134 FR-040: the five most recent activity records, with a link to
       the full Activity log. Admin/personal only (the activity read is an
       admin core door, Spec 107 FR-041). -->
  <section class="card bg-base-100 border" style="border-color: var(--ds-border)" aria-labelledby="home-recent-activity-title" data-test="home-recent-activity">
    <div class="card-body p-4 gap-2">
      <div class="flex items-center justify-between gap-3">
        <h2 id="home-recent-activity-title" class="font-bold">Recent activity</h2>
        <router-link to="/activity" class="btn btn-sm btn-outline max-sm:min-h-11" data-test="recent-activity-view-all">View all activity</router-link>
      </div>
      <p v-if="loading" class="text-sm text-base-content/70" role="status">Loading…</p>
      <p v-else-if="error" class="text-sm" style="color: var(--status-error)" role="alert" data-test="recent-activity-error">{{ error }}</p>
      <p v-else-if="rows.length === 0" class="text-sm text-base-content/70" data-test="recent-activity-empty">No activity yet.</p>
      <ul v-else>
        <li v-for="a in rows" :key="a.id" class="flex items-center justify-between gap-3 py-2 min-w-0 border-t first:border-t-0" style="border-color: var(--ds-border)" data-test="recent-activity-row">
          <div class="flex items-center gap-3 min-w-0">
            <UiIcon :name="getTypeIcon(a.type)" :label="formatType(a.type)" class="w-5 h-5 shrink-0" />
            <div class="min-w-0">
              <div class="text-sm font-medium truncate">
                <span v-if="a.server_name">{{ a.server_name }}</span><span v-if="a.tool_name" class="text-base-content/70">:{{ a.tool_name }}</span>
                <span v-if="!a.server_name && !a.tool_name" class="text-base-content/70">{{ isPreflightActivity(a) ? (formatPreflightSummary(a.metadata) || 'Preflight') : formatType(a.type) }}</span>
              </div>
              <div class="text-xs text-base-content/70">{{ formatRelativeTime(a.timestamp) }}</div>
            </div>
          </div>
          <span data-test="recent-activity-status" :class="statusPresentation(a.status).className">{{ statusPresentation(a.status).label }}</span>
        </li>
      </ul>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import UiIcon from '@/components/UiIcon.vue'
import api from '@/services/api'
import type { ActivityRecord } from '@/types/api'
import { formatPreflightSummary, formatRelativeTime, formatType, getTypeIcon, isPreflightActivity, statusPresentation } from '@/utils/activity'

const rows = ref<ActivityRecord[]>([])
const loading = ref(true)
const error = ref<string | null>(null)

async function load() {
  try {
    const r = await api.getActivities({ limit: 5 })
    if (r.success && r.data) {
      rows.value = (r.data.activities || []).slice(0, 5)
      error.value = null
    } else {
      error.value = r.error || 'Could not load recent activity'
    }
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Could not load recent activity'
  } finally {
    loading.value = false
  }
}

let timer: ReturnType<typeof setInterval> | null = null
onMounted(() => {
  load()
  timer = setInterval(load, 30000)
})
onUnmounted(() => {
  if (timer) clearInterval(timer)
})
</script>
