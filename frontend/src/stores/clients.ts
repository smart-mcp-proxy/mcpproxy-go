import { defineStore } from 'pinia'
import { ref } from 'vue'
import api from '@/services/api'
import type { ClientPresence, RoutingInfo } from '@/types/api'

export const useClientsStore = defineStore('clients', () => {
  const clients = ref<ClientPresence[]>([])
  const routing = ref<RoutingInfo | null>(null)
  const loading = ref(false)
  const error = ref<string | null>(null)

  async function load() {
    loading.value = true
    error.value = null
    const [clientResponse, routingResponse] = await Promise.all([api.getClients(), api.getRouting()])
    if (clientResponse.success && clientResponse.data) clients.value = clientResponse.data.clients
    else error.value = clientResponse.error || 'Unable to load clients'
    if (routingResponse.success && routingResponse.data) routing.value = routingResponse.data
    loading.value = false
  }

  async function loadDetail(id: string) {
    const response = await api.getClient(id)
    if (!response.success || !response.data) return
    const index = clients.value.findIndex(client => client.id === id)
    if (index >= 0) clients.value[index] = response.data
  }

  return { clients, routing, loading, error, load, loadDetail }
})
