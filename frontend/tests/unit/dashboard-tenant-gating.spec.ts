import { describe, it, expect, beforeEach, vi } from 'vitest'
import { ref } from 'vue'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 107 PR-C cross-review round 2, chunk 4 (P1): `refreshSecurityScannerStatus()`
// (Home.vue's onMounted, hitting GET /api/v1/security/overview, a must-refuse
// core door for a tenant session) is called unconditionally, unlike its
// sibling loaders which all carry `principalKind === 'tenant'` guards.
//
// Must be silent for a tenant principal — no call at all, not a call-then-403
// — matching FR-041's "hidden rather than issued-and-403'd".
//
// Spec 109 FR-051: Usage is now its own page, and Home's new summary strip
// has a separate activity-summary request. Both must stay silent for tenant
// sessions because those endpoints are admin-only.

const refreshSecuritySpy = vi.hoisted(() => vi.fn().mockResolvedValue(undefined))
const getActivitySummarySpy = vi.hoisted(() => vi.fn().mockResolvedValue({ success: true, data: {} }))

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
    getAttention: ok({ count: 0, items: [] }),
    getActivitySummary: getActivitySummarySpy,
    getServers: ok({ servers: [{ name: 'srv-a', enabled: true, connected: true, tool_count: 1 }] }),
    createEventSource: vi.fn(() => fakeEventSource),
    hasAPIKey: vi.fn(() => false),
    getAPIKeyPreview: vi.fn(() => 'none'),
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
  refreshSecurityScannerStatus: refreshSecuritySpy,
  useSecurityScannerStatus: () => ({
    totalFindings: ref(0),
    totalScans: ref(0),
    loaded: ref(true),
  }),
}))

import Home from '@/views/Home.vue'
import { useAuthStore } from '@/stores/auth'

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
      { path: '/add-server', name: 'add-server', component: { template: '<div />' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
    ],
  })
}

async function mountHomeAsAdmin() {
  const router = makeRouter()
  router.push('/')
  await router.isReady()

  return {
    router,
    wrapper: shallowMount(Home, {
      global: {
        plugins: [router],
        stubs: {
          RouterLink: { template: '<a><slot /></a>' },
          UsageSummaryStrip: false,
        },
      },
    }),
  }
}

async function mountHomeAsTenant() {
  const router = makeRouter()
  router.push('/')
  await router.isReady()

  const authStore = useAuthStore()
  authStore.isTeamsEdition = true
  authStore.user = {
    id: 'carol',
    email: 'carol@example.com',
    display_name: 'Carol',
    role: 'user',
    provider: 'oidc',
    created_at: '',
    last_login_at: '',
  }

  return shallowMount(Home, {
    global: {
      plugins: [router],
      stubs: {
        RouterLink: { template: '<a><slot /></a>' },
        UsageSummaryStrip: false,
      },
    },
  })
}

describe('Home tenant gating (Spec 107 FR-041, cross-review round 2 P1)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    refreshSecuritySpy.mockClear()
    getActivitySummarySpy.mockClear()
    ;(globalThis as unknown as { EventSource: unknown }).EventSource = FakeEventSource
  })

  it('never calls refreshSecurityScannerStatus for a tenant principal', async () => {
    await mountHomeAsTenant()
    await flushPromises()

    expect(refreshSecuritySpy).not.toHaveBeenCalled()
  })

  it('hides the usage summary strip and never requests its admin-only data for a tenant', async () => {
    const wrapper = await mountHomeAsTenant()
    await flushPromises()

    expect(wrapper.find('[data-test="home-usage-strip-top"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="home-usage-strip-bottom"]').exists()).toBe(false)
    expect(getActivitySummarySpy).not.toHaveBeenCalled()
  })

  // Spec 107 PR-C cross-review round 3, chunk 4 (P2): the topology's Connect
  // Clients / Import from client configs / Recent Sessions actions and the
  // "Add Server" button all reach core admin-only doors (/connect*,
  // POST /api/v1/tools/call via AddServerModal, /sessions) that the
  // tenant-session allowlist refuses with 403 — an enabled control that
  // always fails to act, contradicting FR-041's "hidden, not
  // issued-and-403'd". They must be absent from the DOM for a tenant
  // principal, not merely non-functional.
  it('hides the admin-only action buttons and links for a tenant principal', async () => {
    const wrapper = await mountHomeAsTenant()
    await flushPromises()

    expect(wrapper.find('[data-test="dashboard-admin-left-actions"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="dashboard-recent-sessions-link"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="dashboard-right-add-server"]').exists()).toBe(false)
  })

  it('routes Add Server and client-config import to their catalog-first tabs', async () => {
    const { wrapper, router } = await mountHomeAsAdmin()
    await flushPromises()

    await wrapper.find('[data-test="dashboard-right-add-server"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/add-server')

    await router.push('/')
    await flushPromises()
    await wrapper.find('[data-test="dashboard-import-configs"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/add-server?tab=import')
  })
})
