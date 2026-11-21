import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReviewScreen from '@/components/ReviewScreen.vue'
import api from '@/services/api'

// Spec 125 review round 1, finding 4: the open review reloads (and clears its
// scanning state) only for a review.changed about its own server's tools. A
// frame for another server, or one a skills change caused, leaves it alone.

vi.mock('@/services/api', () => ({ default: {
  getServerReview: vi.fn(), securityApprove: vi.fn(), securityReject: vi.fn(),
  approveTools: vi.fn(), blockTools: vi.fn(), discoverServerTools: vi.fn(), listScanHistory: vi.fn(), scanAll: vi.fn(), getQueueProgress: vi.fn(), cancelAllScans: vi.fn(), startScan: vi.fn(), quarantineServer: vi.fn(),
} }))

const review = { success: true, data: {
  server: { name: 'alpha', transport: 'stdio', quarantined: true, definitions_captured: true },
  tools: [{ name: 't', description: 't', tier: 'read', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: true }],
} }

const frame = (payload?: Record<string, unknown>) =>
  new CustomEvent('mcpproxy:review-changed', payload ? { detail: { payload, timestamp: 1 } } : undefined)

describe('ReviewScreen review.changed filter', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(api.getServerReview as any).mockResolvedValue(review)
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
    ;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
  })

  async function open() {
    const wrapper = mount(ReviewScreen, { props: { serverName: 'alpha' }, global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()
    ;(api.getServerReview as any).mockClear()
    return wrapper
  }

  it('reloads for its own server and for a frame with no detail', async () => {
    const wrapper = await open()
    window.dispatchEvent(frame({ server: 'alpha' })); await flushPromises()
    expect(api.getServerReview).toHaveBeenCalledTimes(1)
    window.dispatchEvent(frame()); await flushPromises()
    expect(api.getServerReview).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('ignores another server and a skills-caused frame', async () => {
    const wrapper = await open()
    window.dispatchEvent(frame({ server: 'beta' })); await flushPromises()
    window.dispatchEvent(frame({ server: 'alpha', skills: true })); await flushPromises()
    expect(api.getServerReview).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
