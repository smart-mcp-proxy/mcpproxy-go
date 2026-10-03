import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import Review from '@/views/Review.vue'
import api from '@/services/api'
import type { ReviewTool } from '@/types'

vi.mock('@/services/api', () => ({ default: {
  getReviewQueue: vi.fn(), getServerReview: vi.fn(), listScanHistory: vi.fn(), getQueueProgress: vi.fn(),
  securityApprove: vi.fn(), securityReject: vi.fn(), approveTools: vi.fn(), blockTools: vi.fn(), discoverServerTools: vi.fn(),
  scanAll: vi.fn(), cancelAllScans: vi.fn(), startScan: vi.fn(), quarantineServer: vi.fn(),
} }))

const tool = (name: string, extra: Partial<ReviewTool> = {}): ReviewTool => ({
  name, description: name, tier: 'read', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: true, ...extra,
})

async function mountRoute() {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/review', component: Review }, { path: '/review/:server', component: Review }] })
  await router.push('/review/fixture'); await router.isReady()
  const wrapper = mount(Review, { global: { plugins: [router], stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
  await flushPromises()
  return wrapper
}

describe('Review route keeps the explicit selection across review.changed (D43.4)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(api.getReviewQueue as any).mockResolvedValue({ success: true, data: { count: 1, servers: [{ server: 'fixture', kind: 'tool_review', quarantined: true, pending: 2, changed: 0 }] } })
    ;(api.getServerReview as any).mockResolvedValue({ success: true, data: {
      server: { name: 'fixture', transport: 'stdio', quarantined: true, definitions_captured: true },
      tools: [tool('read_0'), tool('write_0', { tier: 'write', default_allowed: false })],
    } })
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
    ;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
  })

  it('keeps the checked and unchecked tools after a review.changed reload', async () => {
    const wrapper = await mountRoute()
    const box = (name: string) => wrapper.get(`[data-test="review-allow-${name}"]`)
    await box('write_0').setValue(true)
    await box('read_0').setValue(false)
    window.dispatchEvent(new CustomEvent('mcpproxy:review-changed')); await flushPromises()
    expect(api.getReviewQueue).toHaveBeenCalledTimes(2)
    expect((box('write_0').element as HTMLInputElement).checked).toBe(true)
    expect((box('read_0').element as HTMLInputElement).checked).toBe(false)
  })

  it('does not replace the review screen with the spinner while the queue reloads', async () => {
    const wrapper = await mountRoute()
    const screen = wrapper.get('[data-test="review-screen"]').element
    let release: (v: unknown) => void = () => {}
    ;(api.getReviewQueue as any).mockReturnValueOnce(new Promise((resolve) => { release = resolve }))
    window.dispatchEvent(new CustomEvent('mcpproxy:review-changed')); await flushPromises()
    expect(wrapper.get('[data-test="review-screen"]').element).toBe(screen)
    release({ success: true, data: { count: 1, servers: [] } }); await flushPromises()
    expect(wrapper.get('[data-test="review-screen"]').element).toBe(screen)
  })

  it('keeps the review screen when a background queue reload fails', async () => {
    const wrapper = await mountRoute()
    const screen = wrapper.get('[data-test="review-screen"]').element
    ;(api.getReviewQueue as any).mockResolvedValueOnce({ success: false, error: 'boom' })
    window.dispatchEvent(new CustomEvent('mcpproxy:review-changed')); await flushPromises()
    expect(wrapper.get('[data-test="review-screen"]').element).toBe(screen)
  })
})
