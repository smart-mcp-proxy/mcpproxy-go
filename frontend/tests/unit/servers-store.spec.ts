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
