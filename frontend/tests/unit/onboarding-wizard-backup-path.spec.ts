import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import OnboardingWizard from '@/components/OnboardingWizard.vue'
import api from '@/services/api'

// Spec 078 US2 / FR-006: the wizard's connect step must surface the timestamped
// backup path after a successful connect (and explicitly represent the
// "no prior file to back up" case) — the backend has always returned
// ConnectResult.backup_path but the wizard previously dropped it.

vi.mock('@/services/api', () => ({
  default: {
    getConnectStatus: vi.fn(),
    getOnboardingState: vi.fn(),
    markOnboardingState: vi.fn(),
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

function cursorClient() {
  return {
    id: 'cursor',
    name: 'Cursor',
    config_path: '/Users/test/.cursor/mcp.json',
    exists: true,
    connected: false,
    supported: true,
    icon: 'cursor',
  }
}

async function openClientsTab(pinia: any) {
  const wrapper = mount(OnboardingWizard, {
    props: { show: false },
    global: { plugins: [pinia] },
  })
  await wrapper.setProps({ show: true })
  await flushPromises()
  await wrapper.find('[data-test="tab-clients"]').trigger('click')
  await flushPromises()
  return wrapper
}

describe('OnboardingWizard backup path surfacing (Spec 078 US2)', () => {
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
    ;(api.getOnboardingState as any).mockResolvedValue(onboardingState([]))
    ;(api.markOnboardingState as any).mockResolvedValue(onboardingState([]))
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: [cursorClient()] })
  })

  it('shows the backup path in the client row after a successful connect', async () => {
    ;(api.connectClient as any).mockResolvedValue({
      success: true,
      data: {
        success: true,
        client: 'cursor',
        config_path: '/Users/test/.cursor/mcp.json',
        backup_path: '/Users/test/.cursor/mcp.json.bak.20260702-101530',
        server_name: 'mcpproxy',
        action: 'added',
        message: 'MCPProxy registered in Cursor as mcpproxy',
      },
    })
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.assign(navigator, { clipboard: { writeText } })

    const wrapper = await openClientsTab(pinia)

    // No backup info before any connect happened in this session.
    expect(wrapper.find('[data-test="client-backup-cursor"]').exists()).toBe(false)

    await wrapper.find('[data-test="connect-cursor"]').trigger('click')
    await flushPromises()

    const backup = wrapper.find('[data-test="client-backup-cursor"]')
    expect(backup.exists()).toBe(true)
    expect(backup.text()).toContain('A backup of your previous config was saved to')
    expect(backup.text()).toContain('/Users/test/.cursor/mcp.json.bak.20260702-101530')

    // One-click copy of the backup path.
    const copyBtn = wrapper.find('[data-test="client-copy-backup-cursor"]')
    expect(copyBtn.exists()).toBe(true)
    await copyBtn.trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith('/Users/test/.cursor/mcp.json.bak.20260702-101530')
  })

  it('states there was no prior file to back up when connect created the config', async () => {
    ;(api.connectClient as any).mockResolvedValue({
      success: true,
      data: {
        success: true,
        client: 'cursor',
        config_path: '/Users/test/.cursor/mcp.json',
        // no backup_path: the file did not exist before the write
        server_name: 'mcpproxy',
        action: 'added',
        message: 'MCPProxy registered in Cursor as mcpproxy',
      },
    })

    const wrapper = await openClientsTab(pinia)

    await wrapper.find('[data-test="connect-cursor"]').trigger('click')
    await flushPromises()

    const backup = wrapper.find('[data-test="client-backup-cursor"]')
    expect(backup.exists()).toBe(true)
    expect(backup.text()).toContain('No prior config file existed, so no backup was needed.')
    expect(backup.find('[data-test="client-copy-backup-cursor"]').exists()).toBe(false)
  })
})
