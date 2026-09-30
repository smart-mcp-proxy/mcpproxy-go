<template>
  <!-- Spec 109 FR-053: one header status pill — "● 2 of 4 online · 13 tools ·
       Retrieve". Online means the ONE usable predicate (health.usable), so a
       connected-but-quarantined server does not count; this never reads
       /status upstream_stats.connected_servers. Below 1100px it collapses to
       "● 2/4". Links to Servers. Rendered for admin/personal principals only
       (a tenant's serversStore is never loaded; Spec 107 FR-041). -->
  <router-link
    to="/servers"
    class="flex items-center gap-2 px-3 py-2 bg-base-200 rounded-lg text-sm whitespace-nowrap shrink-0 hover:bg-base-300 transition-colors"
    :aria-label="ariaLabel"
    data-test="header-status-pill"
  >
    <span :class="['w-2 h-2 rounded-full shrink-0', dotClass]" data-test="header-status-dot" aria-hidden="true" />
    <span class="font-medium tabular-nums min-[1100px]:hidden" data-test="header-status-compact">{{ online }}/{{ total }}</span>
    <span class="hidden min-[1100px]:inline" data-test="header-status-full">
      <span class="font-medium" data-test="header-status-online">{{ online }} of {{ total }} online</span>
      <span class="text-base-content/50"> · </span>
      <span data-test="header-status-tools">{{ toolsLabel }}</span>
      <span class="text-base-content/50"> · </span>
      <span class="text-base-content/70" data-test="header-status-mode" :title="modeTitle">{{ modeLabel }}</span>
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

const online = computed(() => serversStore.servers.filter(isOnline).length)
const total = computed(() => serversStore.servers.length)
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

const dotClass = computed(() => {
  if (!systemStore.isRunning) return 'bg-error'
  if (total.value === 0) return 'bg-base-content/30'
  return online.value === total.value ? 'bg-success' : 'bg-warning'
})

const ariaLabel = computed(
  () =>
    `${online.value} of ${total.value} ${total.value === 1 ? 'server' : 'servers'} online, ` +
    `${toolsLabel.value}, routing mode ${modeLabel.value}`,
)
</script>
