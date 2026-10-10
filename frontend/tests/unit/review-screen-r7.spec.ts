import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReviewScreen from '@/components/ReviewScreen.vue'
import api from '@/services/api'

// UX-02 cross-review round 7: the "Approve without seeing tools" confirmation
// is bound to the empty snapshot that opened it, not to whatever review is on
// screen when the operator confirms.

vi.mock('@/services/api', () => ({ default: {
  getServerReview: vi.fn(), securityApprove: vi.fn(), securityReject: vi.fn(),
  approveTools: vi.fn(), blockTools: vi.fn(), discoverServerTools: vi.fn(), listScanHistory: vi.fn(), scanAll: vi.fn(), getQueueProgress: vi.fn(), cancelAllScans: vi.fn(), startScan: vi.fn(), quarantineServer: vi.fn(),
} }))

const stubs = { RouterLink: { template: '<a><slot /></a>' } }
const blindReview = () => ({
  success: true,
  data: { server: { name: 'fixture', transport: 'stdio', quarantined: true, definitions_captured: false }, tools: [] as any[] },
})
// A clean read tool the core pre-selects: confirming against this review
// would approve it with its fresh hash and an empty block list.
const refreshedReview = () => ({
  success: true,
  data: {
    server: { name: 'fixture', transport: 'stdio', quarantined: true, definitions_captured: true },
    tools: [{ name: 'read_new', description: 'read', tier: 'read', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: true, current_hash: 'h-read-new' }],
  },
})
const blindButton = (wrapper: any) => wrapper.findAll('button').find((b: any) => b.text() === 'Approve without seeing tools' && b.classes().includes('btn-warning'))

beforeEach(() => {
  vi.clearAllMocks()
  ;(api.securityApprove as any).mockResolvedValue({ success: true })
  ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
  ;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
})

async function openBlindDialog() {
  ;(api.getServerReview as any).mockResolvedValue(blindReview())
  const wrapper = mount(ReviewScreen, { props: { serverName: 'fixture' }, global: { stubs } })
  await flushPromises()
  for (const d of wrapper.findAll('dialog')) (d.element as any).showModal = vi.fn()
  await wrapper.get('[data-test="review-approve-server"]').trigger('click')
  expect(blindButton(wrapper)).toBeTruthy()
  return wrapper
}

describe('ReviewScreen blind approval is bound to the snapshot that opened it (UX-02 r7)', () => {
  it('a review refreshed while the confirmation is open never approves the new tool', async () => {
    const wrapper = await openBlindDialog()

    ;(api.getServerReview as any).mockResolvedValue(refreshedReview())
    window.dispatchEvent(new Event('mcpproxy:review-changed'))
    await flushPromises()
    expect(wrapper.find('[data-test="review-tool-read_new"]').exists()).toBe(true)

    await blindButton(wrapper)!.trigger('click')
    await flushPromises()
    for (const call of (api.securityApprove as any).mock.calls) {
      expect(call[3]).toEqual({}) // never the refreshed tool's hash
    }
    expect(api.securityApprove).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="review-stale-notice"]').text()).toMatch(/nothing was approved/i)
  })

  it('a dangerous answer and its force retry keep the empty-snapshot decision captured at the click', async () => {
    const wrapper = await openBlindDialog()
    ;(api.securityApprove as any).mockResolvedValueOnce({ success: false, error: 'Approval blocked: dangerous findings detected; use force' })
    await blindButton(wrapper)!.trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenLastCalledWith('fixture', false, [], {})

    ;(api.getServerReview as any).mockResolvedValue(refreshedReview())
    window.dispatchEvent(new Event('mcpproxy:review-changed'))
    await flushPromises()
    const force = wrapper.findAll('button').find(b => b.text() === 'Force approve server')
    await force!.trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledTimes(2)
    expect(api.securityApprove).toHaveBeenLastCalledWith('fixture', true, [], {})
  })

  it('cancelling the confirmation drops the captured decision', async () => {
    const wrapper = await openBlindDialog()
    await wrapper.findAll('button').find(b => b.text() === 'Cancel')!.trigger('click')
    ;(api.getServerReview as any).mockResolvedValue(refreshedReview())
    window.dispatchEvent(new Event('mcpproxy:review-changed'))
    await flushPromises()
    // The refreshed review is approved on its own terms, bound to its hashes.
    await wrapper.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenLastCalledWith('fixture', false, [], { read_new: 'h-read-new' })
  })
})
