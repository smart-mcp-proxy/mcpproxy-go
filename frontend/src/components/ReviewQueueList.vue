<template>
  <ul class="divide-y divide-base-300 border border-base-300 rounded-lg" data-test="review-queue-list">
    <li v-for="row in rows" :key="row.server" class="flex items-center justify-between gap-3 px-4 py-3" :data-test="`servers-review-row-${row.server}`">
      <span class="min-w-0 flex-1"><span class="block font-medium truncate">{{ row.server }}</span><span class="block text-xs text-base-content/60">{{ row.kind === 'server_review' ? 'Server awaiting review' : `${row.pending ?? 0} new · ${row.changed ?? 0} changed` }}</span></span>
      <span v-if="row.quarantined" class="badge badge-warning badge-sm">Quarantined</span>
      <router-link :to="{ path: `/review/${encodeURIComponent(row.server)}`, query: { ...(change ? { change } : {}) } }" class="btn btn-primary btn-xs shrink-0" :data-test="`servers-review-link-${row.server}`" @click="$emit('review', $event, row.server)">Review</router-link>
    </li>
  </ul>
</template>
<script setup lang="ts">
import type { ReviewQueueRow } from '@/types'
defineProps<{ rows: ReviewQueueRow[]; change?: string }>()
defineEmits<{ review: [event: MouseEvent, server: string] }>()
</script>
