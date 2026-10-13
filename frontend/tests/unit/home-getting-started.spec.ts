import { describe, it, expect, beforeEach, vi } from 'vitest'
import { ref } from 'vue'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 109 FR-051 / US1-4 (T170): a fresh instance (no server configured)
// shows a "Get started" card instead of the green "All clear" banner, which
// would be false reassurance. Any attention item still wins; the CLI is
// unchanged.

const state = vi.hoisted(() => ({
  servers: [] as unknown[],
  attention: { count: 0, items: [] as unknown[] },
  onboarding: { has_connected_client: false } as Record<string, unknown>,
  serversPending: false,
}))

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
    getAttention: vi.fn(async () => ({ success: true, data: state.attention })),
    getServers: vi.fn(() =>
      state.serversPending
        ? new Promise(() => {})
        : Promise.resolve({ success: true, data: { servers: state.servers } })
    ),
    getOnboardingState: vi.fn(async () => ({
      success: true,
      data: { should_show_wizard: false, state: { engaged: false }, ...state.onboarding },
    })),
    getActivitySummary: ok({ call_count: 0, blocked_count: 0, call_error_count: 0 }),
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
import api from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { useOnboardingStore } from '@/stores/onboarding'

class FakeEventSource {
  close() {}
  addEventListener() {}
  onmessage: ((e: unknown) => void) | null = null
  onerror: ((e: unknown) => void) | null = null
}

const ATTENTION_ITEM = {
  id: 'server_review:server:github',
  kind: 'server_review',
  rank: 50,
  subject: { type: 'server', id: 'github', name: 'github' },
  summary: 'github: waiting for review',
  fix: { verb: 'review', label: 'Review', target: '/review/github' },
  since: '2026-09-25T06:00:00Z',
}

async function mountHome(opts: { tenant?: boolean } = {}) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'home', component: Home },
      { path: '/add-server', name: 'add-server', component: { template: '<div />' } },
      { path: '/clients', name: 'clients', component: { template: '<div />' } },
      { path: '/activity', name: 'activity', component: { template: '<div />' } },
      { path: '/usage', name: 'usage', component: { template: '<div />' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
    ],
  })
  router.push('/')
  await router.isReady()
  const pinia = createPinia()
  setActivePinia(pinia)
  if (opts.tenant) {
    const auth = useAuthStore()
    auth.isTeamsEdition = true
    auth.user = {
      id: 'carol',
      email: 'carol@example.com',
      display_name: 'Carol',
      role: 'user',
      provider: 'oidc',
      created_at: '',
      last_login_at: '',
    }
    ;(api.hasAPIKey as unknown as ReturnType<typeof vi.fn>).mockReturnValue(false)
  }
  const wrapper = shallowMount(Home, {
    global: {
      plugins: [pinia, router],
      stubs: { RouterLink: false, AttentionList: false, UsageSummaryStrip: false },
    },
  })
  await flushPromises()
  return { wrapper, router, pinia }
}

const CARD = '[data-test="home-getting-started"]'
const ALL_CLEAR = '[data-test="attention-all-clear"]'

describe('Home getting-started card (Spec 109 FR-051, T170)', () => {
  beforeEach(() => {
    state.servers = []
    state.attention = { count: 0, items: [] }
    state.onboarding = { has_connected_client: false }
    state.serversPending = false
    ;(api.hasAPIKey as unknown as ReturnType<typeof vi.fn>).mockReturnValue(true)
    ;(globalThis as unknown as { EventSource: unknown }).EventSource = FakeEventSource
  })

  it('replaces "All clear" and hides the top usage strip when no server is configured', async () => {
    const { wrapper, router, pinia } = await mountHome()

    expect(wrapper.find(CARD).exists()).toBe(true)
    expect(wrapper.find(CARD).text()).toContain('Get started')
    expect(wrapper.find(CARD).text()).toContain('MCPProxy has no servers yet')
    expect(wrapper.find(ALL_CLEAR).exists()).toBe(false)
    expect(wrapper.find('[data-test="home-usage-strip-top"]').exists()).toBe(false)

    await wrapper.find('[data-test="home-getting-started-add-server"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/add-server')

    await router.push('/')
    await wrapper.find('[data-test="home-getting-started-connect-client"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/clients')

    const onboarding = useOnboardingStore(pinia)
    const openWizard = vi.spyOn(onboarding, 'openWizard')
    await wrapper.find('[data-test="home-getting-started-wizard"]').trigger('click')
    expect(openWizard).toHaveBeenCalled()
  })

  it('lets an attention item win over the card', async () => {
    state.attention = { count: 1, items: [ATTENTION_ITEM] }
    const { wrapper } = await mountHome()

    expect(wrapper.find(CARD).exists()).toBe(false)
    expect(wrapper.find('[data-test="attention-list-card"]').exists()).toBe(true)
  })

  it('keeps "All clear" once a server is configured', async () => {
    state.servers = [{ name: 'notes', enabled: true, connected: true, tool_count: 3 }]
    const { wrapper } = await mountHome()

    expect(wrapper.find(CARD).exists()).toBe(false)
    expect(wrapper.find(ALL_CLEAR).exists()).toBe(true)
    expect(wrapper.find('[data-test="home-usage-strip-top"]').exists()).toBe(true)
  })

  it('does not flash the card or "All clear" before the server list has loaded', async () => {
    state.serversPending = true
    const { wrapper } = await mountHome()

    expect(wrapper.find(CARD).exists()).toBe(false)
    // The server count is unknown, so "All clear" would be a guess too.
    expect(wrapper.find(ALL_CLEAR).exists()).toBe(false)
  })

  it('shows the client step as done once a client is connected', async () => {
    state.onboarding = { has_connected_client: true }
    const { wrapper } = await mountHome()

    const step = wrapper.find('[data-test="home-getting-started-client-done"]')
    expect(step.exists()).toBe(true)
    expect(step.text()).toContain('connected')
  })

  it('shows no card to a tenant principal', async () => {
    const { wrapper } = await mountHome({ tenant: true })
    expect(wrapper.find(CARD).exists()).toBe(false)
  })
})
