<template>
  <span
    v-if="status"
    class="status-pill"
    :class="`tone-${tone}`"
    :data-tone="tone"
    data-test="status-pill"
  >
    <span class="status-pill__dot" data-pill-dot aria-hidden="true" />
    {{ text }}
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { healthPillText, healthTone } from '@/utils/healthTone'

// Spec 134 FR-003: the one component that prints a server's `health.status`.
// Known values print the Spec 109 label; an unknown value prints raw in the
// neutral tone; a missing value renders nothing (the caller keeps its present
// fallback, see utils/health.ts healthStatusText).
const props = defineProps<{ status?: string | null }>()
const tone = computed(() => healthTone(props.status))
const text = computed(() => (props.status ? healthPillText(props.status) : ''))
</script>
