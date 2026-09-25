<template>
  <div v-if="attentionStore.loaded" data-test="attention-list">
    <!-- Spec 109 FR-001/FR-003: the ONE needs-attention list, read verbatim
         from GET /api/v1/attention. Every fix is a plain navigation to
         `fix.target` (FR-005) — never a one-click approve or a direct
         action from this list. -->
    <div v-if="attentionStore.items.length === 0" class="alert alert-success" data-test="attention-all-clear">
      <svg class="w-5 h-5 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7" />
      </svg>
      <span>All clear — nothing needs your attention</span>
    </div>
    <div v-else class="card bg-base-100 border border-warning/30 shadow-sm" data-test="attention-list-card">
      <div class="card-body p-4">
        <h3 class="font-bold flex items-center gap-2">
          <svg class="w-5 h-5 text-warning shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.732-.833-2.5 0L3.732 16.5c-.77.833.192 2.5 1.732 2.5z" />
          </svg>
          Needs attention ({{ attentionStore.items.length }})
        </h3>
        <ul class="divide-y divide-base-300 mt-2">
          <li
            v-for="item in attentionStore.items"
            :key="item.id"
            class="flex flex-wrap items-center justify-between gap-3 py-2 min-w-0"
            :data-test="`attention-item-${item.id}`"
          >
            <div class="min-w-0">
              <div class="text-sm font-medium break-words">{{ item.summary }}</div>
              <div v-if="item.detail" class="text-xs opacity-60 break-words">{{ item.detail }}</div>
            </div>
            <router-link
              :to="item.fix.target"
              class="btn btn-xs btn-primary shrink-0"
              :data-test="`attention-fix-${item.id}`"
            >
              {{ item.fix.label }}
            </router-link>
          </li>
        </ul>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue'
import { useAttentionStore } from '@/stores/attention'

const attentionStore = useAttentionStore()

onMounted(() => {
  attentionStore.fetchAttention()
})
</script>
