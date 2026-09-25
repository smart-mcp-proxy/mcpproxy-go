import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

// Spec 109-k / audit finding F-Token: the Usage tab's "Tokens saved per
// request" tile must show an "estimate" badge while the backend reports
// tokens_saved_estimated=true (no real retrieve_tools call observed yet), and
// must not show it once a real average is available.

const usageSpy = vi.hoisted(() => vi.fn())

vi.mock('@/services/api', () => {
  const ok = (data: unknown = null) => vi.fn().mockResolvedValue({ success: true, data })
  const base: Record<string, unknown> = {
    getActivityUsage: usageSpy,
    hasAPIKey: vi.fn(() => true),
    onAuthError: vi.fn(() => () => {}),
  }
  return {
    default: new Proxy(base, {
      get(target: Record<string, unknown>, prop: string) {
        if (prop in target) return target[prop]
        target[prop] = ok()
        return target[prop]
      },
    }),
  }
})

import Usage from '@/views/Usage.vue'

function aggregate(estimated: boolean) {
  return {
    success: true,
    data: {
      window: '24h',
      generated_at: '2026-09-26T12:00:00Z',
      freshness_ms: 100,
      token_source: 'bytes',
      tokens_saved: 12000,
      tokens_saved_percentage: 92.4,
      tokens_saved_estimated: estimated,
      tools: [],
      timeline: [],
    },
  }
}

function mountUsage() {
  return mount(Usage, {
    global: {
      plugins: [createPinia()],
      stubs: { RouterLink: { template: '<a><slot /></a>' }, Line: true, Bar: true, Doughnut: true, Pie: true },
    },
  })
}

describe('Usage tokens-saved estimate badge', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    usageSpy.mockReset()
  })

  it('shows the estimate badge before any real retrieve_tools call', async () => {
    usageSpy.mockResolvedValueOnce(aggregate(true))
    const wrapper = mountUsage()
    await flushPromises()

    expect(wrapper.find('[data-test="usage-tokens-saved-estimate-badge"]').exists()).toBe(true)
  })

  it('hides the estimate badge once a real average is reported', async () => {
    usageSpy.mockResolvedValueOnce(aggregate(false))
    const wrapper = mountUsage()
    await flushPromises()

    expect(wrapper.find('[data-test="usage-tokens-saved-estimate-badge"]').exists()).toBe(false)
  })
})
