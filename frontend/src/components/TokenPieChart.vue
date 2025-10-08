<template>
  <div class="relative">
    <Pie :data="chartData" :options="chartOptions" />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { Pie } from 'vue-chartjs'
import {
  Chart as ChartJS,
  ArcElement,
  Tooltip,
  Legend
} from 'chart.js'
import type { ChartOptions } from 'chart.js'

ChartJS.register(ArcElement, Tooltip, Legend)

interface Props {
  data: {
    name: string
    value: number
    percentage: number
    color: string
  }[]
}

const props = defineProps<Props>()

const chartData = computed(() => ({
  labels: props.data.map(d => d.name),
  datasets: [
    {
      data: props.data.map(d => d.value),
      backgroundColor: props.data.map(d => d.color),
      borderWidth: 2,
      borderColor: 'hsl(var(--b1))'
    }
  ]
}))

const chartOptions = computed<ChartOptions<'pie'>>(() => ({
  responsive: true,
  maintainAspectRatio: true,
  plugins: {
    legend: {
      display: false
    },
    tooltip: {
      callbacks: {
        label: (context) => {
          const label = context.label || ''
          const value = context.parsed || 0
          const percentage = props.data[context.dataIndex]?.percentage || 0
          return `${label}: ${value.toLocaleString()} (${percentage.toFixed(1)}%)`
        }
      }
    }
  }
}))
</script>
