import { describe, it, expect, beforeEach, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useServersStore } from '@/stores/servers'
import api from '@/services/api'

// The server list has three independent, uncancelled writers: a fetch from any
// component (App.vue and Dashboard.vue both issue one on mount), a silent
// background refresh, and the Spec 047 SSE full-list payload. A response that
// was already in flight when a newer list was applied must not be able to
// resurrect the older list.

vi.mock('@/services/api', () => ({
  default: {
    getServers: vi.fn(),
    deleteServer: vi.fn(),
    securityApprove: vi.fn(),
    unquarantineServer: vi.fn(),
  },
}))

function mkServer(name: string) {
  return {
    name,
    protocol: 'http' as const,
    enabled: true,
    quarantined: false,
    connected: true,
    connecting: false,
    tool_count: 1,
  }
}

describe('useServersStore — server list write sequencing', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('drops a stale fetch response that lands after a newer one', async () => {
    let resolveFirst: (v: unknown) => void = () => {}
    const first = new Promise((resolve) => {
      resolveFirst = resolve
    })
    ;(api.getServers as any).mockReturnValueOnce(first)
    ;(api.getServers as any).mockResolvedValueOnce({
      success: true,
      data: { servers: [mkServer('added-by-user')] },
    })

    const store = useServersStore()
    // Overlapping requests, e.g. App.vue's and Dashboard.vue's mount fetches.
    const slow = store.fetchServers()
    await store.fetchServers()

    expect(store.serverCount.total).toBe(1)

    // The older request finally answers with the pre-addition (empty) list.
    resolveFirst({ success: true, data: { servers: [] } })
    await slow

    expect(store.serverCount.total).toBe(1)
    expect(store.servers[0].name).toBe('added-by-user')
  })

  it('does not let an in-flight fetch overwrite a list delivered by SSE', async () => {
    let resolveFetch: (v: unknown) => void = () => {}
    const pending = new Promise((resolve) => {
      resolveFetch = resolve
    })
    ;(api.getServers as any).mockReturnValueOnce(pending)

    const store = useServersStore()
    const inFlight = store.fetchServers()

    window.dispatchEvent(
      new CustomEvent('mcpproxy:servers-changed', {
        detail: { payload: { servers: [mkServer('from-sse')] } },
      })
    )
    expect(store.serverCount.total).toBe(1)

    resolveFetch({ success: true, data: { servers: [] } })
    await inFlight

    expect(store.serverCount.total).toBe(1)
    expect(store.servers[0].name).toBe('from-sse')
  })

  it('marks the list loaded when it arrives by SSE rather than by fetch', async () => {
    const store = useServersStore()
    expect(store.loaded).toBe(false)

    window.dispatchEvent(
      new CustomEvent('mcpproxy:servers-changed', {
        detail: { payload: { servers: [] } },
      })
    )

    // An empty authoritative list is still an answer: consumers must be able to
    // tell "no servers configured" from "we don't know yet".
    expect(store.loaded).toBe(true)
    expect(store.serverCount.total).toBe(0)
  })

  it('does not let an in-flight fetch restore a server the user just deleted', async () => {
    // Seed the store with one server.
    ;(api.getServers as any).mockResolvedValueOnce({
      success: true,
      data: { servers: [mkServer('doomed')] },
    })
    const store = useServersStore()
    await store.fetchServers()

    // A refresh starts, then the user deletes the server before it resolves.
    let resolveRefresh: (v: unknown) => void = () => {}
    ;(api.getServers as any).mockReturnValueOnce(
      new Promise((resolve) => {
        resolveRefresh = resolve
      })
    )
    const refresh = store.fetchServers(true)
    ;(api.deleteServer as any).mockResolvedValueOnce({ success: true })
    await store.deleteServer('doomed')
    expect(store.serverCount.total).toBe(0)

    // The refresh answers with the pre-deletion list.
    resolveRefresh({ success: true, data: { servers: [mkServer('doomed')] } })
    await refresh

    expect(store.serverCount.total).toBe(0)
  })

  it('does not mark the list loaded when the fetch fails', async () => {
    ;(api.getServers as any).mockResolvedValueOnce({ success: false, error: 'boom' })

    const store = useServersStore()
    await store.fetchServers()

    expect(store.loaded).toBe(false)
    expect(store.loading.error).toBe('boom')
  })
})
