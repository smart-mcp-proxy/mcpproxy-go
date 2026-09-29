import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import OnboardingWizard from '@/components/OnboardingWizard.vue'
import api from '@/services/api'

// A tab the user picks while onOpened()'s fetches are still in flight must not
// be overwritten when those fetches finally resolve and the wizard applies its
// computed initial tab.

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

function onboardingState() {
  return {
    success: true,
    data: {
      // A client is connected but no server is usable, so the computed
      // initial tab is 'servers'.
      has_connected_client: true,
      has_configured_server: false,
      connected_client_count: 1,
      connected_client_ids: ['cursor'],
      configured_server_count: 0,
      state: { engaged: false },
      should_show_wizard: true,
      first_mcp_client_ever: false,
      mcp_clients_seen_ever: [],
      incomplete_tab_count: 1,
    },
  }
}

describe('OnboardingWizard initial tab vs user selection', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.getActivities as any).mockResolvedValue({ success: true, data: { activities: [] } })
    ;(api.getConfig as any).mockResolvedValue({ success: true, data: {} })
    ;(api.getDockerStatus as any).mockResolvedValue({ success: true, data: { available: true } })
    ;(api.getOnboardingState as any).mockResolvedValue(onboardingState())
    ;(api.getCanonicalConfigPaths as any).mockResolvedValue({ success: true, data: { paths: [] } })
    ;(api.markOnboardingState as any).mockResolvedValue({ success: true, data: {} })
  })

  function mountWizard() {
    return mount(OnboardingWizard, {
      props: { show: false },
      global: {
        stubs: {
          RouterLink: { template: '<a><slot /></a>' },
          AddServerModal: { template: '<div />' },
        },
      },
    })
  }

  it('keeps the tab the user clicked while the open-time fetches were pending', async () => {
    let resolveClients: (v: unknown) => void = () => {}
    ;(api.getConnectStatus as any).mockImplementation(
      () => new Promise((resolve) => { resolveClients = resolve }),
    )

    const wrapper = mountWizard()
    await wrapper.setProps({ show: true })
    await flushPromises()

    // The client fetch is still pending: the user picks the Verify tab.
    await wrapper.find('[data-test="tab-verify"]').trigger('click')
    expect(wrapper.find('[data-test="panel-verify"]').exists()).toBe(true)

    resolveClients({ success: true, data: { clients: [] } })
    await flushPromises()

    expect(wrapper.find('[data-test="panel-verify"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="panel-servers"]').exists()).toBe(false)
  })

  it('still applies the computed initial tab when the user did not pick one', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: { clients: [] } })

    const wrapper = mountWizard()
    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(wrapper.find('[data-test="panel-servers"]').exists()).toBe(true)
  })
})
