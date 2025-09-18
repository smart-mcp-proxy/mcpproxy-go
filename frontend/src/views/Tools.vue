<template>
  <div class="space-y-6">
    <!-- Page Header -->
    <div class="flex justify-between items-center">
      <div>
        <h1 class="text-3xl font-bold">Tools</h1>
        <p class="text-base-content/70 mt-1">Browse and manage MCP tools across all servers</p>
      </div>
      <div class="flex items-center space-x-2">
        <div class="badge badge-outline">{{ totalTools }} tools</div>
        <div class="badge badge-success">{{ connectedServers }} servers</div>
      </div>
    </div>

    <!-- Search and Filters -->
    <div class="card bg-base-100 shadow-md">
      <div class="card-body">
        <div class="flex flex-col lg:flex-row gap-4">
          <!-- Search Input -->
          <div class="flex-1">
            <div class="relative">
              <input
                v-model="searchQuery"
                type="text"
                placeholder="Search tools by name or description..."
                class="input input-bordered w-full pl-10"
                @input="debouncedSearch"
              />
              <svg class="absolute left-3 top-3 w-5 h-5 text-base-content/50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
              </svg>
            </div>
          </div>

          <!-- Server Filter -->
          <div class="lg:w-64">
            <select v-model="selectedServer" class="select select-bordered w-full">
              <option value="">All Servers</option>
              <option v-for="server in availableServers" :key="server" :value="server">
                {{ server }}
              </option>
            </select>
          </div>

          <!-- View Toggle -->
          <div class="btn-group">
            <button
              :class="['btn', viewMode === 'grid' ? 'btn-active' : '']"
              @click="viewMode = 'grid'"
            >
              <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2H6a2 2 0 01-2-2V6zM14 6a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2h-2a2 2 0 01-2-2V6zM4 16a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2H6a2 2 0 01-2-2v-2zM14 16a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2h-2a2 2 0 01-2-2v-2z" />
              </svg>
            </button>
            <button
              :class="['btn', viewMode === 'list' ? 'btn-active' : '']"
              @click="viewMode = 'list'"
            >
              <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 10h16M4 14h16M4 18h16" />
              </svg>
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Loading State -->
    <div v-if="loading" class="text-center py-12">
      <span class="loading loading-spinner loading-lg"></span>
      <p class="mt-4">Loading tools...</p>
    </div>

    <!-- Error State -->
    <div v-else-if="error" class="alert alert-error">
      <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <span>{{ error }}</span>
      <button class="btn btn-sm" @click="loadTools">Retry</button>
    </div>

    <!-- Empty State -->
    <div v-else-if="paginatedTools.length === 0" class="text-center py-12">
      <svg class="w-16 h-16 mx-auto mb-4 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
      </svg>
      <h3 class="text-xl font-semibold mb-2">
        {{ searchQuery ? 'No tools found' : 'No tools available' }}
      </h3>
      <p class="text-base-content/70 mb-4">
        {{ searchQuery ? 'Try adjusting your search or filter criteria.' : 'Connect some MCP servers to see their tools here.' }}
      </p>
      <div class="space-x-2">
        <button v-if="searchQuery" class="btn btn-outline" @click="clearSearch">
          Clear Search
        </button>
        <router-link to="/servers" class="btn btn-primary">
          Manage Servers
        </router-link>
      </div>
    </div>

    <!-- Tools Display -->
    <div v-else>
      <!-- Grid View -->
      <div v-if="viewMode === 'grid'" class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
        <div
          v-for="tool in paginatedTools"
          :key="`${tool.server}:${tool.name}`"
          class="card bg-base-100 shadow-md hover:shadow-lg transition-shadow"
        >
          <div class="card-body">
            <div class="flex items-start justify-between">
              <h3 class="card-title text-lg">{{ tool.name }}</h3>
              <div class="badge badge-secondary badge-sm">{{ tool.server }}</div>
            </div>
            <p class="text-sm text-base-content/70 line-clamp-3">
              {{ tool.description || 'No description available' }}
            </p>
            <div class="card-actions justify-end mt-4">
              <button
                class="btn btn-sm btn-outline"
                @click="viewToolDetails(tool)"
              >
                View Details
              </button>
            </div>
          </div>
        </div>
      </div>

      <!-- List View -->
      <div v-else class="space-y-4">
        <div
          v-for="tool in paginatedTools"
          :key="`${tool.server}:${tool.name}`"
          class="card bg-base-100 shadow-md"
        >
          <div class="card-body py-4">
            <div class="flex items-center justify-between">
              <div class="flex-1">
                <div class="flex items-center space-x-3">
                  <h3 class="text-lg font-semibold">{{ tool.name }}</h3>
                  <div class="badge badge-secondary badge-sm">{{ tool.server }}</div>
                </div>
                <p class="text-sm text-base-content/70 mt-1">
                  {{ tool.description || 'No description available' }}
                </p>
              </div>
              <div class="flex items-center space-x-2">
                <button
                  class="btn btn-sm btn-outline"
                  @click="viewToolDetails(tool)"
                >
                  Details
                </button>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Pagination -->
      <div v-if="filteredTools.length > itemsPerPage" class="flex justify-center mt-8">
        <div class="btn-group">
          <button
            :class="['btn', currentPage === 1 ? 'btn-disabled' : '']"
            @click="currentPage = Math.max(1, currentPage - 1)"
          >
            Previous
          </button>
          <span class="btn btn-disabled">
            Page {{ currentPage }} of {{ totalPages }}
          </span>
          <button
            :class="['btn', currentPage === totalPages ? 'btn-disabled' : '']"
            @click="currentPage = Math.min(totalPages, currentPage + 1)"
          >
            Next
          </button>
        </div>
      </div>
    </div>

    <!-- Tool Details Modal -->
    <div v-if="selectedTool" class="modal modal-open">
      <div class="modal-box max-w-4xl">
        <h3 class="font-bold text-lg mb-4">{{ selectedTool.name }}</h3>

        <div class="space-y-4">
          <div>
            <label class="block text-sm font-medium mb-1">Server</label>
            <div class="badge badge-secondary">{{ selectedTool.server }}</div>
          </div>

          <div>
            <label class="block text-sm font-medium mb-1">Description</label>
            <p class="text-sm">{{ selectedTool.description || 'No description available' }}</p>
          </div>

          <div v-if="selectedTool.input_schema">
            <label class="block text-sm font-medium mb-1">Input Schema</label>
            <div class="mockup-code">
              <pre><code>{{ JSON.stringify(selectedTool.input_schema, null, 2) }}</code></pre>
            </div>
          </div>
        </div>

        <div class="modal-action">
          <button class="btn" @click="selectedTool = null">Close</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useServersStore } from '@/stores/servers'
import { useSystemStore } from '@/stores/system'
import type { Tool } from '@/types'
import api from '@/services/api'

const serversStore = useServersStore()
const systemStore = useSystemStore()

// State
const allTools = ref<Tool[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const searchQuery = ref('')
const selectedServer = ref('')
const viewMode = ref<'grid' | 'list'>('grid')
const selectedTool = ref<Tool | null>(null)
const currentPage = ref(1)
const itemsPerPage = ref(12)

// Computed
const totalTools = computed(() => allTools.value.length)
const connectedServers = computed(() => serversStore.serverCount.connected)

const availableServers = computed(() => {
  const servers = new Set(allTools.value.map(tool => tool.server))
  return Array.from(servers).sort()
})

const filteredTools = computed(() => {
  let tools = allTools.value

  // Filter by server
  if (selectedServer.value) {
    tools = tools.filter(tool => tool.server === selectedServer.value)
  }

  // Filter by search query
  if (searchQuery.value) {
    const query = searchQuery.value.toLowerCase()
    tools = tools.filter(tool =>
      tool.name.toLowerCase().includes(query) ||
      tool.description?.toLowerCase().includes(query) ||
      tool.server.toLowerCase().includes(query)
    )
  }

  return tools
})

const paginatedTools = computed(() => {
  const start = (currentPage.value - 1) * itemsPerPage.value
  return filteredTools.value.slice(start, start + itemsPerPage.value)
})

const totalPages = computed(() => Math.ceil(filteredTools.value.length / itemsPerPage.value))

// Debounced search
let searchTimeout: number | null = null
const debouncedSearch = () => {
  if (searchTimeout) {
    clearTimeout(searchTimeout)
  }
  searchTimeout = setTimeout(() => {
    currentPage.value = 1 // Reset to first page on search
  }, 300)
}

// Methods
async function loadTools() {
  loading.value = true
  error.value = null
  allTools.value = []

  try {
    // Load servers first
    await serversStore.fetchServers()

    // Get tools from all connected servers
    const connectedServersList = serversStore.servers.filter(s => s.connected && s.enabled)

    for (const server of connectedServersList) {
      try {
        const response = await api.getServerTools(server.name)
        if (response.success && response.data) {
          const serverTools = response.data.tools.map(tool => ({
            ...tool,
            server: server.name
          }))
          allTools.value.push(...serverTools)
        }
      } catch (err) {
        console.warn(`Failed to load tools from server ${server.name}:`, err)
      }
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to load tools'
  } finally {
    loading.value = false
  }
}

function viewToolDetails(tool: Tool) {
  selectedTool.value = tool
}

function clearSearch() {
  searchQuery.value = ''
  currentPage.value = 1
}

// Watch for server changes and reload tools
watch(() => serversStore.serverCount.connected, () => {
  if (serversStore.serverCount.connected > 0) {
    loadTools()
  }
})

// Watch for search/filter changes to reset pagination
watch([searchQuery, selectedServer], () => {
  currentPage.value = 1
})

// Load tools on mount
onMounted(() => {
  loadTools()
})
</script>

<style scoped>
.line-clamp-3 {
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
</style>