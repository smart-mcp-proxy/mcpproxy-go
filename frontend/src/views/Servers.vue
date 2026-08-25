<template>
  <div class="space-y-6">
    <!-- Page Header -->
    <div class="flex justify-between items-center">
      <div>
        <h1 class="text-3xl font-bold">Servers</h1>
        <p class="text-base-content/70 mt-1">Manage upstream MCP servers</p>
      </div>
      <div class="flex items-center space-x-2">
        <button
          @click="refreshServers"
          :disabled="serversStore.loading.loading"
          class="btn btn-outline"
        >
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
          </svg>
          <span v-if="serversStore.loading.loading" class="loading loading-spinner loading-sm"></span>
          {{ serversStore.loading.loading ? 'Refreshing...' : 'Refresh' }}
        </button>
        <button
          v-if="hasEnabledScanners()"
          @click="scanAllServers"
          :disabled="scanAllRunning"
          class="btn btn-primary"
        >
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z" />
          </svg>
          <span v-if="scanAllRunning" class="loading loading-spinner loading-sm"></span>
          {{ scanAllRunning ? 'Scanning...' : 'Scan All' }}
        </button>
      </div>
    </div>

    <!-- Everything between the header and the server grid describes a list that
         does not exist yet on a fresh install: four zero-valued stat tiles,
         four zero-count filter pills and a search box with nothing to search.
         They are hidden until the first server is configured so the empty
         state below is the only thing on the page. -->
    <!-- Summary Stats — clickable cards drive the filter row (issue #436) -->
    <div v-if="hasServers" class="stats shadow bg-base-100 w-full">
      <button
        type="button"
        data-test="kpi-card-total"
        :class="['stat text-left transition-colors cursor-pointer hover:bg-base-200/60', filter === 'all' ? 'bg-base-200 ring-2 ring-inset ring-primary/40' : '']"
        :aria-pressed="filter === 'all'"
        @click="filter = 'all'"
      >
        <div class="stat-title">Total Servers</div>
        <div class="stat-value">{{ serversStore.serverCount.total }}</div>
        <div class="stat-desc">{{ serversStore.serverCount.enabled }} enabled</div>
      </button>

      <button
        type="button"
        data-test="kpi-card-connected"
        :class="['stat text-left transition-colors cursor-pointer hover:bg-base-200/60', filter === 'connected' ? 'bg-base-200 ring-2 ring-inset ring-primary/40' : '']"
        :aria-pressed="filter === 'connected'"
        @click="filter = filter === 'connected' ? 'all' : 'connected'"
      >
        <div class="stat-title">Connected</div>
        <div class="stat-value text-success">{{ serversStore.serverCount.connected }}</div>
        <div class="stat-desc">{{ Math.round((serversStore.serverCount.connected / serversStore.serverCount.total) * 100) || 0 }}% online</div>
      </button>

      <button
        type="button"
        data-test="kpi-card-quarantined"
        :class="['stat text-left transition-colors cursor-pointer hover:bg-base-200/60', filter === 'quarantined' ? 'bg-base-200 ring-2 ring-inset ring-primary/40' : '']"
        :aria-pressed="filter === 'quarantined'"
        @click="filter = filter === 'quarantined' ? 'all' : 'quarantined'"
      >
        <div class="stat-title">Quarantined</div>
        <div class="stat-value text-warning">{{ serversStore.serverCount.quarantined }}</div>
        <div class="stat-desc">Need security review</div>
      </button>

      <div class="stat" data-test="kpi-card-total-tools">
        <div class="stat-title">Total Tools</div>
        <div class="stat-value text-info">{{ serversStore.totalTools }}</div>
        <div class="stat-desc">Available across all servers</div>
      </div>
    </div>

    <!-- Filters -->
    <div v-if="hasServers" class="flex flex-wrap gap-4 items-center justify-between">
      <div class="flex flex-wrap gap-2">
        <button
          @click="filter = 'all'"
          :class="['btn btn-sm', filter === 'all' ? 'btn-primary' : 'btn-outline']"
        >
          All ({{ serversStore.servers.length }})
        </button>
        <button
          @click="filter = 'connected'"
          :class="['btn btn-sm', filter === 'connected' ? 'btn-primary' : 'btn-outline']"
        >
          Connected ({{ serversStore.connectedServers.length }})
        </button>
        <button
          @click="filter = 'enabled'"
          :class="['btn btn-sm', filter === 'enabled' ? 'btn-primary' : 'btn-outline']"
        >
          Enabled ({{ serversStore.enabledServers.length }})
        </button>
        <button
          @click="filter = 'quarantined'"
          :class="['btn btn-sm', filter === 'quarantined' ? 'btn-primary' : 'btn-outline']"
        >
          Quarantined ({{ serversStore.quarantinedServers.length }})
        </button>
      </div>

      <div class="form-control">
        <input
          v-model="searchQuery"
          type="text"
          placeholder="Search servers..."
          class="input input-bordered input-sm w-64"
        />
      </div>
    </div>

    <!-- A refresh failed but we still hold a list. Say so without throwing the
         servers away — the cached grid is still the most useful thing on the
         page, and the failure is about freshness, not about the servers. This
         is additive, so it sits outside the state chain below. -->
    <div
      v-if="hasServers && serversStore.loading.error"
      class="alert alert-warning"
      data-test="servers-refresh-error"
    >
      <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <div class="flex-1 text-sm">
        Couldn't refresh the server list — showing the last known state.
        <span class="opacity-70">{{ serversStore.loading.error }}</span>
      </div>
      <button @click="refreshServers" class="btn btn-sm">Retry</button>
    </div>

    <!-- Loading State -->
    <div v-if="serversStore.loading.loading" class="text-center py-12">
      <span class="loading loading-spinner loading-lg"></span>
      <p class="mt-4">Loading servers...</p>
    </div>

    <!-- Error State: the load failed and we have nothing to fall back on. -->
    <div v-else-if="serversStore.loading.error && !hasServers" class="alert alert-error">
      <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <div>
        <h3 class="font-bold">Failed to load servers</h3>
        <div class="text-sm">{{ serversStore.loading.error }}</div>
      </div>
      <button @click="refreshServers" class="btn btn-sm">
        Try Again
      </button>
    </div>

    <!-- First-run empty state: no servers configured at all. This is the page a
         new user lands on after closing the setup wizard, so it has to offer
         the next action rather than restate that the list is empty. Kept
         distinct from the filter/search empty state below — "you have nothing
         yet" and "nothing matched" need different answers. -->
    <div v-else-if="isFirstRun" class="text-center py-12" data-test="servers-first-run-empty">
      <svg class="w-24 h-24 mx-auto mb-4 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2" />
      </svg>
      <h3 class="text-xl font-semibold mb-2">No servers yet</h3>
      <p class="text-base-content/70 mb-4 max-w-lg mx-auto">
        Upstream MCP servers are where your tools come from. Add one and MCPProxy
        indexes its tools, so your AI agent can find them without loading every
        schema into its context.
      </p>
      <div class="flex flex-wrap gap-2 justify-center">
        <button
          @click="showAddServer = true"
          class="btn btn-primary"
          data-test="servers-empty-add"
        >
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
          </svg>
          Add Server
        </button>
        <router-link
          to="/repositories"
          class="btn btn-outline"
          data-test="servers-empty-registry"
        >
          Browse Registry
        </router-link>
      </div>
      <p class="mt-4 text-sm">
        <button
          type="button"
          class="link link-primary"
          data-test="servers-empty-import"
          @click="openImportWizard"
        >
          Import from your AI client configs
        </button>
      </p>
    </div>

    <!-- Nothing to show and no list has arrived. This view does not fetch on
         mount — App.vue and the Dashboard do — so on a direct load there is a
         window where `loading` is still false and `servers` is still empty,
         and neither empty state is true yet. Say "not yet" rather than pick
         one of them and be wrong. Servers we already hold are a list by
         definition, so this never gates the grid below. -->
    <div
      v-else-if="!hasServers && !serversStore.loaded"
      class="text-center py-12"
      data-test="servers-pending"
    >
      <span class="loading loading-spinner loading-lg"></span>
    </div>

    <!-- Filter/search empty state: servers exist, none match the current view. -->
    <div v-else-if="filteredServers.length === 0" class="text-center py-12" data-test="servers-filter-empty">
      <svg class="w-24 h-24 mx-auto mb-4 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2" />
      </svg>
      <h3 class="text-xl font-semibold mb-2">No servers found</h3>
      <p class="text-base-content/70 mb-4">
        {{ searchQuery ? 'No servers match your search criteria' : `No ${filter === 'all' ? '' : filter} servers available`.replace(/\s+/g, ' ').trim() }}
      </p>
      <button v-if="searchQuery" @click="searchQuery = ''" class="btn btn-outline">
        Clear Search
      </button>
      <button v-else @click="filter = 'all'" class="btn btn-outline">
        Show all servers
      </button>
    </div>

    <!-- Servers Grid with Smooth Transitions -->
    <TransitionGroup
      v-else
      name="server-list"
      tag="div"
      class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6"
    >
      <ServerCard
        v-for="server in filteredServers"
        :key="server.name"
        :server="server"
        v-memo="[
          server.connected,
          server.connecting,
          server.enabled,
          server.quarantined,
          server.tool_count,
          server.last_error,
          server.authenticated,
          server.trust_mode,
          server.quarantine?.pending_count,
          server.quarantine?.changed_count
        ]"
      />
    </TransitionGroup>

    <!-- Hints Panel (Bottom of Page) -->
    <CollapsibleHintsPanel :hints="serversHints" />

    <AddServerModal :show="showAddServer" @close="showAddServer = false" @added="onServerAdded" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useServersStore } from '@/stores/servers'
import { useSystemStore } from '@/stores/system'
import { useOnboardingStore } from '@/stores/onboarding'
import api from '@/services/api'
import ServerCard from '@/components/ServerCard.vue'
import AddServerModal from '@/components/AddServerModal.vue'
import CollapsibleHintsPanel from '@/components/CollapsibleHintsPanel.vue'
import type { Hint } from '@/components/CollapsibleHintsPanel.vue'
import { useSecurityScannerStatus } from '@/composables/useSecurityScannerStatus'

const serversStore = useServersStore()
const systemStore = useSystemStore()
const onboardingStore = useOnboardingStore()
const router = useRouter()
const filter = ref<'all' | 'connected' | 'enabled' | 'quarantined'>('all')
const searchQuery = ref('')
const scanAllRunning = ref(false)
const showAddServer = ref(false)
const { hasEnabledScanners } = useSecurityScannerStatus()

// The page chrome (stat tiles, filter pills, search) only describes a list that
// exists. `servers.length` — not `loaded` — is the right gate: it is also false
// while the very first fetch is in flight, so a fresh install never flashes a
// row of zeroes before the empty state resolves.
const hasServers = computed(() => serversStore.servers.length > 0)

// Telling the user "you have no servers" is a claim, and only a list that
// actually arrived can support it. `loaded` (set once any successful list has
// been applied) is what distinguishes "none configured" from "we don't know
// yet" — `servers` starts empty either way.
const isFirstRun = computed(() => serversStore.loaded && serversStore.servers.length === 0)

function onServerAdded() {
  showAddServer.value = false
  void serversStore.fetchServers()
}

// The setup wizard is mounted by Dashboard.vue, so opening it from here means
// navigating there first; the store carries the tab request across the hop.
function openImportWizard() {
  onboardingStore.openWizard('servers')
  void router.push('/')
}

const filteredServers = computed(() => {
  let servers = serversStore.servers

  // Apply filter
  switch (filter.value) {
    case 'connected':
      servers = serversStore.connectedServers
      break
    case 'enabled':
      servers = serversStore.enabledServers
      break
    case 'quarantined':
      servers = serversStore.quarantinedServers
      break
    default:
      // 'all' - no additional filtering
      break
  }

  // Apply search
  if (searchQuery.value) {
    const query = searchQuery.value.toLowerCase()
    servers = servers.filter(server =>
      server.name.toLowerCase().includes(query) ||
      server.url?.toLowerCase().includes(query) ||
      server.command?.toLowerCase().includes(query)
    )
  }

  return servers
})

async function refreshServers() {
  await serversStore.fetchServers()
}

async function scanAllServers() {
  scanAllRunning.value = true
  try {
    const res = await api.scanAll()
    if (res.success) {
      systemStore.addToast({
        type: 'success',
        title: 'Batch Scan Started',
        message: 'Scanning all servers. Check the Security page for progress.',
      })
    } else {
      systemStore.addToast({
        type: 'error',
        title: 'Scan Failed',
        message: res.error || 'Failed to start batch scan',
      })
    }
  } catch (e: any) {
    systemStore.addToast({
      type: 'error',
      title: 'Scan Failed',
      message: e.message || 'Failed to start batch scan',
    })
  } finally {
    scanAllRunning.value = false
  }
}

// Servers hints
const serversHints = computed<Hint[]>(() => {
  return [
    {
      icon: '➕',
      title: 'Add New MCP Servers',
      description: 'Multiple ways to add servers to MCPProxy',
      sections: [
        {
          title: 'Add HTTP/HTTPS server',
          codeBlock: {
            language: 'bash',
            code: `# Add a remote MCP server\nmcpproxy call tool --tool-name=upstream_servers \\\n  --json_args='{"operation":"add","name":"my-server","url":"https://api.example.com/mcp","protocol":"http","enabled":true}'`
          }
        },
        {
          title: 'Add stdio server (npx)',
          codeBlock: {
            language: 'bash',
            code: `# Add an npm-based MCP server\nmcpproxy call tool --tool-name=upstream_servers \\\n  --json_args='{"operation":"add","name":"filesystem","command":"npx","args_json":"[\\"@modelcontextprotocol/server-filesystem\\"]","protocol":"stdio","enabled":true}'`
          }
        },
        {
          title: 'Add stdio server (uvx)',
          codeBlock: {
            language: 'bash',
            code: `# Add a Python-based MCP server\nmcpproxy call tool --tool-name=upstream_servers \\\n  --json_args='{"operation":"add","name":"python-server","command":"uvx","args_json":"[\\"mcp-server-package\\"]","protocol":"stdio","enabled":true}'`
          }
        }
      ]
    },
    {
      icon: '🔧',
      title: 'Manage Servers via CLI',
      description: 'Common server management operations',
      sections: [
        {
          title: 'List all servers',
          codeBlock: {
            language: 'bash',
            code: `# View all upstream servers\nmcpproxy upstream list`
          }
        },
        {
          title: 'Enable/disable server',
          codeBlock: {
            language: 'bash',
            code: `# Disable a server\nmcpproxy call tool --tool-name=upstream_servers \\\n  --json_args='{"operation":"update","name":"server-name","enabled":false}'\n\n# Enable a server\nmcpproxy call tool --tool-name=upstream_servers \\\n  --json_args='{"operation":"update","name":"server-name","enabled":true}'`
          }
        },
        {
          title: 'Remove server',
          codeBlock: {
            language: 'bash',
            code: `# Delete a server\nmcpproxy call tool --tool-name=upstream_servers \\\n  --json_args='{"operation":"delete","name":"server-name"}'`
          }
        }
      ]
    },
    {
      icon: '🤖',
      title: 'Use LLM Agents to Manage Servers',
      description: 'Let AI agents help you configure MCPProxy',
      sections: [
        {
          title: 'Example LLM prompts',
          list: [
            'Add the GitHub MCP server from @modelcontextprotocol/server-github to my configuration',
            'Show me all quarantined servers and help me review them',
            'Disable all servers that haven\'t been used in the last 24 hours',
            'Find and add MCP servers for working with Slack'
          ]
        }
      ]
    }
  ]
})
</script>
