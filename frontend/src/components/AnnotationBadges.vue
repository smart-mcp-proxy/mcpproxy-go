<template>
  <div v-if="hasAnnotations" class="flex flex-wrap gap-1 items-center" :class="compact ? 'gap-0.5' : 'gap-1'">
    <!-- Title -->
    <div
      v-if="annotations?.title"
      class="text-sm font-medium text-base-content/80"
      :class="compact ? 'text-xs' : ''"
    >
      {{ annotations.title }}
    </div>

    <!-- Read Only -->
    <div
      v-if="annotations?.readOnlyHint"
      :class="badgeClasses('info')"
      :title="compact ? 'Read-only: Does not modify data' : ''"
    >
      <span v-if="!compact">📖 Read-only</span>
      <span v-else>📖</span>
    </div>

    <!-- Destructive -->
    <div
      v-if="annotations?.destructiveHint"
      :class="badgeClasses('error')"
      :title="compact ? 'Destructive: May delete or modify data' : ''"
    >
      <span v-if="!compact">⚠️ Destructive</span>
      <span v-else>⚠️</span>
    </div>

    <!-- Idempotent -->
    <div
      v-if="annotations?.idempotentHint"
      :class="badgeClasses('neutral')"
      :title="compact ? 'Idempotent: Safe to retry' : ''"
    >
      <span v-if="!compact">🔄 Idempotent</span>
      <span v-else>🔄</span>
    </div>

    <!-- Open World -->
    <div
      v-if="annotations?.openWorldHint"
      :class="badgeClasses('secondary')"
      :title="compact ? 'Open World: May access external resources' : ''"
    >
      <span v-if="!compact">🌐 Open World</span>
      <span v-else>🌐</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { ToolAnnotation } from '@/types/api'

const props = withDefaults(defineProps<{
  annotations?: ToolAnnotation
  compact?: boolean
}>(), {
  compact: false
})

const hasAnnotations = computed(() => {
  if (!props.annotations) return false
  return (
    props.annotations.title ||
    props.annotations.readOnlyHint ||
    props.annotations.destructiveHint ||
    props.annotations.idempotentHint ||
    props.annotations.openWorldHint
  )
})

const badgeClasses = (variant: 'info' | 'error' | 'neutral' | 'secondary') => {
  const baseClasses = props.compact
    ? 'badge badge-sm cursor-help'
    : 'badge badge-sm'

  switch (variant) {
    case 'info':
      return `${baseClasses} badge-info`
    case 'error':
      return `${baseClasses} badge-error`
    case 'neutral':
      return `${baseClasses} badge-neutral`
    case 'secondary':
      return `${baseClasses} badge-secondary`
    default:
      return baseClasses
  }
}
</script>
