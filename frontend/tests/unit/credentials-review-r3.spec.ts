import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

vi.mock('@/services/api', () => ({ default: { getClients: vi.fn(), getClient: vi.fn(), getRouting: vi.fn() } }))

// Spec 115 review code-r3 (UI chunk): a detail request started while a roster
// refresh is in flight shares that refresh's generation. When the roster
// (revoked / lease ended) lands first and the older detail (active, connected)
// lands after it, the detail must not overwrite the row: it predates the
// roster that was applied while it was in flight.
describe('a client detail that started during a roster refresh', () => {
  const stores: { $dispose: () => void }[] = []
  beforeEach(() => setActivePinia(createPinia()))
  afterEach(() => { for (const s of stores.splice(0)) s.$dispose() })

  const base = { id: 'w', display_name: 'w', kind: 'custom', installed: false, active_sessions: 1, last_seen: '2026-10-10T10:00:00Z', calls_24h: 0 }
  const active = { ...base, credential_state: 'client', connected: true, state: 'connected_seen' }

  function deferred<T>() {
    let resolve!: (v: T) => void
    const promise = new Promise<T>(r => { resolve = r })
    return { promise, resolve }
  }

  for (const ended of ['revoked', 'expired'] as const) {
    it(`does not let the older active detail overwrite the ${ended} roster`, async () => {
      const api = (await import('@/services/api')).default as any
      const { useClientsStore } = await import('@/stores/clients')
      api.getClients = vi.fn().mockResolvedValue({ success: true, data: { clients: [active] } })
      const store = useClientsStore()
      stores.push(store)
      await store.refreshPresence()

      const endedRoster = { ...base, credential_state: ended, connected: false, state: 'other' }
      const roster = deferred<unknown>()
      api.getClients = vi.fn().mockReturnValueOnce(roster.promise)
        .mockResolvedValue({ success: true, data: { clients: [endedRoster] } })
      const staleDetail = deferred<unknown>()
      api.getClient = vi.fn().mockReturnValueOnce(staleDetail.promise)
        .mockResolvedValue({ success: true, data: { ...endedRoster, sessions: [] } })

      // SSE disconnected: a poll starts, then the detail starts before it settles.
      const refreshing = store.refreshPresence()
      const detail = store.loadDetail('w')
      // The poll snapshots the ended credential and is applied first.
      roster.resolve({ success: true, data: { clients: [endedRoster] } })
      await refreshing
      expect(store.clients[0].credential_state).toBe(ended)
      // The older detail (snapshotted while the credential was active) lands after.
      staleDetail.resolve({ success: true, data: { ...active, sessions: [{ id: 'live' }] } })
      await detail
      expect(store.clients[0].connected).toBe(false)
      expect(store.clients[0].state).toBe('other')
      expect(store.clients[0].sessions ?? []).toEqual([])

      for (let i = 0; i < 2; i++) {
        await store.refreshPresence()
        await vi.waitFor(() => expect(store.clients[0].credential_state).toBe(ended))
        expect(store.clients[0].connected).toBe(false)
        expect(store.clients[0].state).toBe('other')
        expect(store.clients[0].sessions ?? []).toEqual([])
      }
    })
  }
})
