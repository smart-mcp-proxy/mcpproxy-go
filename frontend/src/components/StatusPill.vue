<template>
  <!-- Spec 109 FR-052/FR-053: one header status pill — "● 2 of 4 online · 13
       tools · Retrieve". Online means the ONE usable predicate (health.usable),
       so a connected-but-quarantined server does not count; this never reads
       /status upstream_stats.connected_servers. When servers wait for review
       the online part says so ("● 0 online · 4 awaiting review · 0 tools ·
       Retrieve") instead of reading as a failed install, and before the first
       server list it reads "Loading servers…". Below 1100px it collapses to
       "● 2/4"; the title carries the full text. Links to Servers. Rendered for
       admin/personal principals only (a tenant's serversStore is never loaded;
       Spec 107 FR-041). -->
  <router-link
    to="/servers"
    class="flex items-center gap-2 px-3 py-2 bg-base-200 rounded-lg text-sm whitespace-nowrap shrink-0 hover:bg-base-300 transition-colors"
    :aria-label="ariaLabel"
    :title="fullText"
    data-test="header-status-pill"
  >
    <span :class="['w-2 h-2 rounded-full shrink-0', dotClass]" data-test="header-status-dot" aria-hidden="true" />
    <span class="font-medium tabular-nums min-[1100px]:hidden" data-test="header-status-compact">{{ compactText }}</span>
    <span class="hidden min-[1100px]:inline" data-test="header-status-full">
      <span v-if="stateKind !== 'ok'" class="font-medium" data-test="header-status-state">{{ fullText }}</span>
      <template v-else>
        <span class="font-medium" data-test="header-status-online">{{ onlineText }}</span>
        <template v-if="awaiting > 0">
          <span class="text-base-content/50"> · </span>
          <span data-test="header-status-awaiting">{{ awaiting }} awaiting review</span>
          <template v-if="offline > 0">
            <span class="text-base-content/50"> · </span>
            <span data-test="header-status-offline">{{ offline }} offline</span>
          </template>
        </template>
        <span class="text-base-content/50"> · </span>
        <span data-test="header-status-tools">{{ toolsLabel }}</span>
        <span class="text-base-content/50"> · </span>
        <span class="text-base-content/70" data-test="header-status-mode" :title="modeTitle">{{ modeLabel }}</span>
      </template>
    </span>
  </router-link>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useServersStore } from '@/stores/servers'
import { useSystemStore } from '@/stores/system'
import { routingModeMeta } from '@/utils/routingMode'
import type { Server } from '@/types'

const serversStore = useServersStore()
const systemStore = useSystemStore()

// A server is online when the core's unified health says it is usable. An old
// core that predates `health` falls back to the same facts the predicate is
// built from: connected, enabled and not quarantined.
function isOnline(server: Server): boolean {
  if (server.health) return server.health.usable
  return server.connected && server.enabled && !server.quarantined
}

// Awaiting review is the same "waiting for review" the Home attention list
// shows: admin_state quarantined (a disabled+quarantined row is admin_state
// disabled, so it is not counted). An old core falls back to quarantined+enabled.
function isAwaiting(server: Server): boolean {
  return server.health ? server.health.admin_state === 'quarantined' : server.quarantined && server.enabled
}

function isEnabled(server: Server): boolean {
  return server.health ? server.health.admin_state !== 'disabled' : server.enabled
}

const online = computed(() => serversStore.servers.filter(isOnline).length)
const total = computed(() => serversStore.servers.length)
const awaiting = computed(() => serversStore.servers.filter(s => !isOnline(s) && isAwaiting(s)).length)
// Offline: enabled and neither usable nor waiting for review (connecting,
// error, sign-in, secret, config). Disabled rows are in no bucket.
const offline = computed(() => serversStore.servers.filter(s => isEnabled(s) && !isOnline(s) && !isAwaiting(s)).length)
const tools = computed(() =>
  serversStore.servers.filter(isOnline).reduce((sum, s) => sum + (s.tool_count ?? 0), 0),
)
const toolsLabel = computed(() => `${tools.value} ${tools.value === 1 ? 'tool' : 'tools'}`)

const modeLabel = computed(() => routingModeMeta(systemStore.routingMode).label)
const modeTitle = computed(() =>
  systemStore.routingRestartRequired
    ? `Restart pending: /mcp will serve ${routingModeMeta(systemStore.pendingRoutingMode).label} after the next start`
    : undefined,
)

// Before the first server list lands the counts are unknown, not zero: print
// that instead of "0 of 0 online". If that first load failed, say so.
const stateKind = computed<'loading' | 'error' | 'ok'>(() => {
  if (serversStore.loaded) return 'ok'
  return serversStore.loading.error ? 'error' : 'loading'
})

const onlineText = computed(() => (awaiting.value > 0 ? `${online.value} online` : `${online.value} of ${total.value} online`))

const fullText = computed(() => {
  if (stateKind.value === 'loading') return 'Loading servers…'
  if (stateKind.value === 'error') return 'Servers unavailable'
  const parts = [onlineText.value]
  if (awaiting.value > 0) {
    parts.push(`${awaiting.value} awaiting review`)
    if (offline.value > 0) parts.push(`${offline.value} offline`)
  }
  parts.push(toolsLabel.value, modeLabel.value)
  return parts.join(' · ')
})

const compactText = computed(() => {
  if (stateKind.value === 'loading') return '…'
  if (stateKind.value === 'error') return '!'
  return `${online.value}/${total.value}`
})

const dotClass = computed(() => {
  if (!systemStore.isRunning) return 'bg-error'
  if (stateKind.value === 'error') return 'bg-error'
  if (stateKind.value === 'loading' || total.value === 0) return 'bg-base-content/30'
  if (online.value === total.value) return 'bg-success'
  // Only reviews pending: informational, not a fault.
  if (awaiting.value > 0 && offline.value === 0) return 'bg-info'
  return 'bg-warning'
})

const ariaLabel = computed(() => {
  if (stateKind.value === 'loading') return 'Loading server status'
  if (stateKind.value === 'error') return 'Servers unavailable'
  let label = `${online.value} of ${total.value} ${total.value === 1 ? 'server' : 'servers'} online, `
  if (awaiting.value > 0) {
    label += `${awaiting.value} awaiting review, `
    if (offline.value > 0) label += `${offline.value} offline, `
  }
  return `${label}${toolsLabel.value}, routing mode ${modeLabel.value}`
})
</script>
