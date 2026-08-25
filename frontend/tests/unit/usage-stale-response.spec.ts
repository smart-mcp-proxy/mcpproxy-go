import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

// The Usage panel has no request cancellation and several triggers that can
// overlap: a 30s auto-refresh, the window selector, the status/sort selects and
// the "widen the window" reset. If a slower earlier request is allowed to write
// state after a newer one has landed, the panel repaints with data for a window
// the user already moved off — now visible on the default landing page.

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

function aggregate(window: string) {
  return {
    success: true,
    data: {
      window,
      generated_at: '2026-08-25T12:00:00Z',
      freshness_ms: 100,
      token_source: 'bytes',
      tokens_saved: 1,
      tokens_saved_percentage: 1,
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

describe('Usage panel overlapping reloads', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    usageSpy.mockReset()
  })

  it('ignores a slow earlier response that lands after a newer one', async () => {
    let resolveFirst: (v: unknown) => void = () => {}
    const first = new Promise((resolve) => {
      resolveFirst = resolve
    })

    // Mount fires the initial 24h request; hold it open.
    usageSpy.mockReturnValueOnce(first)
    const wrapper = mountUsage()
    await flushPromises()

    // The user switches to 7d; that request resolves immediately.
    usageSpy.mockResolvedValueOnce(aggregate('7d'))
    await wrapper.find('[data-test="usage-window-7d"]').trigger('click')
    await flushPromises()
    expect(usageSpy).toHaveBeenCalledTimes(2)

    // Now the stale 24h response finally arrives — it must be discarded.
    resolveFirst(aggregate('24h'))
    await flushPromises()

    expect(usageSpy).toHaveBeenLastCalledWith(expect.objectContaining({ window: '7d' }))
    expect(wrapper.find('[data-test="usage-freshness"]').exists()).toBe(true)
    expect(wrapper.vm.$data ?? {}).toBeDefined()
    // The rendered aggregate is still the 7d one the user asked for.
    expect((wrapper.vm as unknown as { data: { window: string } }).data.window).toBe('7d')
  })

  it('does not let a superseded request clear the spinner owned by the newest one', async () => {
    let resolveFirst: (v: unknown) => void = () => {}
    const first = new Promise((resolve) => {
      resolveFirst = resolve
    })
    usageSpy.mockReturnValueOnce(first)
    const wrapper = mountUsage()
    await flushPromises()

    // Second request is still in flight when the first one resolves.
    usageSpy.mockReturnValueOnce(new Promise(() => {}))
    await wrapper.find('[data-test="usage-window-7d"]').trigger('click')
    await flushPromises()

    resolveFirst(aggregate('24h'))
    await flushPromises()

    expect((wrapper.vm as unknown as { loading: boolean }).loading).toBe(true)
  })
})
