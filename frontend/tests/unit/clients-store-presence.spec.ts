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

    // Same presence (active_sessions/last_seen): the detail fields survive.
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [{ ...listRow, calls_24h: 9 }] } })
    await store.refreshPresence()

    const row = store.clients[0]
    expect(row.sessions).toEqual([{ id: 's1' }])
    expect(row.connected).toBe(true)
    expect(row.connection_unverified).toBe(true)
    expect(row.active_sessions).toBe(1)
    expect(row.calls_24h).toBe(9)
  })

  it('takes presence from the poll and reloads the detail when it changed (#1451-10)', async () => {
    const store = useClientsStore()
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [listRow] } })
    await store.refreshPresence()
    ;(api.getClient as any).mockResolvedValue({
      success: true,
      data: { ...listRow, connected: true, sessions: [{ id: 's1' }] },
    })
    await store.loadDetail('cursor')
    ;(api.getClient as any).mockClear()
    // The reload answers with the converged detail.
    let release: (v: unknown) => void = () => {}
    ;(api.getClient as any).mockReturnValue(new Promise(resolve => { release = resolve }))

    ;(api.getClients as any).mockResolvedValue({
      success: true,
      data: { clients: [{ ...listRow, active_sessions: 2, last_seen: '2026-10-05T10:00:00Z' }] },
    })
    await store.refreshPresence()

    // Immediately after the poll: presence from the poll, stale detail dropped.
    expect(store.clients[0].active_sessions).toBe(2)
    expect(store.clients[0].last_seen).toBe('2026-10-05T10:00:00Z')
    expect(store.clients[0].sessions).toBeUndefined()
    expect(api.getClient).toHaveBeenCalledWith('cursor')
    release({
      success: true,
      data: { ...listRow, active_sessions: 2, last_seen: '2026-10-05T10:00:00Z', sessions: [{ id: 's1' }, { id: 's2' }] },
    })
    await vi.waitFor(() => expect(store.clients[0].sessions).toEqual([{ id: 's1' }, { id: 's2' }]))
  })

  it('drops rows the core no longer lists and adds new ones', async () => {
    const store = useClientsStore()
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [listRow] } })
    await store.refreshPresence()
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [{ ...listRow, id: 'zed' }] } })
    await store.refreshPresence()
    expect(store.clients.map(c => c.id)).toEqual(['zed'])
  })

  it('keeps detail-resolved fields when the detail response has NO sessions key', async () => {
    const store = useClientsStore()
    // The metadata list row reports the stat-only guess; the detail read resolves it.
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [{ ...listRow, state: 'available', installed: false, connected: false }] } })
    await store.refreshPresence()

    // Backend omits `sessions` (json omitempty) when there are none.
    ;(api.getClient as any).mockResolvedValue({
      success: true,
      data: {
        ...listRow, state: 'connected', installed: true, connected: true, connection_unverified: true,
        config_path: '/home/u/.cursor/mcp.json', display_path: '~/.cursor/mcp.json',
      },
    })
    await store.loadDetail('cursor')

    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [{ ...listRow, state: 'available', installed: false, connected: false, calls_24h: 11 }] } })
    await store.refreshPresence()

    const row = store.clients[0]
    expect(row.state).toBe('connected')
    expect(row.installed).toBe(true)
    expect(row.connected).toBe(true)
    expect(row.connection_unverified).toBe(true)
    expect(row.config_path).toBe('/home/u/.cursor/mcp.json')
    expect(row.display_path).toBe('~/.cursor/mcp.json')
    expect(row.active_sessions).toBe(1)
    expect(row.calls_24h).toBe(11)
  })

  it('refreshes only metadata for rows whose detail was never loaded', async () => {
    const store = useClientsStore()
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [listRow] } })
    await store.refreshPresence()
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [{ ...listRow, state: 'available', connected: true }] } })
    await store.refreshPresence()
    expect(store.clients[0].state).toBe('available')
    expect(store.clients[0].connected).toBe(true)
  })
})
