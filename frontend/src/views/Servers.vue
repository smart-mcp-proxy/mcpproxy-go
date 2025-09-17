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
      </div>
    </div>

    <!-- Summary Stats -->
    <div class="stats shadow bg-base-100 w-full">
      <div class="stat">
        <div class="stat-title">Total Servers</div>
        <div class="stat-value">{{ serversStore.serverCount.total }}</div>
        <div class="stat-desc">{{ serversStore.serverCount.enabled }} enabled</div>
      </div>

      <div class="stat">
        <div class="stat-title">Connected</div>
        <div class="stat-value text-success">{{ serversStore.serverCount.connected }}</div>
        <div class="stat-desc">{{ Math.round((serversStore.serverCount.connected / serversStore.serverCount.total) * 100) || 0 }}% online</div>
      </div>

      <div class="stat">
        <div class="stat-title">Quarantined</div>
        <div class="stat-value text-warning">{{ serversStore.serverCount.quarantined }}</div>
        <div class="stat-desc">Need security review</div>
      </div>

      <div class="stat">
        <div class="stat-title">Total Tools</div>
        <div class="stat-value text-info">{{ serversStore.totalTools }}</div>
        <div class="stat-desc">Available across all servers</div>
      </div>
    </div>

    <!-- Filters -->
    <div class="flex flex-wrap gap-4 items-center justify-between">
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

    <!-- Loading State -->
    <div v-if="serversStore.loading.loading" class="text-center py-12">
      <span class="loading loading-spinner loading-lg"></span>
      <p class="mt-4">Loading servers...</p>
    </div>

    <!-- Error State -->
    <div v-else-if="serversStore.loading.error" class="alert alert-error">
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

    <!-- Empty State -->
    <div v-else-if="filteredServers.length === 0" class="text-center py-12">
      <svg class="w-24 h-24 mx-auto mb-4 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2" />
      </svg>
      <h3 class="text-xl font-semibold mb-2">No servers found</h3>
      <p class="text-base-content/70 mb-4">
        {{ searchQuery ? 'No servers match your search criteria' : `No ${filter} servers available` }}
      </p>
      <button v-if="searchQuery" @click="searchQuery = ''" class="btn btn-outline">
        Clear Search
      </button>
    </div>

    <!-- Servers Grid -->
    <div v-else class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
      <ServerCard
        v-for="server in filteredServers"
        :key="server.name"
        :server="server"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useServersStore } from '@/stores/servers'
import ServerCard from '@/components/ServerCard.vue'

const serversStore = useServersStore()
const filter = ref<'all' | 'connected' | 'enabled' | 'quarantined'>('all')
const searchQuery = ref('')

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
</script>