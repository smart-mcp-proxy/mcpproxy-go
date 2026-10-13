import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReviewScreen from '@/components/ReviewScreen.vue'
import api from '@/services/api'
import type { ReviewTool } from '@/types'

vi.mock('@/services/api', () => ({ default: {
  getServerReview: vi.fn(), securityApprove: vi.fn(), securityReject: vi.fn(),
  approveTools: vi.fn(), blockTools: vi.fn(), discoverServerTools: vi.fn(), listScanHistory: vi.fn(), scanAll: vi.fn(), getQueueProgress: vi.fn(), cancelAllScans: vi.fn(), startScan: vi.fn(), quarantineServer: vi.fn(),
} }))

const tool = (name: string, extra: Partial<ReviewTool> = {}): ReviewTool => ({
  name, description: name, tier: 'read', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: true, ...extra,
})
const review = (name: string, tools: string[], server: Record<string, unknown> = {}, toolExtra: Partial<ReviewTool> = {}) => ({ success: true, data: {
  server: { name, transport: 'stdio', quarantined: true, definitions_captured: true, ...server },
  tools: tools.map(t => tool(t, toolExtra)),
} })
function deferred<T = any>() { let resolve!: (v: T) => void; const promise = new Promise<T>(r => { resolve = r }); return { promise, resolve } }
const stubs = { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } }
const mockReviews = (byName: Record<string, any>) => (api.getServerReview as any).mockImplementation((n: string) => Promise.resolve(byName[n]))
const mountOn = async (serverName: string) => { const w = mount(ReviewScreen, { props: { serverName }, ...stubs }); await flushPromises(); return w }
const goTo = async (w: any, serverName: string) => { await w.setProps({ serverName }); await flushPromises() }
const approveBtn = (w: any) => w.get('[data-test="review-approve-server"]')
const loadsOf = (name: string) => (api.getServerReview as any).mock.calls.filter((c: unknown[]) => c[0] === name).length

describe('ReviewScreen drops late responses for another server or visit (#1568)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
    ;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
  })

  it('a late quarantine answer for A does not reload or touch B', async () => {
    mockReviews({ a: review('a', ['t'], { quarantined: false }, { approval_status: 'approved' }), b: review('b', ['t']) })
    const held = deferred(); ;(api.quarantineServer as any).mockReturnValue(held.promise)
    const w = await mountOn('a')
    await w.get('[data-test="review-requarantine-confirm"]').trigger('click')
    await goTo(w, 'b')
    const before = loadsOf('b')
    held.resolve({ success: false, error: 'boom for a' }); await flushPromises()
    expect(w.text()).not.toContain('boom for a')
    expect(w.find('[data-test="review-allow-t"]').exists()).toBe(true)
    held.resolve({ success: true }); await flushPromises()
    expect(loadsOf('b')).toBe(before)
  })

  it('a late fetch-definitions failure for A does not replace B with an error panel', async () => {
    mockReviews({ a: review('a', ['t'], { definitions_captured: false }), b: review('b', ['t']) })
    const held = deferred(); ;(api.discoverServerTools as any).mockReturnValue(held.promise)
    const w = await mountOn('a')
    await w.get('[data-test="review-no-definitions"] button').trigger('click')
    await goTo(w, 'b')
    held.resolve({ success: false, error: 'discover failed for a' }); await flushPromises()
    expect(w.text()).not.toContain('discover failed for a')
    expect(w.find('[data-test="review-allow-t"]').exists()).toBe(true)
  })

  it('a late reject answer for A does not unlock B while B approval is in flight and does not reload B', async () => {
    mockReviews({ a: review('a', ['t']), b: review('b', ['t']) })
    const rejectA = deferred(); const approveB = deferred()
    ;(api.securityReject as any).mockReturnValue(rejectA.promise)
    ;(api.securityApprove as any).mockReturnValue(approveB.promise)
    const w = await mountOn('a')
    await w.get('button.btn-error').trigger('click')
    await goTo(w, 'b')
    await approveBtn(w).trigger('click'); await flushPromises()
    expect((w.get('[data-test="review-allow-t"]').element as HTMLInputElement).disabled).toBe(true)
    const before = loadsOf('b')
    rejectA.resolve({ success: true }); await flushPromises()
    expect((w.get('[data-test="review-allow-t"]').element as HTMLInputElement).disabled).toBe(true)
    expect(approveBtn(w).attributes('disabled')).toBeDefined()
    expect(loadsOf('b')).toBe(before)
  })

  it('late approve-tool and block-tool answers for A do not reload B', async () => {
    const pending = (n: string) => review(n, ['t'], { quarantined: false })
    mockReviews({ a: pending('a'), b: pending('b') })
    const okA = deferred(); const blockA = deferred()
    ;(api.approveTools as any).mockReturnValue(okA.promise); ;(api.blockTools as any).mockReturnValue(blockA.promise)
    const w = await mountOn('a')
    const buttons = w.findAll('article button')
    await buttons[0].trigger('click'); await buttons[1].trigger('click')
    await goTo(w, 'b')
    const before = loadsOf('b')
    okA.resolve({ success: true }); blockA.resolve({ success: true }); await flushPromises()
    expect(loadsOf('b')).toBe(before)
  })

  it('A -> B -> A: an obsolete approve success keeps the new visit choices', async () => {
    mockReviews({ a: review('a', ['t', 'u']), b: review('b', ['x']) })
    const held = deferred(); ;(api.securityApprove as any).mockReturnValue(held.promise)
    const w = await mountOn('a')
    await approveBtn(w).trigger('click'); await flushPromises()
    await goTo(w, 'b'); await goTo(w, 'a')
    await w.get('[data-test="review-allow-u"]').setValue(false)
    held.resolve({ success: true }); await flushPromises()
    expect((w.get('[data-test="review-allow-u"]').element as HTMLInputElement).checked).toBe(false)
    expect(w.emitted('approved')).toBeUndefined()
  })

  it('A -> B -> A: an obsolete dangerous answer does not reopen the force dialog', async () => {
    mockReviews({ a: review('a', ['t']), b: review('b', ['x']) })
    const held = deferred(); ;(api.securityApprove as any).mockReturnValue(held.promise)
    const w = await mountOn('a')
    const dlg = w.findAll('dialog')[1].element as HTMLDialogElement; dlg.showModal = vi.fn()
    await approveBtn(w).trigger('click'); await flushPromises()
    await goTo(w, 'b'); await goTo(w, 'a')
    held.resolve({ success: false, error: 'dangerous baseline finding' }); await flushPromises()
    expect(dlg.showModal).not.toHaveBeenCalled()
  })

  it('selection controls are locked while an approval is in flight', async () => {
    mockReviews({ a: review('a', ['t', 'u']) })
    const held = deferred(); ;(api.securityApprove as any).mockReturnValue(held.promise)
    const w = await mountOn('a')
    await approveBtn(w).trigger('click'); await flushPromises()
    expect((w.get('[data-test="review-allow-t"]').element as HTMLInputElement).disabled).toBe(true)
    held.resolve({ success: true }); await flushPromises()
    expect((w.get('[data-test="review-allow-t"]').element as HTMLInputElement).disabled).toBe(false)
  })

  it('blind approval confirmed after a refresh captured tools does not approve them unseen', async () => {
    const blind = review('a', [], { definitions_captured: false })
    const fresh = review('a', ['new_tool'])
    ;(api.getServerReview as any).mockResolvedValueOnce(blind).mockResolvedValue(fresh)
    ;(api.securityApprove as any).mockResolvedValue({ success: true })
    const w = await mountOn('a')
    const dlg = w.findAll('dialog')[0].element as HTMLDialogElement; dlg.showModal = vi.fn()
    await approveBtn(w).trigger('click')
    window.dispatchEvent(new CustomEvent('mcpproxy:review-changed')); await flushPromises()
    await w.findAll('dialog')[0].get('button.btn-warning').trigger('click'); await flushPromises()
    expect(api.securityApprove).not.toHaveBeenCalled()
    expect(w.get('[data-test="review-stale-force-notice"]').text()).toContain('changed')
  })

  it('blind approval confirmed after a FAILED refresh is not sent and needs a fresh review', async () => {
    const blind = review('a', [], { definitions_captured: false })
    const refresh = deferred()
    ;(api.getServerReview as any).mockResolvedValueOnce(blind).mockReturnValueOnce(refresh.promise)
    ;(api.securityApprove as any).mockResolvedValue({ success: true })
    const w = await mountOn('a')
    const dlg = w.findAll('dialog')[0].element as HTMLDialogElement; dlg.showModal = vi.fn()
    await approveBtn(w).trigger('click')
    window.dispatchEvent(new CustomEvent('mcpproxy:review-changed')); await flushPromises()
    refresh.resolve({ success: false, error: 'boom' }); await flushPromises()
    await w.findAll('dialog')[0].get('button.btn-warning').trigger('click'); await flushPromises()
    expect(api.securityApprove).not.toHaveBeenCalled()
  })

  it('blind approval on an unchanged empty review still goes through', async () => {
    mockReviews({ a: review('a', [], { definitions_captured: false }) })
    ;(api.securityApprove as any).mockResolvedValue({ success: true })
    const w = await mountOn('a')
    const dlg = w.findAll('dialog')[0].element as HTMLDialogElement; dlg.showModal = vi.fn()
    await approveBtn(w).trigger('click')
    await w.findAll('dialog')[0].get('button.btn-warning').trigger('click'); await flushPromises()
    // An empty review binds to the empty snapshot (Spec 117 PR-2).
    expect(api.securityApprove).toHaveBeenCalledWith('a', false, [], undefined, {})
  })

  it('a forced retry after a refresh is not sent with the old block list', async () => {
    mockReviews({ a: review('a', ['t', 'u']) })
    ;(api.securityApprove as any).mockResolvedValueOnce({ success: false, error: 'dangerous baseline finding' })
    const w = await mountOn('a')
    const force = w.findAll('dialog')[1].element as HTMLDialogElement; force.showModal = vi.fn()
    await w.get('[data-test="review-allow-u"]').setValue(false)
    await approveBtn(w).trigger('click'); await flushPromises()
    window.dispatchEvent(new CustomEvent('mcpproxy:review-changed')); await flushPromises()
    await w.findAll('dialog')[1].get('button.btn-error').trigger('click'); await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledTimes(1)
  })
})
