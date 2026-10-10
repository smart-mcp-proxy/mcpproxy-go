import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReviewScreen from '@/components/ReviewScreen.vue'
import api from '@/services/api'
import type { ReviewTool } from '@/types'

vi.mock('@/services/api', () => ({ default: {
  getServerReview: vi.fn(), listScanHistory: vi.fn(), securityApprove: vi.fn(), securityReject: vi.fn(),
  approveTools: vi.fn(), blockTools: vi.fn(), discoverServerTools: vi.fn(), startScan: vi.fn(), quarantineServer: vi.fn(),
} }))

const tool = (name: string, description = name): ReviewTool => ({
  name, description, tier: 'read', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: true,
})
const review = (name: string, tools: ReviewTool[]) => ({ success: true, data: {
  server: { name, transport: 'stdio', quarantined: true, definitions_captured: true }, tools,
} })
const deferred = () => { let resolve!: (v: unknown) => void; const promise = new Promise((r) => { resolve = r }); return { promise, resolve } }

describe('ReviewScreen ignores superseded review loads', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
    ;(api.securityApprove as any).mockResolvedValue({ success: true })
  })

  it('A response arriving after B keeps only B definitions, and approve targets B with B block list', async () => {
    const a = deferred(); const b = deferred()
    ;(api.getServerReview as any).mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise)
      // Third load: the post-approval refresh of B.
      .mockResolvedValueOnce(review('B', [tool('b_only'), tool('shared'), tool('post_approve_marker')]))
    const wrapper = mount(ReviewScreen, { props: { serverName: 'A' }, global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()
    await wrapper.setProps({ serverName: 'B' }); await flushPromises()
    b.resolve(review('B', [tool('b_only'), tool('shared')])); await flushPromises()
    a.resolve(review('A', [tool('a_only'), tool('shared')])); await flushPromises()

    expect(wrapper.find('[data-test="review-tool-a_only"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="review-tool-b_only"]').exists()).toBe(true)

    await wrapper.get('[data-test="review-allow-b_only"]').setValue(false)
    await wrapper.get('[data-test="review-approve-server"]').trigger('click'); await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledTimes(1)
    const [server, force, blocked] = (api.securityApprove as any).mock.calls[0]
    expect(server).toBe('B'); expect(force).toBe(false)
    expect([...blocked].sort()).toEqual(['b_only'])
    // The post-approval refresh ran and its state is rendered.
    expect(api.getServerReview).toHaveBeenCalledTimes(3)
    expect(wrapper.find('[data-test="review-tool-post_approve_marker"]').exists()).toBe(true)
  })

  it('an older refresh of the same server cannot restore stale definitions', async () => {
    const first = deferred(); const second = deferred()
    ;(api.getServerReview as any).mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    const wrapper = mount(ReviewScreen, { props: { serverName: 'A' }, global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()
    window.dispatchEvent(new CustomEvent('mcpproxy:review-changed')); await flushPromises()
    second.resolve(review('A', [tool('fresh', 'new text')])); await flushPromises()
    first.resolve(review('A', [tool('stale', 'old text')])); await flushPromises()
    expect(wrapper.find('[data-test="review-tool-fresh"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="review-tool-stale"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('old text')
  })
})
