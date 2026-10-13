import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReviewScreen from '@/components/ReviewScreen.vue'
import api from '@/services/api'
import type { ReviewTool } from '@/types'
import { expectedHashes } from '@/utils/reviewPresentation'

vi.mock('@/services/api', () => ({ default: {
  getServerReview: vi.fn(), securityApprove: vi.fn(), securityReject: vi.fn(),
  approveTools: vi.fn(), blockTools: vi.fn(), discoverServerTools: vi.fn(), listScanHistory: vi.fn(), scanAll: vi.fn(), getQueueProgress: vi.fn(), cancelAllScans: vi.fn(), startScan: vi.fn(), quarantineServer: vi.fn(),
} }))

// Spec 117 PR-2 (#1566, T213): the Web review screen binds approvals to the review it shows.

const tool = (name: string, hash: string | undefined, extra: Partial<ReviewTool> = {}): ReviewTool => ({
  name, description: name, tier: 'read', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: true,
  ...(hash ? { current_hash: hash } : {}), ...extra,
})
const review = (tools: ReviewTool[], server: Record<string, unknown> = {}) => ({ success: true, data: {
  server: { name: 'notes2', transport: 'stdio', quarantined: true, definitions_captured: tools.length > 0, ...server }, tools,
} })
const stubs = { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } }
const mountOn = async () => { const w = mount(ReviewScreen, { props: { serverName: 'notes2' }, ...stubs }); await flushPromises(); return w }
const approveBtn = (w: any) => w.get('[data-test="review-approve-server"]')
const STALE = "tool review for server 'notes2' is out of date (definition changed since review: read_note); nothing was approved — fetch the review again"

describe('expectedHashes (mirrors the tray)', () => {
  it('binds every hashed tool, {} for an empty review, undefined only for a review without fingerprints', () => {
    expect(expectedHashes([tool('a', 'rv1-a'), tool('b', 'rv1-b')])).toEqual({ a: 'rv1-a', b: 'rv1-b' })
    expect(expectedHashes([])).toEqual({})
    expect(expectedHashes([tool('a', undefined)])).toBeUndefined()
    const all = [tool('a', 'rv1-a'), tool('b', undefined)]
    expect(expectedHashes([all[1]], all)).toEqual({})
  })
})

describe('ReviewScreen review binding (Spec 117 PR-2)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
    ;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
  })

  it('sends the click-time hashes of every reviewed tool, blocked ones included', async () => {
    ;(api.getServerReview as any).mockResolvedValue(review([tool('read_note', 'rv1-r'), tool('wipe', 'rv1-w', { default_allowed: false, tier: 'destructive' })]))
    ;(api.securityApprove as any).mockResolvedValue({ success: true })
    const w = await mountOn()
    await approveBtn(w).trigger('click'); await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledWith('notes2', false, ['wipe'], undefined, { read_note: 'rv1-r', wipe: 'rv1-w' })
  })

  it('a not-captured (blind) review binds to the empty snapshot', async () => {
    ;(api.getServerReview as any).mockResolvedValue(review([], { definitions_captured: false }))
    ;(api.securityApprove as any).mockResolvedValue({ success: true })
    const w = await mountOn()
    const dlg = w.findAll('dialog')[0].element as HTMLDialogElement; dlg.showModal = vi.fn()
    await approveBtn(w).trigger('click')
    await w.findAll('dialog')[0].get('button.btn-warning').trigger('click'); await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledWith('notes2', false, [], undefined, {})
  })

  it('a review without fingerprints (older core) sends an unbound request', async () => {
    ;(api.getServerReview as any).mockResolvedValue(review([tool('read_note', undefined)]))
    ;(api.securityApprove as any).mockResolvedValue({ success: true })
    const w = await mountOn()
    await approveBtn(w).trigger('click'); await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledWith('notes2', false, [])
  })

  it('the force retry re-sends the binding of the attempt that answered "dangerous"', async () => {
    ;(api.getServerReview as any).mockResolvedValue(review([tool('read_note', 'rv1-r')]))
    ;(api.securityApprove as any).mockResolvedValueOnce({ success: false, error: 'server has 1 dangerous (hard-tier) finding(s)' })
      .mockResolvedValueOnce({ success: true })
    const w = await mountOn()
    const force = w.findAll('dialog')[1].element as HTMLDialogElement; force.showModal = vi.fn()
    await approveBtn(w).trigger('click'); await flushPromises()
    expect(force.showModal).toHaveBeenCalled()
    await w.findAll('dialog')[1].get('button.btn-error').trigger('click'); await flushPromises()
    expect(api.securityApprove).toHaveBeenNthCalledWith(2, 'notes2', true, [], undefined, { read_note: 'rv1-r' })
  })

  it('a 409 out of date reloads the review, shows the notice and never opens the force dialog', async () => {
    ;(api.getServerReview as any)
      .mockResolvedValueOnce(review([tool('read_note', 'rv1-r1'), tool('write_note', 'rv1-w', { default_allowed: false, tier: 'write' })]))
      .mockResolvedValue(review([tool('read_note', 'rv1-r2', { tier: 'destructive', default_allowed: false }), tool('write_note', 'rv1-w', { default_allowed: false, tier: 'write' })]))
    ;(api.securityApprove as any).mockResolvedValue({ success: false, error: STALE })
    const w = await mountOn()
    const force = w.findAll('dialog')[1].element as HTMLDialogElement; force.showModal = vi.fn()
    // An explicit choice for a tool that is still present survives the reload.
    await w.get('[data-test="review-allow-write_note"]').setValue(true)
    await approveBtn(w).trigger('click'); await flushPromises()
    expect(force.showModal).not.toHaveBeenCalled()
    expect((api.getServerReview as any).mock.calls.length).toBe(2)
    expect(w.get('[data-test="review-stale-force-notice"]').text()).toContain('out of date')
    expect(w.find('.alert-error').exists()).toBe(false)
    expect((w.get('[data-test="review-allow-write_note"]').element as HTMLInputElement).checked).toBe(true)
    // The next approval binds to the reloaded review.
    ;(api.securityApprove as any).mockResolvedValue({ success: true })
    await approveBtn(w).trigger('click'); await flushPromises()
    expect(api.securityApprove).toHaveBeenLastCalledWith('notes2', false, ['read_note'], undefined, { read_note: 'rv1-r2', write_note: 'rv1-w' })
  })

  it('a per-tool approve sends that tool\'s fingerprint and reloads with the notice on 409', async () => {
    const trusted = review([tool('t', 'rv1-t', { approval_status: 'changed' }), tool('u', undefined, { approval_status: 'pending' })], { quarantined: false })
    ;(api.getServerReview as any).mockResolvedValue(trusted)
    ;(api.approveTools as any).mockResolvedValue({ success: true })
    const w = await mountOn()
    const approveButtons = w.findAll('button').filter((b: any) => b.text() === 'Approve')
    await approveButtons[0].trigger('click'); await flushPromises()
    expect(api.approveTools).toHaveBeenCalledWith('notes2', ['t'], undefined, { t: 'rv1-t' })
    await approveButtons[1].trigger('click'); await flushPromises()
    expect(api.approveTools).toHaveBeenLastCalledWith('notes2', ['u'], undefined, {})

    ;(api.approveTools as any).mockResolvedValue({ success: false, error: STALE })
    const loads = (api.getServerReview as any).mock.calls.length
    await w.findAll('button').filter((b: any) => b.text() === 'Approve')[0].trigger('click'); await flushPromises()
    expect((api.getServerReview as any).mock.calls.length).toBe(loads + 1)
    expect(w.get('[data-test="review-stale-notice"]').text()).toContain('out of date')
  })
  // Spec 116 x Spec 117 PR-2: the two bindings coexist and stay distinct.
  it('sends BOTH expected_incarnation and the hashes on a server approval and a per-tool approve', async () => {
    ;(api.getServerReview as any).mockResolvedValue(review([tool('read_note', 'rv1-r')], { incarnation: 7 }))
    ;(api.securityApprove as any).mockResolvedValue({ success: true })
    let w = await mountOn()
    await approveBtn(w).trigger('click'); await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledWith('notes2', false, [], 7, { read_note: 'rv1-r' })

    ;(api.getServerReview as any).mockResolvedValue(review([tool('t', 'rv1-t', { approval_status: 'changed' })], { quarantined: false, incarnation: 7 }))
    ;(api.approveTools as any).mockResolvedValue({ success: true })
    w = await mountOn()
    await w.findAll('button').filter((b: any) => b.text() === 'Approve')[0].trigger('click'); await flushPromises()
    expect(api.approveTools).toHaveBeenLastCalledWith('notes2', ['t'], 7, { t: 'rv1-t' })
  })

  it('shows a server_replaced refusal beside the tools, not as the stale-review notice', async () => {
    ;(api.getServerReview as any).mockResolvedValue(review([tool('t', 'rv1-t', { approval_status: 'changed' })], { quarantined: false, incarnation: 7 }))
    ;(api.approveTools as any).mockResolvedValue({ success: false, error: "server_replaced: server 'notes2' was removed or replaced since it was reviewed" })
    const w = await mountOn()
    await w.findAll('button').filter((b: any) => b.text() === 'Approve')[0].trigger('click'); await flushPromises()
    expect(w.get('[data-test="review-tool-notice"]').text()).toContain('server_replaced')
    expect(w.find('[data-test="review-stale-notice"]').exists()).toBe(false)
  })
})
