import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReviewScreen from '@/components/ReviewScreen.vue'
import api from '@/services/api'

// UX-02 cross-review round 8: a server rejection (or a per-tool block) answers
// only the review session that sent it. A late answer for server A must not
// unlock B's in-flight approval, reload, or touch B's state.

vi.mock('@/services/api', () => ({ default: {
  getServerReview: vi.fn(), securityApprove: vi.fn(), securityReject: vi.fn(),
  approveTools: vi.fn(), blockTools: vi.fn(), discoverServerTools: vi.fn(), listScanHistory: vi.fn(), scanAll: vi.fn(), getQueueProgress: vi.fn(), cancelAllScans: vi.fn(), startScan: vi.fn(), quarantineServer: vi.fn(),
} }))

const stubs = { RouterLink: { template: '<a><slot /></a>' } }
const reviewFor = (name: string) => ({
  success: true,
  data: {
    server: { name, transport: 'stdio', quarantined: true, definitions_captured: true },
    tools: [
      { name: 'read_a', description: 'read', tier: 'read', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: true, current_hash: `${name}-h1` },
      { name: 'read_b', description: 'read', tier: 'read', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: true, current_hash: `${name}-h2` },
    ],
  },
})
function deferred<T>() { let resolve!: (v: T) => void; const promise = new Promise<T>(r => { resolve = r }); return { promise, resolve } }

beforeEach(() => {
  vi.clearAllMocks()
  ;(api.getServerReview as any).mockImplementation(async (name: string) => reviewFor(name))
  ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
  ;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
})

const rejectButton = (w: any) => w.findAll('button').find((b: any) => b.text() === 'Reject server')
function assertLocked(w: any) {
  expect(w.get('[data-test="review-approve-server"]').attributes('disabled')).toBeDefined()
  expect(rejectButton(w)!.attributes('disabled')).toBeDefined()
  for (const cb of w.findAll('input[type="checkbox"]')) expect(cb.attributes('disabled')).toBeDefined()
}

describe('ReviewScreen late rejection for another server (UX-02 r8)', () => {
  for (const path of [['A', 'B'], ['A', 'B', 'A']]) {
    it(`a rejection resolved after ${path.join(' -> ')} keeps the new session's approval locked`, async () => {
      const reject = deferred<any>(); const approval = deferred<any>()
      ;(api.securityReject as any).mockReturnValue(reject.promise)
      ;(api.securityApprove as any).mockReturnValue(approval.promise)
      const wrapper = mount(ReviewScreen, { props: { serverName: 'A' }, global: { stubs } })
      await flushPromises()
      await rejectButton(wrapper)!.trigger('click')
      expect(api.securityReject).toHaveBeenCalledWith('A')

      for (const name of path.slice(1)) { await wrapper.setProps({ serverName: name }); await flushPromises() }
      const current = path[path.length - 1]
      await wrapper.get('[data-test="review-approve-server"]').trigger('click')
      await flushPromises()
      expect(api.securityApprove).toHaveBeenCalledTimes(1)
      expect((api.securityApprove as any).mock.calls[0][0]).toBe(current)
      assertLocked(wrapper)
      const loadsBefore = (api.getServerReview as any).mock.calls.length

      reject.resolve({ success: true })
      await flushPromises()
      assertLocked(wrapper)
      expect((api.getServerReview as any).mock.calls.length).toBe(loadsBefore) // A's completion does not reload
      expect(wrapper.find('.alert-error').exists()).toBe(false)
      await wrapper.get('[data-test="review-approve-server"]').trigger('click')
      await flushPromises()
      expect(api.securityApprove).toHaveBeenCalledTimes(1) // no extra approval while B's is pending

      approval.resolve({ success: true })
      await flushPromises()
      expect(wrapper.get('[data-test="review-approve-server"]').attributes('disabled')).toBeUndefined()
    })
  }

  it('a failed rejection for A does not surface its error on B', async () => {
    const reject = deferred<any>()
    ;(api.securityReject as any).mockReturnValue(reject.promise)
    const wrapper = mount(ReviewScreen, { props: { serverName: 'A' }, global: { stubs } })
    await flushPromises()
    await rejectButton(wrapper)!.trigger('click')
    await wrapper.setProps({ serverName: 'B' }); await flushPromises()
    reject.resolve({ success: false, error: 'Reject failed for A' })
    await flushPromises()
    expect(wrapper.text()).not.toContain('Reject failed for A')
  })

  it('a block for A resolved after switching to B does not reload B', async () => {
    const block = deferred<any>()
    ;(api.blockTools as any).mockReturnValue(block.promise)
    ;(api.getServerReview as any).mockImplementation(async (name: string) => { const r = reviewFor(name); r.data.server.quarantined = false; return r })
    const wrapper = mount(ReviewScreen, { props: { serverName: 'A' }, global: { stubs } })
    await flushPromises()
    const blockBtn = wrapper.findAll('button').find(b => b.text() === 'Reject' && b.classes().includes('btn-error'))
    expect(blockBtn).toBeTruthy() // a trusted server's pending tool offers per-tool approve/reject
    await blockBtn!.trigger('click')
    await wrapper.setProps({ serverName: 'B' }); await flushPromises()
    const loadsBefore = (api.getServerReview as any).mock.calls.length
    block.resolve({ success: true }); await flushPromises()
    expect((api.getServerReview as any).mock.calls.length).toBe(loadsBefore)
  })
})
