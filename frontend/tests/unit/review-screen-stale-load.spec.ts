import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReviewScreen from '@/components/ReviewScreen.vue'
import api from '@/services/api'
import type { ReviewTool } from '@/types'

vi.mock('@/services/api', () => ({ default: {
  getServerReview: vi.fn(), securityApprove: vi.fn(), securityReject: vi.fn(),
  approveTools: vi.fn(), blockTools: vi.fn(), discoverServerTools: vi.fn(), listScanHistory: vi.fn(), scanAll: vi.fn(), getQueueProgress: vi.fn(), cancelAllScans: vi.fn(), startScan: vi.fn(), quarantineServer: vi.fn(),
} }))

const tool = (name: string): ReviewTool => ({
  name, description: name, tier: 'read', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: true,
})
const review = (name: string, tools: string[]) => ({ success: true, data: {
  server: { name, transport: 'stdio', quarantined: true, definitions_captured: true },
  tools: tools.map(tool),
} })

describe('ReviewScreen discards stale review loads', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
    ;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
  })

  it('a late response for server A never overwrites server B (tools, selection, block list)', async () => {
    let releaseA: (v: unknown) => void = () => {}
    ;(api.getServerReview as any).mockImplementation((name: string) =>
      name === 'a' ? new Promise((resolve) => { releaseA = resolve }) : Promise.resolve(review('b', ['shared', 'only_b'])))
    ;(api.securityApprove as any).mockResolvedValue({ success: true })

    const wrapper = mount(ReviewScreen, { props: { serverName: 'a' }, global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()
    await wrapper.setProps({ serverName: 'b' })
    await flushPromises()

    const box = (name: string) => wrapper.get(`[data-test="review-allow-${name}"]`)
    await box('shared').setValue(false)

    releaseA(review('a', ['shared', 'only_a']))
    await flushPromises()

    expect(wrapper.find('[data-test="review-allow-only_a"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="review-allow-only_b"]').exists()).toBe(true)
    expect((box('shared').element as HTMLInputElement).checked).toBe(false)

    ;(api.getServerReview as any).mockResolvedValue(review('b', ['shared', 'only_b']))
    await wrapper.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledWith('b', false, ['shared'])
  })

  it('overlapping reloads of one server resolved out of order keep the newest', async () => {
    let resolveFirst: (v: unknown) => void = () => {}
    let resolveSecond: (v: unknown) => void = () => {}
    ;(api.getServerReview as any)
      .mockReturnValueOnce(new Promise((r) => { resolveFirst = r }))
      .mockReturnValueOnce(new Promise((r) => { resolveSecond = r }))
    const wrapper = mount(ReviewScreen, { props: { serverName: 'a' }, global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    window.dispatchEvent(new CustomEvent('mcpproxy:review-changed'))
    resolveSecond(review('a', ['new_tool'])); await flushPromises()
    resolveFirst(review('a', ['old_tool'])); await flushPromises()
    expect(wrapper.find('[data-test="review-allow-new_tool"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="review-allow-old_tool"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
