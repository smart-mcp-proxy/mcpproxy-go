import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReviewScreen from '@/components/ReviewScreen.vue'
import api from '@/services/api'

vi.mock('@/services/api', () => ({ default: {
  getServerReview: vi.fn(), securityApprove: vi.fn(), securityReject: vi.fn(),
  approveTools: vi.fn(), blockTools: vi.fn(), discoverServerTools: vi.fn(), listScanHistory: vi.fn(), scanAll: vi.fn(), getQueueProgress: vi.fn(), cancelAllScans: vi.fn(), startScan: vi.fn(), quarantineServer: vi.fn(),
} }))

const tool = (name: string) => ({ name, description: name, tier: 'read', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: true })
const review = (server: string, tools: string[]) => ({ success: true, data: { server: { name: server, transport: 'stdio', quarantined: true, definitions_captured: true }, tools: tools.map(tool) } })
function deferred<T>() { let resolve!: (v: T) => void; const promise = new Promise<T>(r => { resolve = r }); return { promise, resolve } }

describe('ReviewScreen ignores stale detail loads', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
    ;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
    ;(api.securityApprove as any).mockResolvedValue({ success: true })
  })

  it('a late response for server A never replaces server B and approval blocks B tools', async () => {
    const a = deferred<any>(); const b = deferred<any>()
    ;(api.getServerReview as any).mockImplementation((name: string) => name === 'A' ? a.promise : b.promise)
    const wrapper = mount(ReviewScreen, { props: { serverName: 'A' }, global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await wrapper.setProps({ serverName: 'B' })
    b.resolve(review('B', ['b_one', 'b_two'])); await flushPromises()
    a.resolve(review('A', ['a_one'])); await flushPromises()
    expect(wrapper.find('[data-test="review-allow-b_one"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="review-allow-a_one"]').exists()).toBe(false)
    await wrapper.get('[data-test="review-allow-b_one"]').setValue(false)
    await wrapper.get('[data-test="review-approve-server"]').trigger('click'); await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledWith('B', false, ['b_one'])
  })

  it('overlapping reloads of one server resolved out of order keep the newest', async () => {
    const first = deferred<any>(); const second = deferred<any>()
    ;(api.getServerReview as any).mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    const wrapper = mount(ReviewScreen, { props: { serverName: 'A' }, global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    window.dispatchEvent(new CustomEvent('mcpproxy:review-changed'))
    second.resolve(review('A', ['new_tool'])); await flushPromises()
    first.resolve(review('A', ['old_tool'])); await flushPromises()
    expect(wrapper.find('[data-test="review-allow-new_tool"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="review-allow-old_tool"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
