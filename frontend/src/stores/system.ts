import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { StatusUpdate, Theme, Toast } from '@/types'
import api from '@/services/api'

export const useSystemStore = defineStore('system', () => {
  // State
  const status = ref<StatusUpdate | null>(null)
  const eventSource = ref<EventSource | null>(null)
  const connected = ref(false)
  const currentTheme = ref<string>('corporate')
  const toasts = ref<Toast[]>([])

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
  const isRunning = computed(() => status.value?.running ?? false)
  const listenAddr = computed(() => status.value?.listen_addr ?? '')
  const upstreamStats = computed(() => status.value?.upstream_stats ?? {
    connected_servers: 0,
    total_servers: 0,
    total_tools: 0,
  })

  const currentThemeConfig = computed(() =>
    themes.find(t => t.name === currentTheme.value) || themes[0]
  )

  // Actions
  function connectEventSource() {
    if (eventSource.value) {
      eventSource.value.close()
    }

    const es = api.createEventSource()
    eventSource.value = es

    es.onopen = () => {
      connected.value = true
      console.log('EventSource connected')
    }

    es.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data) as StatusUpdate
        status.value = data

        // You could emit events here for other stores to listen to
        // For example, update server statuses
      } catch (error) {
        console.error('Failed to parse SSE message:', error)
      }
    }

    es.onerror = () => {
      connected.value = false
      console.error('EventSource error')

      // Retry connection after a delay
      setTimeout(() => {
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

  // Initialize theme on store creation
  loadTheme()

  return {
    // State
    status,
    connected,
    currentTheme,
    toasts,
    themes,

    // Computed
    isRunning,
    listenAddr,
    upstreamStats,
    currentThemeConfig,

    // Actions
    connectEventSource,
    disconnectEventSource,
    setTheme,
    loadTheme,
    addToast,
    removeToast,
    clearToasts,
  }
})