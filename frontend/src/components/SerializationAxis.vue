<template>
  <!--
    One serialization axis (Spec 085 `tool_response_mode` or Spec 102
    `direct_tool_response_mode`) as a two-option choice.

    Both axes are ALWAYS in effect somewhere — the dedicated endpoints never go
    away — so the surface is named rather than the axis greyed out. That is the
    difference an operator needs: "Direct listings" still governs /mcp/all while
    /mcp serves Retrieve.

    One visible line per option; the full explanation is the hover hint, so the
    panel never has to scroll.
  -->
  <div
    class="px-2 pb-1"
    role="radiogroup"
    :aria-label="title"
    :data-test="`serialization-axis-${axisId}`"
  >
    <div class="px-1 flex items-baseline justify-between gap-2">
      <span class="text-xs font-medium">{{ title }}</span>
      <span class="flex items-baseline gap-2">
        <code
          class="text-[0.65rem] font-mono text-base-content/60"
          :data-test="`serialization-surface-${axisId}`"
        >{{ surface }}</code>
        <a
          v-if="docHref"
          :href="docHref"
          target="_blank"
          rel="noopener"
          class="text-xs link link-hover text-base-content/60"
          :data-test="`serialization-doc-${axisId}`"
        >Docs ↗</a>
      </span>
    </div>
    <p
      v-if="note"
      class="px-1 text-xs text-base-content/70 leading-snug cursor-help"
      :title="noteHint"
      :data-test="`serialization-note-${axisId}`"
    >{{ note }}</p>
    <!-- role=radio + aria-checked, because the selection was conveyed only by a
         background tint and an unlabelled tick — neither reaches assistive tech.
         aria-busy rather than `disabled` while a write is in flight: disabling
         the focused button drops focus to <body> mid-interaction. -->
    <button
      v-for="o in options"
      :key="o.value"
      type="button"
      role="radio"
      :aria-checked="o.value === selected"
      :data-test="`serialization-option-${axisId}-${o.value}`"
      :title="o.detail"
      class="w-full text-left px-2 py-1.5 mt-1 rounded hover:bg-base-200 flex items-center justify-between gap-2"
      :class="{ 'bg-base-200': o.value === selected }"
      :aria-busy="busy"
      @click="emit('select', o.value)"
    >
      <span class="min-w-0">
        <span class="text-sm font-medium">{{ o.label }}</span>
        <span class="text-xs text-base-content/70"> — {{ o.summary }}</span>
      </span>
      <svg
        v-if="o.value === selected"
        class="w-4 h-4 text-success shrink-0"
        :data-test="`serialization-active-${axisId}`"
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
  /** The reason behind `note`, as a hover hint. */
  noteHint?: string
  /** Docs page for THIS axis — the two axes are documented separately. */
  docHref?: string
}>()

const emit = defineEmits<{ (e: 'select', value: string): void }>()
</script>
