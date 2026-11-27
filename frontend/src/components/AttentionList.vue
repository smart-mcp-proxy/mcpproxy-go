<template>
  <div data-test="attention-list">
    <!-- Spec 109 FR-001/FR-003: the ONE needs-attention list, read verbatim
         from GET /api/v1/attention. Every fix is a plain navigation to
         `fix.target` (FR-005) — never a one-click approve or a direct
         action from this list. Spec 134 FR-040: five items, then "See all"
         reveals the rest in place; loading and failed say so. -->
    <p
      v-if="!attentionStore.loaded && !attentionStore.loading.error"
      class="text-sm text-base-content/70"
      role="status"
      data-test="attention-loading"
    >Checking what needs attention…</p>
    <div
      v-else-if="!attentionStore.loaded"
      class="flex flex-wrap items-center gap-3 rounded border px-3 py-2 text-sm"
      style="background: var(--status-error-soft); color: var(--status-error); border-color: var(--status-error)"
      role="alert"
      data-test="attention-failed"
    >
      <span class="min-w-0 break-words">Could not load the attention list: {{ attentionStore.loading.error }}</span>
      <button type="button" class="btn btn-sm btn-outline max-sm:min-h-11" data-test="attention-retry" @click="attentionStore.fetchAttention()">Retry</button>
    </div>
    <p
      v-else-if="attentionStore.items.length === 0"
      class="flex items-center gap-2 text-sm text-base-content/70"
      data-test="attention-all-clear"
    >Nothing needs attention.</p>
    <div v-else class="card bg-base-100 border shadow-sm" style="border-color: var(--ds-border)" data-test="attention-list-card">
      <div class="card-body p-4">
        <h3 class="font-bold flex items-center gap-2" id="home-attention-title">
          <UiIcon name="alert" class="w-5 h-5 shrink-0" style="color: var(--status-review)" />
          Needs attention ({{ attentionStore.items.length }})
        </h3>
        <ul class="mt-2" aria-labelledby="home-attention-title">
          <li
            v-for="item in shown"
            :key="item.id"
            class="flex flex-wrap items-center justify-between gap-3 py-2 min-w-0 border-t first:border-t-0"
            style="border-color: var(--ds-border)"
            :data-test="`attention-item-${item.id}`"
          >
            <div class="min-w-0">
              <div class="text-sm font-medium break-words">{{ item.summary }}</div>
              <div v-if="item.detail" class="text-xs text-base-content/70 break-words">{{ item.detail }}</div>
            </div>
            <router-link
              :to="item.fix.target"
              class="btn btn-sm btn-primary shrink-0 max-sm:min-h-11"
              :data-test="`attention-fix-${item.id}`"
            >
              {{ item.fix.label }}
            </router-link>
          </li>
        </ul>
        <button
          v-if="attentionStore.items.length > LIMIT"
          type="button"
          class="btn btn-sm btn-outline self-start max-sm:min-h-11"
          :aria-expanded="expanded ? 'true' : 'false'"
          data-test="attention-see-all"
          @click="expanded = !expanded"
        >
          {{ expanded ? 'Show fewer' : `See all ${attentionStore.items.length}` }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useAttentionStore } from '@/stores/attention'
import UiIcon from '@/components/UiIcon.vue'

const LIMIT = 5
const attentionStore = useAttentionStore()
const expanded = ref(false)
const shown = computed(() => (expanded.value ? attentionStore.items : attentionStore.items.slice(0, LIMIT)))

onMounted(() => {
  attentionStore.fetchAttention()
})
</script>
