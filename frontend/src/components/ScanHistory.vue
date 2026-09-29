<template>
  <section class="card bg-base-100 border border-base-300" data-test="scan-history">
    <div class="card-body">
      <div class="flex items-center justify-between gap-3">
        <div><h2 class="card-title">Scan history</h2><p class="text-sm text-base-content/70">Security scan results remain available from the review workflow.</p></div>
        <button class="btn btn-ghost btn-sm" :disabled="loading" @click="load">Refresh</button>
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
import { onMounted, ref } from 'vue'
import api from '@/services/api'
import { scanReportPath } from '@/utils/serverRoute'

const scans = ref<any[]>([])
const loading = ref(false)
async function load() { loading.value = true; const res = await api.listScanHistory({ limit: 20, sort: 'started_at', order: 'desc' }); loading.value = false; if (res.success) scans.value = res.data?.scans ?? [] }
function relativeTime(value?: string) { if (!value) return '—'; const minutes = Math.floor((Date.now() - new Date(value).getTime()) / 60000); if (minutes < 1) return 'just now'; if (minutes < 60) return `${minutes}m ago`; if (minutes < 1440) return `${Math.floor(minutes / 60)}h ago`; return `${Math.floor(minutes / 1440)}d ago` }
function badgeClass(status?: string) { return status === 'completed' ? 'badge-success' : status === 'failed' ? 'badge-error' : status === 'running' ? 'badge-info' : 'badge-ghost' }
onMounted(load)
</script>
