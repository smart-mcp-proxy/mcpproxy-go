import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReviewScreen from '@/components/ReviewScreen.vue'
import api from '@/services/api'

// UX-02 cross-review round 6.

vi.mock('@/services/api', () => ({ default: {
  getServerReview: vi.fn(), securityApprove: vi.fn(), securityReject: vi.fn(),
  approveTools: vi.fn(), blockTools: vi.fn(), discoverServerTools: vi.fn(), listScanHistory: vi.fn(), scanAll: vi.fn(), getQueueProgress: vi.fn(), cancelAllScans: vi.fn(), startScan: vi.fn(), quarantineServer: vi.fn(),
} }))

const stubs = { RouterLink: { template: '<a><slot /></a>' } }
const emptyReview = (definitionsCaptured: boolean) => ({
  success: true,
  data: { server: { name: 'fixture', transport: 'stdio', quarantined: true, definitions_captured: definitionsCaptured }, tools: [] as any[] },
})
const hashedReview = () => ({
  success: true,
  data: {
    server: { name: 'fixture', transport: 'stdio', quarantined: true, definitions_captured: true },
    tools: [
      { name: 'read_file', description: 'read', tier: 'read', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: true, current_hash: 'h-read' },
      { name: 'drop_all', description: 'drop', tier: 'destructive', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: false, current_hash: 'h-drop' },
    ],
  },
})
function deferred<T>() { let resolve!: (v: T) => void; const promise = new Promise<T>(r => { resolve = r }); return { promise, resolve } }

beforeEach(() => {
  vi.clearAllMocks()
  ;(api.securityApprove as any).mockResolvedValue({ success: true })
  ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
  ;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
})

describe('ReviewScreen empty-inventory approval is bound to the empty snapshot (UX-02 r6)', () => {
  it('a blind approval and its force retry bind to {} so tools captured meanwhile are never approved unseen', async () => {
    ;(api.getServerReview as any).mockResolvedValue(emptyReview(false))
    const wrapper = mount(ReviewScreen, { props: { serverName: 'fixture' }, global: { stubs } })
    await flushPromises()
    for (const d of wrapper.findAll('dialog')) (d.element as any).showModal = vi.fn()

    ;(api.securityApprove as any).mockResolvedValueOnce({ success: false, error: 'Approval blocked: dangerous findings detected; use force' })
    await wrapper.get('[data-test="review-approve-server"]').trigger('click')
    const blind = wrapper.findAll('button').find(b => b.text() === 'Approve without seeing tools' && b.classes().includes('btn-warning'))
    expect(blind).toBeTruthy()
    await blind!.trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenLastCalledWith('fixture', false, [], {})

    // Definitions arrive in the background while the force dialog is open.
    ;(api.getServerReview as any).mockResolvedValue(hashedReview())
    window.dispatchEvent(new Event('mcpproxy:review-changed'))
    await flushPromises()
    expect(wrapper.find('[data-test="review-tool-drop_all"]').exists()).toBe(true)

    const force = wrapper.findAll('button').find(b => b.text() === 'Force approve server')
    await force!.trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledTimes(2)
    // Still the empty-snapshot decision: the core refuses it as out of date
    // because read_file and drop_all are not in the reviewed snapshot.
    expect(api.securityApprove).toHaveBeenLastCalledWith('fixture', true, [], {})
  })

  it('a captured but empty inventory is bound to {} as well', async () => {
    ;(api.getServerReview as any).mockResolvedValue(emptyReview(true))
    const wrapper = mount(ReviewScreen, { props: { serverName: 'fixture' }, global: { stubs } })
    await flushPromises()
    await wrapper.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledWith('fixture', false, [], {})
  })
})

describe('ReviewScreen selection is locked while an approval is in flight (UX-02 r6)', () => {
  it('disables the tool checkboxes until the approval answers', async () => {
    ;(api.getServerReview as any).mockResolvedValue(hashedReview())
    const pending = deferred<any>()
    ;(api.securityApprove as any).mockImplementationOnce(() => pending.promise)
    const wrapper = mount(ReviewScreen, { props: { serverName: 'fixture' }, global: { stubs } })
    await flushPromises()
    const box = () => wrapper.get('[data-test="review-allow-read_file"]').element as HTMLInputElement
    expect(box().disabled).toBe(false)

    await wrapper.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(box().disabled).toBe(true)
    expect((wrapper.get('[data-test="review-allow-drop_all"]').element as HTMLInputElement).disabled).toBe(true)

    // A failed attempt hands the selection back for a new decision.
    pending.resolve({ success: false, error: 'Approval failed: boom' })
    await flushPromises()
    expect(wrapper.find('[data-test="review-screen"]').exists()).toBe(true)
  })

  it('re-enables the checkboxes when the approval fails', async () => {
    ;(api.getServerReview as any).mockResolvedValue(hashedReview())
    const pending = deferred<any>()
    ;(api.securityApprove as any).mockImplementationOnce(() => pending.promise)
    const wrapper = mount(ReviewScreen, { props: { serverName: 'fixture' }, global: { stubs } })
    await flushPromises()
    await wrapper.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    pending.resolve({ success: false, error: 'tool review is out of date (not in the review: x)' })
    await flushPromises()
    expect((wrapper.get('[data-test="review-allow-read_file"]').element as HTMLInputElement).disabled).toBe(false)
  })
})
