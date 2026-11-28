<template>
  <section class="space-y-2 rounded border bg-base-100 p-3" style="border-color: var(--ds-border)" aria-label="Server facts" data-test="server-facts" :data-has-diagnostic="hasDiagnostic ? 'true' : 'false'">
    <dl class="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-3 gap-x-6 gap-y-2 text-sm">
      <div class="flex items-center gap-1.5 min-w-0" data-test="fact-identity">
        <dt class="text-base-content/70">Id</dt>
        <dd class="font-mono break-all" data-test="fact-id">{{ server.name }}</dd>
        <template v-if="titleDiffers">
          <dt class="text-base-content/70 ml-2">Title</dt>
          <dd data-test="fact-title">{{ server.title }}</dd>
        </template>
      </div>
      <div class="flex items-center gap-1.5" data-test="fact-transport">
        <dt class="text-base-content/70">Transport</dt>
        <dd>{{ server.protocol }}<span class="text-base-content/70">, {{ connectionText }}</span></dd>
      </div>
      <div v-if="adminState" class="flex items-center gap-1.5" data-test="fact-admin-state">
        <dt class="text-base-content/70">State</dt>
        <dd><span class="font-medium" :class="adminTone" data-test="server-admin-state">{{ adminState }}</span><span class="text-base-content/70">, <span data-test="server-admin-state-desc">{{ adminStateDesc }}</span></span></dd>
      </div>
      <div v-if="toolCount !== undefined" class="flex items-center gap-1.5" data-test="fact-tools">
        <dt class="text-base-content/70">Tools</dt>
        <dd data-test="server-tools-count"><span>{{ toolCount }} available</span><span v-if="(disabledTools ?? 0) > 0" class="text-error">, {{ disabledTools }} disabled</span></dd>
      </div>
      <div class="flex flex-wrap items-center gap-x-1.5 gap-y-0.5 col-span-full" data-test="fact-trust">
        <dt class="text-base-content/70">Trust mode</dt>
        <dd class="flex items-center gap-1" :title="trust.title">
          <span role="img" :class="['tooltip tooltip-bottom', trust.tone]" :data-tip="trust.title" :title="trust.title" :aria-label="trust.title" data-test="server-trust-mode" :data-trust-invalid="trust.invalid ? 'true' : undefined"><UiIcon name="shield" class="w-4 h-4" /></span>
          <span data-test="server-facts-trust-label">{{ trust.label }}{{ trust.invalid ? ' (configured value not recognised)' : '' }}</span>
        </dd>
        <dd class="text-base-content/70 w-full" data-test="server-facts-trust-detail">{{ trust.detail }}</dd>
      </div>
      <div v-if="security" class="flex items-center gap-1.5" data-test="fact-scan">
        <span data-test="server-facts-security" class="flex items-center gap-1.5"><dt class="text-base-content/70">Scan</dt>{{ ' ' }}<dd><span :class="securityToneClass" :data-test="server.quarantined ? 'server-card-quarantine-scan-note' : 'security-scan-badge'">{{ security }}</span></dd></span>
      </div>
      <div class="flex items-center gap-1.5" data-test="fact-held">
        <dt class="text-base-content/70">Held tools</dt>
        <dd data-test="fact-held-counts">
          <template v-if="held.total > 0"><span :data-test="server.quarantined ? undefined : 'server-facts-held'">{{ held.pending }} pending, {{ held.changed }} changed</span></template>
          <template v-else>none</template>
        </dd>
      </div>
      <div class="flex items-center gap-1.5" data-test="fact-activity">
        <dt class="sr-only">Activity</dt>
        <dd><router-link :to="activityLink" class="link link-hover" data-test="fact-activity-link">{{ activityText }}</router-link></dd>
      </div>
      <div v-if="hasDiagnostic" class="flex items-center">
        <button type="button" class="btn btn-sm btn-outline min-h-11" data-test="open-diagnostic" aria-haspopup="dialog" @click="openDiagnostic">Full diagnostic</button>
      </div>
    </dl>

    <!-- The launch line (or URL) on its own row: monospace, wraps cleanly, with Copy. -->
    <div v-if="launchLine" class="flex flex-wrap items-start gap-2 border-t pt-2 text-sm" style="border-color: var(--ds-border)" data-test="fact-launch">
      <span class="text-base-content/70 shrink-0">{{ server.command ? 'Launch line' : 'URL' }}</span>
      <code class="min-w-0 flex-1 basis-60 break-all whitespace-pre-wrap font-mono text-xs bg-base-200 rounded px-2 py-1" data-test="fact-endpoint">{{ launchLine }}</code>
      <button type="button" class="btn btn-xs min-h-11 sm:min-h-0" data-test="fact-launch-copy" @click="copyLaunch">{{ launchCopied ? 'Copied' : 'Copy' }}</button>
    </div>

    <ul v-if="lines.length" class="space-y-1 border-t pt-2" style="border-color: var(--ds-border)" data-test="attention-lines">
      <li v-for="line in lines" :key="line.id">
        <button type="button" class="link link-primary text-left text-sm min-h-11 sm:min-h-0" :data-test="`attention-${line.id}`" @click="$emit('go', line.id)">{{ line.text }}</button>
      </li>
    </ul>

    <dialog ref="dialogEl" class="modal" data-test="diagnostic-dialog" aria-labelledby="diag-title" @close="onClose">
      <div v-if="isOpen" class="modal-box max-w-3xl">
        <h3 id="diag-title" class="font-bold text-lg">Full diagnostic for {{ server.name }}</h3>
        <p v-if="sections.length === 0" class="py-3 text-base-content/70" data-test="diagnostic-empty">This server reports no diagnostic and no error.</p>
        <dl v-else class="py-3 space-y-3 text-sm">
          <div v-for="s in sections" :key="s.label" class="min-w-0">
            <dt class="font-medium">{{ s.label }}</dt>
            <dd><pre class="whitespace-pre-wrap break-words font-mono text-xs bg-base-200 rounded p-2 overflow-x-auto" data-test="diagnostic-value">{{ s.value }}</pre></dd>
          </div>
        </dl>
        <div class="modal-action">
          <button v-if="sections.length" type="button" class="btn min-h-11" data-test="diagnostic-copy" @click="copy">{{ copied ? 'Copied' : 'Copy' }}</button>
          <button type="button" class="btn min-h-11" data-test="diagnostic-close" @click="closeDiagnostic">Close</button>
        </div>
      </div>
    </dialog>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import api from '@/services/api'
import { formatLaunchCommand } from '@/utils/reviewPresentation'
import type { Server } from '@/types'
import UiIcon from '@/components/UiIcon.vue'
import { attentionLines, diagnosticSections, heldCounts, type AttentionTarget } from '@/utils/serverDetailFacts'
import { securityLine, securityTone, trustBadge } from '@/utils/serverFacts'

const props = defineProps<{ server: Server; primaryAction?: string; primaryLabel?: string; adminState?: string; adminStateDesc?: string; adminTone?: string; toolCount?: number; disabledTools?: number }>()
defineEmits<{ go: [target: AttentionTarget] }>()

const dialogEl = ref<HTMLDialogElement | null>(null)
const copied = ref(false)
const isOpen = ref(false)
let opener: HTMLElement | null = null

const titleDiffers = computed(() => !!props.server.title && props.server.title !== props.server.name)
const trust = computed(() => trustBadge(props.server))
const security = computed(() => securityLine(props.server))
const securityToneClass = computed(() => securityTone(props.server))
const held = computed(() => heldCounts(props.server))
const sections = computed(() => diagnosticSections(props.server))
// The dialog is worth opening only when it holds more than the one-line summary the page header already shows
// (a healthy server's whole diagnostic is "Connected (15 tools)").
const hasDiagnostic = computed(() => sections.value.some(section => section.label !== 'Summary'))
const lines = computed(() => attentionLines(props.server, { primaryAction: props.primaryAction, primaryLabel: props.primaryLabel }))
const connectionText = computed(() => {
  const s = props.server
  if (s.enabled === false) return 'not running (disabled)'
  if (s.connected) return 'connected'
  if (s.connecting) return 'connecting'
  return 'not connected'
})
const launchLine = computed(() => props.server.command ? formatLaunchCommand(props.server.command, props.server.args ?? []) : (props.server.url || ''))
const launchCopied = ref(false)
async function copyLaunch() {
  try { await navigator.clipboard.writeText(launchLine.value) } catch { /* the line is selectable */ }
  launchCopied.value = true
  setTimeout(() => { launchCopied.value = false }, 1500)
}

// The 24 h numbers come from the same summary the Servers list reads; until they load the link says so plainly.
const calls24h = ref<{ calls: number; errors: number } | null>(null)
let statsSeq = 0
async function loadActivityStats() {
  const seq = ++statsSeq
  calls24h.value = null
  if (typeof api.getActivitySummary !== 'function') return
  try {
    const res = await api.getActivitySummary('24h')
    if (seq !== statsSeq || !res?.success) return
    const entry = (res.data?.per_server ?? []).find(e => e.name === props.server.name)
    calls24h.value = { calls: entry?.calls ?? 0, errors: entry?.errors ?? 0 }
  } catch { /* the fallback label stays */ }
}
onMounted(loadActivityStats)
watch(() => props.server.name, loadActivityStats)
const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? '' : 's'}`
const activityText = computed(() => calls24h.value ? `Last 24 h: ${plural(calls24h.value.calls, 'call')}, ${plural(calls24h.value.errors, 'error')}` : 'Calls in Activity')
const activityLink = computed(() => {
  const base = `/activity?server=${encodeURIComponent(props.server.name)}&from=-24h`
  return (calls24h.value?.errors ?? 0) > 0 ? `${base}&status=error` : base
})

// The text is rendered only while the dialog is open, so a closed dialog adds nothing to the page.
async function openDiagnostic(e: Event) {
  opener = e.currentTarget as HTMLElement
  isOpen.value = true
  await nextTick()
  dialogEl.value?.showModal?.()
}
function closeDiagnostic() { dialogEl.value?.close?.(); onClose() }
function onClose() { isOpen.value = false; opener?.focus?.() }
async function copy() {
  const text = sections.value.map(s => `${s.label}: ${s.value}`).join('\n')
  try { await navigator.clipboard.writeText(text) } catch { /* the text is selectable in the dialog */ }
  copied.value = true
  setTimeout(() => { copied.value = false }, 1500)
}
</script>
