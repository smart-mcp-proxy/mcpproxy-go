<template>
  <!-- Not rendered at all when there is nothing to report: absence IS the clean
       state. A permanent "0 findings" panel is chrome an operator learns to skip,
       and skipping it is exactly what must not happen on the day it is non-zero. -->
  <div
    v-if="visible"
    data-test="flagged-tools-panel"
    class="card bg-base-200 border border-base-300 mb-4"
  >
    <div class="card-body p-4 gap-3">
      <div class="flex items-start justify-between gap-3 flex-wrap">
        <div>
          <h3 class="font-semibold text-sm flex items-center gap-2">
            <span aria-hidden="true">🔎</span>
            <span>Flagged tool descriptions ({{ groups.length }})</span>
          </h3>
          <!-- Non-goal 9 / scan_informational.go: the informational scan path
               drives NO gating whatsoever, and the panel has to say so or it
               reads as a block the operator cannot find the release for. -->
          <p class="text-xs text-base-content/60 mt-0.5" data-test="flagged-tools-informational">
            Informational — these findings do not block the tool.
          </p>
        </div>
        <slot name="actions" />
      </div>

      <div v-if="loading" class="space-y-2" data-test="flagged-tools-loading">
        <div v-for="i in 3" :key="i" class="skeleton h-8 w-full"></div>
      </div>

      <div v-else-if="error" class="alert alert-warning py-2" data-test="flagged-tools-error">
        <span class="text-sm">Could not load scan findings.</span>
        <button type="button" class="btn btn-xs" @click="emit('retry')">Retry</button>
      </div>

      <template v-else>
        <p
          v-if="stale"
          class="text-xs text-base-content/60 flex items-center gap-1"
          data-test="flagged-tools-stale"
        >
          <span aria-hidden="true">⟳</span>
          <span>Some findings predate the current tool descriptions.</span>
        </p>

        <ul class="space-y-2">
          <li
            v-for="group in groups"
            :key="group.tool"
            class="flex items-start gap-3 flex-wrap rounded-lg bg-base-100 p-2"
            :data-test="`flagged-tool-row-${group.tool}`"
          >
            <FindingChip :state="group.level" :count="group.findings.length" class="mt-0.5" />
            <div class="flex-1 min-w-0">
              <div class="flex items-center gap-2 flex-wrap">
                <code class="font-mono text-sm break-all">{{ group.tool }}</code>
                <span
                  v-for="ruleId in group.ruleIds"
                  :key="ruleId"
                  class="badge badge-xs badge-ghost font-mono"
                  :title="ruleId"
                >{{ ruleId }}</span>
              </div>
              <p class="text-xs text-base-content/70 mt-0.5 line-clamp-2">
                {{ detailLine(group) }}
              </p>
            </div>
            <!-- "Show in description" scrolls to the tool's card. When the tool
                 is not in the server's current tool list there IS no card, and
                 the button was a silent no-op — the exact dead button this panel
                 goes out of its way to avoid elsewhere. Say why instead. -->
            <button
              v-if="isPresent(group.tool)"
              type="button"
              class="btn btn-xs btn-outline"
              :data-test="`flagged-tool-show-${group.tool}`"
              @click="emit('show-in-description', group.tool)"
            >
              Show in description
            </button>
            <span
              v-else
              class="text-xs text-base-content/50 self-center"
              :data-test="`flagged-tool-absent-${group.tool}`"
              title="The finding predates the server's current tool list, or the server is not connected."
            >Not in the current tool list</span>
          </li>
        </ul>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import FindingChip from '@/components/FindingChip.vue'
import type { FlaggedToolGroup } from '@/utils/toolLocation'

/**
 * The flagged-tool list, rendered next to the tools it is about.
 *
 * Before this, reading one finding cost four clicks and a scan-history table,
 * and the report never named which of a server's fifteen tools was the problem.
 */
const props = withDefaults(
  defineProps<{
    groups?: FlaggedToolGroup[]
    loading?: boolean
    error?: boolean
    /** At least one finding may predate the current tool descriptions. */
    stale?: boolean
    /**
     * Names of the tools the server currently exposes, when the caller knows
     * them. `null`/undefined means "unknown" and every row keeps its button —
     * an unknown inventory must never be reported as a missing tool.
     */
    presentTools?: string[] | null
  }>(),
  { groups: () => [], loading: false, error: false, stale: false, presentTools: null },
)

const emit = defineEmits<{
  (e: 'show-in-description', tool: string): void
  (e: 'retry'): void
}>()

const groups = computed(() => props.groups ?? [])

// The loading and error states keep the header, because in both cases the
// operator asked a question we have not answered yet. Only "nothing found"
// removes the panel entirely.
const visible = computed(() => props.loading || props.error || groups.value.length > 0)

// A Set, because a server with a large tool surface would otherwise turn the row
// loop into a quadratic scan on every re-render.
const presentToolSet = computed(() =>
  props.presentTools ? new Set(props.presentTools) : null,
)

function isPresent(tool: string): boolean {
  const present = presentToolSet.value
  return present === null || present.has(tool)
}

function detailLine(group: FlaggedToolGroup): string {
  const first = group.findings[0]
  const detail = first?.description?.trim() || first?.title?.trim()
  if (detail) return detail
  return group.level === 'dangerous'
    ? 'The scan flagged this tool description.'
    : 'The scan raised a review-only signal on this tool description.'
}
</script>
