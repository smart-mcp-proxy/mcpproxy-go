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

      <div class="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-5 gap-2" data-test="review-tier-counts">
        <div v-for="tier in tiers" :key="tier" class="min-w-0 rounded border border-base-300 bg-base-100 px-3 py-2"><div class="text-xs capitalize text-base-content/70 truncate">{{ tier }}</div><div class="text-xl font-semibold">{{ tierCounts[tier] }}</div></div>
      </div>
      <div v-if="banner" class="alert" :class="bannerClass" data-test="review-scan-summary">
        <span>{{ banner.text }}</span>
        <button v-if="banner.action !== 'none'" class="btn btn-sm" data-test="review-scan-action" @click="rescan">{{ banner.action === 'scan-now' ? 'Scan now' : 'Rescan' }}</button>
      </div>
      <div class="rounded border border-base-300 p-3 text-sm space-y-1 min-w-0" data-test="review-server-identity">
        <div><span class="font-medium">Transport:</span> {{ review.server.transport }}</div>
        <template v-if="review.server.command">
          <div class="min-w-0" data-test="review-identity-command"><span class="font-medium">Command:</span> <code class="break-all whitespace-pre-wrap">{{ review.server.command }}</code></div>
          <div v-if="review.server.args?.length" class="min-w-0" data-test="review-identity-args">
            <span class="font-medium">Arguments:</span>
            <ol class="mt-1 flex flex-col gap-1">
              <li v-for="(arg, i) in review.server.args" :key="i" class="min-w-0"><code class="break-all whitespace-pre-wrap" data-test="review-identity-arg">{{ arg }}</code></li>
            </ol>
          </div>
          <div v-if="review.server.working_dir" class="min-w-0" data-test="review-identity-workdir"><span class="font-medium">Working directory:</span> <code class="break-all whitespace-pre-wrap">{{ review.server.working_dir }}</code></div>
          <div class="min-w-0" data-test="review-identity-launch">
            <span class="font-medium">Launch line:</span>
            <div class="mt-1 flex flex-wrap items-start gap-2">
              <code class="min-w-0 flex-1 basis-60 break-all whitespace-pre-wrap rounded bg-base-200 px-2 py-1" data-test="review-identity-launch-line">{{ launchLine }}</code>
              <button type="button" class="btn btn-xs" data-test="review-identity-copy" @click="copyLaunchLine">{{ copied ? 'Copied' : 'Copy' }}</button>
            </div>
          </div>
        </template>
        <div v-else-if="review.server.url" class="min-w-0"><span class="font-medium">URL:</span> <code class="break-all">{{ review.server.url }}</code></div>
        <div v-if="review.server.trust_mode"><span class="font-medium">Trust mode:</span> {{ review.server.trust_mode }}</div>
        <div v-if="review.server.source_registry_id"><span class="font-medium">Origin:</span> {{ review.server.source_registry_id }}<template v-if="review.server.source_registry_provenance"> · {{ review.server.source_registry_provenance }}</template></div>
      </div>

      <div v-if="!review.server.definitions_captured" class="alert alert-warning" data-test="review-no-definitions">
        <span>Tool definitions have not been captured yet.</span>
        <button class="btn btn-sm" :disabled="scanning" @click="fetchDefinitions">{{ scanning ? 'Fetching…' : 'Fetch tool definitions' }}</button>
      </div>

      <p v-if="review.server.quarantined && review.server.definitions_captured && review.tools.length > 0" class="text-sm text-base-content/70" data-test="review-selection-hint">{{ REVIEW_SELECTION_HINT }}</p>

      <div v-if="review.tools.length > 0" class="space-y-2" data-test="review-toolbar">
        <div class="flex flex-wrap items-end gap-2">
          <input v-model="searchQuery" type="search" class="input input-bordered input-sm min-w-0 flex-1 basis-48" placeholder="Search tool name or description" aria-label="Search tools by name or description" data-test="review-search">
          <select v-model="tierFilter" class="select select-bordered select-sm" aria-label="Filter by tier" data-test="review-filter-tier">
            <option value="all">All tiers</option>
            <option v-for="tier in tiers" :key="tier" :value="tier">{{ tier }}</option>
          </select>
          <select v-model="stateFilter" class="select select-bordered select-sm" aria-label="Filter by approval state" data-test="review-filter-state">
            <option value="all">All states</option>
            <option value="pending">Pending</option>
            <option value="changed">Changed</option>
            <option value="approved">Approved</option>
          </select>
          <select v-if="review.server.quarantined" v-model="selectionFilter" class="select select-bordered select-sm" aria-label="Filter by decision" data-test="review-filter-selection">
            <option value="all">Allowed and blocked</option>
            <option value="allowed">Allowed only</option>
            <option value="blocked">Blocked only</option>
          </select>
          <button v-if="filtersActive" type="button" class="btn btn-ghost btn-sm" data-test="review-clear-filters" @click="clearFilters">Clear filters</button>
        </div>
        <div v-if="review.server.quarantined && review.server.definitions_captured" class="flex flex-wrap items-center gap-2">
          <button type="button" class="btn btn-outline btn-sm" :disabled="!filtersActive || filteredTools.length === 0" :title="filtersActive ? '' : 'Narrow the list with a search or filter first. Use Approve all to allow everything.'" data-test="review-bulk-allow" @click="bulkSet(true)">Allow all filtered ({{ filteredTools.length }})</button>
          <button type="button" class="btn btn-outline btn-sm" :disabled="filteredTools.length === 0" data-test="review-bulk-block" @click="bulkSet(false)">Block all filtered ({{ filteredTools.length }})</button>
        </div>
        <p class="text-sm text-base-content/70" data-test="review-range" aria-live="polite">{{ rangeText }}</p>
      </div>
      <div v-if="review.tools.length > 0 && filteredTools.length === 0" class="rounded border border-base-300 p-4 text-sm" data-test="review-no-match">
        No tools match. <button type="button" class="link" @click="clearFilters">Clear filters</button>
      </div>

      <article v-for="tool in pagedTools" :key="tool.name" class="card bg-base-100 border border-base-300" :data-test="`review-tool-${tool.name}`">
        <div class="card-body gap-3 p-4">
          <div class="flex flex-wrap justify-between gap-2">
            <div class="min-w-0"><h3 class="font-mono font-semibold break-all">{{ tool.name }}</h3><span class="badge badge-outline mt-1">{{ tool.tier }}</span> <span class="badge badge-ghost mt-1">{{ tool.scan_verdict }}</span></div>
            <label v-if="toolState(tool, review.server.quarantined) === 'allow-toggle'" class="label cursor-pointer gap-2"><span class="label-text">Allow this tool</span><input v-model="allowedTools" class="checkbox checkbox-primary" type="checkbox" :value="tool.name" :data-test="`review-allow-${tool.name}`" @change="recordChoice(tool, ($event.target as HTMLInputElement).checked)"></label>
            <div v-else-if="toolState(tool, review.server.quarantined) === 'approve-reject'" class="join"><button class="btn btn-sm" @click="approveTool(tool.name)">Approve</button><button class="btn btn-sm btn-outline btn-error" @click="blockTool(tool.name)">Reject</button></div>
            <span v-else class="badge" :class="toolState(tool, review.server.quarantined) === 'blocked' ? 'badge-error badge-outline' : 'badge-success badge-outline'" :data-test="`review-tool-state-${tool.name}`">{{ toolState(tool, review.server.quarantined) === 'blocked' ? 'Blocked' : 'Approved' }}</span>
          </div>
          <ToolDefinitionText :text="tool.description" />
          <details v-if="tool.annotations || tool.input_schema || tool.output_schema"><summary class="cursor-pointer text-sm">Definition</summary><pre class="mt-2 text-xs whitespace-pre-wrap break-words" v-text="definitionText(tool)"></pre></details>
          <details v-if="tool.diff || tool.previous" open data-test="review-tool-diff"><summary class="cursor-pointer text-sm font-medium">Changed definition</summary><pre class="mt-2 text-xs whitespace-pre-wrap break-words" v-text="diffText(tool)"></pre></details>
        </div>
      </article>

      <nav v-if="filteredTools.length > 0 && (totalPages > 1 || filteredTools.length > REVIEW_PAGE_SIZES[0])" class="flex flex-wrap items-center justify-between gap-2" aria-label="Review pages" data-test="review-pager">
        <div class="join">
          <button type="button" class="btn btn-sm join-item" :disabled="currentPage <= 1" aria-label="First page" data-test="review-page-first" @click="page = 1">«</button>
          <button type="button" class="btn btn-sm join-item" :disabled="currentPage <= 1" aria-label="Previous page" data-test="review-page-prev" @click="page = currentPage - 1">‹</button>
          <span class="btn btn-sm join-item pointer-events-none" data-test="review-page-label">Page {{ currentPage }} of {{ totalPages }}</span>
          <button type="button" class="btn btn-sm join-item" :disabled="currentPage >= totalPages" aria-label="Next page" data-test="review-page-next" @click="page = currentPage + 1">›</button>
          <button type="button" class="btn btn-sm join-item" :disabled="currentPage >= totalPages" aria-label="Last page" data-test="review-page-last" @click="page = totalPages">»</button>
        </div>
        <label class="flex items-center gap-2 text-sm">Rows per page
          <select v-model.number="pageSize" class="select select-bordered select-sm" aria-label="Rows per page" data-test="review-page-size">
            <option v-for="size in REVIEW_PAGE_SIZES" :key="size" :value="size">{{ size }}</option>
          </select>
        </label>
      </nav>

      <div v-if="review.server.quarantined" class="sticky bottom-0 z-10 -mx-4 sm:-mx-6 border-t border-base-300 bg-base-100 px-4 sm:px-6 py-2 space-y-2" data-test="review-decision-bar">
        <p class="text-sm" data-test="review-decision-summary" aria-live="polite"><span class="font-medium" data-test="review-allowed-count">Allowed {{ allowedTools.length }}</span> · <span class="font-medium" data-test="review-blocked-count">Blocked {{ blockedCount }}</span> of {{ review.tools.length }}</p>
        <p v-if="notice" class="text-sm text-warning" role="status" data-test="review-stale-force-notice">{{ notice }}</p>
        <div class="flex flex-wrap gap-2">
          <button class="btn btn-primary btn-sm sm:btn-md" :disabled="approving || refreshing" data-test="review-approve-server" @click="requestApprove(false)">{{ primaryLabel }}</button>
          <button v-if="showApproveAll" class="btn btn-outline btn-sm sm:btn-md" :disabled="approving || refreshing" :title="APPROVE_ALL_HINT" data-test="review-approve-all" @click="requestApprove(true)">{{ approveAllLabel(review.tools.length) }}</button>
          <button class="btn btn-outline btn-error btn-sm sm:btn-md" :disabled="approving" @click="rejectServer">Reject server</button>
        </div>
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
import { APPROVE_ALL_HINT, REVIEW_PAGE_SIZES, REVIEW_SELECTION_HINT, approveAllLabel, approveLabel, clampPage, filterReviewTools, formatLaunchCommand, hasActiveFilters, mergeSelection, reviewHeadline, scanBanner, toolState, type SelectionChoice } from '@/utils/reviewPresentation'

const props = defineProps<{ serverName: string; change?: string }>()
const emit = defineEmits<{ approved: []; refreshed: [] }>()
const review = ref<ServerReviewResponse | null>(null)
// refreshing: a load is in flight. The list on screen may predate a tool the server just added, and the
// block list is the complement of what is on screen, so approval waits for the newest load to settle.
const STALE_FORCE_NOTICE = 'The review changed after the approval attempt. Review the updated list and approve again.'
const notice = ref('')
const loading = ref(false); const refreshing = ref(false); const scanning = ref(false); const approving = ref(false); const error = ref('')
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
// Filters and paging decide which rows are drawn. allowedTools and the block list
// in approve() always come from the FULL review.tools, never from these.
// The route's ?change= restriction is just the initial value of the visible state dropdown, so All states / Clear filters really clear it.
function initialStateFilter(c?: string) { return c && ['pending', 'changed', 'approved'].includes(c) ? c : 'all' }
const searchQuery = ref(''); const tierFilter = ref('all'); const stateFilter = ref(initialStateFilter(props.change)); const selectionFilter = ref('all')
const page = ref(1); const pageSize = ref<number>(REVIEW_PAGE_SIZES[0])
const filters = computed(() => ({ query: searchQuery.value, tier: tierFilter.value, state: stateFilter.value, selection: selectionFilter.value }))
const filtersActive = computed(() => hasActiveFilters(filters.value))
const allowedSet = computed(() => new Set(allowedTools.value))
const filteredTools = computed(() => filterReviewTools(review.value?.tools ?? [], filters.value, allowedSet.value))
const totalPages = computed(() => Math.max(1, Math.ceil(filteredTools.value.length / pageSize.value)))
const currentPage = computed(() => clampPage(page.value, filteredTools.value.length, pageSize.value))
const pagedTools = computed(() => filteredTools.value.slice((currentPage.value - 1) * pageSize.value, currentPage.value * pageSize.value))
const blockedCount = computed(() => (review.value?.tools.length ?? 0) - allowedTools.value.length)
const rangeText = computed(() => {
  const n = filteredTools.value.length; const total = review.value?.tools.length ?? 0
  if (n === 0) return `Showing 0 of ${total}`
  const from = (currentPage.value - 1) * pageSize.value + 1; const to = Math.min(n, currentPage.value * pageSize.value)
  return `Showing ${from}\u2013${to} of ${n}${n !== total ? ` (filtered from ${total})` : ''}`
})
const launchLine = computed(() => review.value?.server.command ? formatLaunchCommand(review.value.server.command, review.value.server.args ?? []) : '')
const copied = ref(false)
async function copyLaunchLine() {
  const text = launchLine.value
  try { await navigator.clipboard.writeText(text) } catch {
    const ta = document.createElement('textarea'); ta.value = text; ta.style.position = 'fixed'; ta.style.opacity = '0'; document.body.appendChild(ta); ta.select()
    try { document.execCommand('copy') } finally { document.body.removeChild(ta) }
  }
  copied.value = true; setTimeout(() => { copied.value = false }, 1500)
}
function clearFilters() { searchQuery.value = ''; tierFilter.value = 'all'; stateFilter.value = 'all'; selectionFilter.value = 'all'; page.value = 1 }
// Bulk actions touch exactly the filtered set (all pages); everything else keeps its decision.
function bulkSet(allow: boolean) {
  if (!review.value || !review.value.server.quarantined) return
  const names = new Set(allowedTools.value)
  for (const tool of filteredTools.value) {
    if (allow) names.add(tool.name); else names.delete(tool.name)
    recordChoice(tool, allow)
  }
  allowedTools.value = review.value.tools.filter(t => names.has(t.name)).map(t => t.name)
}
watch(() => props.change, c => { stateFilter.value = initialStateFilter(c) })
watch([searchQuery, tierFilter, stateFilter, selectionFilter, pageSize], () => { page.value = 1 })
const primaryLabel = computed(() => approveLabel(allowedTools.value.length, review.value?.tools.length ?? 0, review.value?.server.definitions_captured ?? false))
const showApproveAll = computed(() => !!review.value && review.value.server.quarantined && review.value.server.definitions_captured && review.value.tools.length > 0 && allowedTools.value.length < review.value.tools.length)
const tierCounts = computed(() => Object.fromEntries(tiers.map(t => [t, (review.value?.tools ?? []).filter(x => x.tier === t).length])))
// An explicit click is remembered with the payload the user saw, so a reload keeps it (D43.4).
function recordChoice(tool: ReviewTool, allowed: boolean) { choices.set(tool.name, { allowed, tool }) }
function definitionText(tool: ReviewTool) { return JSON.stringify({ input_schema: tool.input_schema, output_schema: tool.output_schema, annotations: tool.annotations }, null, 2) }
function diffText(tool: ReviewTool) { return Object.values(tool.diff ?? {}).filter(Boolean).join('\n\n') || JSON.stringify(tool.previous, null, 2) }
// A force retry is bound to the snapshot its failed attempt reviewed. Once the review is replaced the block list no longer describes it, so the retry is dropped and a fresh approval decision is required.
function invalidateForceRetry() { if (lastBlock.value === null) return; lastBlock.value = null; if (forceDialog.value?.open) { forceDialog.value.close?.(); notice.value = STALE_FORCE_NOTICE } }
// Each load takes a generation; only the newest response may replace the review, so an older refresh that resolves late cannot resurrect an allow the newer definitions invalidated.
let loadGeneration = 0
// Each visit (a serverName change) takes a generation too: a response that started in an earlier visit is dropped even when the screen is back on the same server name (A -> B -> A).
let visitGeneration = 0
async function load() { if (typeof api.getServerReview !== 'function') return; const server = props.serverName; const generation = ++loadGeneration; if (!review.value) loading.value = true; refreshing.value = true; invalidateForceRetry(); error.value = ''; const res = await api.getServerReview(server); if (server !== props.serverName || generation !== loadGeneration) return; loading.value = false; refreshing.value = false; if (!res.success || !res.data) { error.value = res.error || 'Failed to load review'; return }; review.value = res.data; allowedTools.value = mergeSelection(res.data.tools, choices); invalidateForceRetry(); emit('refreshed') }
async function rescan() {
  const server = props.serverName
  const visit = visitGeneration
  rescanning.value = true
  const res = await api.startScan(server)
  if (server !== props.serverName || visit !== visitGeneration) return // late response for a server the screen no longer shows
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
function requestApprove(everything: boolean) { if (refreshing.value) return; if (!review.value?.server.definitions_captured) { confirmOpen.value = true; confirmDialog.value?.showModal(); return }; void approve(false, everything ? [] : undefined) }
function closeConfirm() { confirmOpen.value = false; confirmDialog.value?.close?.() }
// The force retry re-sends the block list of the attempt that triggered it (D43.5).
async function approve(force: boolean, block?: string[]) {
  if (refreshing.value) return // never derive a block list from a list a refresh is about to replace
  if (force && !lastBlock.value) { notice.value = STALE_FORCE_NOTICE; return } // stale force retry: needs a fresh decision
  const server = props.serverName // a response for a server the screen no longer shows is dropped below
  const visit = visitGeneration
  closeConfirm(); forceDialog.value?.close?.(); approving.value = true; notice.value = ''
  const all = review.value?.tools.map(t => t.name) ?? []
  const blocked = block ?? (force && lastBlock.value ? lastBlock.value : all.filter(name => !allowedTools.value.includes(name)))
  lastBlock.value = blocked
  const res = await api.securityApprove(server, force, blocked)
  if (server !== props.serverName || visit !== visitGeneration) return // the watcher on serverName already reset approving and the force state
  approving.value = false
  if (!res.success) {
    const message = res.error || 'Approval failed'
    // A refresh that began while this attempt was in flight dropped its block list (lastBlock was cleared): the force retry is bound to a snapshot that is gone, so do not offer it and do not replace the refreshed review with an error panel; show the stale notice beside the fresh approval controls instead.
    if (!force && /dangerous/i.test(message) && lastBlock.value === null) { notice.value = STALE_FORCE_NOTICE; return }
    error.value = message
    if (!force && /dangerous/i.test(message)) forceDialog.value?.showModal?.()
    return
  }
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
watch(() => props.serverName, () => { visitGeneration++; choices.clear(); review.value = null; allowedTools.value = []; clearFilters(); stateFilter.value = initialStateFilter(props.change); lastBlock.value = null; notice.value = ''; approving.value = false; refreshing.value = false; forceDialog.value?.close?.(); closeConfirm(); scanning.value = false; rescanning.value = false; error.value = ''; void load() })
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
