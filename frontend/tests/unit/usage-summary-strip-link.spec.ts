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
import { setAvailableFeatures } from '@/composables/useScopeQuery'

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
    setAvailableFeatures([])
  })

  it('links to the full /usage page from Home', async () => {
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

  it('keeps the exact activity targets', async () => {
    const router = makeRouter()
    router.push('/')
    await router.isReady()
    const wrapper = shallowMount(UsageSummaryStrip, {
      global: { plugins: [router], stubs: { RouterLink: false } },
    })
    await flushPromises()

    expect(wrapper.get('a[data-test="usage-strip-calls"]').attributes('href')).toBe('/activity?view=calls&from=-24h')
    expect(wrapper.get('a[data-test="usage-strip-blocked"]').attributes('href')).toBe('/activity?view=calls&from=-24h&status=blocked')
    expect(wrapper.get('a[data-test="usage-strip-errors"]').attributes('href')).toBe('/activity?view=calls&from=-24h&status=error')
  })

  // Spec 109-i FR-058 / url-filter-contract rule 3: the strip builds its links
  // through useScopeQuery.linkTo, so sticky scope (client here) carries from
  // Home into Activity, while the strip's own `from=-24h` wins over a sticky
  // `from` (the patch overrides).
  it('carries sticky client scope and lets its own window win', async () => {
    setAvailableFeatures(['scope_filters'])
    const router = makeRouter()
    router.push('/?client=cursor&from=-7d')
    await router.isReady()
    const wrapper = shallowMount(UsageSummaryStrip, {
      global: { plugins: [router], stubs: { RouterLink: false } },
    })
    await flushPromises()

    const href = wrapper.get('a[data-test="usage-strip-calls"]').attributes('href')!
    const url = new URL(href, 'http://x')
    expect(url.pathname).toBe('/activity')
    expect(url.searchParams.get('client')).toBe('cursor')
    expect(url.searchParams.get('from')).toBe('-24h')
    expect(url.searchParams.get('view')).toBe('calls')
    const blocked = new URL(wrapper.get('a[data-test="usage-strip-blocked"]').attributes('href')!, 'http://x')
    expect(blocked.searchParams.get('status')).toBe('blocked')
    expect(blocked.searchParams.get('client')).toBe('cursor')
  })
})
