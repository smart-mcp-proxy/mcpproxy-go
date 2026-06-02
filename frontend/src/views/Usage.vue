<template>
  <div class="space-y-6" data-test="usage-view">
    <!-- Controls: window + filters -->
    <div class="flex flex-wrap items-center gap-3">
      <div class="join" data-test="usage-window-selector">
        <button
          v-for="w in windows"
          :key="w.value"
          class="btn btn-sm join-item"
          :class="window === w.value ? 'btn-primary' : 'btn-ghost'"
          :data-test="`usage-window-${w.value}`"
          @click="setWindow(w.value)"
        >
          {{ w.label }}
        </button>
      </div>

      <select v-model="status" class="select select-sm select-bordered" data-test="usage-status-filter" @change="reload">
        <option value="">All statuses</option>
        <option value="success">Success</option>
        <option value="error">Errors</option>
        <option value="blocked">Blocked</option>
      </select>

      <select v-model="sort" class="select select-sm select-bordered" data-test="usage-sort" @change="reload">
        <option value="resp_bytes">Sort: response size</option>
        <option value="calls">Sort: calls</option>
        <option value="error_rate">Sort: error rate</option>
        <option value="p95">Sort: p95 latency</option>
      </select>

      <span v-if="data" class="text-xs opacity-50 ml-auto" data-test="usage-freshness">
        Updated {{ freshnessLabel }}
      </span>
    </div>

    <!-- Tokens-saved headline (FR-007) -->
    <div v-if="data" class="stats stats-vertical sm:stats-horizontal shadow w-full" data-test="usage-tokens-saved">
      <div class="stat">
        <div class="stat-title">Tokens saved</div>
        <div class="stat-value text-success">{{ formatNumber(data.tokens_saved) }}</div>
        <div class="stat-desc">{{ data.tokens_saved_percentage.toFixed(1) }}% reduction via BM25 discovery</div>
      </div>
      <div class="stat">
        <div class="stat-title">Tool calls</div>
        <div class="stat-value">{{ formatNumber(totalCalls) }}</div>
        <div class="stat-desc">{{ data.tools.length }} active tool{{ data.tools.length === 1 ? '' : 's' }} ({{ windowLabel }})</div>
      </div>
      <div class="stat">
        <div class="stat-title">Errors</div>
        <div class="stat-value" :class="totalErrors > 0 ? 'text-error' : ''">{{ formatNumber(totalErrors) }}</div>
        <div class="stat-desc">{{ overallErrorRate }}% overall error rate</div>
      </div>
    </div>

    <!-- Loading -->
    <div v-if="loading && !data" class="flex justify-center py-16" data-test="usage-loading">
      <span class="loading loading-spinner loading-lg"></span>
    </div>

    <!-- Error -->
    <div v-else-if="error" class="alert alert-error" data-test="usage-error">
      <span>{{ error }}</span>
      <button class="btn btn-sm" @click="reload">Retry</button>
    </div>

    <!-- Empty / low-data state (FR-009) -->
    <div
      v-else-if="data && isEmpty"
      class="card bg-base-200 border border-base-300"
      data-test="usage-empty-state"
    >
      <div class="card-body items-center text-center py-12">
        <svg class="w-12 h-12 opacity-40" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z" />
        </svg>
        <h3 class="font-semibold text-lg mt-2">No usage data yet</h3>
        <p class="text-sm opacity-60 max-w-md">
          Once your agents start calling tools through the proxy, you'll see call volume,
          token sinks, error rates and a timeline here. Try widening the window or clearing filters.
        </p>
        <button v-if="window !== 'all' || status" class="btn btn-sm btn-primary mt-2" data-test="usage-empty-widen" @click="resetFilters">
          Show all time
        </button>
      </div>
    </div>

    <!-- Charts grid -->
    <div v-else-if="data" class="grid grid-cols-1 lg:grid-cols-2 gap-6" data-test="usage-charts">
      <div class="card bg-base-100 shadow">
        <div class="card-body p-4">
          <CallHistogram :tools="data.tools" />
        </div>
      </div>
      <div class="card bg-base-100 shadow">
        <div class="card-body p-4">
          <ResponseSizeRanking :tools="data.tools" />
        </div>
      </div>
      <div class="card bg-base-100 shadow">
        <div class="card-body p-4">
          <ErrorRateChart :tools="data.tools" />
        </div>
      </div>
      <div class="card bg-base-100 shadow">
        <div class="card-body p-4">
          <Timeline :buckets="data.timeline" :window="window" />
        </div>
      </div>

      <!-- "other" fold note when the per-tool list was truncated -->
      <div v-if="data.other" class="lg:col-span-2 text-xs opacity-50 text-center" data-test="usage-other-note">
        + {{ data.other.tools_folded }} more tool{{ data.other.tools_folded === 1 ? '' : 's' }}
        ({{ formatNumber(data.other.calls) }} calls) folded into “other”.
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import api from '@/services/api'
import type { UsageAggregateResponse, UsageWindow, UsageSort, UsageStatus } from '@/types'
import { formatNumber } from '@/utils/usageFormat'
import CallHistogram from '@/components/usage/CallHistogram.vue'
import ResponseSizeRanking from '@/components/usage/ResponseSizeRanking.vue'
import ErrorRateChart from '@/components/usage/ErrorRateChart.vue'
import Timeline from '@/components/usage/Timeline.vue'

const windows: { value: UsageWindow; label: string }[] = [
  { value: '24h', label: '24h' },
  { value: '7d', label: '7d' },
  { value: 'all', label: 'All' },
]

const window = ref<UsageWindow>('24h')
const status = ref<UsageStatus | ''>('')
const sort = ref<UsageSort>('resp_bytes')

const data = ref<UsageAggregateResponse | null>(null)
const loading = ref(false)
const error = ref<string | null>(null)
let refreshTimer: ReturnType<typeof setInterval> | null = null

const windowLabel = computed(() => {
  switch (window.value) {
    case '24h': return 'last 24h'
    case '7d': return 'last 7d'
    default: return 'all time'
  }
})

const totalCalls = computed(() => data.value?.tools.reduce((s, t) => s + t.calls, 0) ?? 0)
const totalErrors = computed(() => data.value?.tools.reduce((s, t) => s + t.errors, 0) ?? 0)
const overallErrorRate = computed(() => {
  const c = totalCalls.value
  return c > 0 ? ((totalErrors.value / c) * 100).toFixed(1) : '0.0'
})

const isEmpty = computed(() => {
  if (!data.value) return false
  return data.value.tools.length === 0 && data.value.timeline.length === 0
})

const freshnessLabel = computed(() => {
  const ms = data.value?.freshness_ms ?? 0
  if (ms < 1000) return 'just now'
  if (ms < 60_000) return `${Math.round(ms / 1000)}s ago`
  return `${Math.round(ms / 60_000)}m ago`
})

async function reload() {
  loading.value = true
  error.value = null
  try {
    const resp = await api.getActivityUsage({
      window: window.value,
      status: status.value || undefined,
      sort: sort.value,
    })
    if (resp.success && resp.data) {
      data.value = resp.data
    } else {
      error.value = resp.error || 'Failed to load usage data'
    }
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load usage data'
  } finally {
    loading.value = false
  }
}

function setWindow(w: UsageWindow) {
  window.value = w
  reload()
}

function resetFilters() {
  window.value = 'all'
  status.value = ''
  reload()
}

onMounted(() => {
  reload()
  // Light auto-refresh so the page stays live without hammering the endpoint
  // (the backend already serves from a short-TTL cached snapshot).
  refreshTimer = setInterval(reload, 30_000)
})

onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer)
})
</script>
