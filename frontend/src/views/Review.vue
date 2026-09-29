<template>
  <div class="space-y-6" data-test="review-queue">
    <div class="flex flex-wrap items-end justify-between gap-3"><div><h1 class="text-3xl font-bold">Review queue</h1><p class="text-base-content/70">Review servers and tool changes before agents can use them.</p></div><span class="badge badge-warning badge-lg">{{ queue?.count ?? 0 }}</span></div>
    <div v-if="loading" class="text-center py-10"><span class="loading loading-spinner loading-lg"></span></div>
    <div v-else-if="error" class="alert alert-error">{{ error }}</div>
    <div v-else-if="selectedServer"><ReviewScreen :server-name="selectedServer" :change="change" @approved="load" /></div>
    <div v-else-if="!(queue?.servers.length)" class="alert alert-success" data-test="review-queue-empty">Nothing is waiting for review.</div>
    <ReviewQueueList v-else :rows="filteredRows" :change="change" />
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
const route = useRoute(); const scopeQuery = useScopeQuery('review'); const queue = ref<ReviewQueueResponse | null>(null); const loading = ref(false); const error = ref('')
const selectedServer = computed(() => typeof route.params.server === 'string' ? route.params.server : '')
const change = computed(() => scopeQuery.state.change ?? '')
const serverFilter = computed(() => scopeQuery.state.server ?? '')
const filteredRows = computed(() => queue.value?.servers.filter(r => (!serverFilter.value || r.server === serverFilter.value) && (!change.value || (change.value === 'pending' ? (r.pending ?? 0) > 0 : (r.changed ?? 0) > 0))) ?? [])
async function load() { loading.value = true; const res = await api.getReviewQueue(); loading.value = false; if (!res.success || !res.data) error.value = res.error || 'Failed to load review queue'; else { queue.value = res.data; error.value = '' } }
function onChanged() { void load() }
onMounted(() => { void load(); window.addEventListener('mcpproxy:review-changed', onChanged) }); onUnmounted(() => window.removeEventListener('mcpproxy:review-changed', onChanged)); watch(() => route.fullPath, () => { if (!selectedServer.value) void load() })
</script>
