<template>
  <span class="inline-flex flex-wrap items-center gap-1" data-test="tool-exceptions">
    <template v-for="exc in exceptions" :key="exc.key">
      <router-link
        v-if="exc.key === 'pending' || exc.key === 'changed'"
        :to="reviewPath(tool.server_name, exc.key)"
        class="status-pill state-tag tone-review no-underline whitespace-nowrap"
        :title="`${exc.title}. Open this server's review`"
        :data-test="`tool-exception-${exc.key}`"
        data-review-link
        @click.stop
      >{{ exc.label }}</router-link>
      <StateTag
        v-else
        :tone="TONE[exc.tone]"
        class="whitespace-nowrap"
        :title="exc.title"
        :data-test="`tool-exception-${exc.key}`"
      >{{ exc.label }}</StateTag>
    </template>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import StateTag from '@/components/StateTag.vue'
import type { GlobalTool } from '@/types/api'
import type { StatusTone } from '@/utils/healthTone'
import { reviewPath } from '@/utils/serverRoute'
import { toolExceptions } from '@/utils/toolsGrouping'

// Spec 134 FR-056: the exception labels of a grouped row. Nothing for an
// ordinary tool. The review labels link to that server's review (Spec 109
// FR-027), a plain path as in the flat table.
const props = defineProps<{ tool: GlobalTool }>()
const exceptions = computed(() => toolExceptions(props.tool))
const TONE: Record<string, StatusTone> = { warning: 'review', error: 'error', review: 'review', neutral: 'off' }
</script>
