<template>
  <div class="space-y-6">
    <!-- Page Header -->
    <div class="flex justify-between items-center">
      <div>
        <h1 class="text-3xl font-bold">Dashboard</h1>
        <p class="text-base-content/70 mt-1">MCPProxy Control Panel Overview</p>
      </div>
      <div class="flex items-center space-x-2">
        <div
          :class="[
            'badge',
            systemStore.isRunning ? 'badge-success' : 'badge-error'
          ]"
        >
          {{ systemStore.isRunning ? 'Running' : 'Stopped' }}
        </div>
        <span class="text-sm">{{ systemStore.listenAddr || 'Not running' }}</span>
      </div>
    </div>

    <!-- Stats Cards -->
    <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6">
      <!-- Servers Stats -->
      <div class="stats shadow bg-base-100">
        <div class="stat">
          <div class="stat-figure text-primary">
            <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2m-2-4h.01M17 16h.01" />
            </svg>
          </div>
          <div class="stat-title">Total Servers</div>
          <div class="stat-value">{{ serversStore.serverCount.total }}</div>
          <div class="stat-desc">{{ serversStore.serverCount.connected }} connected</div>
        </div>
      </div>

      <!-- Tools Stats -->
      <div class="stats shadow bg-base-100">
        <div class="stat">
          <div class="stat-figure text-secondary">
            <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
            </svg>
          </div>
          <div class="stat-title">Available Tools</div>
          <div class="stat-value">{{ serversStore.totalTools }}</div>
          <div class="stat-desc">across all servers</div>
        </div>
      </div>

      <!-- Enabled Servers -->
      <div class="stats shadow bg-base-100">
        <div class="stat">
          <div class="stat-figure text-success">
            <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
          </div>
          <div class="stat-title">Enabled</div>
          <div class="stat-value">{{ serversStore.serverCount.enabled }}</div>
          <div class="stat-desc">servers active</div>
        </div>
      </div>

      <!-- Quarantined Servers -->
      <div class="stats shadow bg-base-100">
        <div class="stat">
          <div class="stat-figure text-warning">
            <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.732-.833-2.5 0L3.732 16.5c-.77.833.192 2.5 1.732 2.5z" />
            </svg>
          </div>
          <div class="stat-title">Quarantined</div>
          <div class="stat-value">{{ serversStore.serverCount.quarantined }}</div>
          <div class="stat-desc">security review needed</div>
        </div>
      </div>
    </div>

    <!-- Recent Servers -->
    <div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
      <!-- Connected Servers -->
      <div class="card bg-base-100 shadow-md">
        <div class="card-body">
          <h2 class="card-title text-xl mb-4">
            Connected Servers
            <div class="badge badge-success">{{ serversStore.connectedServers.length }}</div>
          </h2>

          <div v-if="serversStore.loading.loading" class="text-center py-8">
            <span class="loading loading-spinner loading-lg"></span>
            <p class="mt-2">Loading servers...</p>
          </div>

          <div v-else-if="serversStore.loading.error" class="alert alert-error">
            <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            <span>{{ serversStore.loading.error }}</span>
          </div>

          <div v-else-if="serversStore.connectedServers.length === 0" class="text-center py-8 text-base-content/60">
            <svg class="w-16 h-16 mx-auto mb-4 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2" />
            </svg>
            <p>No servers connected</p>
            <router-link to="/servers" class="btn btn-sm btn-primary mt-2">
              Manage Servers
            </router-link>
          </div>

          <div v-else class="space-y-3">
            <div
              v-for="server in serversStore.connectedServers.slice(0, 5)"
              :key="server.name"
              class="flex items-center justify-between p-3 bg-base-200 rounded-lg"
            >
              <div class="flex items-center space-x-3">
                <div class="w-3 h-3 bg-success rounded-full"></div>
                <div>
                  <div class="font-medium">{{ server.name }}</div>
                  <div class="text-sm text-base-content/70">{{ server.tool_count }} tools</div>
                </div>
              </div>
              <router-link
                :to="`/servers/${server.name}`"
                class="btn btn-xs btn-outline"
              >
                View
              </router-link>
            </div>

            <div v-if="serversStore.connectedServers.length > 5" class="text-center pt-2">
              <router-link to="/servers" class="btn btn-sm btn-outline">
                View All ({{ serversStore.connectedServers.length }})
              </router-link>
            </div>
          </div>
        </div>
      </div>

      <!-- Quick Actions -->
      <div class="card bg-base-100 shadow-md">
        <div class="card-body">
          <h2 class="card-title text-xl mb-4">Quick Actions</h2>

          <div class="space-y-4">
            <router-link to="/search" class="btn btn-outline btn-block justify-start">
              <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
              </svg>
              Search Tools
            </router-link>

            <router-link to="/servers" class="btn btn-outline btn-block justify-start">
              <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2" />
              </svg>
              Manage Servers
            </router-link>

            <router-link to="/tools" class="btn btn-outline btn-block justify-start">
              <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
              </svg>
              Browse All Tools
            </router-link>

            <router-link to="/settings" class="btn btn-outline btn-block justify-start">
              <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
              </svg>
              Settings
            </router-link>
          </div>
        </div>
      </div>
    </div>

    <!-- System Info -->
    <div class="card bg-base-100 shadow-md">
      <div class="card-body">
        <h2 class="card-title text-xl mb-4">System Information</h2>

        <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
          <div class="stat bg-base-200 rounded-lg">
            <div class="stat-title">Status</div>
            <div class="stat-value text-lg">
              {{ systemStore.isRunning ? 'Running' : 'Stopped' }}
            </div>
            <div class="stat-desc">{{ systemStore.listenAddr || 'Not listening' }}</div>
          </div>

          <div class="stat bg-base-200 rounded-lg">
            <div class="stat-title">Real-time Updates</div>
            <div class="stat-value text-lg">
              {{ systemStore.connected ? 'Connected' : 'Disconnected' }}
            </div>
            <div class="stat-desc">Server-Sent Events</div>
          </div>

          <div class="stat bg-base-200 rounded-lg">
            <div class="stat-title">Last Update</div>
            <div class="stat-value text-lg">
              {{ lastUpdateTime }}
            </div>
            <div class="stat-desc">{{ systemStore.status?.timestamp ? 'Live' : 'No data' }}</div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useServersStore } from '@/stores/servers'
import { useSystemStore } from '@/stores/system'

const serversStore = useServersStore()
const systemStore = useSystemStore()

const lastUpdateTime = computed(() => {
  if (!systemStore.status?.timestamp) return 'Never'

  const now = Date.now()
  const timestamp = systemStore.status.timestamp * 1000 // Convert to milliseconds
  const diff = now - timestamp

  if (diff < 1000) return 'Just now'
  if (diff < 60000) return `${Math.floor(diff / 1000)}s ago`
  if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`

  return new Date(timestamp).toLocaleTimeString()
})
</script>