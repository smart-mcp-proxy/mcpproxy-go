<template>
  <!--
    Header mode switcher. Replaces the static `Mode: Retrieve` badge, which had
    a `cursor-help` pointer, a tooltip that browsers render only after a long
    hover, and no way to act on what it told you. Mirrors the ProfileSwitcher
    affordance next to it.

    Three settings live here, on two different axes: routing_mode (which tool
    surface /mcp serves — restart-bound) and the two serialization axes
    (how entries on a surface are rendered — hot-reloadable). The panel keeps
    them visually separate for exactly that reason.
  -->
  <div class="relative" data-test="mode-switcher">
    <button
      type="button"
      data-test="mode-switcher-button"
      class="flex items-center space-x-2 px-3 py-2 bg-base-200 rounded-lg cursor-pointer hover:bg-base-300 transition-colors text-sm"
      :title="buttonTitle"
      :aria-expanded="open"
      aria-haspopup="true"
      @click="toggle"
    >
      <span class="text-xs text-base-content/60">Mode:</span>
      <span class="font-medium" data-test="mode-switcher-active">{{ activeMeta.label }}</span>
      <!-- A non-default serialization changes what an agent receives as much as
           the routing mode does, so it cannot be invisible from the header. -->
      <span
        v-if="serializationBadge"
        class="badge badge-ghost badge-xs"
        data-test="mode-switcher-serialization-badge"
      >{{ serializationBadge }}</span>
      <span
        v-if="showPendingNotice"
        class="badge badge-warning badge-xs"
        data-test="mode-switcher-pending-badge"
      >restart</span>
      <svg
        class="w-3 h-3 opacity-60 transition-transform"
        :class="{ 'rotate-180': open }"
        fill="none"
        stroke="currentColor"
        viewBox="0 0 24 24"
      >
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
      </svg>
    </button>

    <div
      v-if="open"
      class="absolute right-0 top-full mt-2 shadow-lg bg-base-100 rounded-box w-[26rem] max-h-[75vh] overflow-y-auto border border-base-300 z-50"
      data-test="mode-switcher-menu"
    >
      <!-- Pending restart. Shown FIRST because it explains why the selection
           below does not match what the operator just clicked. -->
      <!-- Guarded on the RESOLVED labels, not just the flag: an unrecognised
           pending value falls back to Retrieve like every other unknown mode,
           and rendering "still serving Retrieve, saved on disk: Retrieve" with
           a Cancel button that cancels nothing is worse than saying nothing. -->
      <div
        v-if="showPendingNotice"
        class="m-2 p-3 rounded-lg bg-warning/10 border border-warning/40 text-xs leading-relaxed"
        data-test="mode-switcher-pending-notice"
      >
        <p class="font-semibold text-warning mb-1">Restart pending</p>
        <p>
          <code class="font-mono">/mcp</code> is still serving
          <strong>{{ activeMeta.label }}</strong>. Saved on disk:
          <strong>{{ pendingMeta.label }}</strong> — it applies the next time mcpproxy starts.
          Until then <code class="font-mono">{{ pendingMeta.endpoint }}</code> already serves it.
        </p>
        <button
          type="button"
          class="btn btn-ghost btn-xs mt-2"
          data-test="mode-switcher-cancel-pending"
          :disabled="busy"
          @click="select(activeMeta.mode)"
        >
          Cancel — keep {{ activeMeta.label }}
        </button>
      </div>

      <!-- Two tabs, not one long scroll: the schema-detail axis is the second
           half of the decision and must not sit below the fold of a dropdown.
           Each tab label carries its current value, so both settings are
           readable before either is opened. -->
      <div role="tablist" class="tabs tabs-bordered px-2 pt-2">
        <button
          type="button"
          role="tab"
          data-test="mode-tab-surface"
          class="tab flex-1 gap-2"
          :class="{ 'tab-active font-semibold': tab === 'surface' }"
          :aria-selected="tab === 'surface'"
          @click="tab = 'surface'"
        >
          Surface
          <span class="badge badge-ghost badge-xs">{{ activeMeta.label }}</span>
        </button>
        <button
          type="button"
          role="tab"
          data-test="mode-tab-detail"
          class="tab flex-1 gap-2"
          :class="{ 'tab-active font-semibold': tab === 'detail' }"
          :aria-selected="tab === 'detail'"
          @click="tab = 'detail'"
        >
          Schema detail
          <span class="badge badge-ghost badge-xs">{{ detailTabValue }}</span>
        </button>
      </div>

      <div v-show="tab === 'surface'" data-test="mode-panel-surface">
      <div class="px-3 pt-3 pb-1">
        <div class="text-xs font-semibold text-base-content/60">What /mcp serves</div>
        <div class="text-xs text-base-content/50 mt-0.5">What an agent sees the moment it connects</div>
      </div>

      <div class="px-2 pb-2 space-y-1">
        <button
          v-for="m in ROUTING_MODE_LIST"
          :key="m.mode"
          type="button"
          :data-test="`mode-option-${m.mode}`"
          class="w-full text-left px-2 py-2 rounded hover:bg-base-200 flex items-start justify-between"
          :class="{ 'bg-base-200': m.mode === selectedRoutingMode }"
          :disabled="busy"
          @click="select(m.mode)"
        >
          <div class="min-w-0">
            <div class="flex items-center flex-wrap gap-2">
              <span class="font-medium">{{ m.label }}</span>
              <span
                v-if="m.mode === activeMeta.mode"
                class="badge badge-xs badge-primary"
                :data-test="`mode-active-badge-${m.mode}`"
              >serving now</span>
              <span
                v-else-if="m.mode === systemStore.pendingRoutingMode"
                class="badge badge-xs badge-warning"
                :data-test="`mode-pending-badge-${m.mode}`"
              >after restart</span>
              <span
                v-else
                class="badge badge-xs badge-ghost"
                :data-test="`mode-restart-badge-${m.mode}`"
              >needs restart</span>
            </div>
            <div class="text-xs text-base-content/70 mt-0.5 leading-relaxed">{{ m.description }}</div>
            <div class="text-xs text-base-content/50 mt-1 leading-relaxed">{{ m.tradeoff }}</div>
            <div class="text-xs text-base-content/50 mt-1">
              Always available at <code class="font-mono">{{ m.endpoint }}</code>
            </div>
          </div>
          <svg
            v-if="m.mode === activeMeta.mode"
            class="w-4 h-4 text-success shrink-0 ml-2 mt-0.5"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
          >
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7" />
          </svg>
        </button>
      </div>

      <p class="px-3 pb-3 text-xs text-base-content/60 leading-relaxed" data-test="mode-switcher-restart-note">
        {{ ROUTING_RESTART_NOTE }}
      </p>
      </div>

      <div v-show="tab === 'detail'" data-test="mode-panel-detail">
      <div class="px-3 pt-3 pb-1">
        <div class="text-xs font-semibold text-base-content/60">How much of each tool is written out</div>
        <div class="text-xs text-base-content/50 mt-0.5">
          Applies immediately — no restart, and connected clients are told to refetch their tool list.
        </div>
      </div>

      <SerializationAxis
        axis-id="tool-response"
        title="Search results"
        :surface="toolResponseSurface(systemStore.routingMode)"
        :note="codeExecNote"
        :options="TOOL_RESPONSE_MODES"
        :selected="systemStore.toolResponseMode"
        :busy="busy"
        @select="(v: string) => applyField('tool_response_mode', v)"
      />

      <SerializationAxis
        axis-id="direct-tool-response"
        title="Direct listings"
        :surface="directToolResponseSurface(systemStore.routingMode)"
        :options="DIRECT_TOOL_RESPONSE_MODES"
        :selected="systemStore.directToolResponseMode"
        :busy="busy"
        @select="(v: string) => applyField('direct_tool_response_mode', v)"
      />
      </div>

      <div class="px-3 py-2 border-t border-base-300 flex items-center justify-between">
        <RouterLink
          to="/settings?focus=routing_mode"
          class="text-xs link link-hover"
          data-test="mode-switcher-settings-link"
          @click="open = false"
        >
          All settings
        </RouterLink>
        <a
          href="https://docs.mcpproxy.app/features/routing-modes"
          target="_blank"
          rel="noopener"
          class="text-xs link link-hover"
          data-test="mode-switcher-docs-link"
        >
          Docs ↗
        </a>
      </div>
    </div>

    <!-- Click-outside overlay -->
    <div v-if="open" class="fixed inset-0 z-40" @click="open = false" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onBeforeUnmount, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useSystemStore } from '@/stores/system'
import SerializationAxis from './SerializationAxis.vue'
import {
  ROUTING_MODE_LIST,
  ROUTING_RESTART_NOTE,
  TOOL_RESPONSE_MODES,
  DIRECT_TOOL_RESPONSE_MODES,
  routingModeMeta,
  serializationModeMeta,
  toolResponseSurface,
  directToolResponseSurface,
  TOOL_RESPONSE_CODE_EXEC_NOTE,
} from '@/utils/routingMode'

const systemStore = useSystemStore()

const open = ref(false)
const busy = ref(false)
const tab = ref<'surface' | 'detail'>('surface')

/** The mode /mcp is serving right now. */
const activeMeta = computed(() => routingModeMeta(systemStore.routingMode))
/** The mode a restart would adopt; falls back to the active one when nothing is pending. */
const pendingMeta = computed(() =>
  routingModeMeta(systemStore.pendingRoutingMode || systemStore.routingMode)
)
/** What the radio-style highlight follows: the operator's latest choice. */
const selectedRoutingMode = computed(() => pendingMeta.value.mode)

const showPendingNotice = computed(
  () => systemStore.routingRestartRequired && pendingMeta.value.mode !== activeMeta.value.mode
)

/**
 * Header badge for a non-default serialization, naming only the axis that the
 * current routing mode puts on /mcp — the header is not the place to enumerate
 * every endpoint's serialization.
 */
const serializationBadge = computed(() => {
  const isDirect = systemStore.routingMode === 'direct'
  const value = isDirect ? systemStore.directToolResponseMode : systemStore.toolResponseMode
  if (!value || value === 'full') return ''
  return isDirect ? 'deferred' : 'compact'
})

/**
 * Both tab chips report what is SERVING, never what is pending: two chips
 * disagreeing inside one panel (Surface saying "Direct" while the header badge
 * says "Retrieve") reads as a bug. The pending choice is carried by the notice
 * at the top of the panel and by the per-option "after restart" badge, which
 * say so in words.
 *
 * The Schema-detail chip names the axis the CURRENT routing mode puts on /mcp —
 * the one an operator is asking about when they look at the header.
 */
const detailTabValue = computed(() => {
  const isDirect = systemStore.routingMode === 'direct'
  const value = isDirect ? systemStore.directToolResponseMode : systemStore.toolResponseMode
  return serializationModeMeta(
    isDirect ? DIRECT_TOOL_RESPONSE_MODES : TOOL_RESPONSE_MODES,
    value
  ).label
})

/** Code execution pins the retrieve surface to full schemas — say so, there. */
const codeExecNote = computed(() =>
  systemStore.routingMode === 'code_execution' ? TOOL_RESPONSE_CODE_EXEC_NOTE : undefined
)

const buttonTitle = computed(() => {
  const parts = [activeMeta.value.description]
  if (systemStore.routingRestartRequired) {
    parts.push(`Restart pending: ${pendingMeta.value.label} applies after mcpproxy restarts.`)
  }
  return parts.join(' ')
})

// Escape closes, like every other dismissible surface in the app. Registered on
// the document (not the panel) because focus stays on the trigger button after
// the click that opened it.
function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape' && open.value) open.value = false
}
watch(open, (isOpen) => {
  if (isOpen) document.addEventListener('keydown', onKeydown)
  else document.removeEventListener('keydown', onKeydown)
})
onBeforeUnmount(() => document.removeEventListener('keydown', onKeydown))

function toggle() {
  open.value = !open.value
  // Refresh on open: another surface (Settings, the tray, the config file) may
  // have changed any of these three since the last fetch.
  if (open.value) void systemStore.fetchRouting()
}

async function applyField(field: string, value: string) {
  if (busy.value) return
  busy.value = true
  try {
    const result = await systemStore.applyModeField(field, value)
    if (!result.ok) return
    if (result.requiresRestart) {
      systemStore.addToast({
        type: 'warning',
        title: 'Saved — restart required',
        message: result.restartReason || 'Restart mcpproxy for the new surface to serve on /mcp.',
      })
    } else {
      systemStore.addToast({ type: 'success', title: 'Applied' })
    }
  } finally {
    busy.value = false
  }
}

async function select(mode: string) {
  // Already the pending/served choice — nothing to write.
  if (mode === selectedRoutingMode.value) {
    open.value = false
    return
  }
  await applyField('routing_mode', mode)
}
</script>
