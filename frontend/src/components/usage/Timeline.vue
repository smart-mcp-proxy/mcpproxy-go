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
    y: { stacked: true, beginAtZero: true, ticks: { callback: (v) => formatNumber(Number(v)) } },
  },
}))
</script>
