import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { CREDENTIALS_CHANGED_EVENT } from '@/stores/clients'

vi.mock('@/services/api', () => ({ default: { getClients: vi.fn(), getClient: vi.fn(), getRouting: vi.fn() } }))

// Spec 115 review code-r2 (UI chunk): a detail that was ALREADY loaded before
// a credential lifecycle change must not keep the row connected. Revocation and
// lease expiry can leave active_sessions and last_seen unchanged, so the
// roster's credential_state change itself invalidates the detail-derived
// presence fields.
describe('an already loaded client detail after a credential lifecycle change', () => {
  // Each store listens on window; dispose it so a later test's event does not
  // also drive an earlier test's store.
  const stores: { $dispose: () => void }[] = []
  beforeEach(() => setActivePinia(createPinia()))
  afterEach(() => { for (const s of stores.splice(0)) s.$dispose() })

  const base = { id: 'w', display_name: 'w', kind: 'custom', installed: false, active_sessions: 1, last_seen: '2026-10-10T10:00:00Z', calls_24h: 0 }
  const active = { ...base, credential_state: 'client', connected: true, state: 'connected_seen' }

  for (const ended of ['revoked', 'expired'] as const) {
    it(`shows the worker disconnected at once and after later polls (${ended}, unchanged presence)`, async () => {
      const api = (await import('@/services/api')).default as any
      const { useClientsStore } = await import('@/stores/clients')
      api.getClients = vi.fn().mockResolvedValue({ success: true, data: { clients: [active] } })
      api.getClient = vi.fn().mockResolvedValue({ success: true, data: { ...active, sessions: [{ id: 'live' }] } })
      const store = useClientsStore()
    stores.push(store)
      stores.push(store)
      await store.refreshPresence()
      await store.loadDetail('w')
      expect(store.clients[0].connected).toBe(true)

      const endedRoster = { ...base, credential_state: ended, connected: false, state: 'other' }
      api.getClients = vi.fn().mockResolvedValue({ success: true, data: { clients: [endedRoster] } })
      api.getClient = vi.fn().mockResolvedValue({ success: true, data: { ...endedRoster, sessions: [] } })
      window.dispatchEvent(new Event(CREDENTIALS_CHANGED_EVENT))
      await vi.waitFor(() => expect(store.clients[0].credential_state).toBe(ended))
      expect(store.clients[0].connected).toBe(false)
      expect(store.clients[0].state).toBe('other')
      // The detail is re-requested so the row converges on one source.
      await vi.waitFor(() => expect(api.getClient).toHaveBeenCalledTimes(1))
      await vi.waitFor(() => expect(store.clients[0].sessions).toEqual([]))

      for (let i = 0; i < 2; i++) {
        await store.refreshPresence()
        expect(store.clients[0].credential_state).toBe(ended)
        expect(store.clients[0].connected).toBe(false)
        expect(store.clients[0].state).toBe('other')
      }
      // Unchanged roster polls keep the reloaded detail: no refetch storm.
      expect(api.getClient).toHaveBeenCalledTimes(1)
    })
  }

  it('a supported client whose detail resolves a different credential_state is not refetched every poll', async () => {
    const api = (await import('@/services/api')).default as any
    const { useClientsStore } = await import('@/stores/clients')
    const roster = { ...base, kind: 'supported', credential_state: 'unknown', connected: true, state: 'connected_seen' }
    api.getClients = vi.fn().mockResolvedValue({ success: true, data: { clients: [roster] } })
    api.getClient = vi.fn().mockResolvedValue({ success: true, data: { ...roster, credential_state: 'client', sessions: [{ id: 's' }] } })
    const store = useClientsStore()
    stores.push(store)
    await store.refreshPresence()
    await store.loadDetail('w')
    await store.refreshPresence()
    await store.refreshPresence()
    expect(api.getClient).toHaveBeenCalledTimes(1)
    expect(store.clients[0].sessions).toEqual([{ id: 's' }])
  })
})
