<template>
  <div data-test="usage-error-rate-chart">
    <h3 class="font-semibold text-sm mb-2">Errors &amp; latency</h3>
    <div v-if="tools.length === 0" class="text-sm opacity-60 py-8 text-center" data-test="usage-error-rate-empty">
      No tool calls in this window.
    </div>
    <template v-else>
      <!-- Error rate per tool -->
      <div class="relative mb-4" :style="{ height: chartHeight }">
        <Bar :data="chartData" :options="chartOptions" />
      </div>
      <!-- Per-tool p50/p95 latency (FR: tail-latency visibility, T019) -->
      <div class="overflow-x-auto">
        <table class="table table-xs" data-test="usage-latency-table">
          <thead>
            <tr>
              <th>Tool</th>
              <th class="text-right">p50</th>
              <th class="text-right">p95</th>
              <th class="text-right">Err%</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="t in latencyRows" :key="`${t.server}:${t.tool}`">
              <td class="font-mono text-xs truncate max-w-[12rem]">{{ toolLabel(t.server, t.tool) }}</td>
              <td class="text-right font-mono text-xs">{{ formatLatency(t.p50_ms) }}</td>
              <td class="text-right font-mono text-xs">{{ formatLatency(t.p95_ms) }}</td>
              <td class="text-right font-mono text-xs" :class="t.error_rate > 0 ? 'text-error' : 'opacity-60'">
                {{ (t.error_rate * 100).toFixed(1) }}%
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
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
import { formatNumber, formatLatency, toolLabel } from '@/utils/usageFormat'

ChartJS.register(BarElement, CategoryScale, LinearScale, Tooltip, Legend)

const props = defineProps<{ tools: UsageToolStat[] }>()

const chartHeight = computed(() => `${Math.max(140, props.tools.length * 24 + 40)}px`)

// Worst-offenders first in the latency table; cap the visible rows.
const latencyRows = computed(() =>
  [...props.tools].sort((a, b) => b.p95_ms - a.p95_ms).slice(0, 12)
)

const chartData = computed(() => ({
  labels: props.tools.map(t => toolLabel(t.server, t.tool)),
  datasets: [
    {
      label: 'Error rate %',
      data: props.tools.map(t => +(t.error_rate * 100).toFixed(2)),
      backgroundColor: props.tools.map(t => (t.error_rate > 0 ? '#ef4444' : '#22c55e')),
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
          return `${(t.error_rate * 100).toFixed(1)}% (${formatNumber(t.errors)}/${formatNumber(t.calls)})`
        },
      },
    },
  },
  scales: {
    x: { beginAtZero: true, max: 100, ticks: { callback: (v) => `${v}%` } },
    y: { ticks: { autoSkip: false, font: { size: 11 } } },
  },
}))
</script>
