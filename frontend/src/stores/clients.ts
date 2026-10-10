import { defineStore } from 'pinia'
import { computed, onScopeDispose, ref } from 'vue'
import api from '@/services/api'
import type { ClientPresence, ClientWarning, RoutingInfo } from '@/types/api'

// Spec 108-f: a binding changed (any surface). Like profiles.changed it is an
// invalidation; the rows and warnings are refetched, never patched from it.
export const CLIENT_BINDING_CHANGED_EVENT = 'mcpproxy:client.binding_changed'
// Spec 115 FR-024: a credential was issued, revoked or forgotten (any surface,
// MCP included). An invalidation: the rows are refetched.
export const CREDENTIALS_CHANGED_EVENT = 'mcpproxy:credentials.changed'

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
  // Bumped by every roster fetch; a client detail answered after a newer
  // roster fetch started is discarded.
  let rosterGeneration = 0
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
    if (response.success && Array.isArray(response.data?.clients)) {
      allClients.value = response.data.clients
      stale.value = false
    }
  }

  async function load(nextScope?: { profile?: string; client?: string }) {
    rosterGeneration++
    if (nextScope) scope = nextScope
    const ticket = ++fetchTicket
    const mine = ++loadTicket
    const asked = scopeKey()
    loading.value = true
    error.value = null
    const [[clientResponse, allResponse], routingResponse] = await Promise.all([fetchRosters(), api.getRouting()])
    // A newer load owns the loading flag.
    if (mine !== loadTicket || asked !== scopeKey()) return
    // Routing is only fetched here, so a superseded load still applies it.
    if (routingResponse.success && routingResponse.data) routing.value = routingResponse.data
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
    loading.value = false
  }

  // Rows with at least one live session: the sidebar Clients badge (Spec 109-i).
  const liveCount = computed(() => allClients.value.filter(client => (client.active_sessions ?? 0) > 0).length)

  // Silent presence refresh for the sidebar badge. It fetches GET /clients only
  // and never touches loading/error/routing, so the Clients page does not
  // flash a spinner when a badge poll lands underneath it.
  async function refreshPresence() {
    rosterGeneration++
    const ticket = ++fetchTicket
    const asked = scopeKey()
    try {
      const [response, allResponse] = await fetchRosters()
      if (ticket !== fetchTicket || asked !== scopeKey()) return
      applyAll(allResponse)
      if (response.success && Array.isArray(response.data?.clients)) {
        warnings.value = response.data.warnings ?? []
        // Source-of-truth rule (#1451-10): the 30s presence poll owns
        // `last_seen` and `active_sessions`. GET /clients is metadata-only, so
        // while the poll's presence matches the row, keep every detail-resolved
        // field (state, installed, connected, connection_unverified, config
        // paths, sessions) so a poll never downgrades them (#1444). The backend
        // omits `sessions` when empty, so its absence is not a signal. When the
        // poll's presence differs, the detail-derived presence fields (state,
        // connected, sessions) are stale: take them from the poll, forget the
        // detail flag and reload the detail so the row converges on one source.
        const previous = new Map(clients.value.map(client => [client.id, client]))
        const stale: string[] = []
        clients.value = response.data.clients.map(incoming => {
          const existing = previous.get(incoming.id)
          if (!existing || !detailLoaded.has(incoming.id)) return incoming
          const presenceChanged =
            (incoming.active_sessions ?? 0) !== (existing.active_sessions ?? 0) ||
            (incoming.last_seen ?? null) !== (existing.last_seen ?? null)
          if (presenceChanged) {
            detailLoaded.delete(incoming.id)
            stale.push(incoming.id)
            return {
              ...incoming,
              installed: existing.installed,
              config_path: existing.config_path,
              display_path: existing.display_path,
            }
          }
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
        for (const id of stale) void loadDetail(id)
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
    // A roster refresh that starts while this detail is in flight (an SSE
    // invalidation such as credentials.changed) supersedes it: a stale detail
    // must not overwrite the newer row (Spec 115 UI-004).
    const generation = rosterGeneration
    const response = await api.getClient(id)
    if (generation !== rosterGeneration) return
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
    window.addEventListener(CREDENTIALS_CHANGED_EVENT, refresh)
    window.addEventListener('mcpproxy:profiles.changed', refresh)
    onScopeDispose(() => {
      window.removeEventListener(CLIENT_BINDING_CHANGED_EVENT, refresh)
      window.removeEventListener(CREDENTIALS_CHANGED_EVENT, refresh)
      window.removeEventListener('mcpproxy:profiles.changed', refresh)
    })
  }

  return { clients, allClients, stale, warnings, routing, loading, error, liveCount, load, refreshPresence, loadDetail, replaceRow, clearScope }
})
