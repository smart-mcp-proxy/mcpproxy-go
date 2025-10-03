<template>
  <header class="bg-base-100 border-b border-base-300 sticky top-0 z-30 overflow-x-hidden">
    <div class="flex items-center justify-between px-6 py-4 max-w-full overflow-x-hidden">
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
          <span class="hidden sm:inline ml-2">Add Server</span>
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
          <span class="font-bold">{{ systemStore.upstreamStats.connected_servers }}</span>
          <span class="opacity-60">/</span>
          <span>{{ systemStore.upstreamStats.total_servers }}</span>
          <span class="text-xs opacity-60">Servers</span>
        </div>

        <!-- Tools -->
        <div class="flex items-center space-x-2 px-3 py-2 bg-base-200 rounded-lg text-sm">
          <span class="font-bold">{{ serversStore.totalTools }}</span>
          <span class="text-xs opacity-60">Tools</span>
        </div>

        <!-- Proxy Address with Copy -->
        <div v-if="systemStore.listenAddr" class="flex items-center space-x-2 px-3 py-2 bg-base-200 rounded-lg">
          <span class="text-xs font-medium opacity-60">Proxy:</span>
          <code class="text-xs font-mono">{{ systemStore.listenAddr }}</code>
          <button
            @click="copyAddress"
            class="btn btn-ghost btn-xs p-1 tooltip"
            :data-tip="copyTooltip"
          >
            <svg class="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
            </svg>
          </button>
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
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useSystemStore } from '@/stores/system'
import { useServersStore } from '@/stores/servers'
import AddServerModal from './AddServerModal.vue'

const router = useRouter()
const systemStore = useSystemStore()
const serversStore = useServersStore()

const searchQuery = ref('')
const copyTooltip = ref('Copy MCP address')
const showAddServerModal = ref(false)

async function copyAddress() {
  const address = systemStore.listenAddr
  if (!address) return

  try {
    await navigator.clipboard.writeText(`http://${address}/mcp`)
    copyTooltip.value = 'Copied!'
    setTimeout(() => {
      copyTooltip.value = 'Copy MCP address'
    }, 2000)
  } catch (err) {
    console.error('Failed to copy to clipboard:', err)
    copyTooltip.value = 'Failed to copy'
    setTimeout(() => {
      copyTooltip.value = 'Copy MCP address'
    }, 2000)
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
