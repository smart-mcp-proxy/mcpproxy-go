<template>
  <header class="bg-base-100 border-b border-base-300 sticky top-0 z-30">
    <div class="flex items-center justify-between px-6 py-4 max-w-full">
      <!-- Left: Mobile menu toggle + Search + Add Server -->
<div class="flex items-center space-x-3 flex-1 min-w-0 overflow-x-hidden">
        <!-- Mobile menu toggle -->
        <label for="sidebar-drawer" class="btn btn-ghost btn-square lg:hidden">
          <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 12h16M4 18h16" />
          </svg>
        </label>

        <!-- Search Box with Button -->
        <div class="flex items-center space-x-2 flex-1 max-w-2xl min-w-0">
          <div class="relative flex-1">
            <input
              type="text"
              placeholder="Search tools, servers..."
              class="input input-bordered w-full pr-3"
              v-model="searchQuery"
              @keydown.enter="handleSearch"
            />
          </div>
          <button
            @click="handleSearch"
            class="btn btn-primary"
            :disabled="!searchQuery.trim()"
          >
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
            </svg>
            <span class="hidden sm:inline ml-2">Search</span>
          </button>
        </div>

        <!-- Add Server Button -->
        <button @click="showAddServerModal = true" class="btn btn-primary">
          <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
          </svg>
          <span class="hidden sm:inline ml-2">{{ addServerLabel }}</span>
        </button>
      </div>

      <!-- Right: Stats + Proxy Info -->
      <div class="hidden md:flex items-center space-x-3 flex-shrink-0">
        <!-- Servers -->
        <div class="flex items-center space-x-2 px-3 py-2 bg-base-200 rounded-lg text-sm">
          <div
            :class="[
              'w-2 h-2 rounded-full',
              systemStore.isRunning ? 'bg-success animate-pulse' : 'bg-error'
            ]"
          />
          <span class="font-bold">{{ serversStore.serverCount.connected }}</span>
          <span class="opacity-60">/</span>
          <span>{{ serversStore.serverCount.total }}</span>
          <span class="text-xs opacity-60">Servers</span>
        </div>

        <!-- Tools -->
        <div class="flex items-center space-x-2 px-3 py-2 bg-base-200 rounded-lg text-sm">
          <span class="font-bold">{{ serversStore.totalTools }}</span>
          <span class="text-xs opacity-60">Tools</span>
        </div>

        <!-- Routing Mode -->
        <div class="flex items-center space-x-2 px-3 py-2 bg-base-200 rounded-lg text-sm">
          <span class="text-xs opacity-60">Mode:</span>
          <span class="font-medium">{{ routingModeLabel }}</span>
        </div>

        <!-- MCP Endpoints Dropdown -->
        <div v-if="systemStore.listenAddr" class="relative">
          <button
            @click="showEndpoints = !showEndpoints"
            class="flex items-center space-x-2 px-3 py-2 bg-base-200 rounded-lg cursor-pointer hover:bg-base-300 transition-colors"
          >
            <span class="text-xs font-medium opacity-60">MCP:</span>
            <code class="text-xs font-mono">{{ systemStore.listenAddr }}</code>
            <svg class="w-3 h-3 opacity-60 transition-transform" :class="{ 'rotate-180': showEndpoints }" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
            </svg>
          </button>
          <div
            v-if="showEndpoints"
            class="absolute right-0 top-full mt-2 p-3 shadow-lg bg-base-100 rounded-box w-96 border border-base-300 z-50"
          >
            <div class="text-xs font-semibold opacity-60 mb-2 px-1">MCP Endpoints</div>
            <div class="space-y-1">
              <div
                v-for="ep in mcpEndpoints"
                :key="ep.path"
                class="flex items-center justify-between px-2 py-1.5 rounded hover:bg-base-200 group"
              >
                <div class="min-w-0 flex-1">
                  <div class="flex items-center space-x-2">
                    <code class="text-xs font-mono truncate">{{ ep.url }}</code>
                    <span v-if="ep.isDefault" class="badge badge-xs badge-primary">default</span>
                  </div>
                  <div class="text-xs opacity-50 mt-0.5">{{ ep.description }}</div>
                </div>
                <button
                  @click.stop="copyEndpoint(ep)"
                  class="btn btn-ghost btn-xs p-1 opacity-0 group-hover:opacity-100 transition-opacity tooltip tooltip-left flex-shrink-0 ml-2"
                  :data-tip="ep.copyTooltip"
                >
                  <svg v-if="ep.copyTooltip === 'Copied!'" class="w-3.5 h-3.5 text-success" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7" />
                  </svg>
                  <svg v-else class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
                  </svg>
                </button>
              </div>
            </div>
          </div>
          <!-- Click-outside overlay -->
          <div v-if="showEndpoints" class="fixed inset-0 z-40" @click="showEndpoints = false" />
        </div>
      </div>
    </div>

    <!-- Add Server Modal -->
    <AddServerModal
      :show="showAddServerModal"
      @close="showAddServerModal = false"
      @added="handleServerAdded"
    />
  </header>
</template>

<script setup lang="ts">
import { ref, reactive, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useSystemStore } from '@/stores/system'
import { useServersStore } from '@/stores/servers'
import { useAuthStore } from '@/stores/auth'
import AddServerModal from './AddServerModal.vue'

const router = useRouter()
const systemStore = useSystemStore()
const serversStore = useServersStore()
const authStore = useAuthStore()

const addServerLabel = computed(() => authStore.isTeamsEdition ? 'Add Personal Server' : 'Add Server')

const routingModeLabel = computed(() => {
  const mode = systemStore.routingMode
  switch (mode) {
    case 'direct':
      return 'Direct'
    case 'code_execution':
      return 'Code Exec'
    default:
      return 'Retrieve'
  }
})

const searchQuery = ref('')
const showAddServerModal = ref(false)
const showEndpoints = ref(false)

interface McpEndpoint {
  path: string
  url: string
  description: string
  isDefault: boolean
  copyTooltip: string
}

const mcpEndpoints = computed<McpEndpoint[]>(() => {
  const addr = systemStore.listenAddr
  if (!addr) return []
  const base = `http://${addr}`
  const mode = systemStore.routingMode
  return [
    {
      path: '/mcp',
      url: `${base}/mcp`,
      description: `Default endpoint (${mode === 'direct' ? 'direct' : mode === 'code_execution' ? 'code execution' : 'retrieve tools'} mode)`,
      isDefault: true,
      copyTooltip: 'Copy URL',
    },
    {
      path: '/mcp/call',
      url: `${base}/mcp/call`,
      description: 'Retrieve tools + call_tool_read/write/destructive',
      isDefault: false,
      copyTooltip: 'Copy URL',
    },
    {
      path: '/mcp/all',
      url: `${base}/mcp/all`,
      description: 'Direct access to all tools (serverName__toolName)',
      isDefault: false,
      copyTooltip: 'Copy URL',
    },
    {
      path: '/mcp/code',
      url: `${base}/mcp/code`,
      description: 'Code execution + retrieve_tools for discovery',
      isDefault: false,
      copyTooltip: 'Copy URL',
    },
  ]
})

async function copyEndpoint(ep: McpEndpoint) {
  try {
    await navigator.clipboard.writeText(ep.url)
    ep.copyTooltip = 'Copied!'
    setTimeout(() => { ep.copyTooltip = 'Copy URL' }, 2000)
  } catch (err) {
    console.error('Failed to copy:', err)
    ep.copyTooltip = 'Failed'
    setTimeout(() => { ep.copyTooltip = 'Copy URL' }, 2000)
  }
}

function handleSearch() {
  if (searchQuery.value.trim()) {
    router.push({ path: '/search', query: { q: searchQuery.value } })
  }
}

function handleServerAdded() {
  // Refresh servers list after adding
  serversStore.fetchServers()
}
</script>
