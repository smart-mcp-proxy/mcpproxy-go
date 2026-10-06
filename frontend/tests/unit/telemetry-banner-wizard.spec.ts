import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import TelemetryBanner from '@/components/TelemetryBanner.vue'
import OnboardingWizard from '@/components/OnboardingWizard.vue'
import api from '@/services/api'
import { useOnboardingStore, TELEMETRY_BANNER_STORAGE_KEY } from '@/stores/onboarding'

vi.mock('@/services/api', () => ({
  default: {
    getConnectStatus: vi.fn(),
    getOnboardingState: vi.fn(),
    getActivities: vi.fn(),
    getConfig: vi.fn(),
    getDockerStatus: vi.fn(),
    getCanonicalConfigPaths: vi.fn(),
    getStatus: vi.fn(),
  },
}))

// Spec 109-b FR-044: the telemetry notice must not render while the wizard
// is open, and dismissal is one shared key across both surfaces.

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', name: 'dashboard', component: { template: '<div />' } }],
  })
}

describe('TelemetryBanner (Spec 109-b FR-044)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
  })

  afterEach(() => {
    localStorage.clear()
  })

  async function mountBanner() {
    const router = makeRouter()
    router.push('/')
    await router.isReady()
    return mount(TelemetryBanner, { global: { plugins: [router] } })
  }

  it('renders when the wizard is closed and not previously dismissed', async () => {
    const wrapper = await mountBanner()
    expect(wrapper.find('[data-test="telemetry-banner"]').exists()).toBe(true)
  })

  it('reloads the telemetry state when the window regains focus and stops after unmount (#1466)', async () => {
    const store = useOnboardingStore()
    const load = vi.spyOn(store, 'loadTelemetryState').mockResolvedValue(undefined as never)
    const wrapper = await mountBanner()
    expect(load).toHaveBeenCalledTimes(1)
    window.dispatchEvent(new Event('focus'))
    expect(load).toHaveBeenCalledTimes(2)
    wrapper.unmount()
    window.dispatchEvent(new Event('focus'))
    expect(load).toHaveBeenCalledTimes(2)
  })

  it('does not render while the wizard is open', async () => {
    const store = useOnboardingStore()
    store.wizardOpen = true
    const wrapper = await mountBanner()
    expect(wrapper.find('[data-test="telemetry-banner"]').exists()).toBe(false)
  })

  it('reappears once the wizard closes (still undismissed)', async () => {
    const store = useOnboardingStore()
    store.wizardOpen = true
    const wrapper = await mountBanner()
    expect(wrapper.find('[data-test="telemetry-banner"]').exists()).toBe(false)

    store.wizardOpen = false
    await wrapper.vm.$nextTick()
    expect(wrapper.find('[data-test="telemetry-banner"]').exists()).toBe(true)
  })

  it('stays hidden once dismissed, independent of wizard state', async () => {
    localStorage.setItem(TELEMETRY_BANNER_STORAGE_KEY, 'true')
    const wrapper = await mountBanner()
    expect(wrapper.find('[data-test="telemetry-banner"]').exists()).toBe(false)
  })

  it('dismissing writes the shared storage key', async () => {
    const wrapper = await mountBanner()
    await wrapper.find('[data-test="telemetry-banner-dismiss"]').trigger('click')
    expect(localStorage.getItem(TELEMETRY_BANNER_STORAGE_KEY)).toBe('true')
    expect(wrapper.find('[data-test="telemetry-banner"]').exists()).toBe(false)
  })
})

describe('OnboardingWizard Verify step telemetry one-liner (Spec 109-b FR-044)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    vi.clearAllMocks()
    ;(api.getActivities as any).mockResolvedValue({ success: true, data: { activities: [] } })
    ;(api.getConfig as any).mockResolvedValue({ success: true, data: {} })
    ;(api.getDockerStatus as any).mockResolvedValue({ success: true, data: { available: false } })
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: [] })
    ;(api.getCanonicalConfigPaths as any).mockResolvedValue({ success: true, data: { paths: [] } })
    ;(api.getStatus as any).mockResolvedValue({ success: true, data: {} })
    ;(api.getOnboardingState as any).mockResolvedValue({
      success: true,
      data: {
        has_connected_client: true,
        has_configured_server: true,
        connected_client_count: 1,
        connected_client_ids: ['cursor'],
        configured_server_count: 1,
        state: { engaged: false },
        should_show_wizard: true,
        first_mcp_client_ever: true,
        mcp_clients_seen_ever: ['cursor'],
        incomplete_tab_count: 0,
        has_usable_server: true,
        usable_servers: ['github'],
      },
    })
  })

  afterEach(() => {
    localStorage.clear()
  })

  async function mountOnVerify() {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', name: 'dashboard', component: { template: '<div />' } },
        { path: '/activity', name: 'activity', component: { template: '<div />' } },
        { path: '/servers', name: 'servers', component: { template: '<div />' } },
        { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
      ],
    })
    router.push('/')
    await router.isReady()
    const wrapper = mount(OnboardingWizard, {
      props: { show: true },
      global: {
        plugins: [router],
        stubs: {
          RouterLink: { template: '<a><slot /></a>' },
        },
      },
    })
    await flushPromises()
    await wrapper.find('[data-test="tab-verify"]').trigger('click')
    await flushPromises()
    return wrapper
  }

  it('shows the one-line notice on the final (Verify) step', async () => {
    const wrapper = await mountOnVerify()
    expect(wrapper.find('[data-test="wizard-telemetry-notice"]').exists()).toBe(true)
  })

  it('hides the notice once dismissed from within the wizard', async () => {
    const wrapper = await mountOnVerify()
    await wrapper.find('[data-test="wizard-telemetry-notice-dismiss"]').trigger('click')
    expect(wrapper.find('[data-test="wizard-telemetry-notice"]').exists()).toBe(false)
    expect(localStorage.getItem(TELEMETRY_BANNER_STORAGE_KEY)).toBe('true')
  })

  it('does not show the notice when already dismissed via the post-wizard banner', async () => {
    localStorage.setItem(TELEMETRY_BANNER_STORAGE_KEY, 'true')
    const wrapper = await mountOnVerify()
    expect(wrapper.find('[data-test="wizard-telemetry-notice"]').exists()).toBe(false)
  })
})

// Dashboard.vue mounts TelemetryBanner and OnboardingWizard together for the
// whole session (the banner unconditionally, the wizard behind a `show`
// prop, never v-if) — both stay mounted at once, so dismissal on one surface
// must be visible to the other WITHOUT either one remounting.
describe('Telemetry notice dismissal is shared across mounted surfaces (Spec 109-b FR-044)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    vi.clearAllMocks()
    ;(api.getActivities as any).mockResolvedValue({ success: true, data: { activities: [] } })
    ;(api.getConfig as any).mockResolvedValue({ success: true, data: {} })
    ;(api.getDockerStatus as any).mockResolvedValue({ success: true, data: { available: false } })
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: [] })
    ;(api.getCanonicalConfigPaths as any).mockResolvedValue({ success: true, data: { paths: [] } })
    ;(api.getStatus as any).mockResolvedValue({ success: true, data: {} })
    ;(api.getOnboardingState as any).mockResolvedValue({
      success: true,
      data: {
        has_connected_client: true,
        has_configured_server: true,
        connected_client_count: 1,
        connected_client_ids: ['cursor'],
        configured_server_count: 1,
        state: { engaged: false },
        should_show_wizard: true,
        first_mcp_client_ever: true,
        mcp_clients_seen_ever: ['cursor'],
        incomplete_tab_count: 0,
        has_usable_server: true,
        usable_servers: ['github'],
      },
    })
  })

  afterEach(() => {
    localStorage.clear()
  })

  it('dismissing from the wizard hides the already-mounted banner once the wizard closes, with no remount', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', name: 'dashboard', component: { template: '<div />' } },
        { path: '/activity', name: 'activity', component: { template: '<div />' } },
        { path: '/servers', name: 'servers', component: { template: '<div />' } },
        { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
      ],
    })
    router.push('/')
    await router.isReady()

    const store = useOnboardingStore()
    const banner = mount(TelemetryBanner, { global: { plugins: [router] } })
    const wizard = mount(OnboardingWizard, {
      props: { show: true },
      global: {
        plugins: [router],
        stubs: {
          RouterLink: { template: '<a><slot /></a>' },
        },
      },
    })
    await flushPromises()
    store.wizardOpen = true
    await banner.vm.$nextTick()
    expect(banner.find('[data-test="telemetry-banner"]').exists()).toBe(false)

    await wizard.find('[data-test="tab-verify"]').trigger('click')
    await flushPromises()
    await wizard.find('[data-test="wizard-telemetry-notice-dismiss"]').trigger('click')
    expect(wizard.find('[data-test="wizard-telemetry-notice"]').exists()).toBe(false)
    expect(localStorage.getItem(TELEMETRY_BANNER_STORAGE_KEY)).toBe('true')

    // Closing the wizard makes the banner eligible again on the wizardOpen
    // condition alone — but it must still stay hidden because the dismissal
    // above applies to it too, even though this exact `banner` instance was
    // mounted before the dismiss ever happened.
    store.wizardOpen = false
    await banner.vm.$nextTick()
    expect(banner.find('[data-test="telemetry-banner"]').exists()).toBe(false)
  })
})

// Spec 109 FR-044a (codex first-run user test F-03): the notice reflects the
// effective telemetry state served on /api/v1/status.
describe('Telemetry notice reflects the effective state (Spec 109 FR-044a)', () => {
  const envState = { enabled: false, source: 'env', disabled_by: 'MCPPROXY_TELEMETRY=false' }
  const offLine =
    'Anonymous usage telemetry is off — disabled by MCPPROXY_TELEMETRY=false in the environment. Nothing is sent.'

  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    vi.clearAllMocks()
    ;(api.getActivities as any).mockResolvedValue({ success: true, data: { activities: [] } })
    ;(api.getConfig as any).mockResolvedValue({ success: true, data: {} })
    ;(api.getDockerStatus as any).mockResolvedValue({ success: true, data: { available: false } })
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: [] })
    ;(api.getCanonicalConfigPaths as any).mockResolvedValue({ success: true, data: { paths: [] } })
    ;(api.getOnboardingState as any).mockResolvedValue({
      success: true,
      data: {
        has_connected_client: true,
        has_configured_server: true,
        connected_client_count: 1,
        connected_client_ids: ['cursor'],
        configured_server_count: 1,
        state: { engaged: false },
        should_show_wizard: true,
        first_mcp_client_ever: true,
        mcp_clients_seen_ever: ['cursor'],
        incomplete_tab_count: 0,
        has_usable_server: true,
        usable_servers: ['github'],
      },
    })
  })

  afterEach(() => {
    localStorage.clear()
  })

  function statusWith(telemetry?: unknown) {
    ;(api.getStatus as any).mockResolvedValue({ success: true, data: telemetry ? { telemetry } : {} })
  }

  function routerFor() {
    return createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', name: 'dashboard', component: { template: '<div />' } },
        { path: '/activity', name: 'activity', component: { template: '<div />' } },
        { path: '/servers', name: 'servers', component: { template: '<div />' } },
        { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
      ],
    })
  }

  async function mountBanner() {
    const router = routerFor()
    router.push('/')
    await router.isReady()
    const wrapper = mount(TelemetryBanner, { global: { plugins: [router] } })
    await flushPromises()
    return wrapper
  }

  async function mountWizardOnVerify() {
    const router = routerFor()
    router.push('/')
    await router.isReady()
    const wrapper = mount(OnboardingWizard, {
      props: { show: true },
      global: { plugins: [router], stubs: { RouterLink: { template: '<a><slot /></a>' } } },
    })
    await flushPromises()
    await wrapper.find('[data-test="tab-verify"]').trigger('click')
    await flushPromises()
    return wrapper
  }

  it('env opt-out: the banner states it is off and why, with no Manage link or opt-out disclosure', async () => {
    statusWith(envState)
    const wrapper = await mountBanner()
    const banner = wrapper.find('[data-test="telemetry-banner"]')
    expect(banner.exists()).toBe(true)
    expect(banner.attributes('data-mode')).toBe('off_env')
    expect(wrapper.find('[data-test="telemetry-off-env"]').text()).toBe(offLine)
    expect(wrapper.find('[data-test="telemetry-banner-settings-link"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="telemetry-banner-disclosure"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('sends anonymous usage statistics')
  })

  it('config opt-out: no banner at all (the user chose it)', async () => {
    statusWith({ enabled: false, source: 'config' })
    const wrapper = await mountBanner()
    expect(wrapper.find('[data-test="telemetry-banner"]').exists()).toBe(false)
  })

  it('enabled: the original notice is unchanged', async () => {
    statusWith({ enabled: true, source: 'default' })
    const wrapper = await mountBanner()
    expect(wrapper.find('[data-test="telemetry-banner"]').attributes('data-mode')).toBe('notice')
    expect(wrapper.text()).toContain('MCPProxy sends anonymous usage statistics')
    expect(wrapper.find('[data-test="telemetry-banner-settings-link"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="telemetry-banner-disclosure"]').exists()).toBe(true)
  })

  it('an older core without status.telemetry keeps the original notice', async () => {
    statusWith(undefined)
    const wrapper = await mountBanner()
    expect(wrapper.find('[data-test="telemetry-banner"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('MCPProxy sends anonymous usage statistics')
  })

  it('wizard Verify step: env opt-out shows the off line instead of the usage-statistics copy', async () => {
    statusWith(envState)
    const wrapper = await mountWizardOnVerify()
    const notice = wrapper.find('[data-test="wizard-telemetry-notice"]')
    expect(notice.exists()).toBe(true)
    expect(notice.find('[data-test="telemetry-off-env"]').text()).toBe(offLine)
    expect(notice.text()).not.toContain('MCPProxy sends anonymous usage statistics')
  })

  it('dismissing the off line on the wizard hides the banner too (shared key)', async () => {
    statusWith(envState)
    const router = routerFor()
    router.push('/')
    await router.isReady()
    const banner = mount(TelemetryBanner, { global: { plugins: [router] } })
    const wizard = mount(OnboardingWizard, {
      props: { show: true },
      global: { plugins: [router], stubs: { RouterLink: { template: '<a><slot /></a>' } } },
    })
    await flushPromises()
    await wizard.find('[data-test="tab-verify"]').trigger('click')
    await flushPromises()
    await wizard.find('[data-test="wizard-telemetry-notice-dismiss"]').trigger('click')
    expect(localStorage.getItem(TELEMETRY_BANNER_STORAGE_KEY)).toBe('true')
    expect(banner.find('[data-test="telemetry-banner"]').exists()).toBe(false)
  })

  it('a failed status fetch does not start the reuse window: the next load retries', async () => {
    ;(api.getStatus as any).mockResolvedValueOnce({ success: false, error: 'Unauthorized' })
    const store = useOnboardingStore()
    await store.loadTelemetryState()
    expect(store.telemetryState).toBeNull()
    ;(api.getStatus as any).mockResolvedValueOnce({ success: true, data: { telemetry: envState } })
    await store.loadTelemetryState()
    expect(store.telemetryState?.source).toBe('env')
    // and a success IS reused within 30 s
    const calls = (api.getStatus as any).mock.calls.length
    await store.loadTelemetryState()
    expect((api.getStatus as any).mock.calls.length).toBe(calls)
  })

  it('fetches status once for a banner and an inline notice mounted together', async () => {
    statusWith(envState)
    const router = routerFor()
    router.push('/')
    await router.isReady()
    const store = useOnboardingStore()
    mount(TelemetryBanner, { global: { plugins: [router] } })
    mount(TelemetryBanner, { props: { variant: 'inline' }, global: { plugins: [router] } })
    await flushPromises()
    expect((api.getStatus as any).mock.calls.length).toBe(1)
    expect(store.telemetryState?.source).toBe('env')
  })
})
