<template>
  <section class="card bg-base-100 border border-base-300" data-test="scan-history">
    <div class="card-body">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div><h2 class="card-title">Scan history</h2><p class="text-sm text-base-content/70">Security scan results remain available from the review workflow.</p></div>
        <div class="flex gap-2"><button class="btn btn-sm btn-primary" :disabled="batchRunning" data-test="scan-all-button" @click="startBatch">{{ batchRunning ? 'Scanning…' : 'Scan all servers' }}</button><button class="btn btn-ghost btn-sm" :disabled="loading" @click="load">Refresh</button></div>
      </div>
      <div v-if="queue && queue.status !== 'idle'" class="mt-3 rounded bg-base-200 p-3 text-sm" data-test="scan-queue-progress">
        <div>Batch scan: {{ queue.completed || 0 }}/{{ queue.total || 0 }} completed, {{ queue.running || 0 }} running</div>
        <button v-if="queue.status === 'running'" class="btn btn-sm btn-warning btn-outline mt-2" @click="cancelBatch">Cancel all scans</button>
      </div>
      <div v-if="loading && !scans.length" class="py-5 text-center"><span class="loading loading-spinner"></span></div>
      <p v-else-if="!scans.length" class="py-3 text-sm text-base-content/60">No scan history yet.</p>
      <div v-else class="overflow-x-auto">
        <table class="table table-sm">
          <thead><tr><th>Server</th><th>Started</th><th>Status</th><th>Findings</th><th></th></tr></thead>
          <tbody><tr v-for="scan in scans" :key="scan.id"><td class="font-medium">{{ scan.server_name }}</td><td>{{ relativeTime(scan.started_at) }}</td><td><span class="badge badge-sm" :class="badgeClass(scan.status)">{{ scan.status }}</span></td><td>{{ scan.findings_count || 0 }}</td><td><router-link v-if="scan.status === 'completed'" :to="scanReportPath(scan.id)" class="link link-primary">Details</router-link></td></tr></tbody>
        </table>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import api from '@/services/api'
import { scanReportPath } from '@/utils/serverRoute'

const scans = ref<any[]>([])
const loading = ref(false)
const queue = ref<any>(null)
const batchRunning = ref(false)
let poll: ReturnType<typeof setInterval> | null = null
async function load() { loading.value = true; const res = await api.listScanHistory({ limit: 20, sort: 'started_at', order: 'desc' }); loading.value = false; if (res.success) scans.value = res.data?.scans ?? [] }
function relativeTime(value?: string) { if (!value) return '—'; const minutes = Math.floor((Date.now() - new Date(value).getTime()) / 60000); if (minutes < 1) return 'just now'; if (minutes < 60) return `${minutes}m ago`; if (minutes < 1440) return `${Math.floor(minutes / 60)}h ago`; return `${Math.floor(minutes / 1440)}d ago` }
function badgeClass(status?: string) { return status === 'completed' ? 'badge-success' : status === 'failed' ? 'badge-error' : status === 'running' ? 'badge-info' : 'badge-ghost' }
async function refreshQueue() { if (typeof api.getQueueProgress !== 'function') return; const res = await api.getQueueProgress(); if (res.success && res.data) { queue.value = res.data; if (res.data.status === 'running') { batchRunning.value = true; startPolling() } else if (['completed', 'cancelled', 'failed'].includes(res.data.status)) { batchRunning.value = false; stopPolling(); await load() } } }
function startPolling() { stopPolling(); poll = setInterval(() => void refreshQueue(), 3000) }
function stopPolling() { if (poll) { clearInterval(poll); poll = null } }
async function startBatch() { if (typeof api.scanAll !== 'function') return; batchRunning.value = true; const res = await api.scanAll(); if (!res.success) { batchRunning.value = false; return }; queue.value = res.data; startPolling() }
async function cancelBatch() { if (typeof api.cancelAllScans !== 'function') return; await api.cancelAllScans(); await refreshQueue() }
onMounted(() => { void load(); void refreshQueue() })
onUnmounted(stopPolling)
</script>
