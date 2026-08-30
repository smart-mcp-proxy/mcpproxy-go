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
      aria-haspopup="dialog"
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
      class="absolute right-0 top-full mt-2 shadow-lg bg-base-100 rounded-box w-[24rem] max-w-[calc(100vw-1rem)] max-h-[calc(100vh-5rem)] overflow-y-auto border border-base-300 z-50"
      data-test="mode-switcher-menu"
      role="group"
      aria-label="Tool surface and schema detail"
    >
      <!-- Pending restart. Shown FIRST because it explains why the selection
           below does not match what the operator just clicked. -->
      <!-- Guarded on the RESOLVED labels, not just the flag: an unrecognised
           pending value falls back to Retrieve like every other unknown mode,
           and rendering "still serving Retrieve, saved on disk: Retrieve" with
           a Cancel button that cancels nothing is worse than saying nothing. -->
      <div
        v-if="showPendingNotice"
        class="m-2 px-3 py-2 rounded-lg bg-warning/10 border border-warning/40 text-xs leading-snug flex items-center justify-between gap-2"
        data-test="mode-switcher-pending-notice"
        :title="pendingHint"
      >
        <span v-if="unmetPrerequisite(pendingMeta)">
          <strong class="text-warning">{{ pendingMeta.label }}</strong> after restart —
          {{ unmetPrerequisite(pendingMeta) }}
        </span>
        <span v-else>
          <strong class="text-warning">{{ pendingMeta.label }}</strong> after restart —
          <code class="font-mono">{{ pendingMeta.endpoint }}</code> serves it now.
        </span>
        <button
          type="button"
          class="btn btn-ghost btn-xs shrink-0"
          data-test="mode-switcher-cancel-pending"
          :disabled="busy"
          @click="select(activeMeta.mode)"
        >
          Undo
        </button>
      </div>

      <!-- Two tabs, not one long scroll: the schema-detail axis is the second
           half of the decision and must not sit below the fold of a dropdown.
           Each tab label carries its current value, so both settings are
           readable before either is opened. -->
      <!-- A segmented control, not the page-level `tabs-bordered` used by
           Settings: in a dropdown that rule is nearly invisible and the row read
           as two loose labels. `tabs-boxed` is the idiom this app already uses
           for a compact one-of-two choice (the dashboard's Usage/Overview
           switch), and its filled active segment is the boundary that makes the
           row read as a control. `tabs-box` is the DaisyUI 5 name — `tabs-boxed`
           is the v4 spelling and renders flat, which is exactly the "looks like
           plain text" failure this replaces. Each segment names its CURRENT value, so both
           settings are legible without opening either. -->
      <div role="tablist" class="tabs tabs-box m-2 mb-1 p-1" data-test="mode-switcher-tabs">
        <button
          type="button"
          role="tab"
          data-test="mode-tab-surface"
          class="tab flex-1 h-auto py-1.5 flex-col gap-0"
          :class="tab === 'surface' ? 'tab-active' : 'text-base-content/70 hover:text-base-content'"
          :aria-selected="tab === 'surface'"
          aria-controls="mode-panel-surface"
          @click="tab = 'surface'"
        >
          <span class="text-xs font-semibold leading-tight">Surface</span>
          <span class="text-[0.7rem] font-normal leading-tight">{{ activeMeta.label }}</span>
        </button>
        <button
          type="button"
          role="tab"
          data-test="mode-tab-detail"
          class="tab flex-1 h-auto py-1.5 flex-col gap-0"
          :class="tab === 'detail' ? 'tab-active' : 'text-base-content/70 hover:text-base-content'"
          :aria-selected="tab === 'detail'"
          aria-controls="mode-panel-detail"
          @click="tab = 'detail'"
        >
          <span class="text-xs font-semibold leading-tight">Schema detail</span>
          <span class="text-[0.7rem] font-normal leading-tight">{{ detailTabValue }}</span>
        </button>
      </div>

      <div v-show="tab === 'surface'" id="mode-panel-surface" role="tabpanel" data-test="mode-panel-surface">
        <div class="px-3 pt-2 pb-1 flex items-baseline justify-between gap-2">
          <span class="text-xs font-semibold text-base-content/60">What /mcp serves</span>
          <a
            :href="ROUTING_MODES_DOC"
            target="_blank"
            rel="noopener"
            class="text-xs link link-hover text-base-content/50"
            data-test="mode-switcher-surface-doc"
          >Docs ↗</a>
        </div>

        <div class="px-2 pb-1">
          <button
            v-for="m in ROUTING_MODE_LIST"
            :key="m.mode"
            type="button"
            :data-test="`mode-option-${m.mode}`"
            :title="m.detail"
            role="radio"
            :aria-checked="m.mode === selectedRoutingMode"
            class="w-full text-left px-2 py-1.5 rounded hover:bg-base-200 flex items-center justify-between gap-2"
            :class="{ 'bg-base-200': m.mode === selectedRoutingMode }"
            :aria-busy="busy"
            @click="select(m.mode)"
          >
            <span class="min-w-0">
              <span class="flex items-center flex-wrap gap-1.5">
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
              </span>
              <span class="block text-xs text-base-content/70 leading-snug">{{ m.summary }}</span>
              <!-- A prerequisite the operator has not met is the one thing that
                   cannot live in a hover hint: without it they restart into a
                   surface that finds tools and can call none of them. -->
              <span
                v-if="unmetPrerequisite(m)"
                class="block text-xs text-warning leading-snug"
                :data-test="`mode-option-${m.mode}-prereq`"
              >{{ unmetPrerequisite(m) }}</span>
            </span>
            <code class="text-[0.65rem] font-mono text-base-content/60 shrink-0">{{ m.endpoint }}</code>
          </button>
        </div>

        <p
          class="px-3 pb-2 text-xs text-base-content/50 leading-snug cursor-help"
          :title="ROUTING_RESTART_HINT"
          data-test="mode-switcher-restart-note"
        >
          {{ ROUTING_RESTART_NOTE }}
        </p>
      </div>

      <div v-show="tab === 'detail'" id="mode-panel-detail" role="tabpanel" data-test="mode-panel-detail">
        <div class="px-3 pt-2 pb-1 flex items-baseline justify-between gap-2">
          <span class="text-xs font-semibold text-base-content/60">Applies instantly</span>
          <a
            :href="DIRECT_TOOL_RESPONSE_DOC"
            target="_blank"
            rel="noopener"
            class="text-xs link link-hover text-base-content/50"
            data-test="mode-switcher-detail-doc"
          >Docs ↗</a>
        </div>

      <SerializationAxis
        axis-id="tool-response"
        title="Search results"
        :surface="toolResponseSurface(systemStore.routingMode)"
        :doc-href="TOOL_RESPONSE_DOC"
        :note="codeExecNote"
        :note-hint="TOOL_RESPONSE_CODE_EXEC_HINT"
        :options="TOOL_RESPONSE_MODES"
        :selected="systemStore.toolResponseMode"
        :busy="busy"
        @select="(v: string) => applyField('tool_response_mode', v)"
      />

      <SerializationAxis
        axis-id="direct-tool-response"
        title="Direct listings"
        :surface="directToolResponseSurface(systemStore.routingMode)"
        :doc-href="DIRECT_TOOL_RESPONSE_DOC"
        :options="DIRECT_TOOL_RESPONSE_MODES"
        :selected="systemStore.directToolResponseMode"
        :busy="busy"
        @select="(v: string) => applyField('direct_tool_response_mode', v)"
      />
      </div>

      <div class="px-3 py-1.5 border-t border-base-300">
        <RouterLink
          to="/settings?focus=routing_mode"
          class="text-xs link link-hover text-base-content/60"
          data-test="mode-switcher-settings-link"
          @click="open = false"
        >
          All settings
        </RouterLink>
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
import type { RoutingModeMeta } from '@/utils/routingMode'
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
  TOOL_RESPONSE_CODE_EXEC_HINT,
  ROUTING_RESTART_HINT,
  ROUTING_MODES_DOC,
  TOOL_RESPONSE_DOC,
  DIRECT_TOOL_RESPONSE_DOC,
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
  // Code execution PINS its /mcp surface to full schemas (Spec 085 FR-011), so
  // a "compact" badge there would advertise a serialization the surface does
  // not use. Say nothing rather than something false.
  if (systemStore.routingMode === 'code_execution') return ''
  const isDirect = systemStore.routingMode === 'direct'
  const value = isDirect ? systemStore.directToolResponseMode : systemStore.toolResponseMode
  if (!value || value === 'full') return ''
  // The panel's word for it, not the raw config value: an operator who opens
  // the panel to find "compact" will only see "Signatures".
  return serializationModeMeta(
    isDirect ? DIRECT_TOOL_RESPONSE_MODES : TOOL_RESPONSE_MODES,
    value
  ).label
})

/** The pending notice is one line; the rest of the story is its hover hint. */
const pendingHint = computed(() => {
  const prereq = unmetPrerequisite(pendingMeta.value)
  if (prereq) {
    return `/mcp is still serving ${activeMeta.value.label}. ${pendingMeta.value.label} is saved and applies the next time mcpproxy starts, but that surface can only call tools once code execution is enabled in Settings.`
  }
  return `/mcp is still serving ${activeMeta.value.label}. ${pendingMeta.value.label} is saved and applies the next time mcpproxy starts; until then ${pendingMeta.value.endpoint} already serves it.`
})

/**
 * The Schema-detail chip names what the CURRENT surface actually sends. Under
 * code execution /mcp is pinned to full schemas whatever the config says
 * (Spec 085 FR-011), so the chip must not repeat the config there.
 */
const detailTabValue = computed(() => {
  if (systemStore.routingMode === 'code_execution') return 'Full schemas'
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

/**
 * The prerequisite text for a mode whose prerequisite is NOT met, else "".
 * Only code execution has one today, and its flag defaults to off.
 */
function unmetPrerequisite(meta: RoutingModeMeta): string {
  if (meta.mode !== 'code_execution' || systemStore.codeExecutionEnabled) return ''
  return meta.prerequisiteNote ?? ''
}

const buttonTitle = computed(() => {
  const parts = [`${activeMeta.value.label} mode — ${activeMeta.value.detail}`]
  if (showPendingNotice.value) {
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
