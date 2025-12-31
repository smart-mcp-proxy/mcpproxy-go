import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { StatusUpdate, Theme, Toast, InfoResponse } from '@/types'
import api from '@/services/api'

export const useSystemStore = defineStore('system', () => {
  // State
  const status = ref<StatusUpdate | null>(null)
  const eventSource = ref<EventSource | null>(null)
  const connected = ref(false)
  const currentTheme = ref<string>('corporate')
  const toasts = ref<Toast[]>([])
  const info = ref<InfoResponse | null>(null)

  // Available themes
  const themes: Theme[] = [
    { name: 'light', displayName: 'Light', dark: false },
    { name: 'dark', displayName: 'Dark', dark: true },
    { name: 'corporate', displayName: 'Corporate', dark: false },
    { name: 'business', displayName: 'Business', dark: true },
    { name: 'emerald', displayName: 'Emerald', dark: false },
    { name: 'forest', displayName: 'Forest', dark: true },
    { name: 'aqua', displayName: 'Aqua', dark: false },
    { name: 'lofi', displayName: 'Lo-Fi', dark: false },
    { name: 'pastel', displayName: 'Pastel', dark: false },
    { name: 'fantasy', displayName: 'Fantasy', dark: false },
    { name: 'wireframe', displayName: 'Wireframe', dark: false },
    { name: 'luxury', displayName: 'Luxury', dark: true },
    { name: 'dracula', displayName: 'Dracula', dark: true },
    { name: 'synthwave', displayName: 'Synthwave', dark: true },
    { name: 'cyberpunk', displayName: 'Cyberpunk', dark: true },
  ]

  // Computed
  const isRunning = computed(() => {
    // Priority: Top-level running field, then nested status.running, default false
    if (status.value?.running !== undefined) {
      return status.value.running
    }
    // Fallback to nested status.running if top-level is undefined
    if (status.value?.status?.running !== undefined) {
      return status.value.status.running
    }
    return false
  })
  const listenAddr = computed(() => status.value?.listen_addr ?? '')
  const upstreamStats = computed(() => status.value?.upstream_stats ?? {
    connected_servers: 0,
    total_servers: 0,
    total_tools: 0,
  })

  const currentThemeConfig = computed(() =>
    themes.find(t => t.name === currentTheme.value) || themes[0]
  )

  // Version information
  const version = computed(() => info.value?.version ?? '')
  const updateAvailable = computed(() => info.value?.update?.available ?? false)
  const latestVersion = computed(() => info.value?.update?.latest_version ?? '')

  // Actions
  function connectEventSource() {
    if (eventSource.value) {
      eventSource.value.close()
    }

    console.log('Attempting to connect EventSource...')
    console.log('API key status:', {
      hasApiKey: api.hasAPIKey(),
      apiKeyPreview: api.getAPIKeyPreview()
    })

    const es = api.createEventSource()
    eventSource.value = es

    es.onopen = () => {
      connected.value = true
      console.log('EventSource connected successfully')
    }

    es.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data) as StatusUpdate
        status.value = data

        // Debug logging to help diagnose status issues
        console.log('SSE Status Update:', {
          topLevelRunning: data.running,
          nestedStatusRunning: data.status?.running,
          listen_addr: data.listen_addr,
          timestamp: data.timestamp,
          finalRunningValue: data.running !== undefined ? data.running : (data.status?.running ?? false)
        })

        // You could emit events here for other stores to listen to
        // For example, update server statuses
      } catch (error) {
        console.error('Failed to parse SSE message:', error)
      }
    }

    // Listen specifically for status events
    es.addEventListener('status', (event) => {
      try {
        const data = JSON.parse(event.data) as StatusUpdate
        status.value = data

        // Debug logging to help diagnose status issues
        console.log('SSE Status Event Update:', {
          topLevelRunning: data.running,
          nestedStatusRunning: data.status?.running,
          listen_addr: data.listen_addr,
          timestamp: data.timestamp,
          finalRunningValue: data.running !== undefined ? data.running : (data.status?.running ?? false)
        })
      } catch (error) {
        console.error('Failed to parse SSE status event:', error)
      }
    })

    // Listen for servers.changed events to trigger immediate UI updates
    es.addEventListener('servers.changed', (event) => {
      try {
        const data = JSON.parse(event.data)
        console.log('SSE servers.changed event received:', data)

        // Import and call serversStore.fetchServers() to refresh server list
        // Note: This creates a circular dependency, so we'll emit a custom event instead
        window.dispatchEvent(new CustomEvent('mcpproxy:servers-changed', { detail: data }))
      } catch (error) {
        console.error('Failed to parse SSE servers.changed event:', error)
      }
    })

    // Listen for config.reloaded events
    es.addEventListener('config.reloaded', (event) => {
      try {
        const data = JSON.parse(event.data)
        console.log('SSE config.reloaded event received:', data)

        // Trigger server list refresh on config reload
        window.dispatchEvent(new CustomEvent('mcpproxy:config-reloaded', { detail: data }))
      } catch (error) {
        console.error('Failed to parse SSE config.reloaded event:', error)
      }
    })

    // Listen for config.saved events to notify Configuration page
    es.addEventListener('config.saved', (event) => {
      try {
        const data = JSON.parse(event.data)
        console.log('SSE config.saved event received:', data)

        // Dispatch event for Configuration page to refresh
        window.dispatchEvent(new CustomEvent('mcpproxy:config-saved', { detail: data }))
      } catch (error) {
        console.error('Failed to parse SSE config.saved event:', error)
      }
    })

    // Listen for activity events (tool calls, policy decisions, etc.)
    es.addEventListener('activity.tool_call.started', (event) => {
      try {
        const data = JSON.parse(event.data)
        console.log('SSE activity.tool_call.started event received:', data)
        window.dispatchEvent(new CustomEvent('mcpproxy:activity-started', { detail: data }))
      } catch (error) {
        console.error('Failed to parse SSE activity.tool_call.started event:', error)
      }
    })

    es.addEventListener('activity.tool_call.completed', (event) => {
      try {
        const data = JSON.parse(event.data)
        console.log('SSE activity.tool_call.completed event received:', data)
        window.dispatchEvent(new CustomEvent('mcpproxy:activity-completed', { detail: data }))
      } catch (error) {
        console.error('Failed to parse SSE activity.tool_call.completed event:', error)
      }
    })

    es.addEventListener('activity.policy_decision', (event) => {
      try {
        const data = JSON.parse(event.data)
        console.log('SSE activity.policy_decision event received:', data)
        window.dispatchEvent(new CustomEvent('mcpproxy:activity-policy', { detail: data }))
      } catch (error) {
        console.error('Failed to parse SSE activity.policy_decision event:', error)
      }
    })

    es.addEventListener('activity', (event) => {
      try {
        const data = JSON.parse(event.data)
        console.log('SSE activity event received:', data)
        window.dispatchEvent(new CustomEvent('mcpproxy:activity', { detail: data }))
      } catch (error) {
        console.error('Failed to parse SSE activity event:', error)
      }
    })

    es.onerror = (event) => {
      connected.value = false
      console.error('EventSource error occurred:', event)

      // Check if this might be an authentication error
      if (es.readyState === EventSource.CLOSED) {
        console.error('EventSource connection closed - possible authentication failure')

        // If we have an API key but still failed, try reinitializing
        if (api.hasAPIKey()) {
          console.log('Attempting to reinitialize API key and retry connection...')
          api.reinitializeAPIKey()
        }
      }

      // Retry connection after a delay
      setTimeout(() => {
        console.log('Retrying EventSource connection in 5 seconds...')
        connectEventSource()
      }, 5000)
    }
  }

  function disconnectEventSource() {
    if (eventSource.value) {
      eventSource.value.close()
      eventSource.value = null
    }
    connected.value = false
  }

  function setTheme(themeName: string) {
    const theme = themes.find(t => t.name === themeName)
    if (theme) {
      currentTheme.value = themeName
      document.documentElement.setAttribute('data-theme', themeName)
      localStorage.setItem('mcpproxy-theme', themeName)
    }
  }

  function loadTheme() {
    const savedTheme = localStorage.getItem('mcpproxy-theme')
    if (savedTheme && themes.find(t => t.name === savedTheme)) {
      setTheme(savedTheme)
    } else {
      setTheme('corporate')
    }
  }

  function addToast(toast: Omit<Toast, 'id'>): string {
    const id = Math.random().toString(36).substr(2, 9)
    const newToast: Toast = {
      ...toast,
      id,
      duration: toast.duration ?? 5000,
    }

    toasts.value.push(newToast)

    // Auto-remove toast after duration
    if (newToast.duration && newToast.duration > 0) {
      setTimeout(() => {
        removeToast(id)
      }, newToast.duration)
    }

    return id
  }

  function removeToast(id: string) {
    const index = toasts.value.findIndex(t => t.id === id)
    if (index > -1) {
      toasts.value.splice(index, 1)
    }
  }

  function clearToasts() {
    toasts.value = []
  }

  async function fetchInfo() {
    try {
      const response = await api.getInfo()
      if (response.success && response.data) {
        info.value = response.data
      }
    } catch (error) {
      console.error('Failed to fetch info:', error)
    }
  }

  // Initialize theme on store creation
  loadTheme()

  return {
    // State
    status,
    connected,
    currentTheme,
    toasts,
    themes,
    info,

    // Computed
    isRunning,
    listenAddr,
    upstreamStats,
    currentThemeConfig,
    version,
    updateAvailable,
    latestVersion,

    // Actions
    connectEventSource,
    disconnectEventSource,
    setTheme,
    loadTheme,
    addToast,
    removeToast,
    clearToasts,
    fetchInfo,
  }
})