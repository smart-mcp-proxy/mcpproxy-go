import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { Server, LoadingState } from '@/types'
import api from '@/services/api'

export const useServersStore = defineStore('servers', () => {
  // State
  const servers = ref<Server[]>([])
  const loading = ref<LoadingState>({ loading: false, error: null })

  // Computed
  const serverCount = computed(() => ({
    total: servers.value.length,
    connected: servers.value.filter(s => s.connected).length,
    enabled: servers.value.filter(s => s.enabled).length,
    quarantined: servers.value.filter(s => s.quarantined).length,
  }))

  const connectedServers = computed(() =>
    servers.value.filter(s => s.connected)
  )

  const enabledServers = computed(() =>
    servers.value.filter(s => s.enabled)
  )

  const quarantinedServers = computed(() =>
    servers.value.filter(s => s.quarantined)
  )

  const totalTools = computed(() =>
    servers.value.reduce((sum, server) => sum + server.tool_count, 0)
  )

  // Actions
  async function fetchServers() {
    loading.value = { loading: true, error: null }

    try {
      const response = await api.getServers()
      if (response.success && response.data) {
        servers.value = response.data.servers
      } else {
        loading.value.error = response.error || 'Failed to fetch servers'
      }
    } catch (error) {
      loading.value.error = error instanceof Error ? error.message : 'Unknown error'
    } finally {
      loading.value.loading = false
    }
  }

  async function enableServer(serverName: string) {
    try {
      const response = await api.enableServer(serverName)
      if (response.success) {
        const server = servers.value.find(s => s.name === serverName)
        if (server) {
          server.enabled = true
        }
        return true
      } else {
        throw new Error(response.error || 'Failed to enable server')
      }
    } catch (error) {
      console.error('Failed to enable server:', error)
      throw error
    }
  }

  async function disableServer(serverName: string) {
    try {
      const response = await api.disableServer(serverName)
      if (response.success) {
        const server = servers.value.find(s => s.name === serverName)
        if (server) {
          server.enabled = false
        }
        return true
      } else {
        throw new Error(response.error || 'Failed to disable server')
      }
    } catch (error) {
      console.error('Failed to disable server:', error)
      throw error
    }
  }

  async function restartServer(serverName: string) {
    try {
      const response = await api.restartServer(serverName)
      if (response.success) {
        // Optionally update server state
        const server = servers.value.find(s => s.name === serverName)
        if (server) {
          server.connecting = true
          server.connected = false
        }
        return true
      } else {
        throw new Error(response.error || 'Failed to restart server')
      }
    } catch (error) {
      console.error('Failed to restart server:', error)
      throw error
    }
  }

  async function triggerOAuthLogin(serverName: string) {
    try {
      const response = await api.triggerOAuthLogin(serverName)
      if (response.success) {
        return true
      } else {
        throw new Error(response.error || 'Failed to trigger OAuth login')
      }
    } catch (error) {
      console.error('Failed to trigger OAuth login:', error)
      throw error
    }
  }

  async function quarantineServer(serverName: string) {
    try {
      const response = await api.quarantineServer(serverName)
      if (response.success) {
        const server = servers.value.find(s => s.name === serverName)
        if (server) {
          server.quarantined = true
        }
        return true
      } else {
        throw new Error(response.error || 'Failed to quarantine server')
      }
    } catch (error) {
      console.error('Failed to quarantine server:', error)
      throw error
    }
  }

  async function unquarantineServer(serverName: string) {
    try {
      const response = await api.unquarantineServer(serverName)
      if (response.success) {
        const server = servers.value.find(s => s.name === serverName)
        if (server) {
          server.quarantined = false
        }
        return true
      } else {
        throw new Error(response.error || 'Failed to unquarantine server')
      }
    } catch (error) {
      console.error('Failed to unquarantine server:', error)
      throw error
    }
  }

  function updateServerStatus(statusUpdate: any) {
    // Update servers based on real-time status updates
    if (statusUpdate.upstream_stats) {
      // We could update individual server statuses here
      // For now, just trigger a refresh
      fetchServers()
    }
  }

  async function addServer(serverData: any) {
    try {
      const response = await api.callTool('upstream_servers', serverData)
      if (response.success) {
        // Refresh servers list
        await fetchServers()
        return true
      } else {
        throw new Error(response.error || 'Failed to add server')
      }
    } catch (error) {
      console.error('Failed to add server:', error)
      throw error
    }
  }

  function getServerByName(name: string): Server | undefined {
    return servers.value.find(s => s.name === name)
  }

  return {
    // State
    servers,
    loading,

    // Computed
    serverCount,
    connectedServers,
    enabledServers,
    quarantinedServers,
    totalTools,

    // Actions
    fetchServers,
    enableServer,
    disableServer,
    restartServer,
    triggerOAuthLogin,
    quarantineServer,
    unquarantineServer,
    updateServerStatus,
    getServerByName,
    addServer,
  }
})