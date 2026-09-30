import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

vi.mock('@/services/api', () => ({
  default: {
    getClients: vi.fn(),
    getClient: vi.fn(),
    getRouting: vi.fn(),
  },
}))

import api from '@/services/api'
import { useClientsStore } from '@/stores/clients'

const listRow = { id: 'cursor', display_name: 'Cursor', kind: 'supported', state: 'connected', installed: true, connected: false, active_sessions: 1, calls_24h: 3 }

describe('clients store refreshPresence', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('keeps detail-only fields of an expanded row across a presence poll', async () => {
    const store = useClientsStore()
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [listRow] } })
    await store.refreshPresence()

    ;(api.getClient as any).mockResolvedValue({
      success: true,
      data: { ...listRow, connected: true, connection_unverified: true, sessions: [{ id: 's1' }] },
    })
    await store.loadDetail('cursor')

    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [{ ...listRow, active_sessions: 2, calls_24h: 9 }] } })
    await store.refreshPresence()

    const row = store.clients[0]
    expect(row.sessions).toEqual([{ id: 's1' }])
    expect(row.connected).toBe(true)
    expect(row.connection_unverified).toBe(true)
    expect(row.active_sessions).toBe(2)
    expect(row.calls_24h).toBe(9)
  })

  it('drops rows the core no longer lists and adds new ones', async () => {
    const store = useClientsStore()
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [listRow] } })
    await store.refreshPresence()
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [{ ...listRow, id: 'zed' }] } })
    await store.refreshPresence()
    expect(store.clients.map(c => c.id)).toEqual(['zed'])
  })
})
