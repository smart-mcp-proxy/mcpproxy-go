import { describe, it, expect, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import { vi } from 'vitest'
import OnboardingWizard from '@/components/OnboardingWizard.vue'
import api from '@/services/api'

// Spec 109-b FR-041: has_configured_server only means a server ENTRY exists,
// even while every one of them sits quarantined or has no approved tool.
// The Servers step must stay incomplete until has_usable_server is true.

vi.mock('@/services/api', () => ({
  default: {
    getConnectStatus: vi.fn(),
    getOnboardingState: vi.fn(),
    markOnboardingState: vi.fn(),
    getActivities: vi.fn(),
    getConfig: vi.fn(),
    getDockerStatus: vi.fn(),
    getCanonicalConfigPaths: vi.fn(),
    getConnectPreview: vi.fn(),
    connectClient: vi.fn(),
    importServersFromPath: vi.fn(),
  },
}))

function onboardingState(overrides: Record<string, unknown> = {}) {
  return {
    success: true,
    data: {
      has_connected_client: true,
      has_configured_server: true,
      connected_client_count: 1,
      connected_client_ids: ['cursor'],
      configured_server_count: 1,
      state: { engaged: false },
      should_show_wizard: true,
      first_mcp_client_ever: false,
      mcp_clients_seen_ever: [],
      incomplete_tab_count: 1,
      has_usable_server: false,
      usable_servers: [],
      ...overrides,
    },
  }
}

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'dashboard', component: { template: '<div />' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
    ],
  })
}

async function mountWizard() {
  ;(api.getCanonicalConfigPaths as any).mockResolvedValue({ success: true, data: { paths: [] } })
  const router = makeRouter()
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
  return wrapper
}

describe('OnboardingWizard Servers step usable-completion (Spec 109-b FR-041)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.getActivities as any).mockResolvedValue({ success: true, data: { activities: [] } })
    ;(api.getConfig as any).mockResolvedValue({ success: true, data: {} })
    ;(api.getDockerStatus as any).mockResolvedValue({ success: true, data: { available: true } })
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: { clients: [] } })
    ;(api.markOnboardingState as any).mockResolvedValue(onboardingState())
  })

  it('stays incomplete when servers are configured but none are usable', async () => {
    ;(api.getOnboardingState as any).mockResolvedValue(
      onboardingState({ has_configured_server: true, has_usable_server: false, usable_servers: [] })
    )
    const wrapper = await mountWizard()

    const tab = wrapper.find('[data-test="tab-servers"]')
    expect(tab.text()).not.toContain('✓')
    expect(tab.text()).toContain('2')
  })

  it('becomes complete once has_usable_server is true', async () => {
    ;(api.getOnboardingState as any).mockResolvedValue(
      onboardingState({ has_configured_server: true, has_usable_server: true, usable_servers: ['github'] })
    )
    const wrapper = await mountWizard()

    const tab = wrapper.find('[data-test="tab-servers"]')
    expect(tab.text()).toContain('✓')
  })

  it('lands on the Servers step (not Verify) when servers exist but none are usable', async () => {
    ;(api.getOnboardingState as any).mockResolvedValue(
      onboardingState({
        has_connected_client: true,
        has_configured_server: true,
        has_usable_server: false,
        first_mcp_client_ever: true,
      })
    )
    const wrapper = await mountWizard()

    expect(wrapper.find('[data-test="panel-servers"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="panel-verify"]').exists()).toBe(false)
  })
})
