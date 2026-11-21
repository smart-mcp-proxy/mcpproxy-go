<template>
  <ul class="divide-y divide-base-300 border border-base-300 rounded-lg" data-test="review-queue-list">
    <li v-for="row in rows" :key="row.server" class="flex items-center justify-between gap-3 px-4 py-3" :data-test="`servers-review-row-${row.server}`">
      <span class="min-w-0 flex-1"><span class="block font-medium truncate">{{ row.server }}</span><span class="block text-xs text-base-content/60">{{ reviewQueueRowSubtitle(row) }}</span></span>
      <span v-if="row.enabled === false" class="badge badge-ghost badge-sm" title="This server is disabled, so no live agent is waiting on this review" :data-test="`servers-review-disabled-${row.server}`">Disabled</span>
      <span v-if="row.quarantined" class="badge badge-warning badge-sm">Quarantined</span>
      <!-- Spec 125: a tool row that also has held skills offers the Skills tab too. -->
      <router-link v-if="row.kind !== REVIEW_KIND_SKILL_REVIEW && heldSkillCount(row) > 0" :to="serverSkillsLink(row.server)" class="btn btn-ghost btn-xs shrink-0" :data-test="`servers-review-skills-link-${row.server}`">Skills</router-link>
      <router-link :to="reviewQueueRowLink(row, change)" class="btn btn-primary btn-xs shrink-0" :data-test="`servers-review-link-${row.server}`" @click="$emit('review', $event, row.server)">Review</router-link>
    </li>
  </ul>
</template>
<script setup lang="ts">
import type { ReviewQueueRow } from '@/types'
import { REVIEW_KIND_SKILL_REVIEW, heldSkillCount, reviewQueueRowLink, reviewQueueRowSubtitle, serverSkillsLink } from '@/utils/reviewQueue'
defineProps<{ rows: ReviewQueueRow[]; change?: string }>()
defineEmits<{ review: [event: MouseEvent, server: string] }>()
</script>
