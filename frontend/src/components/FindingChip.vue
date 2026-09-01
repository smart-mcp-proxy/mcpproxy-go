<template>
  <!-- An UNSCANNED tool renders nothing at all. It must never be styled as a
       safe tool, and a "not scanned" nag on every card is the fastest way to
       train an operator to ignore this chip. Absence is the honest state. -->
  <span
    v-if="presentation"
    :data-test="`finding-chip-${state}`"
    class="badge badge-sm gap-1 shrink-0"
    :class="presentation.badgeClass"
    :title="presentation.title"
  >
    <span v-if="state === 'scanning'" class="loading loading-spinner loading-xs" aria-hidden="true"></span>
    <span v-else aria-hidden="true">{{ presentation.icon }}</span>
    <span>{{ presentation.label }}</span>
    <span v-if="stale && state !== 'stale'" aria-hidden="true" title="Findings may predate the current description">⟳</span>
  </span>
</template>

<script lang="ts">
/**
 * Compact per-tool scan status, following the HoldEvidenceBadge house pattern
 * (badge + tone + glyph + explanatory title).
 *
 * Two invariants govern the tones, and they are the whole reason this is a
 * component rather than an inline ternary:
 *
 *   * an unscanned tool is never styled as a safe tool — it renders nothing;
 *   * a FAILED scan is never styled as a threat. A scan that could not finish is
 *     a coverage precaution, not a verdict, so it takes the same neutral tone
 *     `holdEvidence.reasonPresentation()` gives HOLD_REASON_SCAN_COVERAGE and
 *     must never render red.
 *
 * Declared in a plain <script> block so other modules can import the union;
 * <script setup> is not an export surface.
 */
export type FindingChipState =
  | 'dangerous'
  | 'warning'
  | 'stale'
  | 'scanning'
  | 'failed'
  | 'absent'
</script>

<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    state?: FindingChipState
    /** Finding count, when known — rendered as "N findings" for exactness. */
    count?: number
    /** Findings may predate the current tool description. */
    stale?: boolean
  }>(),
  { state: 'absent', count: 0, stale: false },
)

interface ChipPresentation {
  label: string
  icon: string
  badgeClass: string
  title: string
}

const state = computed(() => props.state)

const presentation = computed<ChipPresentation | null>(() => {
  const n = props.count
  const plural = n === 1 ? 'finding' : 'findings'

  switch (props.state) {
    case 'dangerous':
      return {
        label: n > 0 ? `${n} ${plural}` : 'Flagged',
        icon: '▲',
        badgeClass: 'badge-error',
        title:
          'The security scan flagged this tool description with a hard-tier signal. Informational — it does not block the tool.',
      }
    case 'warning':
      return {
        label: n > 0 ? `${n} ${plural}` : 'Review',
        icon: '▮',
        badgeClass: 'badge-warning badge-outline',
        title:
          'The security scan raised a review-only (soft-tier) signal on this tool description.',
      }
    case 'stale':
      return {
        label: 'Findings outdated',
        icon: '⟳',
        badgeClass: 'badge-ghost',
        title:
          'This description changed since the last scan, so the recorded findings may no longer apply. Rescan to refresh them.',
      }
    case 'scanning':
      return {
        label: 'Scanning…',
        icon: '',
        badgeClass: 'badge-ghost',
        title: 'A security scan is running for this server.',
      }
    case 'failed':
      // Precaution tone — deliberately NOT badge-error.
      return {
        label: 'Scan could not complete',
        icon: '🛡️',
        badgeClass: 'badge-warning badge-outline',
        title:
          'The security scan could not be completed for this tool. This is not a threat verdict — retry the scan or review the description manually.',
      }
    default:
      return null
  }
})
</script>
