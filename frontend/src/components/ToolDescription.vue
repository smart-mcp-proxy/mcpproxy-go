<template>
  <div data-test="tool-description">
    <!--
      SECURITY INVARIANT — do not add `v-html` to this component, ever.

      Tool descriptions are attacker-authored: they are the Tool Poisoning
      Attack payload itself. Every upstream character below reaches the DOM
      through `{{ }}` interpolation (or an attribute binding), which Vue escapes.
      A single v-html here would turn the TPA highlighter into the TPA delivery
      vehicle. The guard that survives a future edit is a TEST, not a lint rule:
      `frontend/tests/unit/tool-description-no-v-html.spec.ts` reads this file and
      fails on any `v-html`. It is a test rather than an ESLint override because
      `npm run lint` in this repo is a no-op echo and `eslint.config.cjs` is an
      eslintrc-shaped file that ESLint 10 does not read at all — a rule added
      there would enforce nothing while reading as if it did. See also design doc
      §3.4 and non-goal 10.
    -->
    <p
      class="text-sm text-base-content/70 mt-2 whitespace-pre-wrap break-words"
      data-test="tool-description-text"
    >
      <template v-if="!text">{{ emptyText }}</template>
      <template v-else>
        <template v-for="(part, i) in renderedParts" :key="i">
          <span v-if="part.kind === 'gap'" class="text-base-content/40" aria-label="text omitted">&nbsp;[…]&nbsp;</span>
          <span v-else-if="!part.segment.level">{{ part.segment.text }}</span>
          <!--
            Deliberately NOT interactive: no role, no tabindex, no key handlers.
            Phase 1 renders no findings list for a mark to move focus TO, and a
            control that announces "activate to show the finding" and then does
            nothing is worse than plain emphasis — it also consumed the Space key,
            so the page stopped scrolling while a mark had focus. Wire the
            affordance back on in the phase that ships the target.
          -->
          <mark
            v-else
            class="bg-transparent px-0.5 rounded-sm underline underline-offset-4 decoration-2"
            :class="markClass(part.segment)"
            :data-test="`tool-description-mark-${part.segment.level}`"
          >
            <!-- The severity/rule prefix is a visually-hidden SPAN, not an
                 aria-label: a label REPLACES an element's accessible name, so
                 labelling the mark would have hidden the flagged words themselves
                 from a screen reader — the one thing the mark exists to expose. -->
            <span class="sr-only">{{ markLabel(part.segment) }}</span>
            <!-- Severity is never colour alone: glyph + decoration style + colour. -->
            <span aria-hidden="true" class="font-bold mr-0.5">{{ markGlyph(part.segment) }}</span>
            <template v-for="(piece, j) in reveal(part.segment.text)" :key="j">
              <!-- A unicode.hidden mark has nothing to draw unless the smuggled
                   rune is given a visible body. The rune itself is NEVER removed. -->
              <code
                v-if="piece.hidden"
                class="badge badge-xs badge-error font-mono align-baseline mx-0.5"
                data-test="tool-description-invisible-rune"
                :title="`${piece.name} (U+${piece.hex})`"
              >U+{{ piece.hex }}</code>
              <template v-else>{{ piece.text }}</template>
            </template>
            <sup aria-hidden="true" class="ml-0.5 text-[10px] font-bold">{{ part.segment.sources[0].index }}</sup>
          </mark>
        </template>
      </template>
    </p>

    <!-- Fallback ladder. The description above is ALWAYS rendered in full text
         first; these notes only ever explain why it is not annotated. -->
    <p
      v-if="highlightsStale"
      class="text-xs text-base-content/50 mt-1 flex items-center gap-1"
      data-test="tool-description-stale-note"
    >
      <span aria-hidden="true">⟳</span>
      <span v-if="allHighlightsStale">Description changed since the last scan — highlights hidden.</span>
      <span v-else>
        Description changed since the last scan — {{ staleSpanCount }}
        {{ staleSpanCount === 1 ? 'flagged passage' : 'flagged passages' }} could not be located.
      </span>
    </p>

    <div v-if="excerpted || hiddenMarkCount > 0" class="mt-1 flex items-center gap-2 text-xs">
      <button
        v-if="excerpted"
        type="button"
        class="link link-primary"
        data-test="tool-description-expand"
        @click="expanded = !expanded"
      >
        {{ expanded ? 'Show excerpt around findings' : 'Show full description' }}
      </button>
      <span v-if="hiddenMarkCount > 0" class="text-base-content/50" data-test="tool-description-mark-overflow">
        +{{ hiddenMarkCount }} more {{ hiddenMarkCount === 1 ? 'match' : 'matches' }} not marked
      </span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { SecurityScanFinding } from '@/types/api'
import {
  segmentByFindings,
  usableSources,
  excerptSegments,
  revealInvisibles,
  LONG_DESCRIPTION_CHARS,
  type ExcerptPart,
  type Segment,
  type SpanSource,
} from '@/utils/highlightSpans'

/**
 * A tool description with the exact words a scan flagged marked in place.
 *
 * With no findings — or with findings whose spans no longer verify against the
 * live text — this renders exactly what the Available Tools card rendered
 * before: a plain paragraph. That is the governing rule of the whole feature:
 * the description text never disappears behind a scan state, and every failure
 * mode degrades to today's rendering, which is strictly not worse.
 */
const props = withDefaults(
  defineProps<{
    description?: string | null
    /** The findings whose `location` names THIS tool. */
    findings?: SecurityScanFinding[] | null
    /** Copy shown when the tool has no description at all. */
    emptyText?: string
  }>(),
  {
    description: '',
    findings: () => [],
    emptyText: 'No description available',
  },
)

const expanded = ref(false)

const text = computed(() => props.description ?? '')

/** Every span on every finding for this tool, joined back to its finding. */
const allSources = computed<SpanSource[]>(() =>
  (props.findings ?? []).flatMap((finding, findingIndex) =>
    (finding.spans ?? []).map((span) => ({
      span,
      index: findingIndex + 1,
      ruleId: finding.rule_id,
    })),
  ),
)

const verified = computed(() => usableSources(text.value, allSources.value))

/**
 * How many description spans no longer point at the words they were recorded
 * against. PARTIAL staleness counts: a tool tripping two checks where only one
 * span still verifies used to render as confidently and completely annotated
 * while the other finding vanished with no note and no count — and the flagged
 * list still offered "Show in description" for it, landing the operator on a
 * card with nothing marked. Never guess a new location; say what was lost.
 */
const staleSpanCount = computed(() => verified.value.unverified)

const highlightsStale = computed(() => staleSpanCount.value > 0)

/** Nothing at all could be located — the whole description moved. */
const allHighlightsStale = computed(
  () => highlightsStale.value && verified.value.rendered.length === 0,
)

const hiddenMarkCount = computed(() => verified.value.hidden)

const segments = computed<Segment[]>(() => segmentByFindings(text.value, verified.value.rendered))

/**
 * A very long description collapses to a window around each mark. Marked
 * segments are emitted whole — showing half of the evidence and hiding the rest
 * would be worse than showing none of it — and every elision is announced.
 */
const excerpted = computed(
  () => text.value.length > LONG_DESCRIPTION_CHARS && verified.value.rendered.length > 0,
)

const renderedParts = computed<ExcerptPart[]>(() => {
  if (!excerpted.value || expanded.value) {
    return segments.value.map((segment) => ({ kind: 'text', segment }) as ExcerptPart)
  }
  return excerptSegments(segments.value)
})

function markGlyph(segment: Segment): string {
  return segment.level === 'dangerous' ? '▲' : '▮'
}

function markClass(segment: Segment): string {
  // <mark>'s UA default is a hard yellow that is illegible on the dark theme,
  // so the background is reset above and the tint applied here through DaisyUI
  // semantic tokens only.
  return segment.level === 'dangerous'
    ? 'text-error decoration-double decoration-error'
    : 'text-warning decoration-wavy decoration-warning'
}

/**
 * The spoken prefix for a mark. It names the severity and the rules, and stops
 * there: the flagged words follow it as ordinary content, so quoting them here
 * would read them twice — and truncating that quote (which an aria-label had to
 * do) hid the back half of every long payload from the only readers who could
 * not see it on screen.
 */
function markLabel(segment: Segment): string {
  const rules = [...new Set(segment.sources.map((s) => s.span.check_id))].join(', ')
  const severity = segment.level === 'dangerous' ? 'Dangerous' : 'Warning'
  return `${severity} finding ${rules}:`
}

function reveal(value: string) {
  return revealInvisibles(value)
}
</script>
