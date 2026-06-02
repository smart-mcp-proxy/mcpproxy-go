<template>
  <div data-test="usage-response-size-ranking">
    <div class="flex items-center justify-between mb-1">
      <h3 class="font-semibold text-sm">Token sinks</h3>
      <span class="text-xs opacity-60">by response size</span>
    </div>
    <!-- FR-006: response size is a size-based proxy for token cost, not a real
         tokenizer count. Make that explicit so the number isn't mistaken for tokens. -->
    <p class="text-xs opacity-50 mb-2">
      Ranked by total response bytes (size-based proxy for token cost).
    </p>
    <div v-if="ranked.length === 0" class="text-sm opacity-60 py-8 text-center" data-test="usage-response-size-ranking-empty">
      No sized responses in this window.
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
import { formatBytes, toolLabel, paletteColor } from '@/utils/usageFormat'

ChartJS.register(BarElement, CategoryScale, LinearScale, Tooltip, Legend)

const props = defineProps<{ tools: UsageToolStat[] }>()

// Rank by total response bytes descending; drop tools with no sized output.
const ranked = computed(() =>
  [...props.tools]
    .filter(t => t.total_resp_bytes > 0)
    .sort((a, b) => b.total_resp_bytes - a.total_resp_bytes)
)

const chartHeight = computed(() => `${Math.max(160, ranked.value.length * 28 + 40)}px`)

const chartData = computed(() => ({
  labels: ranked.value.map(t => toolLabel(t.server, t.tool)),
  datasets: [
    {
      label: 'Total response bytes',
      data: ranked.value.map(t => t.total_resp_bytes),
      backgroundColor: ranked.value.map((_, i) => paletteColor(i)),
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
          const t = ranked.value[ctx.dataIndex]
          if (!t) return ''
          const avg = t.avg_resp_bytes != null ? ` · avg ${formatBytes(t.avg_resp_bytes)}` : ''
          return `${formatBytes(t.total_resp_bytes)} total${avg}`
        },
      },
    },
  },
  scales: {
    x: { beginAtZero: true, ticks: { callback: (v) => formatBytes(Number(v)) } },
    y: { ticks: { autoSkip: false, font: { size: 11 } } },
  },
}))
</script>
