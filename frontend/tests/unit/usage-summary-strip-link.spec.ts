import { describe, it, expect, beforeEach, vi } from 'vitest'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Review finding F3: this PR (109-d) removes the old Dashboard Usage/Overview
// tab switcher and adds no replacement link to /usage anywhere in Home.vue,
// TopHeader.vue or SidebarNav.vue. The full Monitor/Usage sidebar entry is
// owned by a later PR (109-i, FR-050), but until it merges, /usage must stay
// reachable from somewhere a user would actually look — the usage summary
// strip Home already renders is the natural, minimal, in-scope place.
vi.mock('@/services/api', () => ({
  default: {
    getActivitySummary: vi.fn().mockResolvedValue({
      success: true,
      data: { call_count: 3, blocked_count: 0, call_error_count: 0 },
    }),
  },
}))

import UsageSummaryStrip from '@/components/UsageSummaryStrip.vue'

function makeRouter() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'home', component: { template: '<div />' } },
      { path: '/usage', name: 'usage', component: { template: '<div />' } },
      { path: '/activity', name: 'activity', component: { template: '<div />' } },
    ],
  })
  return router
}

describe('UsageSummaryStrip (review finding F3)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('links to the full /usage page, keeping it reachable before 109-i adds a sidebar entry', async () => {
    const router = makeRouter()
    router.push('/')
    await router.isReady()
    const wrapper = shallowMount(UsageSummaryStrip, {
      global: { plugins: [router], stubs: { RouterLink: false } },
    })
    await flushPromises()

    const link = wrapper.find('a[data-test="usage-strip-view-usage"]')
    expect(link.exists()).toBe(true)
    expect(link.attributes('href')).toBe('/usage')
  })
})
