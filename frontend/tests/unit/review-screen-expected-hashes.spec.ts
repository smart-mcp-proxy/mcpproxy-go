import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReviewScreen from '@/components/ReviewScreen.vue'
import api from '@/services/api'

// UX-02 cross-review: an approval from the review screen is bound to the
// definitions the operator saw (expected_hashes); a stale review (409) reloads
// the screen and keeps the message visible.

vi.mock('@/services/api', () => ({ default: {
  getServerReview: vi.fn(), securityApprove: vi.fn(), securityReject: vi.fn(),
  approveTools: vi.fn(), blockTools: vi.fn(), discoverServerTools: vi.fn(), listScanHistory: vi.fn(), scanAll: vi.fn(), getQueueProgress: vi.fn(), cancelAllScans: vi.fn(), startScan: vi.fn(), quarantineServer: vi.fn(),
} }))

const review = (withHashes: boolean) => ({
  success: true,
  data: {
    server: { name: 'fixture', transport: 'stdio', quarantined: true, definitions_captured: true },
    tools: [
      { name: 'read_file', description: 'read', tier: 'read', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: true, ...(withHashes ? { current_hash: 'h-read' } : {}) },
      { name: 'remove_file', description: 'remove', tier: 'destructive', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: false, ...(withHashes ? { current_hash: 'h-remove' } : {}) },
    ],
  },
})

async function mountScreen(withHashes = true) {
  ;(api.getServerReview as any).mockResolvedValue(review(withHashes))
  const wrapper = mount(ReviewScreen, { props: { serverName: 'fixture' }, global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
  await flushPromises()
  return wrapper
}

describe('ReviewScreen review-bound approval (UX-02)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(api.securityApprove as any).mockResolvedValue({ success: true })
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
    ;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
  })

  it('sends the reviewed definition hashes with the server approval', async () => {
    const wrapper = await mountScreen()
    await wrapper.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledWith('fixture', false, ['remove_file'], { read_file: 'h-read', remove_file: 'h-remove' })
  })

  it('stays unbound against a core that reports no hashes', async () => {
    const wrapper = await mountScreen(false)
    await wrapper.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledWith('fixture', false, ['remove_file'])
  })

  it('reloads the review and keeps the message when the review is out of date', async () => {
    const wrapper = await mountScreen()
    ;(api.securityApprove as any).mockResolvedValueOnce({ success: false, error: "tool review for server 'fixture' is out of date (not in the review: drop_all); nothing was approved — fetch the review again" })
    const loadsBefore = (api.getServerReview as any).mock.calls.length
    await wrapper.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect((api.getServerReview as any).mock.calls.length).toBe(loadsBefore + 1)
    expect(wrapper.get('[data-test="review-stale-notice"]').text()).toContain('out of date')
    // The reloaded review stays visible next to the notice.
    expect(wrapper.find('[data-test="review-approve-server"]').exists()).toBe(true)
  })

  it('force retry keeps the hashes of the decision that triggered it, not a reloaded review', async () => {
    const wrapper = await mountScreen()
    ;(api.securityApprove as any).mockResolvedValueOnce({ success: false, error: 'Approval blocked: dangerous findings detected; use force' })
    await wrapper.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    // A background reload while the force dialog is open: a new destructive
    // tool appeared and the selected read tool's definition changed.
    const changed = review(true)
    changed.data.tools[0].current_hash = 'h-read-mutated'
    changed.data.tools.push({ name: 'drop_all', description: 'drop', tier: 'destructive', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: false, current_hash: 'h-drop' } as any)
    ;(api.getServerReview as any).mockResolvedValue(changed)
    window.dispatchEvent(new Event('mcpproxy:review-changed'))
    await flushPromises()
    const force = wrapper.findAll('button').find(b => b.text() === 'Force approve server')
    expect(force).toBeTruthy()
    await force!.trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledTimes(2)
    // The retry is the same decision: same block list AND the originally
    // reviewed hashes, so the core rejects it as out of date (drop_all is not
    // in the review, read_file changed) instead of approving it unseen.
    expect(api.securityApprove).toHaveBeenLastCalledWith('fixture', true, ['remove_file'], { read_file: 'h-read', remove_file: 'h-remove' })
  })

  it('keeps the binding when one tool has no hash, so unseen definitions cannot be approved (round 3)', async () => {
    const mixed = review(true)
    delete (mixed.data.tools[1] as any).current_hash
    ;(api.getServerReview as any).mockResolvedValue(mixed)
    const wrapper = mount(ReviewScreen, { props: { serverName: 'fixture' }, global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()
    // Allow every tool, including the hashless one.
    await wrapper.get('[data-test="review-approve-all"]').trigger('click')
    await flushPromises()
    // Still bound: the hashless tool is omitted (the core refuses it as not
    // reviewed) instead of the whole approval going out unbound.
    expect(api.securityApprove).toHaveBeenCalledWith('fixture', false, [], { read_file: 'h-read' })
  })
})

describe('ReviewScreen load ordering (UX-02 round 3)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(api.securityApprove as any).mockResolvedValue({ success: true })
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
    ;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
  })

  const reviewFor = (server: string, hash: string) => ({
    success: true,
    data: {
      server: { name: server, transport: 'stdio', quarantined: true, definitions_captured: true },
      tools: [
        { name: 'read_file', description: 'read', tier: 'read', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: true, current_hash: hash },
        { name: 'remove_file', description: 'remove', tier: 'destructive', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: false, current_hash: hash + '-rm' },
      ],
    },
  })
  function deferred<T>() { let resolve!: (v: T) => void; const promise = new Promise<T>(r => { resolve = r }); return { promise, resolve } }

  it('a late response for the previous server cannot replace the current review', async () => {
    const a = deferred<any>(); const b = deferred<any>()
    ;(api.getServerReview as any).mockImplementation((server: string) => (server === 'srv-a' ? a.promise : b.promise))
    const wrapper = mount(ReviewScreen, { props: { serverName: 'srv-a' }, global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()
    await wrapper.setProps({ serverName: 'srv-b' })
    b.resolve(reviewFor('srv-b', 'hb'))
    await flushPromises()
    a.resolve(reviewFor('srv-a', 'ha'))
    await flushPromises()
    await wrapper.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledTimes(1)
    expect(api.securityApprove).toHaveBeenCalledWith('srv-b', false, ['remove_file'], { read_file: 'hb', remove_file: 'hb-rm' })
  })

  it('an older reload of the same server cannot replace a newer one', async () => {
    const first = deferred<any>(); const second = deferred<any>()
    let calls = 0
    ;(api.getServerReview as any).mockImplementation(() => (++calls === 1 ? first.promise : second.promise))
    const wrapper = mount(ReviewScreen, { props: { serverName: 'fixture' }, global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()
    window.dispatchEvent(new Event('mcpproxy:review-changed'))
    await flushPromises()
    second.resolve(reviewFor('fixture', 'new'))
    await flushPromises()
    first.resolve(reviewFor('fixture', 'old'))
    await flushPromises()
    await wrapper.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledWith('fixture', false, ['remove_file'], { read_file: 'new', remove_file: 'new-rm' })
  })
})
