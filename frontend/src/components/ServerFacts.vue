<template>
  <section class="space-y-2 rounded border bg-base-100 p-3" style="border-color: var(--border)" aria-label="Server facts" data-test="server-facts">
    <dl class="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-3 gap-x-6 gap-y-2 text-sm">
      <div class="flex items-center gap-1.5 min-w-0" data-test="fact-identity">
        <dt class="text-base-content/70">Id</dt>
        <dd class="font-mono break-all" data-test="fact-id">{{ server.name }}</dd>
        <template v-if="titleDiffers">
          <dt class="text-base-content/70 ml-2">Title</dt>
          <dd data-test="fact-title">{{ server.title }}</dd>
        </template>
      </div>
      <div class="flex items-center gap-1.5 min-w-0" data-test="fact-transport">
        <dt class="text-base-content/70">Transport</dt>
        <dd class="min-w-0 break-all">{{ server.protocol }}<span class="text-base-content/70">, {{ connectionText }}</span>
          <code v-if="endpoint" class="ml-1 text-xs" data-test="fact-endpoint">{{ endpoint }}</code></dd>
      </div>
      <div v-if="adminState" class="flex items-center gap-1.5" data-test="fact-admin-state">
        <dt class="text-base-content/70">State</dt>
        <dd><span class="font-medium" :class="adminTone" data-test="server-admin-state">{{ adminState }}</span><span class="text-base-content/70">, <span data-test="server-admin-state-desc">{{ adminStateDesc }}</span></span></dd>
      </div>
      <div v-if="toolCount !== undefined" class="flex items-center gap-1.5" data-test="fact-tools">
        <dt class="text-base-content/70">Tools</dt>
        <dd data-test="server-tools-count"><span>{{ toolCount }} available</span><span v-if="(disabledTools ?? 0) > 0" class="text-error">, {{ disabledTools }} disabled</span></dd>
      </div>
      <div class="flex items-center gap-1.5" data-test="fact-trust">
        <dt class="text-base-content/70">Trust mode</dt>
        <dd class="flex items-center gap-1" :title="trust.title">
          <span role="img" :aria-label="trust.title" data-test="server-trust-mode" :data-trust-invalid="trust.invalid ? 'true' : undefined"><UiIcon name="shield" class="w-4 h-4" /></span>
          {{ trust.label }}<span v-if="trust.invalid" class="text-warning">(unrecognized value)</span>
        </dd>
      </div>
      <div v-if="scan" class="flex items-center gap-1.5" data-test="fact-scan">
        <dt class="text-base-content/70">Scan</dt>
        <dd><StateTag :tone="scan.tone">{{ scan.text }}</StateTag></dd>
      </div>
      <div class="flex items-center gap-1.5" data-test="fact-held">
        <dt class="text-base-content/70">Held tools</dt>
        <dd data-test="fact-held-counts">
          <template v-if="held.total > 0">{{ held.pending }} pending, {{ held.changed }} changed</template>
          <template v-else>none</template>
        </dd>
      </div>
      <div class="flex items-center gap-1.5" data-test="fact-activity">
        <dt class="text-base-content/70">Last 24 h</dt>
        <dd><router-link :to="activityLink" class="link link-hover" data-test="fact-activity-link">calls and errors in Activity</router-link></dd>
      </div>
      <div class="flex items-center">
        <button type="button" class="btn btn-sm btn-outline min-h-11" data-test="open-diagnostic" aria-haspopup="dialog" @click="openDiagnostic">Full diagnostic</button>
      </div>
    </dl>

    <ul v-if="lines.length" class="space-y-1 border-t pt-2" style="border-color: var(--border)" data-test="attention-lines">
      <li v-for="line in lines" :key="line.id">
        <button type="button" class="link link-hover text-left text-sm min-h-11 sm:min-h-0" :data-test="`attention-${line.id}`" @click="$emit('go', line.id)">{{ line.text }}</button>
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
import { computed, nextTick, ref } from 'vue'
import type { Server } from '@/types'
import StateTag from '@/components/StateTag.vue'
import UiIcon from '@/components/UiIcon.vue'
import { attentionLines, diagnosticSections, heldCounts, scanFact, trustFact, type AttentionTarget } from '@/utils/serverDetailFacts'

const props = defineProps<{ server: Server; primaryAction?: string; primaryLabel?: string; adminState?: string; adminStateDesc?: string; adminTone?: string; toolCount?: number; disabledTools?: number }>()
defineEmits<{ go: [target: AttentionTarget] }>()

const dialogEl = ref<HTMLDialogElement | null>(null)
const copied = ref(false)
const isOpen = ref(false)
let opener: HTMLElement | null = null

const titleDiffers = computed(() => !!props.server.title && props.server.title !== props.server.name)
const trust = computed(() => trustFact(props.server))
const scan = computed(() => scanFact(props.server))
const held = computed(() => heldCounts(props.server))
const sections = computed(() => diagnosticSections(props.server))
const lines = computed(() => attentionLines(props.server, { primaryAction: props.primaryAction, primaryLabel: props.primaryLabel }))
const connectionText = computed(() => {
  const s = props.server
  if (s.enabled === false) return 'not running (disabled)'
  if (s.connected) return 'connected'
  if (s.connecting) return 'connecting'
  return 'not connected'
})
const endpoint = computed(() => props.server.url || props.server.command || 'No endpoint')
const activityLink = computed(() => ({ path: '/activity', query: { server: props.server.name } }))

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
