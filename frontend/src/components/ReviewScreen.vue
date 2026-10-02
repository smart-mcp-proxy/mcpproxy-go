<template>
  <section class="space-y-5" data-test="review-screen">
    <div v-if="loading" class="text-center py-8"><span class="loading loading-spinner"></span></div>
    <div v-else-if="error" class="alert alert-error"><span>{{ error }}</span><button class="btn btn-sm" @click="load">Retry</button></div>
    <template v-else-if="review">
      <header class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 class="text-2xl font-bold" data-test="review-heading">{{ headline.title }}</h2>
          <p class="text-sm text-base-content/70" data-test="review-subtitle">{{ headline.subtitle }}</p>
        </div>
        <router-link v-if="review.server.scan?.report_id" :to="scanReportPath(review.server.scan.report_id)" class="btn btn-ghost btn-sm">Scan report</router-link>
      </header>

      <div class="stats bg-base-100 shadow-sm" data-test="review-tier-counts">
        <div v-for="tier in tiers" :key="tier" class="stat py-3"><div class="stat-title capitalize">{{ tier }}</div><div class="stat-value text-xl">{{ tierCounts[tier] }}</div></div>
      </div>
      <div v-if="banner" class="alert" :class="bannerClass" data-test="review-scan-summary">
        <span>{{ banner.text }}</span>
        <button v-if="banner.action !== 'none'" class="btn btn-sm" data-test="review-scan-action" @click="rescan">{{ banner.action === 'scan-now' ? 'Scan now' : 'Rescan' }}</button>
      </div>
      <div class="rounded border border-base-300 p-3 text-sm" data-test="review-server-identity">
        <div><span class="font-medium">Transport:</span> {{ review.server.transport }}</div>
        <div v-if="review.server.command"><span class="font-medium">Command:</span> <code>{{ review.server.command }}</code></div>
        <div v-else-if="review.server.url"><span class="font-medium">URL:</span> <code>{{ review.server.url }}</code></div>
        <div v-if="review.server.trust_mode"><span class="font-medium">Trust mode:</span> {{ review.server.trust_mode }}</div>
        <div v-if="review.server.source_registry_id"><span class="font-medium">Origin:</span> {{ review.server.source_registry_id }}<template v-if="review.server.source_registry_provenance"> · {{ review.server.source_registry_provenance }}</template></div>
      </div>

      <div v-if="!review.server.definitions_captured" class="alert alert-warning" data-test="review-no-definitions">
        <span>Tool definitions have not been captured yet.</span>
        <button class="btn btn-sm" :disabled="scanning" @click="fetchDefinitions">{{ scanning ? 'Fetching…' : 'Fetch tool definitions' }}</button>
      </div>

      <article v-for="tool in filteredTools" :key="tool.name" class="card bg-base-100 border border-base-300" :data-test="`review-tool-${tool.name}`">
        <div class="card-body gap-3">
          <div class="flex flex-wrap justify-between gap-2">
            <div><h3 class="font-mono font-semibold">{{ tool.name }}</h3><span class="badge badge-outline mt-1">{{ tool.tier }}</span> <span class="badge badge-ghost mt-1">{{ tool.scan_verdict }}</span></div>
            <label v-if="toolState(tool, review.server.quarantined) === 'allow-toggle'" class="label cursor-pointer gap-2"><span class="label-text">Allow this tool</span><input v-model="allowedTools" class="checkbox checkbox-primary" type="checkbox" :value="tool.name" :data-test="`review-allow-${tool.name}`" @change="recordChoice(tool, ($event.target as HTMLInputElement).checked)"></label>
            <div v-else-if="toolState(tool, review.server.quarantined) === 'approve-reject'" class="join"><button class="btn btn-sm" @click="approveTool(tool.name)">Approve</button><button class="btn btn-sm btn-outline btn-error" @click="blockTool(tool.name)">Reject</button></div>
            <span v-else class="badge" :class="toolState(tool, review.server.quarantined) === 'blocked' ? 'badge-error badge-outline' : 'badge-success badge-outline'" :data-test="`review-tool-state-${tool.name}`">{{ toolState(tool, review.server.quarantined) === 'blocked' ? 'Blocked' : 'Approved' }}</span>
          </div>
          <ToolDefinitionText :text="tool.description" />
          <details v-if="tool.annotations || tool.input_schema || tool.output_schema"><summary class="cursor-pointer text-sm">Definition</summary><pre class="mt-2 text-xs whitespace-pre-wrap" v-text="definitionText(tool)"></pre></details>
          <details v-if="tool.diff || tool.previous" open data-test="review-tool-diff"><summary class="cursor-pointer text-sm font-medium">Changed definition</summary><pre class="mt-2 text-xs whitespace-pre-wrap" v-text="diffText(tool)"></pre></details>
        </div>
      </article>

      <p v-if="review.server.quarantined && review.server.definitions_captured && review.tools.length > 0" class="text-sm text-base-content/70" data-test="review-selection-hint">{{ REVIEW_SELECTION_HINT }}</p>
      <div v-if="review.server.quarantined" class="flex flex-wrap gap-2">
        <button class="btn btn-primary" :disabled="approving" data-test="review-approve-server" @click="requestApprove(false)">{{ primaryLabel }}</button>
        <button v-if="showApproveAll" class="btn btn-outline" :disabled="approving" data-test="review-approve-all" @click="requestApprove(true)">{{ approveAllLabel(review.tools.length) }}</button>
        <button class="btn btn-outline btn-error" :disabled="approving" @click="rejectServer">Reject server</button>
      </div>
      <div v-if="headline.state === 'approved'" class="flex flex-wrap items-center gap-2" data-test="review-approved-actions">
        <router-link :to="`/servers/${encodeURIComponent(review.server.name)}?tab=tools`" class="btn btn-sm" data-test="review-manage-tools">Manage tools</router-link>
        <button class="btn btn-sm btn-outline" :disabled="requarantining" data-test="review-requarantine" @click="requestRequarantine">Quarantine to review again…</button>
      </div>
      <ScanHistory />
    </template>

    <dialog ref="confirmDialog" class="modal" @close="confirmOpen = false"><div class="modal-box"><h3 class="font-bold text-lg">Approve without seeing tools?</h3><p class="py-3">No tool definitions were captured. Fetch them before approval whenever possible.</p><div class="modal-action"><button class="btn" @click="closeConfirm">Cancel</button><button class="btn btn-warning" @click="approve(false)">Approve without seeing tools</button></div></div></dialog>
    <dialog ref="forceDialog" class="modal"><div class="modal-box"><h3 class="font-bold text-lg">Dangerous findings detected</h3><p class="py-3">The baseline scan found dangerous findings. Force approval activates this server despite that warning.</p><div class="modal-action"><button class="btn" @click="forceDialog?.close()">Cancel</button><button class="btn btn-error" @click="approve(true)">Force approve server</button></div></div></dialog>
      <dialog ref="requarantineDialog" class="modal" data-test="review-requarantine-dialog"><div class="modal-box"><h3 class="font-bold text-lg">Quarantine {{ serverName }} to review again?</h3><p class="py-3">Agents lose access to every tool on {{ serverName }} until you approve it again.</p><div class="modal-action"><button class="btn" data-test="review-requarantine-cancel" @click="requarantineDialog?.close?.()">Cancel</button><button class="btn btn-warning" :disabled="requarantining" data-test="review-requarantine-confirm" @click="requarantine">Quarantine</button></div></div></dialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import api from '@/services/api'
import type { ReviewTool, ServerReviewResponse } from '@/types'
import ToolDefinitionText from '@/components/ToolDefinitionText.vue'
import ScanHistory from '@/components/ScanHistory.vue'
import { scanReportPath } from '@/utils/serverRoute'
import { REVIEW_SELECTION_HINT, approveAllLabel, approveLabel, mergeSelection, reviewHeadline, scanBanner, toolState, type SelectionChoice } from '@/utils/reviewPresentation'

const props = defineProps<{ serverName: string; change?: string }>()
const emit = defineEmits<{ approved: []; refreshed: [] }>()
const review = ref<ServerReviewResponse | null>(null)
const loading = ref(false); const scanning = ref(false); const approving = ref(false); const error = ref('')
const rescanning = ref(false); const requarantining = ref(false); const requarantineDialog = ref<HTMLDialogElement | null>(null)
const allowedTools = ref<string[]>([]); const choices = new Map<string, SelectionChoice>(); const lastBlock = ref<string[] | null>(null); const confirmDialog = ref<HTMLDialogElement | null>(null); const forceDialog = ref<HTMLDialogElement | null>(null); const confirmOpen = ref(false)
const tiers = ['read', 'write', 'destructive', 'unannotated', 'unknown']
const headline = computed(() => review.value ? reviewHeadline(review.value) : { state: 'review', title: '', subtitle: '' })
const banner = computed(() => {
  if (!review.value) return null
  const computedBanner = scanBanner(review.value.server.scan, review.value.server.definitions_captured)
  // A rescan the operator just started reads as in progress until it settles.
  if (computedBanner && rescanning.value) return { severity: 'info' as const, text: 'Scan in progress…', action: 'none' as const }
  return computedBanner
})
const bannerClass = computed(() => `alert-${banner.value?.severity ?? 'info'}`)
const filteredTools = computed(() => review.value?.tools.filter(t => !props.change || t.approval_status === props.change) ?? [])
const primaryLabel = computed(() => approveLabel(allowedTools.value.length, review.value?.tools.length ?? 0, review.value?.server.definitions_captured ?? false))
const showApproveAll = computed(() => !!review.value && review.value.server.quarantined && review.value.server.definitions_captured && review.value.tools.length > 0 && allowedTools.value.length < review.value.tools.length)
const tierCounts = computed(() => Object.fromEntries(tiers.map(t => [t, (review.value?.tools ?? []).filter(x => x.tier === t).length])))
// An explicit click is remembered with the payload the user saw, so a reload keeps it (D41.4).
function recordChoice(tool: ReviewTool, allowed: boolean) { choices.set(tool.name, { allowed, tool }) }
function definitionText(tool: ReviewTool) { return JSON.stringify({ input_schema: tool.input_schema, output_schema: tool.output_schema, annotations: tool.annotations }, null, 2) }
function diffText(tool: ReviewTool) { return Object.values(tool.diff ?? {}).filter(Boolean).join('\n\n') || JSON.stringify(tool.previous, null, 2) }
async function load() { if (typeof api.getServerReview !== 'function') return; loading.value = true; error.value = ''; const res = await api.getServerReview(props.serverName); loading.value = false; if (!res.success || !res.data) { error.value = res.error || 'Failed to load review'; return }; review.value = res.data; allowedTools.value = mergeSelection(res.data.tools, choices); emit('refreshed') }
async function rescan() {
  rescanning.value = true
  const res = await api.startScan(props.serverName)
  if (!res.success) { rescanning.value = false; error.value = res.error || 'Failed to start scan' }
}
function requestRequarantine() { requarantineDialog.value?.showModal?.() }
async function requarantine() {
  requarantining.value = true
  const res = await api.quarantineServer(props.serverName)
  requarantining.value = false
  requarantineDialog.value?.close?.()
  if (!res.success) { error.value = res.error || 'Quarantine failed'; return }
  await load()
}
async function fetchDefinitions() {
  scanning.value = true
  const res = await api.discoverServerTools(props.serverName)
  scanning.value = false
  if (!res.success) { error.value = res.error || 'Failed to capture tool definitions'; return }
  await load()
}
function requestApprove(everything: boolean) { if (!review.value?.server.definitions_captured) { confirmOpen.value = true; confirmDialog.value?.showModal(); return }; void approve(false, everything ? [] : undefined) }
function closeConfirm() { confirmOpen.value = false; confirmDialog.value?.close?.() }
// The force retry re-sends the block list of the attempt that triggered it (D41.5).
async function approve(force: boolean, block?: string[]) {
  closeConfirm(); forceDialog.value?.close?.(); approving.value = true
  const all = review.value?.tools.map(t => t.name) ?? []
  const blocked = block ?? (force && lastBlock.value ? lastBlock.value : all.filter(name => !allowedTools.value.includes(name)))
  lastBlock.value = blocked
  const res = await api.securityApprove(props.serverName, force, blocked)
  approving.value = false
  if (!res.success) { error.value = res.error || 'Approval failed'; if (!force && /dangerous/i.test(error.value)) forceDialog.value?.showModal?.(); return }
  choices.clear(); emit('approved'); await load()
}
async function rejectServer() { approving.value = true; const res = await api.securityReject(props.serverName); approving.value = false; if (!res.success) error.value = res.error || 'Reject failed'; else await load() }
async function approveTool(name: string) { await api.approveTools(props.serverName, [name]); await load() }
async function blockTool(name: string) { await api.blockTools(props.serverName, [name]); await load() }
function refreshAfterReviewChange() { scanning.value = false; rescanning.value = false; void load() }
async function refreshAfterScanSettled(event: Event) {
  const serverName = (event as CustomEvent<{ server_name?: string }>).detail?.server_name
  if (serverName && serverName !== props.serverName) return
  scanning.value = false
  rescanning.value = false
  void load()
}
// Component reuse across /review/A -> /review/B: scan state and any pending force retry belong to the old server.
watch(() => props.serverName, () => { choices.clear(); lastBlock.value = null; forceDialog.value?.close?.(); closeConfirm(); scanning.value = false; rescanning.value = false; error.value = ''; void load() })
onMounted(() => {
  void load()
  window.addEventListener('mcpproxy:review-changed', refreshAfterReviewChange)
  window.addEventListener('mcpproxy:scan-settled', refreshAfterScanSettled)
})
onUnmounted(() => {
  window.removeEventListener('mcpproxy:review-changed', refreshAfterReviewChange)
  window.removeEventListener('mcpproxy:scan-settled', refreshAfterScanSettled)
})
</script>
