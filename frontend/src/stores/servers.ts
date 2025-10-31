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

  // Helper: Smart merge servers to preserve object references and avoid full re-renders
  function mergeServers(existing: Server[], incoming: Server[]): Server[] {
    const existingMap = new Map(existing.map(s => [s.name, s]))
    const incomingMap = new Map(incoming.map(s => [s.name, s]))
    const result: Server[] = []

    // Update existing servers in-place or add new ones
    incoming.forEach(incomingServer => {
      const existingServer = existingMap.get(incomingServer.name)

      if (existingServer) {
        // Update existing server in-place (preserves object reference)
        // Only update properties that have changed
        let hasChanges = false
        Object.assign(existingServer, incomingServer)
        hasChanges = true

        if (hasChanges) {
          console.log(`Server ${existingServer.name} updated with changes`)
        }

        result.push(existingServer)
      } else {
        // Add new server
        console.log(`New server added: ${incomingServer.name}`)
        result.push(incomingServer)
      }
    })

    // Log removed servers
    existing.forEach(existingServer => {
      if (!incomingMap.has(existingServer.name)) {
        console.log(`Server removed: ${existingServer.name}`)
      }
    })

    // Sort alphabetically by name to match tray menu order
    return result.sort((a, b) => a.name.localeCompare(b.name))
  }

  // Actions
  async function fetchServers(silent = false) {
    if (!silent) {
      loading.value = { loading: true, error: null }
    }

    try {
      const response = await api.getServers()
      if (response.success && response.data) {
        // Use smart merge to preserve object references and avoid unnecessary re-renders
        servers.value = mergeServers(servers.value, response.data.servers)
      } else {
        loading.value.error = response.error || 'Failed to fetch servers'
      }
    } catch (error) {
      loading.value.error = error instanceof Error ? error.message : 'Unknown error'
    } finally {
      if (!silent) {
        loading.value.loading = false
      }
    }
  }

  async function enableServer(serverName: string) {
    try {
      const server = servers.value.find(s => s.name === serverName)

      // Optimistic update: show "connecting" status immediately
      if (server) {
        server.enabled = true
        server.connecting = true
        server.connected = false
      }

      const response = await api.enableServer(serverName)
      if (response.success) {
        // The SSE event will trigger a full refresh with actual state
        return true
      } else {
        // Revert optimistic update on error
        if (server) {
          server.enabled = false
          server.connecting = false
        }
        throw new Error(response.error || 'Failed to enable server')
      }
    } catch (error) {
      console.error('Failed to enable server:', error)
      // Revert optimistic update
      const server = servers.value.find(s => s.name === serverName)
      if (server) {
        server.enabled = false
        server.connecting = false
      }
      throw error
    }
  }

  async function disableServer(serverName: string) {
    try {
      const server = servers.value.find(s => s.name === serverName)

      // Optimistic update: show "disconnected" status immediately
      if (server) {
        server.enabled = false
        server.connecting = false
        server.connected = false
      }

      const response = await api.disableServer(serverName)
      if (response.success) {
        // The SSE event will trigger a full refresh with actual state
        return true
      } else {
        // Revert optimistic update on error
        if (server) {
          server.enabled = true
        }
        throw new Error(response.error || 'Failed to disable server')
      }
    } catch (error) {
      console.error('Failed to disable server:', error)
      // Revert optimistic update
      const server = servers.value.find(s => s.name === serverName)
      if (server) {
        server.enabled = true
      }
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

  async function deleteServer(serverName: string) {
    try {
      const response = await api.deleteServer(serverName)
      if (response.success) {
        // Remove server from local state
        servers.value = servers.value.filter(s => s.name !== serverName)
        return true
      } else {
        throw new Error(response.error || 'Failed to delete server')
      }
    } catch (error) {
      console.error('Failed to delete server:', error)
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

  // Set up event listeners for real-time updates
  function setupEventListeners() {
    window.addEventListener('mcpproxy:servers-changed', handleServersChanged)
    window.addEventListener('mcpproxy:config-reloaded', handleConfigReloaded)
  }

  function cleanupEventListeners() {
    window.removeEventListener('mcpproxy:servers-changed', handleServersChanged)
    window.removeEventListener('mcpproxy:config-reloaded', handleConfigReloaded)
  }

  function handleServersChanged(event: Event) {
    const customEvent = event as CustomEvent
    console.log('Servers changed event received, updating in background...', customEvent.detail)
    // Silent background refresh to avoid scroll jumps and loading states
    fetchServers(true)
  }

  function handleConfigReloaded(event: Event) {
    const customEvent = event as CustomEvent
    console.log('Config reloaded event received, updating in background...', customEvent.detail)
    // Silent background refresh to avoid scroll jumps and loading states
    fetchServers(true)
  }

  // Initialize event listeners
  setupEventListeners()

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
    deleteServer,
    updateServerStatus,
    getServerByName,
    addServer,
    cleanupEventListeners,
  }
})