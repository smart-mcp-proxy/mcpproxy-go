<template>
  <div class="space-y-6">
    <!-- Page Header -->
    <div class="flex justify-between items-center">
      <div>
        <h1 class="text-3xl font-bold">Repositories</h1>
        <p class="text-base-content/70 mt-1">Browse and discover MCP server repositories</p>
      </div>
    </div>

    <!-- Registry Selector & Search -->
    <div class="card bg-base-100 shadow-md">
      <div class="card-body">
        <div class="flex flex-col sm:flex-row gap-4">
          <!-- Registry Selector -->
          <div class="form-control flex-1">
            <label class="label">
              <span class="label-text font-semibold">Select Registry</span>
            </label>
            <select
              v-model="selectedRegistry"
              class="select select-bordered w-full"
              @change="handleRegistryChange"
              :disabled="loadingRegistries"
            >
              <option disabled value="">Choose a registry...</option>
              <option v-for="registry in registries" :key="registry.id" :value="registry.id">
                {{ registry.name }}
              </option>
            </select>
          </div>

          <!-- Search Input -->
          <div class="form-control flex-1">
            <label class="label">
              <span class="label-text font-semibold">Search Servers</span>
            </label>
            <input
              v-model="searchQuery"
              type="text"
              placeholder="Search by name or description..."
              class="input input-bordered w-full"
              @input="handleSearchInput"
              :disabled="!selectedRegistry || loadingServers"
            />
          </div>

          <!-- Search Button -->
          <div class="form-control sm:self-end">
            <button
              @click="searchServers"
              class="btn btn-primary"
              :disabled="!selectedRegistry || loadingServers"
            >
              <span v-if="loadingServers" class="loading loading-spinner loading-sm"></span>
              <span v-else>Search</span>
            </button>
          </div>
        </div>

        <!-- Registry Info -->
        <div v-if="selectedRegistryInfo" class="alert alert-info mt-4">
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <div>
            <p class="font-semibold">{{ selectedRegistryInfo.name }}</p>
            <p class="text-sm">{{ selectedRegistryInfo.description }}</p>
          </div>
        </div>
      </div>
    </div>

    <!-- Loading State -->
    <div v-if="loadingServers" class="card bg-base-100 shadow-md">
      <div class="card-body">
        <div class="flex flex-col items-center justify-center py-12">
          <div class="loading loading-spinner loading-lg mb-4"></div>
          <p class="text-base-content/70">Searching servers...</p>
        </div>
      </div>
    </div>

    <!-- Error State -->
    <div v-else-if="error" class="alert alert-error">
      <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <span>{{ error }}</span>
    </div>

    <!-- Server Results -->
    <div v-else-if="servers.length > 0" class="space-y-4">
      <div class="flex justify-between items-center">
        <p class="text-sm text-base-content/70">Found {{ servers.length }} server(s)</p>
      </div>

      <!-- Server Cards -->
      <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
        <div v-for="server in servers" :key="server.id" class="card bg-base-100 shadow-md hover:shadow-lg transition-shadow">
          <div class="card-body">
            <div class="flex justify-between items-start">
              <h3 class="card-title text-lg">{{ server.name }}</h3>
              <div class="badge badge-outline badge-sm">{{ server.registry }}</div>
            </div>

            <p class="text-sm text-base-content/70 line-clamp-3">
              {{ server.description }}
            </p>

            <!-- Repository Info Badges -->
            <div class="flex flex-wrap gap-2 mt-2">
              <div v-if="server.repository_info?.npm?.exists" class="badge badge-success badge-sm">
                <svg class="w-3 h-3 mr-1" fill="currentColor" viewBox="0 0 24 24">
                  <path d="M0 0h24v24H0z" fill="none"/>
                  <path d="M9 16.17L4.83 12l-1.42 1.41L9 19 21 7l-1.41-1.41z"/>
                </svg>
                NPM
              </div>
              <div v-if="server.url" class="badge badge-info badge-sm">
                <svg class="w-3 h-3 mr-1" fill="currentColor" viewBox="0 0 24 24">
                  <path d="M0 0h24v24H0z" fill="none"/>
                  <path d="M3.9 12c0-1.71 1.39-3.1 3.1-3.1h4V7H7c-2.76 0-5 2.24-5 5s2.24 5 5 5h4v-1.9H7c-1.71 0-3.1-1.39-3.1-3.1zM8 13h8v-2H8v2zm9-6h-4v1.9h4c1.71 0 3.1 1.39 3.1 3.1s-1.39 3.1-3.1 3.1h-4V17h4c2.76 0 5-2.24 5-5s-2.24-5-5-5z"/>
                </svg>
                Remote
              </div>
            </div>

            <!-- Install Command -->
            <div v-if="server.installCmd" class="mt-3">
              <div class="flex items-center justify-between bg-base-200 rounded px-2 py-1">
                <code class="text-xs flex-1 overflow-x-auto">{{ server.installCmd }}</code>
                <button
                  @click="copyToClipboard(server.installCmd)"
                  class="btn btn-ghost btn-xs ml-2"
                  title="Copy install command"
                >
                  <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
                  </svg>
                </button>
              </div>
            </div>

            <!-- Actions -->
            <div class="card-actions justify-end mt-4">
              <button
                v-if="server.source_code_url"
                @click="openURL(server.source_code_url)"
                class="btn btn-ghost btn-sm"
              >
                <svg class="w-4 h-4 mr-1" fill="currentColor" viewBox="0 0 24 24">
                  <path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z"/>
                </svg>
                Source
              </button>
              <button
                @click="addServer(server)"
                class="btn btn-primary btn-sm"
                :disabled="addingServerId === server.id"
              >
                <span v-if="addingServerId === server.id" class="loading loading-spinner loading-xs"></span>
                <span v-else>Add to MCP</span>
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Empty State (no search yet) -->
    <div v-else-if="!selectedRegistry" class="card bg-base-100 shadow-md">
      <div class="card-body">
        <div class="text-center py-12">
          <svg class="w-16 h-16 mx-auto mb-4 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 11H5m14 0a2 2 0 012 2v6a2 2 0 01-2 2H5a2 2 0 01-2-2v-6a2 2 0 012-2m14 0V9a2 2 0 00-2-2M5 11V9a2 2 0 012-2m0 0V5a2 2 0 012-2h6a2 2 0 012 2v2M7 7h10" />
          </svg>
          <h3 class="text-xl font-semibold mb-2">Select a Registry</h3>
          <p class="text-base-content/70">Choose a registry from the dropdown to start browsing MCP servers.</p>
        </div>
      </div>
    </div>

    <!-- Empty State (no results) -->
    <div v-else class="card bg-base-100 shadow-md">
      <div class="card-body">
        <div class="text-center py-12">
          <svg class="w-16 h-16 mx-auto mb-4 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9.172 16.172a4 4 0 015.656 0M9 10h.01M15 10h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <h3 class="text-xl font-semibold mb-2">No Servers Found</h3>
          <p class="text-base-content/70">Try adjusting your search query or select a different registry.</p>
        </div>
      </div>
    </div>

    <!-- Success Toast -->
    <div v-if="showSuccessToast" class="toast toast-end">
      <div class="alert alert-success">
        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
        </svg>
        <span>{{ successMessage }}</span>
      </div>
    </div>

    <!-- Hints Panel (Bottom of Page) -->
    <CollapsibleHintsPanel :hints="repositoriesHints" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import api from '@/services/api'
import CollapsibleHintsPanel from '@/components/CollapsibleHintsPanel.vue'
import type { Hint } from '@/components/CollapsibleHintsPanel.vue'
import type { Registry, RepositoryServer } from '@/types'

// State
const registries = ref<Registry[]>([])
const selectedRegistry = ref<string>('')
const searchQuery = ref<string>('')
const servers = ref<RepositoryServer[]>([])
const loadingRegistries = ref(false)
const loadingServers = ref(false)
const error = ref<string | null>(null)
const addingServerId = ref<string | null>(null)
const showSuccessToast = ref(false)
const successMessage = ref('')

let searchDebounceTimer: ReturnType<typeof setTimeout> | null = null

// Computed
const selectedRegistryInfo = computed(() => {
  return registries.value.find(r => r.id === selectedRegistry.value)
})

const repositoriesHints = computed<Hint[]>(() => {
  return [
    {
      icon: '📦',
      title: 'Discover MCP Servers',
      description: 'Browse official and community MCP servers from multiple registries',
      sections: [
        {
          title: 'How to use',
          list: [
            'Select a registry from the dropdown menu',
            'Search for servers by name or description',
            'Click "Add to MCP" to install a server',
            'View source code and installation commands for each server'
          ]
        }
      ]
    },
    {
      icon: '🤖',
      title: 'LLM Agent Integration',
      description: 'Let AI agents help you discover and install MCP servers',
      sections: [
        {
          title: 'Example prompts',
          list: [
            'Find and add MCP servers for working with GitHub',
            'Install the best MCP server for file system operations',
            'Search for database-related MCP servers and add them',
            'Discover Slack integration servers and configure them'
          ]
        }
      ]
    },
    {
      icon: '💡',
      title: 'Installation Tips',
      description: 'Servers can be installed via npm, pip, or connected remotely',
      sections: [
        {
          title: 'Server types',
          list: [
            'NPM packages: Installed with npx command',
            'Python packages: Installed with uvx or pipx',
            'Remote servers: Connected via HTTP endpoints',
            'Docker containers: Run in isolated environments'
          ]
        }
      ]
    }
  ]
})

// Methods
async function loadRegistries() {
  loadingRegistries.value = true
  error.value = null

  try {
    const response = await api.listRegistries()
    if (response.success && response.data) {
      registries.value = response.data.registries
    } else {
      error.value = response.error || 'Failed to load registries'
    }
  } catch (err) {
    error.value = 'Failed to load registries: ' + (err as Error).message
  } finally {
    loadingRegistries.value = false
  }
}

async function searchServers() {
  if (!selectedRegistry.value) return

  loadingServers.value = true
  error.value = null

  try {
    const response = await api.searchRegistryServers(selectedRegistry.value, {
      query: searchQuery.value,
      limit: 20
    })

    if (response.success && response.data) {
      servers.value = response.data.servers
    } else {
      error.value = response.error || 'Failed to search servers'
      servers.value = []
    }
  } catch (err) {
    error.value = 'Failed to search servers: ' + (err as Error).message
    servers.value = []
  } finally {
    loadingServers.value = false
  }
}

function handleRegistryChange() {
  searchQuery.value = ''
  servers.value = []
  error.value = null
  if (selectedRegistry.value) {
    searchServers()
  }
}

function handleSearchInput() {
  if (searchDebounceTimer) {
    clearTimeout(searchDebounceTimer)
  }

  searchDebounceTimer = setTimeout(() => {
    if (selectedRegistry.value) {
      searchServers()
    }
  }, 500)
}

async function addServer(server: RepositoryServer) {
  addingServerId.value = server.id
  error.value = null

  try {
    const response = await api.addServerFromRepository(server)
    if (response.success) {
      showToast(`Server "${server.name}" added successfully!`)
    } else {
      error.value = response.error || 'Failed to add server'
    }
  } catch (err) {
    error.value = 'Failed to add server: ' + (err as Error).message
  } finally {
    addingServerId.value = null
  }
}

function copyToClipboard(text: string) {
  navigator.clipboard.writeText(text)
  showToast('Installation command copied to clipboard!')
}

function openURL(url: string) {
  window.open(url, '_blank')
}

function showToast(message: string) {
  successMessage.value = message
  showSuccessToast.value = true
  setTimeout(() => {
    showSuccessToast.value = false
  }, 3000)
}

// Lifecycle
onMounted(() => {
  loadRegistries()
})
</script>
