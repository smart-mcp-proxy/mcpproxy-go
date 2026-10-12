import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import Tools from '@/views/Tools.vue'
import ReviewScreen from '@/components/ReviewScreen.vue'
import api from '@/services/api'

// Spec 116 PR2 review round 2 (Sol chunk 4): a decision the operator made on
// one incarnation never carries over to a same-name replacement with
// identical tools. Selections and explicit checks are bound to the incarnation
// they were made on, and a refresh that returns a new incarnation drops them.

vi.mock('@/services/api', () => ({
  default: {
    getGlobalTools: vi.fn(),
    setToolEnabled: vi.fn(),
    approveTools: vi.fn(),
    blockTools: vi.fn(),
    getServerReview: vi.fn(),
    securityApprove: vi.fn(),
    listScanHistory: vi.fn(),
    getQueueProgress: vi.fn(),
  },
}))

const rows = (inc: number) => [
  { name: 'new_a', server_name: 'alpha', approval_status: 'pending', enabled: true, description: '', server_incarnation: inc },
  { name: 'new_b', server_name: 'alpha', approval_status: 'pending', enabled: true, description: '', server_incarnation: inc },
]
const listing = (inc: number) => ({ success: true, data: { stats: { total: 2, enabled: 2, disabled: 0, pending_approval: 2 }, tools: rows(inc) } })

describe('Tools view — selection is bound to the listed incarnation', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.approveTools as any).mockResolvedValue({ success: true, data: { approved: 2 } })
    ;(api.blockTools as any).mockResolvedValue({ success: true, data: { blocked: 2 } })
    ;(api.setToolEnabled as any).mockResolvedValue({ success: true, data: {} })
  })

  it('a refresh onto a re-added server drops the selection made on the old one', async () => {
    ;(api.getGlobalTools as any).mockResolvedValue(listing(5))
    const wrapper = mount(Tools, { global: { plugins: [createPinia()], stubs: { CollapsibleHintsPanel: { template: '<div />' } } } })
    await flushPromises()
    await wrapper.find('[data-test="tools-select-all"]').setValue(true)
    await flushPromises()
    expect(wrapper.find('[data-test="tools-batch-bar"]').exists()).toBe(true)

    // alpha removed and re-added (incarnation 7) with identical tools; Refresh.
    ;(api.getGlobalTools as any).mockResolvedValue(listing(7))
    await (wrapper.vm as any).loadTools()
    await flushPromises()

    expect(wrapper.find('[data-test="tools-batch-bar"]').exists()).toBe(false)
    expect((wrapper.vm as any).selectedKeys.size).toBe(0)
    await (wrapper.vm as any).batchApproval('approve')
    await (wrapper.vm as any).batchEnable(false)
    await flushPromises()
    expect(api.approveTools).not.toHaveBeenCalled()
    expect(api.setToolEnabled).not.toHaveBeenCalled()
  })

  it('a refresh of the same incarnation keeps the selection', async () => {
    ;(api.getGlobalTools as any).mockResolvedValue(listing(5))
    const wrapper = mount(Tools, { global: { plugins: [createPinia()], stubs: { CollapsibleHintsPanel: { template: '<div />' } } } })
    await flushPromises()
    await wrapper.find('[data-test="tools-select-all"]').setValue(true)
    await flushPromises()
    await (wrapper.vm as any).loadTools()
    await flushPromises()
    expect((wrapper.vm as any).selectedKeys.size).toBe(2)
    await wrapper.find('[data-test="batch-approve"]').trigger('click')
    await flushPromises()
    expect((api.approveTools as any).mock.calls[0][2]).toBe(5)
  })
})

const tool = (name: string, extra: Record<string, unknown> = {}) => ({
  name, description: name, tier: 'read', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: true, ...extra,
})
const review = (inc: number) => ({ success: true, data: {
  server: { name: 'acme', transport: 'stdio', quarantined: true, definitions_captured: true, incarnation: inc },
  tools: [tool('read_a'), tool('write_a', { tier: 'write', default_allowed: false })],
} })

describe('ReviewScreen — explicit choices are bound to the reviewed incarnation', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(api.securityApprove as any).mockResolvedValue({ success: true })
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
    ;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
  })

  const checked = (w: ReturnType<typeof mount>, name: string) => (w.get(`[data-test="review-allow-${name}"]`).element as HTMLInputElement).checked

  it('a refresh onto a same-name replacement resets an explicit allow', async () => {
    ;(api.getServerReview as any).mockResolvedValue(review(3))
    const w = mount(ReviewScreen, { props: { serverName: 'acme' }, global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()
    await w.get('[data-test="review-allow-write_a"]').setValue(true)
    expect(checked(w, 'write_a')).toBe(true)

    ;(api.getServerReview as any).mockResolvedValue(review(9))
    window.dispatchEvent(new CustomEvent('mcpproxy:review-changed'))
    await flushPromises()
    expect(checked(w, 'write_a')).toBe(false)

    await w.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenLastCalledWith('acme', false, ['write_a'], 9)
  })

  it('a refresh of the same incarnation keeps the explicit allow', async () => {
    ;(api.getServerReview as any).mockResolvedValue(review(3))
    const w = mount(ReviewScreen, { props: { serverName: 'acme' }, global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()
    await w.get('[data-test="review-allow-write_a"]').setValue(true)
    window.dispatchEvent(new CustomEvent('mcpproxy:review-changed'))
    await flushPromises()
    expect(checked(w, 'write_a')).toBe(true)
  })
})
