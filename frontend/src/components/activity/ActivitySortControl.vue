<template>
  <label class="flex items-center gap-2 text-sm" data-test="activity-sort-control">
    <span class="text-base-content/70">Sort</span>
    <select
      class="select select-bordered select-sm max-sm:min-h-11 border-[var(--ds-border)]"
      aria-label="Sort by"
      data-test="activity-sort"
      :value="`${column}:${direction}`"
      @change="onChange"
    >
      <option v-for="opt in ACTIVITY_SORT_OPTIONS" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
    </select>
  </label>
</template>

<script setup lang="ts">
import { ACTIVITY_SORT_OPTIONS, type ActivitySortColumn, type ActivitySortDirection } from '@/utils/activitySort'

// Spec 134 FR-046: one keyboard-operable control for every sort key in both
// directions. The table headers that remain still sort by click; this is the
// only way to sort by a column the calls view no longer shows (type, server).
defineProps<{ column: ActivitySortColumn; direction: ActivitySortDirection }>()
const emit = defineEmits<{ (e: 'update', column: ActivitySortColumn, direction: ActivitySortDirection): void }>()

function onChange(event: Event) {
  const [column, direction] = (event.target as HTMLSelectElement).value.split(':')
  emit('update', column as ActivitySortColumn, direction as ActivitySortDirection)
}
</script>
