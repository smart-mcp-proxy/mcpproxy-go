<template>
  <div
    v-if="evidence"
    class="flex items-center gap-1 mt-1 text-xs whitespace-nowrap"
    :class="evidence.toneClass"
    :title="evidence.description"
    data-test="tool-hold-evidence"
  >
    <UiIcon :name="evidence.icon" class="w-3 h-3" />
    <span v-if="evidence.verdict" class="badge badge-xs" :class="evidence.verdict.badgeClass" data-test="tool-hold-verdict">
      {{ evidence.verdict.label }}
    </span>
    <!-- The reason reads inline when no signal chips crowd it out; otherwise it
         stays available to screen readers. -->
    <span :class="evidence.signals.length ? 'sr-only' : ''">{{ evidence.label }}</span>
    <span
      v-for="signal in evidence.signals"
      :key="signal.raw"
      class="badge badge-xs"
      :class="evidence.chipClass"
      :title="signal.raw"
      data-test="tool-hold-signal"
    >{{ signal.label }}</span>
    <span
      v-if="evidence.collapsedCount > 0"
      class="opacity-70"
      :title="`${evidence.collapsedCount} more matched signal(s)`"
      data-test="tool-hold-signal-more"
    >+{{ evidence.collapsedCount }}</span>
  </div>
  <!-- Details: the reason and the raw signal ids as text, not only as hover titles. -->
  <p v-if="evidence && expanded" class="text-xs text-base-content/70 mt-1 break-words" data-test="tool-hold-description">
    {{ evidence.description }}<template v-if="evidence.signals.length"> Matched: {{ evidence.signals.map(x => x.raw).join(', ') }}<template v-if="evidence.collapsedCount > 0"> and {{ evidence.collapsedCount }} more</template>.</template>
  </p>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import UiIcon from '@/components/UiIcon.vue'
import type { UiIconName } from '@/utils/uiIcons'
import type { GlobalTool } from '@/types/api'
import { parseHoldEvidence, displaySignals, reasonPresentation, verdictPresentation } from '@/utils/holdEvidence'

// Spec 088 FR-008/FR-009/FR-012, moved out of Tools.vue (Spec 134 PR 6) so the
// flat table and the Details dialog print the same compact evidence. This page
// is a cross-server list, so the evidence stays COMPACT: the reason icon carries
// the threat-vs-precaution distinction, plus the TPA signature ids and an
// overflow count. Compact cap: TPA ids are never collapsed by it.
const COMPACT_SIGNAL_CAP = 1

const props = defineProps<{ tool: GlobalTool; expanded?: boolean }>()

const evidence = computed(() => {
  const tool = props.tool
  // FR-012: only a tool still awaiting a decision may show hold evidence, so an
  // approved/released record never renders stale findings.
  if (tool.approval_status !== 'pending' && tool.approval_status !== 'changed') return null
  const parsed = parseHoldEvidence(tool)
  if (!parsed) return null
  const reason = reasonPresentation(parsed)
  if (!reason) return null
  const { visible, collapsedCount } = displaySignals(parsed, COMPACT_SIGNAL_CAP)
  const v = verdictPresentation(parsed.verdict)
  return {
    icon: (reason.tone === 'threat' ? 'alert' : reason.tone === 'precaution' ? 'shield' : 'info') as UiIconName,
    label: reason.label,
    description: reason.description,
    toneClass: reason.tone === 'threat' ? 'text-error' : reason.tone === 'precaution' ? 'text-warning' : 'text-base-content/70',
    chipClass: reason.tone === 'threat' ? 'badge-error' : reason.tone === 'precaution' ? 'badge-warning' : 'badge-ghost',
    signals: visible.map(s => ({ label: s.label, raw: s.raw })),
    collapsedCount,
    // FR-009: verdict severity must be visible, or a warnings hold and a
    // dangerous hold with the same signals would look identical.
    verdict: v ? { label: v.label, badgeClass: v.tone === 'danger' ? 'badge-error' : v.tone === 'warning' ? 'badge-warning' : 'badge-ghost' } : null,
  }
})
</script>
