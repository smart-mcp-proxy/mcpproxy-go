import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import Tools from '@/views/Tools.vue'
import ReviewScreen from '@/components/ReviewScreen.vue'
import api from '@/services/api'

// Spec 116 PR2 review round 1 (FR-017): every Web UI decision is bound to the
// incarnation of the data on screen.
//  - The global Tools view passes each row's server_incarnation.
//  - The review screen shows a failed per-tool decision on an unquarantined
//    server (its notice used to live in the quarantined-only decision bar).

vi.mock('@/services/api', () => ({
  default: {
    getGlobalTools: vi.fn(),
    setToolEnabled: vi.fn(),
    approveTools: vi.fn(),
    blockTools: vi.fn(),
    getServerReview: vi.fn(),
    listScanHistory: vi.fn(),
    getQueueProgress: vi.fn(),
  },
}))

const TOOLS = [
  { name: 'new_a', server_name: 'alpha', approval_status: 'pending', enabled: true, description: '', server_incarnation: 5 },
  { name: 'new_b', server_name: 'beta', approval_status: 'pending', enabled: true, description: '', server_incarnation: 6 },
]

describe('Tools view — expected_incarnation per row', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.getGlobalTools as any).mockResolvedValue({ success: true, data: { stats: { total: 2, enabled: 2, disabled: 0, pending_approval: 2 }, tools: TOOLS } })
    ;(api.approveTools as any).mockResolvedValue({ success: true, data: { approved: 1 } })
    ;(api.blockTools as any).mockResolvedValue({ success: true, data: { blocked: 1 } })
    ;(api.setToolEnabled as any).mockResolvedValue({ success: true, data: {} })
  })

  async function mountAndSelectAll() {
    const wrapper = mount(Tools, { global: { plugins: [createPinia()], stubs: { CollapsibleHintsPanel: { template: '<div />' } } } })
    await flushPromises()
    await wrapper.find('[data-test="tools-select-all"]').setValue(true)
    await flushPromises()
    return wrapper
  }

  it('batch approve and reject send each server its listed incarnation', async () => {
    let wrapper = await mountAndSelectAll()
    await wrapper.find('[data-test="batch-approve"]').trigger('click')
    await flushPromises()
    const approve = Object.fromEntries((api.approveTools as any).mock.calls.map((c: any[]) => [c[0], c[2]]))
    expect(approve).toEqual({ alpha: 5, beta: 6 })

    wrapper = await mountAndSelectAll()
    await wrapper.find('[data-test="batch-reject"]').trigger('click')
    await flushPromises()
    const block = Object.fromEntries((api.blockTools as any).mock.calls.map((c: any[]) => [c[0], c[2]]))
    expect(block).toEqual({ alpha: 5, beta: 6 })
  })

  it('batch enable/disable sends the row incarnation', async () => {
    const wrapper = await mountAndSelectAll()
    await wrapper.find('[data-test="batch-disable"]').trigger('click')
    await flushPromises()
    const calls = (api.setToolEnabled as any).mock.calls.map((c: any[]) => [c[0], c[3]])
    expect(calls.sort()).toEqual([['alpha', 5], ['beta', 6]])
  })
})

describe('ReviewScreen — per-tool failure on an unquarantined server', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
    ;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
    ;(api.getServerReview as any).mockResolvedValue({ success: true, data: {
      server: { name: 'acme', transport: 'stdio', quarantined: false, definitions_captured: true, incarnation: 3 },
      tools: [{ name: 't', description: 't', tier: 'read', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: true }],
    } })
  })

  it('shows a 409 server_replaced from Approve, bound to the review incarnation', async () => {
    const msg = "server_replaced: server 'acme' was removed or replaced since it was reviewed"
    ;(api.approveTools as any).mockResolvedValue({ success: false, error: msg })
    const w = mount(ReviewScreen, { props: { serverName: 'acme' }, global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()
    const approveBtn = w.findAll('button').find(b => b.text() === 'Approve')
    expect(approveBtn).toBeDefined()
    await approveBtn!.trigger('click')
    await flushPromises()
    expect((api.approveTools as any).mock.calls[0]).toEqual(['acme', ['t'], 3])
    expect(w.get('[data-test="review-tool-notice"]').text()).toContain('server_replaced')
  })

  it('shows a failed Reject the same way', async () => {
    ;(api.blockTools as any).mockResolvedValue({ success: false, error: 'Block failed: boom' })
    const w = mount(ReviewScreen, { props: { serverName: 'acme' }, global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()
    const rejectBtn = w.findAll('button').find(b => b.text() === 'Reject')
    await rejectBtn!.trigger('click')
    await flushPromises()
    expect(w.get('[data-test="review-tool-notice"]').text()).toContain('boom')
  })
})
