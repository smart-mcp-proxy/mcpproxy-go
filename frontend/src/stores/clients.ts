import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import api from '@/services/api'
import type { ClientPresence, RoutingInfo } from '@/types/api'

export const useClientsStore = defineStore('clients', () => {
  const clients = ref<ClientPresence[]>([])
  const routing = ref<RoutingInfo | null>(null)
  const loading = ref(false)
  const error = ref<string | null>(null)
  // Client ids whose detail-resolved fields must survive a metadata-only refresh.
  const detailLoaded = new Set<string>()

  async function load() {
    loading.value = true
    error.value = null
    const [clientResponse, routingResponse] = await Promise.all([api.getClients(), api.getRouting()])
    if (clientResponse.success && clientResponse.data) {
      clients.value = clientResponse.data.clients
      detailLoaded.clear()
    } else error.value = clientResponse.error || 'Unable to load clients'
    if (routingResponse.success && routingResponse.data) routing.value = routingResponse.data
    loading.value = false
  }

  // Rows with at least one live session: the sidebar Clients badge (Spec 109-i).
  const liveCount = computed(() => clients.value.filter(client => (client.active_sessions ?? 0) > 0).length)

  // Silent presence refresh for the sidebar badge. It fetches GET /clients only
  // and never touches loading/error/routing, so the Clients page does not
  // flash a spinner when a badge poll lands underneath it.
  async function refreshPresence() {
    try {
      const response = await api.getClients()
      if (response.success && Array.isArray(response.data?.clients)) {
        // GET /clients is metadata-only. For rows whose detail was loaded via
        // loadDetail(), keep every detail-resolved field (state, installed,
        // connected, connection_unverified, config paths, sessions) and take
        // only the presence metadata from the poll. The backend omits
        // `sessions` when empty, so its absence is not a signal.
        const previous = new Map(clients.value.map(client => [client.id, client]))
        clients.value = response.data.clients.map(incoming => {
          const existing = previous.get(incoming.id)
          if (!existing || !detailLoaded.has(incoming.id)) return incoming
          return {
            ...incoming,
            state: existing.state,
            installed: existing.installed,
            connected: existing.connected,
            connection_unverified: existing.connection_unverified,
            config_path: existing.config_path,
            display_path: existing.display_path,
            sessions: existing.sessions,
          }
        })
        for (const id of [...detailLoaded]) {
          if (!clients.value.some(client => client.id === id)) detailLoaded.delete(id)
        }
      }
    } catch {
      // A badge must not fail the sidebar on an old or offline core.
    }
  }

  async function loadDetail(id: string) {
    const response = await api.getClient(id)
    if (!response.success || !response.data) return
    const index = clients.value.findIndex(client => client.id === id)
    if (index >= 0) {
      clients.value[index] = response.data
      detailLoaded.add(id)
    }
  }

  return { clients, routing, loading, error, liveCount, load, refreshPresence, loadDetail }
})
