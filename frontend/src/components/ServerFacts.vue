<template>
  <!-- Spec 134 PR 2 (FR-020): the facts the Servers card used to carry and the
       row no longer does, on the first screen of server detail. Small and
       self-contained on purpose: PR 3 restructures this page and moves this
       block into its summary. -->
  <section class="server-facts flex flex-wrap items-start gap-x-6 gap-y-2 text-sm" aria-label="Server facts" data-test="server-facts">
    <div v-if="showId" data-test="server-facts-id">
      <span class="text-base-content/60">ID</span> <code class="text-xs break-all">{{ server.name }}</code>
    </div>

    <div class="flex items-center gap-1.5" data-test="server-facts-trust">
      <span class="text-base-content/60">Trust mode</span>
      <!-- Effective mode; an unrecognised configured value shows the fail-closed
           mode with its raw value in the title (spec 088 FR-007). -->
      <span
        :class="['inline-flex items-center gap-1 tooltip tooltip-bottom', trust.tone]"
        :data-tip="trust.title"
        :title="trust.title"
        role="img"
        :aria-label="trust.title"
        :data-trust-invalid="trust.invalid ? 'true' : undefined"
        data-test="server-trust-mode"
      >
        <svg class="w-4 h-4" fill="currentColor" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M12 2L3.5 6.5V11c0 5.55 3.84 10.74 8.5 12 4.66-1.26 8.5-6.45 8.5-12V6.5L12 2zm0 2.18l6.5 3.35V11c0 4.52-3.15 8.76-6.5 9.93C8.65 19.76 5.5 15.52 5.5 11V7.53L12 4.18z"/>
        </svg>
        <span data-test="server-facts-trust-label">{{ trust.label }}{{ trust.invalid ? ' (configured value not recognised)' : '' }}</span>
      </span>
    </div>

    <div v-if="security" data-test="server-facts-security">
      <span class="text-base-content/60">Scan</span>{{ ' ' }}<span :class="[securityToneClass]" :data-test="server.quarantined ? 'server-card-quarantine-scan-note' : 'security-scan-badge'">{{ security }}</span>
    </div>

    <div v-if="held && !server.quarantined" class="text-warning" data-test="server-facts-held">
      <span data-test="server-card-tool-quarantine-note">{{ held }}</span>
    </div>

    <details v-if="hasDiagnostic" class="w-full" data-test="server-facts-diagnostic" @toggle="diagOpen = ($event.target as HTMLDetailsElement).open">
      <summary class="cursor-pointer link link-primary min-h-11 md:min-h-0 inline-flex items-center">Full diagnostic</summary>
      <dl v-if="diagOpen" class="mt-2 space-y-2 [overflow-wrap:anywhere]">
        <div v-if="server.health?.detail"><dt class="text-base-content/60">Health detail</dt><dd data-test="server-facts-health-detail">{{ server.health.detail }}</dd></div>
        <div v-if="server.diagnostic?.user_message"><dt class="text-base-content/60">Diagnostic</dt><dd data-test="server-facts-diagnostic-message">{{ server.diagnostic.user_message }}<span v-if="server.diagnostic.code" class="text-base-content/60"> ({{ server.diagnostic.code }})</span></dd></div>
        <div v-if="server.diagnostic?.cause"><dt class="text-base-content/60">Cause</dt><dd>{{ server.diagnostic.cause }}</dd></div>
        <div v-if="server.last_error"><dt class="text-base-content/60">Raw error</dt><dd><pre class="whitespace-pre-wrap text-xs" data-test="server-facts-raw-error">{{ server.last_error }}</pre></dd></div>
      </dl>
    </details>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Server } from '@/types'
import { serverDisplayName } from '@/utils/serverRoute'
import { securityLine, securityTone, toolQuarantineSummary, trustBadge } from '@/utils/serverFacts'

const props = defineProps<{ server: Server }>()

// The text is rendered only while the control is open: a collapsed fault in the
// DOM would read as a second, louder copy of the calm quarantine banner (#1076).
const diagOpen = ref(false)

// The id is shown only when the display title hides it.
const showId = computed(() => serverDisplayName(props.server) !== props.server.name)
const trust = computed(() => trustBadge(props.server))
const security = computed(() => securityLine(props.server))
const securityToneClass = computed(() => securityTone(props.server))
const held = computed(() => toolQuarantineSummary(props.server))
const hasDiagnostic = computed(() =>
  !!(props.server.health?.detail || props.server.last_error || props.server.diagnostic?.user_message || props.server.diagnostic?.cause))
</script>
