<template>
  <!-- Nothing at all when the record carries no hold evidence: approved/released
       tools and pre-086 records must render exactly as before (FR-012). -->
  <div
    v-if="evidence && reason"
    data-test="hold-evidence-badge"
    class="flex flex-wrap items-center gap-1.5"
  >
    <!-- Hold reason. A coverage hold is a PRECAUTION (the scan could not run),
         never a threat verdict — hence the deliberately different tone. -->
    <span
      data-test="hold-reason-pill"
      class="badge badge-sm gap-1 cursor-help"
      :class="reasonClass"
      :title="reason.description"
    >
      <span aria-hidden="true">{{ reasonIcon }}</span>
      <span>{{ reason.label }}</span>
    </span>

    <!-- Verdict at hold time, shown as received. -->
    <span
      v-if="verdict"
      data-test="hold-verdict-badge"
      class="badge badge-sm badge-outline"
      :class="verdictClass"
      title="Scan verdict recorded when this tool change was held"
    >{{ verdict.label }}</span>

    <!-- Matched signature ids: TPA signatures first and never collapsed. -->
    <template v-for="chip in chips.visible" :key="chip.raw">
      <span
        v-if="chip.kind === 'tpa'"
        data-test="hold-tpa-chip"
        class="badge badge-sm badge-error badge-outline font-mono cursor-help"
        :title="chip.raw"
      >{{ chip.label }}</span>
      <span
        v-else
        data-test="hold-heuristic-chip"
        class="badge badge-sm badge-ghost font-mono cursor-help"
        :title="chip.raw"
      >{{ chip.label }}</span>
    </template>

    <!-- Counts ONLY received-but-collapsed signals — never a claim beyond the
         delivered (already capped) list. -->
    <span
      v-if="chips.collapsedCount > 0"
      data-test="hold-signal-overflow"
      class="text-xs text-base-content/60"
      :title="collapsedTitle"
    >+{{ chips.collapsedCount }} more</span>

    <!-- Best-effort link to the server's latest scan report. The query carries
         the FULL RAW signal strings so ScanReport.vue can intersect them with
         findings[].signals exactly; display labels shorten, the query never
         does (research D4). No report path → no link; the caller renders its
         own "Run scan" CTA instead (FR-011). -->
    <router-link
      v-if="reportLink"
      :to="reportLink"
      data-test="hold-evidence-report-link"
      class="link link-primary text-xs"
      title="Open the server's most recent scan report (best-effort match on the matched signals)"
    >View scan report</router-link>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import {
  parseHoldEvidence,
  displaySignals,
  reasonPresentation,
  verdictPresentation,
  type HoldEvidence,
  type HoldEvidenceFields,
} from '@/utils/holdEvidence'

/**
 * Accepts either the raw payload fields (any record carrying `held_*` — a
 * ToolApproval row, an inventory tool, a diff payload) or an already-parsed
 * HoldEvidence, so every surface can pass whatever it already holds.
 */
type HoldEvidenceInput = HoldEvidence | (HoldEvidenceFields & Record<string, unknown>)

const props = defineProps<{
  evidence?: HoldEvidenceInput | null
  /** Route path of the server's most recent scan report, when one exists. */
  reportPath?: string | null
}>()

function isParsed(value: HoldEvidenceInput): value is HoldEvidence {
  const candidate = value as HoldEvidence
  return typeof candidate.reason === 'string' && Array.isArray(candidate.tpa)
}

const evidence = computed<HoldEvidence | null>(() => {
  const source = props.evidence
  if (!source) return null
  if (isParsed(source)) return source.reason.trim() ? source : null
  return parseHoldEvidence(source)
})

const reason = computed(() => reasonPresentation(evidence.value))
const verdict = computed(() => verdictPresentation(evidence.value?.verdict))
const chips = computed(() => displaySignals(evidence.value))

const reasonClass = computed(() => {
  switch (reason.value?.tone) {
    case 'threat':
      return 'badge-error'
    case 'precaution':
      return 'badge-warning'
    default:
      return 'badge-ghost'
  }
})

const reasonIcon = computed(() => {
  switch (reason.value?.tone) {
    case 'threat':
      return '⚠️'
    case 'precaution':
      return '🛡️'
    default:
      return '⏸️'
  }
})

const verdictClass = computed(() => {
  switch (verdict.value?.tone) {
    case 'danger':
      return 'badge-error'
    case 'warning':
      return 'badge-warning'
    default:
      return 'badge-neutral'
  }
})

const collapsedTitle = computed(() =>
  evidence.value?.heuristics.slice(chips.value.visible.length - evidence.value.tpa.length).join('\n')
)

const reportLink = computed<string | null>(() => {
  const path = props.reportPath?.trim()
  if (!path || !evidence.value) return null

  const signals = evidence.value.signals
  if (signals.length === 0) return path

  const query = signals.map((signal) => `signal=${encodeURIComponent(signal)}`).join('&')
  return `${path}${path.includes('?') ? '&' : '?'}${query}`
})
</script>
