import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import OnboardingWizard from '@/components/OnboardingWizard.vue'
import api from '@/services/api'

// MCP-2952: GetAllStatus() is stat-only (#706/MCP-2829) and always reports
// connected=false for every installed client. The wizard already fetches the
// onboarding state (which carries content-resolved connected_client_ids) and
// must merge those IDs into the per-client list so connected clients render a
// "Connected" badge instead of a fresh Connect button.

vi.mock('@/services/api', () => ({
  default: {
    getConnectStatus: vi.fn(),
    getOnboardingState: vi.fn(),
    getActivities: vi.fn(),
    getConfig: vi.fn(),
    getDockerStatus: vi.fn(),
    getCanonicalConfigPaths: vi.fn(),
    connectClient: vi.fn(),
  },
}))

function onboardingState(connectedIds: string[]) {
  return {
    success: true,
    data: {
      has_connected_client: connectedIds.length > 0,
      has_configured_server: true,
      connected_client_count: connectedIds.length,
      connected_client_ids: connectedIds,
      configured_server_count: 1,
      state: { engaged: false },
      should_show_wizard: true,
      first_mcp_client_ever: false,
      mcp_clients_seen_ever: [],
      incomplete_tab_count: 0,
    },
  }
}

describe('OnboardingWizard connected_client_ids merge', () => {
  let pinia: any

  beforeEach(() => {
    pinia = createPinia()
    setActivePinia(pinia)
    vi.clearAllMocks()
    // Safe defaults for the wizard's open lifecycle.
    ;(api.getActivities as any).mockResolvedValue({ success: true, data: { activities: [] } })
    ;(api.getConfig as any).mockResolvedValue({ success: true, data: {} })
    ;(api.getDockerStatus as any).mockResolvedValue({ success: true, data: { available: false } })
    ;(api.getCanonicalConfigPaths as any).mockResolvedValue({ success: true, data: { paths: [] } })
  })

  it('renders Connected for a stat-only client present in connected_client_ids, Connect for the rest', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      data: [
        {
          id: 'codex',
          name: 'Codex CLI',
          config_path: '/Users/test/.codex/config.toml',
          exists: true,
          connected: false, // stat-only listing never sets this
          supported: true,
          icon: 'codex',
        },
        {
          id: 'cursor',
          name: 'Cursor',
          config_path: '/Users/test/.cursor/mcp.json',
          exists: true,
          connected: false, // genuinely not connected
          supported: true,
          icon: 'cursor',
        },
      ],
    })
    ;(api.getOnboardingState as any).mockResolvedValue(onboardingState(['codex']))

    const wrapper = mount(OnboardingWizard, {
      props: { show: false },
      global: { plugins: [pinia] },
    })

    await wrapper.setProps({ show: true })
    await flushPromises()

    // Ensure the Clients tab is active (initial tab depends on onboarding
    // predicates; the merge under test lives on the Clients panel).
    await wrapper.find('[data-test="tab-clients"]').trigger('click')
    await flushPromises()

    // codex resolved as connected -> Connected badge, no Connect button.
    const codexRow = wrapper.find('[data-test="client-row-codex"]')
    expect(codexRow.exists()).toBe(true)
    expect(codexRow.text()).toContain('Connected')
    expect(wrapper.find('[data-test="connect-codex"]').exists()).toBe(false)

    // cursor genuinely not connected -> Connect button still offered.
    const cursorRow = wrapper.find('[data-test="client-row-cursor"]')
    expect(cursorRow.exists()).toBe(true)
    expect(wrapper.find('[data-test="connect-cursor"]').exists()).toBe(true)
  })
})
