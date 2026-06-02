<template>
  <div data-test="usage-call-histogram">
    <div class="flex items-center justify-between mb-2">
      <h3 class="font-semibold text-sm">Calls per tool</h3>
      <span class="text-xs opacity-60">{{ tools.length }} tool{{ tools.length === 1 ? '' : 's' }}</span>
    </div>
    <div v-if="tools.length === 0" class="text-sm opacity-60 py-8 text-center" data-test="usage-call-histogram-empty">
      No tool calls in this window.
    </div>
    <div v-else class="relative" :style="{ height: chartHeight }">
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
import type { UsageToolStat } from '@/types'
import { formatNumber, toolLabel, paletteColor } from '@/utils/usageFormat'

ChartJS.register(BarElement, CategoryScale, LinearScale, Tooltip, Legend)

const props = defineProps<{ tools: UsageToolStat[] }>()

// Horizontal bars so long server:tool labels stay readable on high cardinality.
const chartHeight = computed(() => `${Math.max(160, props.tools.length * 28 + 40)}px`)

const chartData = computed(() => ({
  labels: props.tools.map(t => toolLabel(t.server, t.tool)),
  datasets: [
    {
      label: 'Calls',
      data: props.tools.map(t => t.calls),
      backgroundColor: props.tools.map((_, i) => paletteColor(i)),
      borderWidth: 0,
      borderRadius: 3,
    },
  ],
}))

const chartOptions = computed<ChartOptions<'bar'>>(() => ({
  indexAxis: 'y',
  responsive: true,
  maintainAspectRatio: false,
  plugins: {
    legend: { display: false },
    tooltip: {
      callbacks: {
        label: (ctx) => {
          const t = props.tools[ctx.dataIndex]
          if (!t) return ''
          const errPart = t.errors > 0 ? ` · ${formatNumber(t.errors)} errors` : ''
          return `${formatNumber(t.calls)} calls${errPart}`
        },
      },
    },
  },
  scales: {
    x: { beginAtZero: true, ticks: { callback: (v) => formatNumber(Number(v)) } },
    y: { ticks: { autoSkip: false, font: { size: 11 } } },
  },
}))
</script>
