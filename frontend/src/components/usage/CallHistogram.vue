<template>
  <div data-test="usage-call-histogram">
    <div class="flex items-center justify-between mb-2">
      <h3 class="font-semibold text-sm">Calls per tool</h3>
      <span class="text-xs opacity-60">{{ charted.length }} tool{{ charted.length === 1 ? '' : 's' }}</span>
    </div>
    <div v-if="charted.length === 0" class="text-sm opacity-60 py-8 text-center" data-test="usage-call-histogram-empty">
      No completed tool calls in this window.
    </div>
    <div v-else class="relative" :style="{ height: chartHeight }">
      <Bar :data="chartData" :options="chartOptions" />
    </div>
    <!--
      Names that came from failed calls are not a catalog (F22, #1046). They are
      named here, once, so they are not hidden either — the Errors & latency
      chart below still charts them.
    -->
    <p
      v-if="unresolved.length > 0"
      data-test="usage-call-histogram-unresolved"
      class="text-xs opacity-50 mt-2"
      :title="unresolvedLabels"
    >
      {{ unresolved.length }} name{{ unresolved.length === 1 ? '' : 's' }} never completed a call
      and {{ unresolved.length === 1 ? 'is' : 'are' }} excluded here — see “Errors &amp; latency”.
    </p>
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
import { formatNumber, partitionUsageTools, toolLabel, paletteColor } from '@/utils/usageFormat'

ChartJS.register(BarElement, CategoryScale, LinearScale, Tooltip, Legend)

const props = defineProps<{ tools: UsageToolStat[] }>()

// This chart answers "what do my agents use". A name that has never once
// completed a call answers nothing about use — it is a typo the agent made or a
// server that was never reachable, and charting it invents a tool catalog out
// of failures (audit finding F22, #1046).
const split = computed(() => partitionUsageTools(props.tools))
const charted = computed(() => split.value.completed)
const unresolved = computed(() => split.value.unresolved)
const unresolvedLabels = computed(() =>
  unresolved.value.map(t => toolLabel(t.server, t.tool)).join(', ')
)

// Horizontal bars so long server:tool labels stay readable on high cardinality.
const chartHeight = computed(() => `${Math.max(160, charted.value.length * 28 + 40)}px`)

const chartData = computed(() => ({
  labels: charted.value.map(t => toolLabel(t.server, t.tool)),
  datasets: [
    {
      label: 'Calls',
      data: charted.value.map(t => t.calls),
      backgroundColor: charted.value.map((_, i) => paletteColor(i)),
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
          const t = charted.value[ctx.dataIndex]
          if (!t) return ''
          const errPart = t.errors > 0 ? ` · ${formatNumber(t.errors)} errors` : ''
          return `${formatNumber(t.calls)} calls${errPart}`
        },
      },
    },
  },
  scales: {
    // Say what the bars measure. An unlabelled axis is guesswork (F22, #1046).
    x: {
      beginAtZero: true,
      title: { display: true, text: 'Calls (cumulative)' },
      ticks: { callback: (v) => formatNumber(Number(v)) },
    },
    y: { ticks: { autoSkip: false, font: { size: 11 } } },
  },
}))
</script>
