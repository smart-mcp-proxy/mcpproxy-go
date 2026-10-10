<template>
  <div class="space-y-6" data-test="review-queue">
    <div class="flex flex-wrap items-end justify-between gap-3"><div><h1 class="text-3xl font-bold">Review queue</h1><p class="text-base-content/70">Review servers and tool changes before agents can use them.</p></div><div class="text-right max-w-full"><span class="badge badge-warning badge-lg" data-test="review-count-badge" :title="scopeText">{{ queue?.count ?? 0 }}</span><p v-if="queue?.servers.length" class="mt-1 text-xs text-base-content/70" data-test="review-scope-text">{{ scopeText }} <span class="text-base-content/50">Home counts only active blockers.</span></p></div></div>
    <!-- Only the first load blanks the page: a background reload (review.changed) must not unmount ReviewScreen, or its per-instance selection is lost (D43.4). -->
    <div v-if="loading && !queue" class="text-center py-10"><span class="loading loading-spinner loading-lg"></span></div>
    <div v-else-if="error && !queue" class="alert alert-error">{{ error }}</div>
    <div v-else-if="selectedServer">
      <div v-if="error" class="alert alert-error mb-4" data-test="review-queue-error">{{ error }}</div><ReviewScreen :server-name="selectedServer" :change="change" @approved="load" /></div>
    <div v-else-if="!(queue?.servers.length)" class="alert alert-success" data-test="review-queue-empty">Nothing is waiting for review.</div>
    <template v-else>
      <div v-if="error" class="alert alert-error" data-test="review-queue-error">{{ error }}</div>
      <div class="flex flex-wrap items-center gap-2" data-test="review-triage">
        <div class="join" role="group" aria-label="Review queue scope">
          <button type="button" class="btn btn-sm join-item" :class="effectiveView === 'active' ? 'btn-primary' : 'btn-ghost border border-base-300'" :aria-pressed="effectiveView === 'active'" data-test="review-view-active" @click="view = 'active'">Active blockers ({{ summary.active }})</button>
          <button type="button" class="btn btn-sm join-item" :class="effectiveView === 'all' ? 'btn-primary' : 'btn-ghost border border-base-300'" :aria-pressed="effectiveView === 'all'" data-test="review-view-all" @click="view = 'all'">All reviews ({{ summary.total }})</button>
        </div>
        <input v-model="search" type="search" class="input input-bordered input-sm min-w-0 flex-1 basis-40" placeholder="Search servers" aria-label="Search review queue by server name" data-test="review-search" />
        <label class="flex items-center gap-1 text-sm"><span class="text-base-content/70">Sort</span>
          <select v-model="sort" class="select select-bordered select-sm" aria-label="Sort review queue" data-test="review-sort">
            <option value="name">Name</option><option value="impact">Impact (active first)</option><option value="age">Oldest first</option>
          </select></label>
      </div>
      <p v-if="effectiveView === 'active' && summary.deferred > 0 && !search.trim() && !serverFilter" class="text-xs text-base-content/60" data-test="review-deferred-note">{{ summary.deferred }} {{ summary.deferred === 1 ? 'review is' : 'reviews are' }} on disabled servers and hidden from this view. Nothing is approved by hiding it; choose All reviews to see {{ summary.deferred === 1 ? 'it' : 'them' }}.</p>
      <div v-if="!filteredRows.length" class="alert" data-test="review-no-match"><span>No reviews match this view.</span><button v-if="effectiveView === 'active' || search" type="button" class="btn btn-xs" @click="search = ''; view = 'all'">Show all reviews</button></div>
      <ReviewQueueList :rows="filteredRows" :change="change" />
    </template>
    <ScanHistory v-if="!selectedServer" />
  </div>
</template>
<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useScopeQuery } from '@/composables/useScopeQuery'
import api from '@/services/api'
import type { ReviewQueueResponse } from '@/types'
import ReviewScreen from '@/components/ReviewScreen.vue'
import ScanHistory from '@/components/ScanHistory.vue'
import ReviewQueueList from '@/components/ReviewQueueList.vue'
import { isActiveBlocker, reviewQueueScopeText, sortReviewRows, summarizeReviewQueue, type ReviewSort } from '@/utils/reviewQueue'
const route = useRoute(); const scopeQuery = useScopeQuery('review'); const queue = ref<ReviewQueueResponse | null>(null); const loading = ref(false); const error = ref('')
const selectedServer = computed(() => typeof route.params.server === 'string' ? route.params.server : '')
const change = computed(() => scopeQuery.state.change ?? '')
const serverFilter = computed(() => scopeQuery.state.server ?? '')
const summary = computed(() => summarizeReviewQueue(queue.value?.servers))
const scopeText = computed(() => reviewQueueScopeText(summary.value))
// Triage state is local to the page. A deep link (?server=, ?change=) names its
// rows explicitly and a search is a deliberate lookup, so both bypass the
// Active blockers default instead of silently hiding a quarantined server.
const view = ref<'active' | 'all' | null>(null)
const search = ref('')
const sort = ref<ReviewSort>('name')
const effectiveView = computed<'active' | 'all'>(() => view.value ?? (summary.value.active > 0 ? 'active' : 'all'))
const filteredRows = computed(() => {
  const q = search.value.trim().toLowerCase()
  const scoped = effectiveView.value === 'active' && !q && !serverFilter.value
  const rows = (queue.value?.servers ?? []).filter(r =>
    (!serverFilter.value || r.server === serverFilter.value) &&
    (!change.value || (change.value === 'pending' ? (r.pending ?? 0) > 0 : (r.changed ?? 0) > 0)) &&
    (!q || r.server.toLowerCase().includes(q)) &&
    (!scoped || isActiveBlocker(r)))
  return sortReviewRows(rows, sort.value)
})
async function load() { loading.value = true; const res = await api.getReviewQueue(); loading.value = false; if (!res.success || !res.data) error.value = res.error || 'Failed to load review queue'; else { queue.value = res.data; error.value = '' } }
function onChanged() { void load() }
onMounted(() => { void load(); window.addEventListener('mcpproxy:review-changed', onChanged) }); onUnmounted(() => window.removeEventListener('mcpproxy:review-changed', onChanged)); watch(() => route.fullPath, () => { if (!selectedServer.value) void load() })
</script>
