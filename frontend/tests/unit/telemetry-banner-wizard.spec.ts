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
          AddServerModal: { name: 'AddServerModal', props: ['show'], template: '<div />' },
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
