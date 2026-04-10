<template>
  <div class="space-y-6">
    <!-- Telemetry Notice Banner -->
    <TelemetryBanner />

    <!-- Servers Needing Attention Banner (using unified health status) -->
    <div
      v-if="serversNeedingAttention.length > 0"
      class="alert alert-warning"
    >
      <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.732-.833-2.5 0L3.732 16.5c-.77.833.192 2.5 1.732 2.5z" />
      </svg>
      <div class="flex-1">
        <h3 class="font-bold">{{ serversNeedingAttention.length }} server{{ serversNeedingAttention.length !== 1 ? 's' : '' }} need{{ serversNeedingAttention.length === 1 ? 's' : '' }} attention</h3>
        <div class="text-sm space-y-1 mt-1">
          <div v-for="server in serversNeedingAttention.slice(0, 3)" :key="server.name" class="flex items-center gap-2">
            <span :class="server.health?.level === 'unhealthy' ? 'text-error' : 'text-warning'">●</span>
            <router-link :to="`/servers/${server.name}`" class="font-medium link link-hover">{{ server.name }}</router-link>
            <span class="opacity-70">{{ server.health?.summary }}</span>
            <button
              v-if="server.health?.action === 'login'"
              @click="triggerServerAction(server.name, 'oauth_login')"
              class="btn btn-xs btn-primary"
            >
              Login
            </button>
            <button
              v-if="server.health?.action === 'restart'"
              @click="triggerServerAction(server.name, 'restart')"
              class="btn btn-xs btn-primary"
            >
              Restart
            </button>
            <button
              v-if="server.health?.action === 'enable'"
              @click="triggerServerAction(server.name, 'enable')"
              class="btn btn-xs btn-primary"
            >
              Enable
            </button>
            <router-link
              v-if="server.health?.action === 'set_secret'"
              to="/secrets"
              class="btn btn-xs btn-primary"
            >
              Set Secret
            </router-link>
            <router-link
              v-if="server.health?.action === 'configure'"
              :to="`/servers/${server.name}?tab=config`"
              class="btn btn-xs btn-primary"
            >
              Configure
            </router-link>
          </div>
          <div v-if="serversNeedingAttention.length > 3" class="text-xs opacity-60">
            ... and {{ serversNeedingAttention.length - 3 }} more
          </div>
        </div>
      </div>
      <router-link to="/servers" class="btn btn-sm">
        View All Servers
      </router-link>
    </div>

    <!-- Tools Pending Quarantine Approval Banner -->
    <div
      v-if="totalPendingTools > 0"
      class="alert alert-warning"
    >
      <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
      </svg>
      <div class="flex-1">
        <h3 class="font-bold">{{ totalPendingTools }} tool{{ totalPendingTools !== 1 ? 's' : '' }} pending approval across {{ serversWithPendingTools.length }} server{{ serversWithPendingTools.length !== 1 ? 's' : '' }}</h3>
        <div class="text-sm space-y-1 mt-1">
          <div v-for="entry in serversWithPendingTools.slice(0, 5)" :key="entry.serverName" class="flex items-center gap-2">
            <span class="text-warning">&#9679;</span>
            <router-link :to="`/servers/${entry.serverName}`" class="font-medium link link-hover">{{ entry.serverName }}</router-link>
            <span class="opacity-70">{{ entry.count }} tool{{ entry.count !== 1 ? 's' : '' }} pending</span>
          </div>
          <div v-if="serversWithPendingTools.length > 5" class="text-xs opacity-60">
            ... and {{ serversWithPendingTools.length - 5 }} more server{{ serversWithPendingTools.length - 5 !== 1 ? 's' : '' }}
          </div>
        </div>
      </div>
      <router-link to="/servers" class="btn btn-sm">
        Review Tools
      </router-link>
    </div>

    <!-- Hub Visualization -->
    <div class="grid grid-cols-1 lg:grid-cols-[280px_1fr_280px] gap-0 min-h-[520px] relative">

      <!-- Left Column: AI Agents / Clients -->
      <div class="flex flex-col justify-center items-center lg:items-end space-y-3 py-6 lg:pr-0">
        <h3 class="text-xs font-bold uppercase tracking-widest opacity-40 mb-1 w-full max-w-[260px] text-center lg:text-right">AI Agents</h3>

        <!-- Single big clients box -->
        <div class="card card-compact bg-base-100 shadow-sm border border-base-300 w-full max-w-[260px]">
          <div class="card-body py-3 px-4">
            <div v-if="connectedClientNames.length > 0" class="mb-1">
              <div class="flex items-center gap-2 mb-1">
                <div class="w-2.5 h-2.5 rounded-full bg-success flex-shrink-0"></div>
                <span class="text-xs font-bold uppercase tracking-wide opacity-50">Connected</span>
              </div>
              <div class="text-sm font-medium">{{ connectedClientNames.join(', ') }}</div>
            </div>
            <div v-if="supportedClientNames.length > 0">
              <div class="text-xs opacity-40 mt-1">Available: {{ supportedClientNames.join(', ') }}</div>
            </div>
            <div v-if="connectedClientNames.length === 0 && supportedClientNames.length === 0" class="text-sm opacity-50 text-center py-2">
              No clients detected
            </div>
          </div>
        </div>

        <!-- Left Action Buttons -->
        <div class="flex flex-col gap-2 w-full max-w-[260px] pt-3">
          <button @click="showConnectModal = true" class="btn btn-primary btn-sm w-full gap-1">
            Connect Clients
          </button>
          <button @click="showAddServer = true" class="btn btn-secondary btn-outline btn-sm w-full gap-1">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-8l-4-4m0 0L8 8m4-4v12" />
            </svg>
            Import from client configs
          </button>
          <router-link to="/sessions" class="btn btn-ghost btn-sm w-full gap-1">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M2.458 12C3.732 7.943 7.523 5 12 5c4.478 0 8.268 2.943 9.542 7-1.274 4.057-5.064 7-9.542 7-4.477 0-8.268-2.943-9.542-7z" />
            </svg>
            Recent Sessions
          </router-link>
        </div>
      </div>

      <!-- Center Column: MCPProxy Hub -->
      <div class="flex flex-col items-center justify-center relative py-6">
        <!-- Connection lines: one fat horizontal line each side, big green running dot -->
        <svg class="absolute inset-0 w-full h-full pointer-events-none hidden lg:block overflow-visible" preserveAspectRatio="none">
          <!-- Left fat line (clients → hub) -->
          <line x1="0" y1="50%" x2="42%" y2="50%" stroke="oklch(var(--su))" stroke-width="4" stroke-opacity="0.25" />
          <!-- Right fat line (hub → servers) -->
          <line x1="58%" y1="50%" x2="100%" y2="50%" stroke="oklch(var(--su))" stroke-width="4" stroke-opacity="0.25" />

          <!-- Green dots travel once every 20s cycle, 4 dots total -->
          <!-- Left dot 1: clients → hub -->
          <circle r="7" fill="oklch(var(--su))" opacity="0">
            <animate attributeName="cx" values="0%;0%;42%;42%" keyTimes="0;0.05;0.15;1" dur="20s" repeatCount="indefinite" />
            <animate attributeName="cy" values="50%;50%;50%;50%" dur="20s" repeatCount="indefinite" />
            <animate attributeName="opacity" values="0;0.9;0.9;0;0" keyTimes="0;0.05;0.13;0.15;1" dur="20s" repeatCount="indefinite" />
          </circle>
          <!-- Left dot 2: clients → hub, staggered -->
          <circle r="6" fill="oklch(var(--su))" opacity="0">
            <animate attributeName="cx" values="0%;0%;42%;42%" keyTimes="0;0.1;0.2;1" dur="20s" repeatCount="indefinite" />
            <animate attributeName="cy" values="50%;50%;50%;50%" dur="20s" repeatCount="indefinite" />
            <animate attributeName="opacity" values="0;0.7;0.7;0;0" keyTimes="0;0.1;0.18;0.2;1" dur="20s" repeatCount="indefinite" />
          </circle>

          <!-- Right dot 1: servers → hub -->
          <circle r="7" fill="oklch(var(--su))" opacity="0">
            <animate attributeName="cx" values="100%;100%;58%;58%" keyTimes="0;0.07;0.17;1" dur="20s" repeatCount="indefinite" />
            <animate attributeName="cy" values="50%;50%;50%;50%" dur="20s" repeatCount="indefinite" />
            <animate attributeName="opacity" values="0;0.9;0.9;0;0" keyTimes="0;0.07;0.15;0.17;1" dur="20s" repeatCount="indefinite" />
          </circle>
          <!-- Right dot 2: servers → hub, staggered -->
          <circle r="6" fill="oklch(var(--su))" opacity="0">
            <animate attributeName="cx" values="100%;100%;58%;58%" keyTimes="0;0.12;0.22;1" dur="20s" repeatCount="indefinite" />
            <animate attributeName="cy" values="50%;50%;50%;50%" dur="20s" repeatCount="indefinite" />
            <animate attributeName="opacity" values="0;0.7;0.7;0;0" keyTimes="0;0.12;0.2;0.22;1" dur="20s" repeatCount="indefinite" />
          </circle>

          <!-- Static green dots at hub connection points -->
          <circle cx="42%" cy="50%" r="5" fill="oklch(var(--su))" opacity="0.7" />
          <circle cx="58%" cy="50%" r="5" fill="oklch(var(--su))" opacity="0.7" />
        </svg>

        <!-- Token savings badge (above hub) -->
        <div class="mb-6 z-10">
          <div
            v-if="tokenSavingsData && tokenSavingsData.saved_tokens_percentage > 0"
            class="badge badge-lg gap-1 px-4 py-3 bg-primary/10 text-primary border-primary/30"
          >
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 7h8m0 0v8m0-8l-8 8-4-4-6 6" />
            </svg>
            <span class="text-lg font-bold">{{ tokenSavingsData.saved_tokens_percentage >= 99.995 ? '99.99' : tokenSavingsData.saved_tokens_percentage >= 10 ? tokenSavingsData.saved_tokens_percentage.toFixed(1) : tokenSavingsData.saved_tokens_percentage.toFixed(0) }}%</span>
            <span class="text-xs font-medium">tokens saved</span>
          </div>
        </div>

        <!-- Logo Hub -->
        <div class="relative z-10">
          <div class="w-36 h-36 flex items-center justify-center transition-all duration-500"
            :class="systemStore.isRunning ? 'hub-glow' : ''">
            <img :src="logoSvg" alt="MCPProxy" class="w-28 h-28" />
          </div>
          <div class="text-center mt-1 select-none">
            <div class="text-xs font-bold uppercase tracking-wider" :class="systemStore.isRunning ? 'text-primary' : 'text-base-content/60'">
              MCPProxy
            </div>
            <div class="text-xs font-medium" :class="systemStore.isRunning ? 'text-success' : 'text-error'">
              {{ systemStore.isRunning ? 'active' : 'stopped' }}
            </div>
            <div v-if="uptime" class="text-[10px] opacity-50">{{ uptime }}</div>
          </div>
        </div>

        <!-- Security Status -->
        <div class="z-10 w-full max-w-[300px] space-y-2 mt-4">
          <!-- Docker Isolation (hidden until status has been fetched to avoid
               flashing a false "disabled" state on initial page load) -->
          <div v-if="dockerStatus" class="flex items-center gap-2 text-xs px-3 py-2 rounded-lg"
               :class="dockerStatus.available ? 'bg-success/10 text-success' : 'bg-warning/10 text-warning'">
            <svg class="w-4 h-4 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
                    d="M20 7l-8-4-8 4m16 0l-8 4m8-4v10l-8 4m0-10L4 7m8 4v10M4 7v10l8 4" />
            </svg>
            <span v-if="dockerStatus.available" class="font-medium">Docker isolation active</span>
            <span v-else class="font-medium">Docker isolation disabled — enable Docker to protect your system</span>
          </div>

          <!-- Quarantine (hidden until config fetch completes) -->
          <div v-if="quarantineEnabled !== null" class="flex items-center gap-2 text-xs px-3 py-2 rounded-lg"
               :class="quarantineEnabled ? 'bg-success/10 text-success' : 'bg-warning/10 text-warning'">
            <svg class="w-4 h-4 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
                    d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z" />
            </svg>
            <span v-if="quarantineEnabled" class="font-medium">Quarantine protection active</span>
            <span v-else class="font-medium">Quarantine disabled — enable to prevent prompt injection attacks</span>
          </div>

          <!-- Activity Log link -->
          <router-link to="/activity" class="flex items-center gap-2 text-xs px-3 py-2 rounded-lg bg-base-100/50 border border-base-300 hover:bg-base-200 transition-colors">
            <svg class="w-4 h-4 flex-shrink-0 opacity-60" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M2.458 12C3.732 7.943 7.523 5 12 5c4.478 0 8.268 2.943 9.542 7-1.274 4.057-5.064 7-9.542 7-4.477 0-8.268-2.943-9.542-7z" />
            </svg>
            <span class="font-medium opacity-70">Activity Log</span>
          </router-link>
        </div>
      </div>

      <!-- Right Column: Upstream Servers -->
      <div class="flex flex-col justify-center items-center lg:items-start space-y-3 py-6 lg:pl-4">
        <h3 class="text-xs font-bold uppercase tracking-widest opacity-40 mb-1 w-full max-w-[240px] text-center lg:text-left">Upstream Servers</h3>

        <!-- Connected servers card -->
        <router-link to="/servers" class="card card-compact bg-base-100 shadow-sm border border-base-300 w-full max-w-[240px] hover:shadow-md transition-shadow">
          <div class="card-body py-3 px-4">
            <div class="flex items-center gap-2">
              <div class="w-2.5 h-2.5 rounded-full bg-success flex-shrink-0"></div>
              <span class="text-2xl font-bold leading-none">{{ serversStore.serverCount.connected }}</span>
              <span class="text-sm opacity-60">connected</span>
            </div>
            <div class="text-sm mt-1">
              <span class="font-bold">{{ serversStore.totalTools }}</span>
              <span class="opacity-60"> tools available</span>
            </div>
            <div
              v-if="disabledCount > 0"
              class="text-xs opacity-50 mt-0.5"
            >
              {{ disabledCount }} disabled
            </div>
          </div>
        </router-link>

        <!-- Quarantine card -->
        <router-link
          v-if="serversStore.serverCount.quarantined > 0"
          to="/servers"
          class="card card-compact bg-warning/10 border border-warning/30 w-full max-w-[240px] hover:shadow-md transition-shadow"
        >
          <div class="card-body py-3 px-4">
            <div class="flex items-center gap-2">
              <svg class="w-4 h-4 text-warning flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
              </svg>
              <span class="text-lg font-bold text-warning leading-none">{{ serversStore.serverCount.quarantined }}</span>
              <span class="text-sm">in quarantine</span>
            </div>
          </div>
        </router-link>

        <!-- Right Action Buttons -->
        <div class="flex flex-col gap-2 w-full max-w-[240px] pt-3">
          <button @click="showAddServer = true" class="btn btn-primary btn-sm w-full gap-1">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 6v6m0 0v6m0-6h6m-6 0H6" />
            </svg>
            Add Server
          </button>
          <router-link to="/repositories" class="btn btn-ghost btn-sm w-full gap-1">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
            </svg>
            Browse Registry
          </router-link>
          <router-link to="/security" class="btn btn-ghost btn-sm w-full gap-1">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z" />
            </svg>
            Security Scan
            <span v-if="securityScannerLoaded && securityTotalScans === 0" class="badge badge-ghost badge-xs ml-1">Run first scan</span>
            <span v-else-if="securityTotalFindings > 0" class="badge badge-warning badge-xs ml-1">{{ securityTotalFindings }} issue{{ securityTotalFindings === 1 ? '' : 's' }}</span>
          </router-link>
        </div>
      </div>
    </div>

    <!-- Token Savings Collapsible Detail -->
    <div v-if="tokenSavingsData" class="collapse collapse-arrow bg-base-100 shadow-sm border border-base-300">
      <input type="checkbox" />
      <div class="collapse-title font-medium flex items-center gap-3">
        <svg class="w-5 h-5 text-success" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 7h8m0 0v8m0-8l-8 8-4-4-6 6" />
        </svg>
        Token Savings Details
        <span class="badge badge-success badge-sm ml-auto">{{ formatNumber(tokenSavingsData.saved_tokens) }} saved</span>
      </div>
      <div class="collapse-content">
        <div class="grid grid-cols-1 lg:grid-cols-2 gap-6 pt-2">
          <!-- Token Savings Stats -->
          <div>
            <div class="grid grid-cols-3 gap-4">
              <div>
                <div class="text-sm opacity-60">Tokens Saved</div>
                <div class="text-2xl font-bold text-success">{{ formatNumber(tokenSavingsData.saved_tokens) }}</div>
                <div class="text-xs opacity-60">{{ tokenSavingsData.saved_tokens_percentage.toFixed(1) }}% reduction</div>
              </div>
              <div>
                <div class="text-sm opacity-60">Full Tool List</div>
                <div class="text-xl font-bold">{{ formatNumber(tokenSavingsData.total_server_tool_list_size) }}</div>
                <div class="text-xs opacity-60">All servers</div>
              </div>
              <div>
                <div class="text-sm opacity-60">Typical Query</div>
                <div class="text-xl font-bold">{{ formatNumber(tokenSavingsData.average_query_result_size) }}</div>
                <div class="text-xs opacity-60">BM25 result</div>
              </div>
            </div>
          </div>

          <!-- Token Distribution Chart -->
          <div>
            <div class="flex items-center justify-center">
              <div class="w-48 h-48">
                <TokenPieChart v-if="pieChartSegments.length > 0" :data="pieChartSegments" />
              </div>
            </div>
            <div class="mt-3 space-y-1.5 max-h-32 overflow-y-auto">
              <div
                v-for="(segment, index) in pieChartSegments"
                :key="index"
                class="flex items-center justify-between text-sm"
              >
                <div class="flex items-center space-x-2 min-w-0">
                  <div class="w-2.5 h-2.5 rounded flex-shrink-0" :style="{ backgroundColor: segment.color }"></div>
                  <span class="truncate text-xs">{{ segment.name }}</span>
                </div>
                <div class="flex items-center space-x-2 flex-shrink-0">
                  <span class="font-mono text-xs">{{ formatNumber(segment.value) }}</span>
                  <span class="text-xs opacity-50">({{ segment.percentage.toFixed(1) }}%)</span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Hints Panel (Bottom of Page) -->
    <CollapsibleHintsPanel :hints="dashboardHints" />

    <!-- Modals -->
    <ConnectModal :show="showConnectModal" @close="showConnectModal = false" />
    <AddServerModal :show="showAddServer" @close="showAddServer = false" @added="handleServerAdded" />
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch, onMounted, onUnmounted } from 'vue'
import { useServersStore } from '@/stores/servers'
import { useSystemStore } from '@/stores/system'
import { useSecurityScannerStatus, refreshSecurityScannerStatus } from '@/composables/useSecurityScannerStatus'
import api from '@/services/api'
import logoSvg from '@/assets/logo.svg'
import CollapsibleHintsPanel from '@/components/CollapsibleHintsPanel.vue'
import TelemetryBanner from '@/components/TelemetryBanner.vue'
import TokenPieChart from '@/components/TokenPieChart.vue'
import ConnectModal from '@/components/ConnectModal.vue'
import AddServerModal from '@/components/AddServerModal.vue'
import type { Hint } from '@/components/CollapsibleHintsPanel.vue'
import type { ClientStatus } from '@/types'

const serversStore = useServersStore()
const systemStore = useSystemStore()

// Modal state
const showConnectModal = ref(false)
const showAddServer = ref(false)

// Auto-refresh interval
let refreshInterval: ReturnType<typeof setInterval> | null = null

// --- Client statuses ---
const clientStatuses = ref<ClientStatus[]>([])

const connectedClientNames = computed(() =>
  clientStatuses.value.filter(c => c.connected).map(c => c.name)
)
const supportedClientNames = computed(() =>
  clientStatuses.value.filter(c => c.supported && !c.connected && c.exists).map(c => c.name)
)

function clientIcon(client: ClientStatus): string {
  const iconMap: Record<string, string> = {
    'claude-desktop': '\u2728',
    'claude-code': '\u{1F4BB}',
    'cursor': '\u{1F4DD}',
    'vscode': '\u{1F4D0}',
    'windsurf': '\u{1F3C4}',
    'zed': '\u26A1',
    'cline': '\u{1F916}',
    'continue': '\u27A1\uFE0F',
  }
  return iconMap[client.id] || client.icon || '\u{1F527}'
}

const loadClientStatuses = async () => {
  try {
    const response = await api.getConnectStatus()
    if (response.success && response.data) {
      clientStatuses.value = Array.isArray(response.data) ? response.data : []
    }
  } catch {
    // Connect endpoint may not exist yet - graceful degradation
  }
}

// --- Activity count ---
const activityCount = ref(0)

const loadActivitySummary = async () => {
  try {
    const response = await api.getActivitySummary('24h')
    if (response.success && response.data) {
      activityCount.value = response.data.total_count || 0
    }
  } catch {
    // Silently fail
  }
}

// --- Security status ---
const dockerStatus = ref<{available: boolean, version?: string} | null>(null)
// quarantineEnabled is null until the config fetch completes so the template
// can skip rendering the "Quarantine disabled" warning on initial load. A
// plain `false` default would briefly display the warning before data arrives.
const quarantineEnabled = ref<boolean | null>(null)

// Security scanner totals for the "Security Scan" dashboard chip (F-12).
// We reuse the shared composable so we don't double-fetch /security/overview.
const {
  totalFindings: securityTotalFindings,
  totalScans: securityTotalScans,
  loaded: securityScannerLoaded,
} = useSecurityScannerStatus()

const loadSecurityStatus = async () => {
  try {
    // Docker status from dedicated endpoint
    const dockerResponse = await api.getDockerStatus()
    if (dockerResponse.success && dockerResponse.data) {
      let available = dockerResponse.data.docker_available ?? false
      // Workaround: Docker health checker can get stuck at 'max retries exceeded'
      // even when Docker containers are running. If API says unavailable but
      // we have connected stdio servers (which use Docker), treat as available.
      if (!available && serversStore.servers.some(s => s.connected && s.protocol === 'stdio')) {
        available = true
      }
      dockerStatus.value = { available }
    }
  } catch {
    // Docker endpoint may not exist - treat as unavailable
    dockerStatus.value = { available: false }
  }

  try {
    // Quarantine status from config endpoint
    const configResponse = await api.getConfig()
    if (configResponse.success && configResponse.data) {
      const cfg = configResponse.data.config
      // quarantine_enabled defaults to true when omitted (nil)
      quarantineEnabled.value = cfg?.quarantine_enabled ?? true
    }
  } catch {
    // Fallback: assume enabled (safe default)
    quarantineEnabled.value = true
  }
}

// --- Uptime ---
// Track when we first saw the server running via SSE
const serverFirstSeen = ref<number>(0)

watch(() => systemStore.isRunning, (running: boolean) => {
  if (running && !serverFirstSeen.value) {
    serverFirstSeen.value = Date.now()
  }
}, { immediate: true })

const uptime = computed(() => {
  if (!systemStore.isRunning) return ''

  // Use the SSE status timestamp as server epoch if available
  // The status.timestamp is a unix timestamp from the backend
  const ts = systemStore.status?.timestamp
  if (ts && ts > 0) {
    // ts is in seconds — it represents when the status was generated
    // The server start time ~ ts minus how long it's been running
    // But we don't have start_time in API, so use the oldest timestamp we've seen
    const now = Math.floor(Date.now() / 1000)
    // If firstSeen is set, compute from that
    if (serverFirstSeen.value) {
      const diff = Math.floor((Date.now() - serverFirstSeen.value) / 1000)
      if (diff < 60) return 'just started'
      if (diff < 3600) return `${Math.floor(diff / 60)}m uptime`
      if (diff < 86400) return `${Math.floor(diff / 3600)}h uptime`
      return `${Math.floor(diff / 86400)}d uptime`
    }
  }

  return 'online'
})

// --- Recent Sessions ---
const recentSessions = ref<any[]>([])

const loadSessions = async () => {
  try {
    const response = await api.getSessions(5)
    if (response.success && response.data) {
      recentSessions.value = response.data.sessions || []
    }
  } catch {
    // Silently fail
  }
}

// --- Token Savings ---
const tokenSavingsData = ref<any>(null)

const loadTokenSavings = async () => {
  try {
    const response = await api.getTokenStats()
    if (response.success && response.data) {
      tokenSavingsData.value = response.data
    }
  } catch {
    // Silently fail
  }
}

// --- Disabled server count ---
const disabledCount = computed(() => {
  return serversStore.serverCount.total - serversStore.serverCount.connected - serversStore.serverCount.quarantined
})

// --- Servers needing attention ---
// Only show servers that have actionable problems, not transient states like "Connecting..."
const serversNeedingAttention = computed(() => {
  return serversStore.servers.filter(server => {
    if (!server.health) return false
    if (server.health.admin_state === 'disabled' || server.health.admin_state === 'quarantined') return false
    // Only unhealthy servers with an actionable remedy need attention
    // Degraded is for transient states (connecting) — not worth alerting
    if (server.health.level === 'unhealthy') return true
    // Degraded only if there's a specific action the user should take
    if (server.health.level === 'degraded' && server.health.action) return true
    return false
  })
})

// --- Quarantine pending tools ---
interface PendingToolEntry {
  serverName: string
  count: number
}
const pendingToolsByServer = ref<PendingToolEntry[]>([])

const serversWithPendingTools = computed(() =>
  pendingToolsByServer.value.filter(entry => entry.count > 0)
)

const totalPendingTools = computed(() =>
  serversWithPendingTools.value.reduce((sum, entry) => sum + entry.count, 0)
)

const loadPendingTools = async () => {
  try {
    const enabledServers = serversStore.servers.filter(s => s.enabled)
    const results: PendingToolEntry[] = []

    const promises = enabledServers.map(async (server) => {
      try {
        const response = await api.getToolApprovals(server.name)
        if (response.success && response.data?.tools) {
          const pendingCount = response.data.tools.filter(
            (t: any) => t.status === 'pending' || t.status === 'changed'
          ).length
          if (pendingCount > 0) {
            results.push({ serverName: server.name, count: pendingCount })
          }
        }
      } catch {
        // Silently ignore per-server failures
      }
    })

    await Promise.all(promises)
    results.sort((a, b) => b.count - a.count)
    pendingToolsByServer.value = results
  } catch {
    // Silently fail
  }
}

// --- Server actions ---
const triggerServerAction = async (serverName: string, action: string) => {
  try {
    switch (action) {
      case 'oauth_login':
        await serversStore.triggerOAuthLogin(serverName)
        systemStore.addToast({ type: 'success', title: 'OAuth Login', message: `OAuth login initiated for ${serverName}` })
        break
      case 'restart':
        await serversStore.restartServer(serverName)
        systemStore.addToast({ type: 'success', title: 'Server Restarted', message: `${serverName} is restarting` })
        break
      case 'enable':
        await serversStore.enableServer(serverName)
        systemStore.addToast({ type: 'success', title: 'Server Enabled', message: `${serverName} has been enabled` })
        break
      default:
        console.warn(`Unknown action: ${action}`)
    }
    setTimeout(() => serversStore.fetchServers(), 1000)
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Action Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  }
}

// --- Add Server handler ---
const handleServerAdded = () => {
  showAddServer.value = false
  serversStore.fetchServers()
  systemStore.addToast({ type: 'success', title: 'Server Added', message: 'New server has been added successfully' })
}

// --- Formatters ---
const formatNumber = (num: number): string => {
  if (num >= 1000000) return `${(num / 1000000).toFixed(1)}M`
  if (num >= 1000) return `${(num / 1000).toFixed(1)}K`
  return num.toString()
}

// --- Pie chart ---
const pieChartColors = [
  '#3b82f6', '#10b981', '#f59e0b', '#ec4899', '#8b5cf6',
  '#06b6d4', '#ef4444', '#14b8a6', '#f97316', '#a855f7',
  '#6366f1', '#84cc16', '#f43f5e', '#0ea5e9', '#22c55e', '#eab308',
]

const pieChartSegments = computed(() => {
  if (!tokenSavingsData.value?.per_server_tool_list_sizes) return []

  const sizes = tokenSavingsData.value.per_server_tool_list_sizes
  const entries = Object.entries(sizes).sort((a, b) => (b[1] as number) - (a[1] as number))
  const total = entries.reduce((sum, [, value]) => sum + (value as number), 0)

  let offset = 0
  return entries.map(([name, value], index) => {
    const numValue = value as number
    const percentage = total > 0 ? (numValue / total) * 100 : 0
    const segment = {
      name,
      value: numValue,
      percentage,
      offset,
      color: pieChartColors[index % pieChartColors.length],
    }
    offset += percentage
    return segment
  })
})

// --- Dashboard hints ---
const dashboardHints = computed<Hint[]>(() => {
  const hints: Hint[] = []

  hints.push({
    icon: '\u{1F4A1}',
    title: 'CLI Commands for Managing MCPProxy',
    description: 'Useful commands for working with MCPProxy',
    sections: [
      {
        title: 'View all servers',
        codeBlock: {
          language: 'bash',
          code: `# List all upstream servers\nmcpproxy upstream list`,
        },
      },
      {
        title: 'Search for tools',
        codeBlock: {
          language: 'bash',
          code: `# Search across all server tools\nmcpproxy tools search "your query"\n\n# List tools from specific server\nmcpproxy tools list --server=server-name`,
        },
      },
      {
        title: 'Connect to AI clients',
        codeBlock: {
          language: 'bash',
          code: `# Register MCPProxy in Claude Desktop\nmcpproxy connect claude-desktop\n\n# List all detected clients\nmcpproxy connect --list`,
        },
      },
    ],
  })

  hints.push({
    icon: '\u{1F916}',
    title: 'Use MCPProxy with LLM Agents',
    description: 'Connect Claude or other LLM agents to MCPProxy',
    sections: [
      {
        title: 'Example LLM prompts',
        list: [
          'Search for tools related to GitHub issues across all my MCP servers',
          'List all available MCP servers and their connection status',
          'Add a new MCP server from npm package @modelcontextprotocol/server-filesystem',
          'Show me statistics about which tools are being used most frequently',
        ],
      },
      {
        title: 'Configure Claude Desktop',
        text: 'Add MCPProxy to your Claude Desktop config:',
        codeBlock: {
          language: 'json',
          code: `{
  "mcpServers": {
    "mcpproxy": {
      "command": "mcpproxy",
      "args": ["serve"],
      "env": {}
    }
  }
}`,
        },
      },
    ],
  })

  return hints
})

// --- Lifecycle ---
onMounted(() => {
  loadClientStatuses()
  loadTokenSavings()
  loadActivitySummary()
  loadSessions()
  loadSecurityStatus()
  // Populate security scanner totals for the Security Scan chip (F-12).
  void refreshSecurityScannerStatus()
  serversStore.fetchServers().then(() => loadPendingTools())

  // Auto-refresh every 30 seconds
  refreshInterval = setInterval(() => {
    loadClientStatuses()
    loadTokenSavings()
    loadActivitySummary()
    loadSessions()
    loadSecurityStatus()
    void refreshSecurityScannerStatus()
    loadPendingTools()
  }, 30000)

  systemStore.connectEventSource()
  serversStore.fetchServers()
})

onUnmounted(() => {
  if (refreshInterval) {
    clearInterval(refreshInterval)
    refreshInterval = null
  }
})
</script>

<style scoped>
/* Hub glow animation when MCPProxy is active — uses drop-shadow to follow the logo shape */
@keyframes hubGlow {
  0%, 100% {
    filter: drop-shadow(0 4px 8px oklch(var(--p) / 0.15)) drop-shadow(0 2px 4px oklch(var(--p) / 0.1));
  }
  50% {
    filter: drop-shadow(0 6px 16px oklch(var(--p) / 0.3)) drop-shadow(0 3px 8px oklch(var(--p) / 0.15));
  }
}

.hub-glow {
  animation: hubGlow 3s ease-in-out infinite;
}
</style>
