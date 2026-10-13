<template>
  <!-- Spec 109 FR-051: a compact usage summary — "calls today / blocked /
       errors" — each linking to the full Activity log with a filter. Sits
       below the topology normally; Home.vue renders it above instead when
       the attention list is empty. -->
  <div class="flex flex-wrap gap-3" data-test="usage-summary-strip">
    <router-link
      :to="scope.linkTo('activity', { view: 'calls', from: '-24h' })"
      class="card card-compact bg-base-100 border border-base-300 hover:shadow-md transition-shadow flex-1 min-w-[140px]"
      data-test="usage-strip-calls"
    >
      <div class="card-body py-3 px-4">
        <div class="text-2xl font-bold leading-none">{{ loaded ? summary.call_count : '—' }}</div>
        <div class="text-xs opacity-60 mt-1">calls today</div>
      </div>
    </router-link>
    <router-link
      :to="scope.linkTo('activity', { view: 'calls', from: '-24h', status: 'blocked' })"
      class="card card-compact bg-base-100 border border-base-300 hover:shadow-md transition-shadow flex-1 min-w-[140px]"
      data-test="usage-strip-blocked"
    >
      <div class="card-body py-3 px-4">
        <div class="text-2xl font-bold leading-none">{{ loaded ? summary.blocked_count : '—' }}</div>
        <div class="text-xs opacity-60 mt-1">blocked</div>
      </div>
    </router-link>
    <router-link
      :to="scope.linkTo('activity', { view: 'calls', from: '-24h', status: 'error' })"
      class="card card-compact bg-base-100 border border-base-300 hover:shadow-md transition-shadow flex-1 min-w-[140px]"
      data-test="usage-strip-errors"
    >
      <div class="card-body py-3 px-4">
        <div class="text-2xl font-bold leading-none">{{ loaded ? summary.call_error_count : '—' }}</div>
        <div class="text-xs opacity-60 mt-1">errors</div>
      </div>
    </router-link>
    <!-- Review finding F3: Home keeps its own way into the full Usage page
         (the sidebar's Monitor -> Usage entry, Spec 109-i, is a second one). -->
    <router-link
      :to="scope.linkTo('usage')"
      class="card card-compact bg-base-100 border border-base-300 hover:shadow-md transition-shadow flex items-center justify-center min-w-[100px]"
      data-test="usage-strip-view-usage"
    >
      <div class="card-body py-3 px-4 flex-row items-center gap-1">
        <span class="text-sm font-medium">View usage</span>
        <svg class="w-4 h-4 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7" />
        </svg>
      </div>
    </router-link>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { useScopeQuery } from '@/composables/useScopeQuery'
import api from '@/services/api'

const authStore = useAuthStore()
// Spec 109 FR-058: links go through linkTo so sticky scope (from/to and, once
// available, profile/client/token) carries from Home into Activity.
const scope = useScopeQuery('home')
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
