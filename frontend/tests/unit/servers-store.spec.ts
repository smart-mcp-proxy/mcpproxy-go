import { describe, it, expect, beforeEach, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useServersStore } from '@/stores/servers'
import api from '@/services/api'

vi.mock('@/services/api', () => ({
  default: {
    getServers: vi.fn(),
    securityApprove: vi.fn(),
    unquarantineServer: vi.fn(),
  },
}))

describe('useServersStore — mergeServers field-clearing (issue #438)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  function mkServer(overrides: Record<string, unknown> = {}) {
    return {
      name: 'srv',
      protocol: 'http' as const,
      enabled: true,
      quarantined: false,
      connected: true,
      connecting: false,
      tool_count: 4,
      ...overrides,
    }
  }

  it('drops the stale `quarantine` field when the backend stops emitting it', async () => {
    // The backend's enrichServersWithQuarantineStats sets `Quarantine` only
    // when pending > 0 || changed > 0. After "Approve all tools" the field
    // disappears from the JSON entirely; without a clear-on-merge path the
    // old `pending_count: 5` would survive on the in-place reactive object
    // and ServerCard would keep rendering the "5 pending approval" badge.
    ;(api.getServers as any).mockResolvedValueOnce({
      success: true,
      data: { servers: [mkServer({ quarantine: { pending_count: 5, changed_count: 0 } })] },
    })

    const store = useServersStore()
    await store.fetchServers()
    expect(store.servers[0].quarantine).toEqual({ pending_count: 5, changed_count: 0 })

    ;(api.getServers as any).mockResolvedValueOnce({
      success: true,
      data: { servers: [mkServer()] }, // no quarantine field at all
    })

    await store.fetchServers()
    expect(store.servers[0].quarantine).toBeUndefined()
  })

  it('drops `last_error` on recovery (regression coverage for the original special case)', async () => {
    ;(api.getServers as any).mockResolvedValueOnce({
      success: true,
      data: { servers: [mkServer({ last_error: 'connection refused' })] },
    })

    const store = useServersStore()
    await store.fetchServers()
    expect(store.servers[0].last_error).toBe('connection refused')

    ;(api.getServers as any).mockResolvedValueOnce({
      success: true,
      data: { servers: [mkServer()] },
    })

    await store.fetchServers()
    expect(store.servers[0].last_error).toBeUndefined()
  })

  it('drops `oauth_status`, `token_expires_at`, and `user_logged_out` when no longer present', async () => {
    ;(api.getServers as any).mockResolvedValueOnce({
      success: true,
      data: {
        servers: [
          mkServer({
            oauth_status: 'expired',
            token_expires_at: '2026-04-30T00:00:00Z',
            user_logged_out: true,
          }),
        ],
      },
    })
    const store = useServersStore()
    await store.fetchServers()

    ;(api.getServers as any).mockResolvedValueOnce({
      success: true,
      data: { servers: [mkServer()] },
    })
    await store.fetchServers()

    expect(store.servers[0].oauth_status).toBeUndefined()
    expect(store.servers[0].token_expires_at).toBeUndefined()
    expect(store.servers[0].user_logged_out).toBeUndefined()
  })

  it('preserves the existing object reference across merges (identity stability for v-memo)', async () => {
    ;(api.getServers as any).mockResolvedValueOnce({
      success: true,
      data: { servers: [mkServer({ quarantine: { pending_count: 2, changed_count: 0 } })] },
    })
    const store = useServersStore()
    await store.fetchServers()
    const ref1 = store.servers[0]

    ;(api.getServers as any).mockResolvedValueOnce({
      success: true,
      data: { servers: [mkServer()] },
    })
    await store.fetchServers()
    const ref2 = store.servers[0]

    // Same reactive object — v-memo on ServerCard still sees stable identity.
    expect(ref2).toBe(ref1)
    expect(ref2.quarantine).toBeUndefined()
  })

  it('still updates and adds present fields (sanity: the clear logic does not break normal merges)', async () => {
    ;(api.getServers as any).mockResolvedValueOnce({
      success: true,
      data: { servers: [mkServer({ tool_count: 1 })] },
    })
    const store = useServersStore()
    await store.fetchServers()

    ;(api.getServers as any).mockResolvedValueOnce({
      success: true,
      data: { servers: [mkServer({ tool_count: 9, last_error: 'oops' })] },
    })
    await store.fetchServers()

    expect(store.servers[0].tool_count).toBe(9)
    expect(store.servers[0].last_error).toBe('oops')
  })
})

describe('useServersStore — securityApproveServer (F-04)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('calls api.securityApprove with force=false by default', async () => {
    ;(api.securityApprove as any).mockResolvedValueOnce({ success: true })
    const store = useServersStore()
    store.servers.push({
      name: 'srv',
      protocol: 'http',
      enabled: true,
      quarantined: true,
      connected: false,
      connecting: false,
      tool_count: 0,
    } as any)

    const ok = await store.securityApproveServer('srv')

    expect(ok).toBe(true)
    expect(api.securityApprove).toHaveBeenCalledWith('srv', false)
    // Optimistic update: server should be marked unquarantined
    expect(store.servers[0].quarantined).toBe(false)
  })

  it('passes force=true through to api.securityApprove', async () => {
    ;(api.securityApprove as any).mockResolvedValueOnce({ success: true })
    const store = useServersStore()

    await store.securityApproveServer('srv', true)

    expect(api.securityApprove).toHaveBeenCalledWith('srv', true)
  })

  it('throws when the API reports failure', async () => {
    ;(api.securityApprove as any).mockResolvedValueOnce({
      success: false,
      error: 'scan required',
    })
    const store = useServersStore()

    await expect(store.securityApproveServer('srv')).rejects.toThrow('scan required')
    // It should NOT have fallen back to unquarantineServer
    expect(api.unquarantineServer).not.toHaveBeenCalled()
  })
})

describe('useServersStore — a successful list clears a stale error', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  const srv = { name: 'srv', protocol: 'http' as const, enabled: true, connected: true, tool_count: 1 }

  it('clears loading.error once a list arrives', async () => {
    // `loading.error` was write-only: a silent background refresh sets it and
    // nothing ever cleared it, so one transient blip pinned an error on the
    // Servers page for the rest of the session.
    const store = useServersStore()

    ;(api.getServers as any).mockResolvedValue({ success: false, error: 'network down' })
    await store.fetchServers(true)
    expect(store.loading.error).toBe('network down')

    ;(api.getServers as any).mockResolvedValue({ success: true, data: { servers: [srv] } })
    await store.fetchServers(true)

    expect(store.loading.error).toBeNull()
    expect(store.servers).toHaveLength(1)
    expect(store.loaded).toBe(true)
  })

  it('drops a failure from a request the newer list already superseded', async () => {
    // Failures are sequenced like successes: an older request failing after a
    // newer one delivered a list must not raise a stale error over fresh data.
    const store = useServersStore()

    let failOld: (v: unknown) => void = () => {}
    ;(api.getServers as any).mockReturnValueOnce(
      new Promise((resolve) => { failOld = resolve })
    )
    const oldFetch = store.fetchServers(true)

    // A newer request completes first, with a real list.
    ;(api.getServers as any).mockResolvedValueOnce({ success: true, data: { servers: [srv] } })
    await store.fetchServers(true)
    expect(store.loading.error).toBeNull()

    // Only now does the older one come back, failing.
    failOld({ success: false, error: 'stale failure' })
    await oldFetch

    expect(store.loading.error).toBeNull()
    expect(store.servers).toHaveLength(1)
  })

  it('still records a failure that happens after a successful load', async () => {
    // Clearing on success must not make errors unreportable afterwards.
    const store = useServersStore()

    ;(api.getServers as any).mockResolvedValue({ success: true, data: { servers: [srv] } })
    await store.fetchServers(true)
    expect(store.loading.error).toBeNull()

    ;(api.getServers as any).mockResolvedValue({ success: false, error: 'network down' })
    await store.fetchServers(true)

    expect(store.loading.error).toBe('network down')
    // …and the servers we already hold are still there to fall back on.
    expect(store.servers).toHaveLength(1)
  })
})
