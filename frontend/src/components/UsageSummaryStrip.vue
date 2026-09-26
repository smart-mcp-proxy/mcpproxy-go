<template>
  <!-- Spec 109 FR-051: a compact usage summary — "calls today / blocked /
       errors" — each linking to the full Activity log with a filter. Sits
       below the topology normally; Home.vue renders it above instead when
       the attention list is empty. -->
  <div class="flex flex-wrap gap-3" data-test="usage-summary-strip">
    <router-link
      to="/activity?view=calls&from=-24h"
      class="card card-compact bg-base-100 border border-base-300 hover:shadow-md transition-shadow flex-1 min-w-[140px]"
      data-test="usage-strip-calls"
    >
      <div class="card-body py-3 px-4">
        <div class="text-2xl font-bold leading-none">{{ loaded ? summary.call_count : '—' }}</div>
        <div class="text-xs opacity-60 mt-1">calls today</div>
      </div>
    </router-link>
    <router-link
      to="/activity?view=calls&from=-24h&status=blocked"
      class="card card-compact bg-base-100 border border-base-300 hover:shadow-md transition-shadow flex-1 min-w-[140px]"
      data-test="usage-strip-blocked"
    >
      <div class="card-body py-3 px-4">
        <div class="text-2xl font-bold leading-none">{{ loaded ? summary.blocked_count : '—' }}</div>
        <div class="text-xs opacity-60 mt-1">blocked</div>
      </div>
    </router-link>
    <router-link
      to="/activity?view=calls&from=-24h&status=error"
      class="card card-compact bg-base-100 border border-base-300 hover:shadow-md transition-shadow flex-1 min-w-[140px]"
      data-test="usage-strip-errors"
    >
      <div class="card-body py-3 px-4">
        <div class="text-2xl font-bold leading-none">{{ loaded ? summary.call_error_count : '—' }}</div>
        <div class="text-xs opacity-60 mt-1">errors</div>
      </div>
    </router-link>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useAuthStore } from '@/stores/auth'
import api from '@/services/api'

const authStore = useAuthStore()
const loaded = ref(false)
const summary = reactive({ call_count: 0, blocked_count: 0, call_error_count: 0 })

const load = async () => {
  // Spec 107 FR-041: /activity/summary is an admin-only core door — a
  // tenant principal has no fleet-wide usage strip to fill in.
  if (authStore.principalKind === 'tenant') return
  try {
    const response = await api.getActivitySummary('24h')
    if (response.success && response.data) {
      summary.call_count = response.data.call_count ?? 0
      summary.blocked_count = response.data.blocked_count ?? 0
      summary.call_error_count = response.data.call_error_count ?? 0
      loaded.value = true
    }
  } catch {
    // Silently fail — the strip renders its placeholder dashes.
  }
}

onMounted(load)

defineExpose({ load })
</script>
