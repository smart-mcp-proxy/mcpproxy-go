import { describe, it, expect, beforeEach, vi } from 'vitest'
import { ref } from 'vue'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 109 FR-001/FR-003/FR-051: Home renders the ONE needs-attention list
// (replacing Dashboard.vue's two bespoke banners), and a usage summary strip
// that moves above the topology when the list is empty.

const attentionSpy = vi.hoisted(() => vi.fn())

function attentionItem(name: string, target: string, label: string) {
  return {
    id: `sign_in_required:server:${name}`,
    kind: 'sign_in_required',
    rank: 10,
    subject: { type: 'server', id: name, name },
    summary: `${name}: needs attention`,
    fix: { verb: 'login', label, target },
    since: '2026-09-25T06:00:00Z',
  }
}

vi.mock('@/services/api', () => {
  const ok = (data: unknown = null) => vi.fn().mockResolvedValue({ success: true, data })
  const fakeEventSource = {
    onopen: null,
    onmessage: null,
    onerror: null,
    addEventListener() {},
    removeEventListener() {},
    close() {},
  }
  const base: Record<string, unknown> = {
    getAttention: attentionSpy,
    getServers: ok({ servers: [] }),
    getActivitySummary: ok({ call_count: 5, blocked_count: 1, call_error_count: 2 }),
    createEventSource: vi.fn(() => fakeEventSource),
    hasAPIKey: vi.fn(() => true),
    getAPIKeyPreview: vi.fn(() => 'test…'),
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

vi.mock('@/composables/useSecurityScannerStatus', () => ({
  refreshSecurityScannerStatus: vi.fn().mockResolvedValue(undefined),
  useSecurityScannerStatus: () => ({
    totalFindings: ref(0),
    totalScans: ref(0),
    loaded: ref(true),
  }),
}))

import Home from '@/views/Home.vue'

class FakeEventSource {
  close() {}
  addEventListener() {}
  onmessage: ((e: unknown) => void) | null = null
  onerror: ((e: unknown) => void) | null = null
}

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'home', component: Home },
      { path: '/servers/:name', name: 'server-detail', component: { template: '<div />' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
    ],
  })
}

async function mountHome(path = '/') {
  const router = makeRouter()
  router.push(path)
  await router.isReady()
  const wrapper = shallowMount(Home, {
    global: {
      plugins: [createPinia(), router],
      stubs: { RouterLink: false, AttentionList: false, UsageSummaryStrip: false },
    },
  })
  await flushPromises()
  return wrapper
}

describe('Home attention list (Spec 109 FR-001/FR-003)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    attentionSpy.mockReset()
    ;(globalThis as unknown as { EventSource: unknown }).EventSource = FakeEventSource
  })

  it('shows "All clear" and moves the usage strip above the topology when the list is empty', async () => {
    attentionSpy.mockResolvedValue({ success: true, data: { count: 0, items: [] } })
    const wrapper = await mountHome()

    expect(wrapper.find('[data-test="attention-all-clear"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="attention-list-card"]').exists()).toBe(false)

    const stripTop = wrapper.find('[data-test="home-usage-strip-top"]')
    const stripBottom = wrapper.find('[data-test="home-usage-strip-bottom"]')
    expect(stripTop.exists()).toBe(true)
    expect(stripBottom.exists()).toBe(false)
  })

  it('lists items in the order the API returns them, with fix buttons routing to fix.target', async () => {
    attentionSpy.mockResolvedValue({
      success: true,
      data: {
        count: 2,
        items: [
          attentionItem('github', '/servers/github', 'Sign in'),
          attentionItem('weather', '/servers/weather?tab=config&focus=env', 'Add secret'),
        ],
      },
    })
    const wrapper = await mountHome()

    const rows = wrapper.findAll('[data-test^="attention-item-"]')
    expect(rows).toHaveLength(2)
    expect(rows[0].text()).toContain('github')
    expect(rows[1].text()).toContain('weather')

    // router-link (not stubbed) renders an <a> with the resolved href.
    const fixLink = wrapper.find('a[data-test="attention-fix-sign_in_required:server:github"]')
    expect(fixLink.exists()).toBe(true)
    expect(fixLink.attributes('href')).toBe('/servers/github')

    // The usage strip sits below the topology once there is something to act on.
    expect(wrapper.find('[data-test="home-usage-strip-top"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="home-usage-strip-bottom"]').exists()).toBe(true)
  })
})

describe('Home/Usage/Overview routing (Spec 109 FR-051)', () => {
  it('/ resolves to Home, /usage resolves to a different (Usage) component, /overview redirects to /', async () => {
    const { default: appRouter } = await import('@/router')

    const home = appRouter.resolve('/')
    expect(home.name).toBe('home')

    const usage = appRouter.resolve('/usage')
    expect(usage.matched[0].components?.default).not.toBe(home.matched[0].components?.default)

    const overview = appRouter.resolve('/overview')
    expect(overview.redirectedFrom || overview.matched.some(r => r.redirect)).toBeTruthy()
  })
})
