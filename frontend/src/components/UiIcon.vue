<template>
  <svg
    xmlns="http://www.w3.org/2000/svg"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="2"
    stroke-linecap="round"
    stroke-linejoin="round"
    :role="label ? 'img' : undefined"
    :aria-label="label || undefined"
    :aria-hidden="label ? undefined : 'true'"
    focusable="false"
    data-test="ui-icon"
    :data-icon="name"
  >
    <path v-for="(d, i) in paths" :key="i" :d="d" />
  </svg>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { UI_ICONS, type UiIconName } from '@/utils/uiIcons'

// Spec 134 FR-005: the one inline SVG icon (24 px grid, stroke 2). Decorative by
// default (the visible text names it); pass `label` when the icon carries the
// meaning alone.
const props = defineProps<{ name: UiIconName; label?: string }>()
const paths = computed(() => {
  const d = UI_ICONS[props.name]
  return Array.isArray(d) ? d : [d]
})
</script>
