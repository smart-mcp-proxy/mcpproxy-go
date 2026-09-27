<template>
  <div data-test="usage-timeline">
    <div class="flex items-center justify-between mb-2">
      <h3 class="font-semibold text-sm">Activity over time</h3>
      <span class="text-xs opacity-60">{{ windowLabel }}</span>
    </div>
    <div v-if="buckets.length === 0" class="text-sm opacity-60 py-8 text-center" data-test="usage-timeline-empty">
      No activity in this window.
    </div>
    <div v-else class="relative" style="height: 220px">
      <Bar :data="chartData" :options="chartOptions" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { Bar } from 'vue-chartjs'
import {
  Chart as ChartJS,
  BarElement,
  CategoryScale,
  LinearScale,
  Tooltip,
  Legend,
} from 'chart.js'
import type { ChartOptions } from 'chart.js'
import type { UsageTimeBucket, UsageWindow } from '@/types'
import { formatNumber } from '@/utils/usageFormat'

ChartJS.register(BarElement, CategoryScale, LinearScale, Tooltip, Legend)

const props = defineProps<{ buckets: UsageTimeBucket[]; window: UsageWindow }>()

// Spec 109-k, T119: link map "Usage chart bar (tool x bucket)" -> the calls
// behind the bar. A bucket carries only its own `start`; `end` is the next
// bucket's start (or, for the last one, the same span extrapolated forward —
// a single-bucket window falls back to "now").
const emit = defineEmits<{ (e: 'select-bucket', range: { start: string; end: string }): void }>()

function bucketRange(index: number): { start: string; end: string } {
  const start = props.buckets[index].start
  const next = props.buckets[index + 1]
  if (next) return { start, end: next.start }
  const prev = props.buckets[index - 1]
  if (prev) {
    const gapMs = new Date(start).getTime() - new Date(prev.start).getTime()
    return { start, end: new Date(new Date(start).getTime() + gapMs).toISOString() }
  }
  return { start, end: new Date().toISOString() }
}

const windowLabel = computed(() => {
  switch (props.window) {
    case '24h': return 'Last 24 hours'
    case '7d': return 'Last 7 days'
    default: return 'All time'
  }
})

// Coarser label for wider windows; the buckets themselves come from the backend.
function bucketLabel(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  if (props.window === '24h') {
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  }
  return d.toLocaleDateString([], { month: 'short', day: 'numeric' })
}

const chartData = computed(() => ({
  labels: props.buckets.map(b => bucketLabel(b.start)),
  datasets: [
    {
      label: 'Calls',
      data: props.buckets.map(b => b.calls - b.errors),
      backgroundColor: '#3b82f6',
      borderWidth: 0,
      stack: 'activity',
    },
    {
      label: 'Errors',
      data: props.buckets.map(b => b.errors),
      backgroundColor: '#ef4444',
      borderWidth: 0,
      stack: 'activity',
    },
  ],
}))

const chartOptions = computed<ChartOptions<'bar'>>(() => ({
  responsive: true,
  maintainAspectRatio: false,
  onClick: (_event, elements) => {
    const el = elements[0]
    if (el && props.buckets[el.index]) emit('select-bucket', bucketRange(el.index))
  },
  onHover: (event, elements) => {
    const target = event.native?.target as HTMLElement | undefined
    if (target) target.style.cursor = elements.length > 0 ? 'pointer' : 'default'
  },
  plugins: {
    legend: { display: true, position: 'bottom', labels: { boxWidth: 12, font: { size: 11 } } },
    tooltip: {
      callbacks: {
        footer: (items) => {
          const b = props.buckets[items[0]?.dataIndex ?? -1]
          return b ? `Total: ${formatNumber(b.calls)} calls` : ''
        },
      },
    },
  },
  scales: {
    x: { stacked: true, ticks: { maxRotation: 0, autoSkip: true, font: { size: 10 } } },
    // Spec 109 FR-074: a call count is never fractional — force integer
    // ticks rather than letting chart.js round a small max into 0.5/1.5s.
    y: { stacked: true, beginAtZero: true, ticks: { precision: 0, callback: (v) => formatNumber(Number(v)) } },
  },
}))
</script>
