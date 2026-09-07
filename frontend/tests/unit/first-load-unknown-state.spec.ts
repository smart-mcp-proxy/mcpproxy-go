import { describe, it, expect, beforeEach, vi } from 'vitest'
import { ref } from 'vue'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Audit F06 — behind the "Authentication Required" modal, every surface still
// renders. The rule under test is that a screen may not assert a FACT it has
// not been told: no green "Setup ✓" and no "0 connected / stopped" until the
// data those words describe has actually arrived.
//
// Each block asserts all three states — unknown, known-good, known-bad — so a
// harness that silently never resolves the fetch cannot make the suite pass
// vacuously: the known-good cases require the affirmative render.

const onboardingSpy = vi.hoisted(() =>
  vi.fn().mockResolvedValue({ success: false, error: 'Invalid or missing API key' })
)

const serversSpy = vi.hoisted(() =>
  vi.fn().mockResolvedValue({ success: false, error: 'Invalid or missing API key' })
)

const usageSpy = vi.hoisted(() =>
  vi.fn().mockResolvedValue({
    success: true,
    data: { window: '24h', tokens_saved: 0, tokens_saved_percentage: 0, tools: [], timeline: [] },
  })
)

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
    getOnboardingState: onboardingSpy,
    getServers: serversSpy,
    getActivityUsage: usageSpy,
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
  refreshSecurityScannerStatus: vi.fn().mockResolvedValue(undefined),
  useSecurityScannerStatus: () => ({
    totalFindings: ref(0),
    totalScans: ref(0),
    loaded: ref(false),
  }),
}))

import SidebarNav from '@/components/SidebarNav.vue'
import Dashboard from '@/views/Dashboard.vue'
import { useSystemStore } from '@/stores/system'

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
      { path: '/', name: 'dashboard', component: { template: '<div />' }, meta: { dashboardView: 'usage' } },
      { path: '/overview', name: 'dashboard-overview', component: { template: '<div />' }, meta: { dashboardView: 'overview' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
    ],
  })
}

const stubs = {
  RouterLink: { template: '<a><slot /></a>' },
  Suspense: false,
  UsageView: { template: '<div data-test="usage-view-stub" />' },
}

async function mountSidebar() {
  const router = makeRouter()
  router.push('/')
  await router.isReady()
  const wrapper = shallowMount(SidebarNav, {
    global: { plugins: [createPinia(), router], stubs },
  })
  await flushPromises()
  return wrapper
}

/** Collapse the rail and read the Setup entry's tooltip / aria-label. */
async function collapsedTitle(wrapper: Awaited<ReturnType<typeof mountSidebar>>) {
  useSystemStore().sidebarCollapsed = true
  await flushPromises()
  return wrapper.find('[data-test="sidebar-setup"]').attributes('title')
}

async function mountOverview() {
  const router = makeRouter()
  router.push('/overview')
  await router.isReady()
  const wrapper = shallowMount(Dashboard, {
    global: { plugins: [createPinia(), router], stubs },
  })
  await flushPromises()
  return wrapper
}

describe('sidebar Setup entry does not claim completion it was never told', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    onboardingSpy.mockReset()
    ;(globalThis as unknown as { EventSource: unknown }).EventSource = FakeEventSource
  })

  it('renders no checkmark when the onboarding state could not be fetched', async () => {
    // The 401 case behind the auth modal: the fetch resolves unsuccessfully,
    // so the store's `state` stays null forever. This is not a pre-fetch
    // flash — nothing later moves it off null.
    onboardingSpy.mockResolvedValue({ success: false, error: 'Invalid or missing API key' })
    const wrapper = await mountSidebar()

    const setup = wrapper.find('[data-test="sidebar-setup"]')
    expect(setup.exists()).toBe(true)
    expect(setup.text()).toContain('Setup')
    expect(setup.text()).not.toContain('✓')
    expect(wrapper.find('[data-test="sidebar-setup-badge"]').exists()).toBe(false)

    // The collapsed rail replaces the label with a tooltip/aria-label, so the
    // same claim has to be absent there too.
    expect(await collapsedTitle(wrapper)).toBe('Setup')
  })

  it('renders the checkmark once the state is known and nothing is outstanding', async () => {
    onboardingSpy.mockResolvedValue({
      success: true,
      data: { incomplete_tab_count: 0, state: { engaged: false } },
    })
    const wrapper = await mountSidebar()

    const setup = wrapper.find('[data-test="sidebar-setup"]')
    expect(setup.text()).toContain('✓')
    expect(wrapper.find('[data-test="sidebar-setup-badge"]').exists()).toBe(false)
    expect(await collapsedTitle(wrapper)).toBe('Setup ✓')
  })

  it('renders the outstanding-step badge when the state is known and incomplete', async () => {
    onboardingSpy.mockResolvedValue({
      success: true,
      data: { incomplete_tab_count: 2, state: { engaged: false } },
    })
    const wrapper = await mountSidebar()

    const setup = wrapper.find('[data-test="sidebar-setup"]')
    expect(setup.text()).not.toContain('✓')
    expect(wrapper.find('[data-test="sidebar-setup-badge"]').text()).toBe('2')
  })
})

describe('overview tiles do not assert counts or a run state they have not fetched', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    serversSpy.mockReset()
    onboardingSpy.mockResolvedValue({ success: false, error: 'Invalid or missing API key' })
    ;(globalThis as unknown as { EventSource: unknown }).EventSource = FakeEventSource
  })

  it('shows a dash, not "0 connected", while the server list is unknown', async () => {
    serversSpy.mockResolvedValue({ success: false, error: 'Invalid or missing API key' })
    const wrapper = await mountOverview()

    // Asserted on the panel's own text as well as the tiles, so this stays a
    // real failure rather than a missing-selector one.
    const panel = wrapper.find('[data-test="dashboard-overview-panel"]').text()
    expect(panel).not.toMatch(/0\s*connected/)
    expect(panel).not.toMatch(/0\s*tools available/)

    expect(wrapper.find('[data-test="overview-connected-count"]').text()).toBe('—')
    expect(wrapper.find('[data-test="overview-tool-count"]').text()).toBe('—')
  })

  it('shows the real counts once a server list has arrived', async () => {
    serversSpy.mockResolvedValue({
      success: true,
      data: {
        servers: [
          { name: 'srv-a', enabled: true, connected: true, tool_count: 3 },
          { name: 'srv-b', enabled: true, connected: true, tool_count: 4 },
        ],
      },
    })
    const wrapper = await mountOverview()

    expect(wrapper.find('[data-test="overview-connected-count"]').text()).toBe('2')
    expect(wrapper.find('[data-test="overview-tool-count"]').text()).toBe('7')
  })

  it('says nothing about the proxy run state until a status has arrived', async () => {
    // `isRunning` falls back to false when `status` is null, which labelled a
    // demonstrably running proxy "stopped" in error red behind the modal.
    const wrapper = await mountOverview()
    const system = useSystemStore()
    expect(system.status).toBeNull()

    expect(wrapper.find('[data-test="overview-proxy-state"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="dashboard-overview-panel"]').text()).not.toContain('stopped')
  })

  it('reports the run state once a status has arrived', async () => {
    const wrapper = await mountOverview()
    const system = useSystemStore()

    system.status = { running: true } as never
    await flushPromises()
    expect(wrapper.find('[data-test="overview-proxy-state"]').text()).toBe('active')

    system.status = { running: false } as never
    await flushPromises()
    expect(wrapper.find('[data-test="overview-proxy-state"]').text()).toBe('stopped')
  })
})
