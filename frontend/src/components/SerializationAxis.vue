<template>
  <!--
    One serialization axis (Spec 085 `tool_response_mode` or Spec 102
    `direct_tool_response_mode`) rendered as a two-option choice.

    Both axes are ALWAYS in effect somewhere — the dedicated endpoints never go
    away — so the surface is named rather than the axis greyed out. That is the
    difference an operator needs to make the call: "Direct listings" still
    governs /mcp/all while /mcp serves Retrieve.
  -->
  <div class="px-2 pb-2" :data-test="`serialization-axis-${axisId}`">
    <div class="px-1 flex items-baseline justify-between gap-2">
      <span class="text-xs font-medium">{{ title }}</span>
      <code class="text-[0.65rem] font-mono text-base-content/50" :data-test="`serialization-surface-${axisId}`">{{ surface }}</code>
    </div>
    <p
      v-if="note"
      class="px-1 mt-1 text-xs text-base-content/60 leading-relaxed"
      :data-test="`serialization-note-${axisId}`"
    >{{ note }}</p>
    <button
      v-for="o in options"
      :key="o.value"
      type="button"
      :data-test="`serialization-option-${axisId}-${o.value}`"
      class="w-full text-left px-2 py-2 mt-1 rounded hover:bg-base-200 flex items-start justify-between"
      :class="{ 'bg-base-200': o.value === selected }"
      :disabled="busy"
      @click="emit('select', o.value)"
    >
      <div class="min-w-0">
        <div class="flex items-center gap-2">
          <span class="text-sm font-medium">{{ o.label }}</span>
          <span
            v-if="o.value === selected"
            class="badge badge-xs badge-primary"
            :data-test="`serialization-active-${axisId}`"
          >active</span>
        </div>
        <div class="text-xs text-base-content/60 mt-0.5 leading-relaxed">{{ o.description }}</div>
      </div>
      <svg
        v-if="o.value === selected"
        class="w-4 h-4 text-success shrink-0 ml-2 mt-0.5"
        fill="none"
        stroke="currentColor"
        viewBox="0 0 24 24"
      >
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7" />
      </svg>
    </button>
  </div>
</template>

<script setup lang="ts">
import type { SerializationModeMeta } from '@/utils/routingMode'

defineProps<{
  axisId: string
  title: string
  /** Endpoints this axis governs right now — never empty. */
  surface: string
  options: SerializationModeMeta[]
  selected: string
  busy: boolean
  /** Caveat shown under the heading when the axis does not govern what the
      surface line alone would imply. */
  note?: string
}>()

const emit = defineEmits<{ (e: 'select', value: string): void }>()
</script>
