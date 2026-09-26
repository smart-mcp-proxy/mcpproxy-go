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

  // Review finding: T057's coverage never exercised the SSE
  // `attention.changed` live-update path — every EventSource mock is inert,
  // registered but never fired. This drives the actual production path: the
  // attention store's own window listener (`mcpproxy:attention-changed`,
  // dispatched by system.ts's SSE handler), not a mocked EventSource.
  it('refetches and re-renders when mcpproxy:attention-changed fires', async () => {
    attentionSpy.mockResolvedValue({ success: true, data: { count: 0, items: [] } })
    const wrapper = await mountHome()
    expect(wrapper.find('[data-test="attention-all-clear"]').exists()).toBe(true)

    attentionSpy.mockResolvedValue({
      success: true,
      data: { count: 1, items: [attentionItem('github', '/servers/github', 'Sign in')] },
    })
    window.dispatchEvent(new CustomEvent('mcpproxy:attention-changed'))
    await flushPromises()

    expect(wrapper.find('[data-test="attention-all-clear"]').exists()).toBe(false)
    const rows = wrapper.findAll('[data-test^="attention-item-"]')
    expect(rows).toHaveLength(1)
    expect(rows[0].text()).toContain('github')
  })

  // Review finding: only a `verb: 'login'` fix-button href was ever asserted;
  // no fixture used `server_review`/`tool_review`, so a regression breaking
  // FR-005's review-fix routing (`/review/<n>[?change=...]`) would stay green.
  it('routes a server_review/tool_review fix button to /review/<name> (FR-005)', async () => {
    attentionSpy.mockResolvedValue({
      success: true,
      data: {
        count: 2,
        items: [
          {
            id: 'server_review:server:github',
            kind: 'server_review',
            rank: 50,
            subject: { type: 'server', id: 'github', name: 'github' },
            summary: 'github: waiting for review',
            fix: { verb: 'review', label: 'Review', target: '/review/github' },
            since: '2026-09-25T06:00:00Z',
          },
          {
            id: 'tool_review:server:weather:pending',
            kind: 'tool_review',
            rank: 61,
            subject: { type: 'server', id: 'weather', name: 'weather' },
            summary: 'weather: 1 new tool(s), needs review',
            fix: { verb: 'review', label: 'Review', target: '/review/weather?change=pending' },
            since: '2026-09-25T06:00:00Z',
          },
        ],
      },
    })
    const wrapper = await mountHome()

    const serverReviewLink = wrapper.find('a[data-test="attention-fix-server_review:server:github"]')
    expect(serverReviewLink.exists()).toBe(true)
    expect(serverReviewLink.attributes('href')).toBe('/review/github')

    const toolReviewLink = wrapper.find(
      'a[data-test="attention-fix-tool_review:server:weather:pending"]'
    )
    expect(toolReviewLink.exists()).toBe(true)
    expect(toolReviewLink.attributes('href')).toBe('/review/weather?change=pending')
  })

  // Review finding: both fixtures for "order" used rank 10 for every item
  // with names already alphabetized, so the test titled "lists items in the
  // order the API returns them" passed trivially under any order-preserving
  // OR accidentally-correct sort. This fixture is neither rank-sorted nor
  // alphabetical, so only genuinely preserving the API's own order (the
  // backend's contract: rank ascending, then subject.name, per
  // contracts/rest-api.md#attention) renders it correctly — the frontend
  // must never re-sort it.
  it('renders items in exactly the order the API returns, without re-sorting client-side', async () => {
    attentionSpy.mockResolvedValue({
      success: true,
      data: {
        count: 3,
        items: [
          {
            id: 'client_never_seen:client:zzz-client',
            kind: 'client_never_seen',
            rank: 70,
            subject: { type: 'client', id: 'zzz-client', name: 'zzz-client' },
            summary: 'zzz-client: connected, never seen',
            fix: { verb: 'reload_hint', label: 'How to restart', target: '/clients?focus=zzz-client' },
            since: '2026-09-25T06:00:00Z',
          },
          attentionItem('aaa-server', '/servers/aaa-server', 'Sign in'),
          {
            id: 'server_review:server:mmm-server',
            kind: 'server_review',
            rank: 50,
            subject: { type: 'server', id: 'mmm-server', name: 'mmm-server' },
            summary: 'mmm-server: waiting for review',
            fix: { verb: 'review', label: 'Review', target: '/review/mmm-server' },
            since: '2026-09-25T06:00:00Z',
          },
        ],
      },
    })
    const wrapper = await mountHome()

    const rows = wrapper.findAll('[data-test^="attention-item-"]')
    expect(rows.map((r) => r.text())).toEqual([
      expect.stringContaining('zzz-client'),
      expect.stringContaining('aaa-server'),
      expect.stringContaining('mmm-server'),
    ])
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
