import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReviewScreen from '@/components/ReviewScreen.vue'
import api from '@/services/api'

vi.mock('@/services/api', () => ({ default: {
  getServerReview: vi.fn(), securityApprove: vi.fn(), securityReject: vi.fn(),
  approveTools: vi.fn(), blockTools: vi.fn(), discoverServerTools: vi.fn(), listScanHistory: vi.fn(), scanAll: vi.fn(), getQueueProgress: vi.fn(), cancelAllScans: vi.fn(), startScan: vi.fn(), quarantineServer: vi.fn(),
} }))

const stubs = { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } }
const tool = { name: 't1', description: 'd', tier: 'read', scan_verdict: 'clean', state: 'pending' }
function review(captured: boolean, extra: Record<string, unknown> = {}) {
  return { success: true, data: { server: { name: 'fx', transport: 'stdio', quarantined: true, definitions_captured: captured, ...extra }, tools: captured ? [tool] : [] } }
}

// Spec 134 FR-032 / SC-009: while definitions are not captured the primary button never approves.
describe('ReviewScreen not-captured branch (Spec 134 FR-032, SC-009)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
    ;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
  })

  it('not captured: fetch is the only primary button; approve is secondary and confirms with the server name', async () => {
    ;(api.getServerReview as any).mockResolvedValue(review(false))
    const w = mount(ReviewScreen, { props: { serverName: 'fx' }, ...stubs }); await flushPromises()
    const fetchBtn = w.get('[data-test="review-fetch-definitions"]')
    const approve = w.get('[data-test="review-approve-server"]')
    expect(fetchBtn.classes()).toContain('btn-primary')
    expect(approve.classes()).not.toContain('btn-primary')
    expect(w.get('[data-test="review-decision-bar"]').findAll('button.btn-primary')).toHaveLength(0)
    expect(w.get('[data-test="review-no-definitions"]').findAll('button.btn-primary').map(b => b.text())).toEqual(['Fetch tool definitions'])
    expect(w.get('[data-test="review-blind-server"]').text()).toContain('fx')
    expect(w.get('[data-test="review-blind-confirm"]').text()).toContain('fx')
    const dialog = w.findAll('dialog')[0].element as HTMLDialogElement
    dialog.showModal = vi.fn()
    await approve.trigger('click')
    expect(dialog.showModal).toHaveBeenCalled()
    expect(api.securityApprove).not.toHaveBeenCalled()
  })

  it('captured: the counted approve button is primary and no fetch button is shown', async () => {
    ;(api.getServerReview as any).mockResolvedValue(review(true))
    const w = mount(ReviewScreen, { props: { serverName: 'fx' }, ...stubs }); await flushPromises()
    expect(w.get('[data-test="review-approve-server"]').classes()).toContain('btn-primary')
    expect(w.get('[data-test="review-approve-server"]').text()).toBe('Approve server (0 of 1 tool)')
    expect(w.find('[data-test="review-fetch-definitions"]').exists()).toBe(false)
  })

  it('a failed attempt is reported on its own line, keeps the not-captured branch and approves nothing', async () => {
    ;(api.getServerReview as any).mockResolvedValue(review(false))
    ;(api.discoverServerTools as any).mockResolvedValue({ success: false, error: 'boom' })
    const w = mount(ReviewScreen, { props: { serverName: 'fx' }, ...stubs }); await flushPromises()
    await w.get('[data-test="review-fetch-definitions"]').trigger('click'); await flushPromises()
    expect(w.get('[data-test="review-fetch-outcome"]').text()).toContain('boom')
    expect(w.find('[data-test="review-no-definitions"]').exists()).toBe(true)
    expect(w.find('.alert-error').exists()).toBe(false)
    expect(api.securityApprove).not.toHaveBeenCalled()
  })

  it('zero tools and disabled server are reported; the branch follows the reloaded flag only', async () => {
    ;(api.getServerReview as any).mockResolvedValue(review(false, { enabled: false }))
    ;(api.discoverServerTools as any).mockResolvedValue({ success: true })
    const w = mount(ReviewScreen, { props: { serverName: 'fx' }, ...stubs }); await flushPromises()
    await w.get('[data-test="review-fetch-definitions"]').trigger('click'); await flushPromises()
    expect(w.get('[data-test="review-fetch-outcome"]').text()).toContain('disabled')
    expect(w.find('[data-test="review-no-definitions"]').exists()).toBe(true)
    ;(api.getServerReview as any).mockResolvedValue(review(true))
    await w.get('[data-test="review-fetch-definitions"]').trigger('click'); await flushPromises()
    expect(w.find('[data-test="review-no-definitions"]').exists()).toBe(false)
    expect(w.get('[data-test="review-approve-server"]').classes()).toContain('btn-primary')
  })
})
