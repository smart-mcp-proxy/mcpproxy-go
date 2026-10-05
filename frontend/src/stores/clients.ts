import { defineStore } from 'pinia'
import { computed, onScopeDispose, ref } from 'vue'
import api from '@/services/api'
import type { ClientPresence, ClientWarning, RoutingInfo } from '@/types/api'

// Spec 108-f: a binding changed (any surface). Like profiles.changed it is an
// invalidation; the rows and warnings are refetched, never patched from it.
export const CLIENT_BINDING_CHANGED_EVENT = 'mcpproxy:client.binding_changed'

export const useClientsStore = defineStore('clients', () => {
  const clients = ref<ClientPresence[]>([])
  // Instance-level warnings from GET /clients (Spec 108-f): never filtered away
  // by the profile/client scope.
  const warnings = ref<ClientWarning[]>([])
  // The REST scope (profile/client) of the last page load, so a silent refresh
  // keeps showing the same rows.
  let scope: { profile?: string; client?: string } = {}
  const routing = ref<RoutingInfo | null>(null)
  const loading = ref(false)
  const error = ref<string | null>(null)
  // Client ids whose detail-resolved fields must survive a metadata-only refresh.
  const detailLoaded = new Set<string>()

  // A response is applied only while it is still the latest of its kind and the
  // scope it was asked for is still the active one: changing ?profile= / ?client=
  // must not let the previous scope's rows land afterwards.
  // One ticket covers every GET /clients (load and refreshPresence), so an older
  // response can never overwrite a newer one. loadTicket only owns the loading
  // flag, so a load superseded by a presence poll still clears it.
  let fetchTicket = 0
  let loadTicket = 0
  const scopeKey = () => JSON.stringify(scope)
  const isUnscoped = () => !scope.profile && !scope.client

  // The unscoped roster (updated only by unscoped fetches). The sidebar badge,
  // the assign dialog and the profile usage list read it, so a ?profile= /
  // ?client= filter on the Clients page never shrinks them.
  const allClients = ref<ClientPresence[]>([])
  // Set by clearScope(): the roster must be refetched before it is trusted.
  const stale = ref(false)
  // Both GET /clients reads of one refresh; the unscoped one is shared when the
  // active scope is already unscoped.
  async function fetchRosters() {
    const scoped = api.getClients(scope)
    const all = isUnscoped() ? scoped : api.getClients({})
    return Promise.all([scoped, all])
  }
  function applyAll(response: { success: boolean; data?: { clients?: ClientPresence[] } }) {
    if (response.success && Array.isArray(response.data?.clients)) allClients.value = response.data.clients
      stale.value = false
    }

  async function load(nextScope?: { profile?: string; client?: string }) {
    if (nextScope) scope = nextScope
    const ticket = ++fetchTicket
    const mine = ++loadTicket
    const asked = scopeKey()
    loading.value = true
    error.value = null
    const [[clientResponse, allResponse], routingResponse] = await Promise.all([fetchRosters(), api.getRouting()])
    // A newer load owns the loading flag.
    if (mine !== loadTicket || asked !== scopeKey()) return
    // A newer fetch (another load or a presence poll) owns the rows.
    if (ticket !== fetchTicket) {
      loading.value = false
      return
    }
    applyAll(allResponse)
    if (clientResponse.success && clientResponse.data) {
      clients.value = clientResponse.data.clients
      warnings.value = clientResponse.data.warnings ?? []
      detailLoaded.clear()
    } else error.value = clientResponse.error || 'Unable to load clients'
    if (routingResponse.success && routingResponse.data) routing.value = routingResponse.data
    loading.value = false
  }

  // Rows with at least one live session: the sidebar Clients badge (Spec 109-i).
  const liveCount = computed(() => allClients.value.filter(client => (client.active_sessions ?? 0) > 0).length)

  // Silent presence refresh for the sidebar badge. It fetches GET /clients only
  // and never touches loading/error/routing, so the Clients page does not
  // flash a spinner when a badge poll lands underneath it.
  async function refreshPresence() {
    const ticket = ++fetchTicket
    const asked = scopeKey()
    try {
      const [response, allResponse] = await fetchRosters()
      if (ticket !== fetchTicket || asked !== scopeKey()) return
      applyAll(allResponse)
      if (response.success && Array.isArray(response.data?.clients)) {
        warnings.value = response.data.warnings ?? []
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

  // A row answered by a binding route replaces the loaded one; the warnings are
  // recomputed by the next refresh.
  function replaceRow(row: ClientPresence) {
    const index = clients.value.findIndex(client => client.id === row.id)
    if (index >= 0) clients.value[index] = { ...clients.value[index], ...row }
    else clients.value.push(row)
  }

  // Dropping the scope also invalidates the rows: they were fetched under the
  // old filter, so the next consumer must refetch rather than trust them.
  function clearScope() {
    scope = {}
    stale.value = true
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

  if (typeof window !== 'undefined') {
    const refresh = () => { void refreshPresence() }
    window.addEventListener(CLIENT_BINDING_CHANGED_EVENT, refresh)
    window.addEventListener('mcpproxy:profiles.changed', refresh)
    onScopeDispose(() => {
      window.removeEventListener(CLIENT_BINDING_CHANGED_EVENT, refresh)
      window.removeEventListener('mcpproxy:profiles.changed', refresh)
    })
  }

  return { clients, allClients, stale, warnings, routing, loading, error, liveCount, load, refreshPresence, loadDetail, replaceRow, clearScope }
})
