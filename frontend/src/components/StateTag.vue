<template>
  <span class="status-pill state-tag" :class="`tone-${tone}`" :data-tone="tone" data-test="state-tag">
    <slot />
  </span>
</template>

<script setup lang="ts">
import type { StatusTone } from '@/utils/healthTone'

// Spec 134 FR-003/FR-037: a small tag for state labels that are not a server
// `health.status` (skill states, trust, transport...). Same tones, no status
// vocabulary.
withDefaults(defineProps<{ tone?: StatusTone }>(), { tone: 'neutral' })
</script>
