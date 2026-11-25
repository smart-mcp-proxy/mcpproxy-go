<template>
  <!-- One row, fixed height: the boundary between the filters and the results
       does not move when a chip is added, removed or the list overflows. -->
  <div class="flex items-center gap-2 h-12 min-w-0" data-test="activity-filter-chips">
    <template v-if="chips.length > 0">
      <div
        ref="rowEl"
        data-test="activity-active-filters"
        class="flex flex-nowrap items-center gap-2 overflow-x-auto min-w-0 flex-1 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
      >
        <span class="text-xs text-base-content/70 shrink-0">Filters:</span>
        <button
          v-for="chip in chips"
          :key="chip.key"
          type="button"
          :data-test="chipTestId(chip)"
          class="badge badge-ghost gap-1 shrink-0 h-8 max-sm:h-11 max-sm:px-3 border border-[var(--ds-border)] hover:bg-base-300"
          :title="chip.title || chip.label"
          :aria-label="`Remove filter ${chip.label}`"
          @click="emit('remove', chip)"
        >
          <span class="max-w-[16rem] truncate">{{ chip.label }}</span>
          <UiIcon name="x" class="w-3 h-3 opacity-70 shrink-0" />
        </button>
      </div>

      <!-- Shown when the chips do not fit: lists every active filter and removes each. -->
      <div v-if="overflowing" ref="overflowEl" class="relative shrink-0">
        <button
          type="button"
          data-test="activity-filter-overflow"
          class="btn btn-sm btn-outline max-sm:min-h-11 border-[var(--ds-border)]"
          aria-haspopup="dialog"
          :aria-expanded="overflowOpen"
          @click="overflowOpen = !overflowOpen"
        >All {{ chips.length }} filters</button>
        <div
          v-if="overflowOpen"
          role="dialog"
          aria-label="Active filters"
          data-test="activity-filter-overflow-list"
          class="absolute right-0 top-full mt-1 w-72 max-w-[calc(100vw-2rem)] max-h-72 overflow-y-auto p-2 bg-base-100 border border-[var(--ds-border)] rounded-box shadow-lg z-[var(--z-dropdown)] space-y-1"
          @keydown.esc.stop="closeOverflow(true)"
        >
          <div v-for="chip in chips" :key="chip.key" class="flex items-center gap-2">
            <span class="text-sm flex-1 min-w-0 break-words">{{ chip.label }}</span>
            <button
              type="button"
              :data-test="`activity-overflow-chip-${chip.kind}`"
              class="btn btn-xs btn-outline max-sm:min-h-11 shrink-0 border-[var(--ds-border)]"
              :aria-label="`Remove filter ${chip.label}`"
              @click="emit('remove', chip)"
            >Remove</button>
          </div>
        </div>
      </div>

      <button
        type="button"
        data-test="activity-clear-all"
        class="btn btn-xs btn-ghost max-sm:min-h-11 shrink-0"
        @click="emit('clear-all')"
      >Clear all</button>
    </template>
    <span v-else class="text-xs text-base-content/60" data-test="activity-active-filters-empty">No filters applied</span>
  </div>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import UiIcon from '@/components/UiIcon.vue'
import type { ActiveFilterChip } from '@/utils/activity'

// Spec 134 FR-047: the active filters as removable chips in one row, with an
// overflow control that lists and removes every one of them when they do not fit.
const props = defineProps<{ chips: ActiveFilterChip[] }>()
const emit = defineEmits<{ (e: 'remove', chip: ActiveFilterChip): void; (e: 'clear-all'): void }>()

const rowEl = ref<HTMLElement | null>(null)
const overflowEl = ref<HTMLElement | null>(null)
const overflowing = ref(false)
const overflowOpen = ref(false)

/** The sub-call chip keeps its own hook — it is the code_execution exit door. */
const chipTestId = (chip: ActiveFilterChip): string =>
  chip.kind === 'parent' ? 'activity-parent-filter-chip' : `activity-filter-chip-${chip.kind}`

function measure() {
  const el = rowEl.value
  overflowing.value = Boolean(el) && el!.scrollWidth > el!.clientWidth + 1
  if (!overflowing.value) overflowOpen.value = false
}

function closeOverflow(returnFocus = false) {
  overflowOpen.value = false
  if (returnFocus) void nextTick(() => overflowEl.value?.querySelector<HTMLElement>('[data-test="activity-filter-overflow"]')?.focus())
}

function onDocClick(event: MouseEvent) {
  if (overflowOpen.value && overflowEl.value && !overflowEl.value.contains(event.target as Node)) overflowOpen.value = false
}

let observer: ResizeObserver | null = null
onMounted(() => {
  if (typeof ResizeObserver !== 'undefined') {
    observer = new ResizeObserver(measure)
    if (rowEl.value) observer.observe(rowEl.value)
  }
  document.addEventListener('click', onDocClick)
  measure()
})
onBeforeUnmount(() => {
  observer?.disconnect()
  document.removeEventListener('click', onDocClick)
})
watch(
  () => props.chips.map(c => c.key + c.label).join('|'),
  async () => {
    await nextTick()
    if (rowEl.value) observer?.observe(rowEl.value)
    measure()
  },
  { flush: 'post' }
)
</script>
