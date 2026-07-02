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

// Bridge client with NO config file on disk — the only real-world producer of
// an empty backup_path (connect creates the file). Spec 078 US2's independent
// test runs against exactly this shape.
function bridgeClientNoConfig() {
  return {
    id: 'claude-desktop',
    name: 'Claude Desktop',
    config_path: '/Users/test/Library/Application Support/Claude/claude_desktop_config.json',
    exists: false,
    connected: false,
    supported: true,
    bridge: true,
    note: 'Connects via an mcp-remote stdio bridge (npx -y mcp-remote). Requires Node.js.',
    icon: 'claude-desktop',
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

  // Spec 078 US2 independent test / FR-006: a bridge client with no config file
  // on disk MUST be connectable from the wizard (Connect creates the file) and
  // the result MUST state the "no prior file to back up" case. Previously the
  // wizard row rendered 'Not installed' for every exists=false client — making
  // the no-prior-file branch unreachable from the wizard.
  it('offers Connect for a bridge client with no config file and states no backup was needed', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      data: [bridgeClientNoConfig()],
    })
    ;(api.connectClient as any).mockResolvedValue({
      success: true,
      data: {
        success: true,
        client: 'claude-desktop',
        config_path: '/Users/test/Library/Application Support/Claude/claude_desktop_config.json',
        // no backup_path: connect created the file
        server_name: 'mcpproxy',
        action: 'added',
        message: 'MCPProxy registered in Claude Desktop as mcpproxy',
      },
    })

    const wrapper = await openClientsTab(pinia)

    const row = wrapper.find('[data-test="client-row-claude-desktop"]')
    expect(row.exists()).toBe(true)
    // Bridge clients are connectable even without an existing config file
    // (parity with ConnectModal's connectableClients gating).
    expect(row.text()).not.toContain('Not installed')
    const connectBtn = wrapper.find('[data-test="connect-claude-desktop"]')
    expect(connectBtn.exists()).toBe(true)

    await connectBtn.trigger('click')
    await flushPromises()

    const backup = wrapper.find('[data-test="client-backup-claude-desktop"]')
    expect(backup.exists()).toBe(true)
    expect(backup.text()).toContain('No prior config file existed, so no backup was needed.')
  })

  // A non-bridge client that is simply not installed must still NOT offer Connect.
  it('keeps Not installed (no Connect) for a non-bridge client without a config', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      data: [{ ...cursorClient(), exists: false }],
    })

    const wrapper = await openClientsTab(pinia)

    const row = wrapper.find('[data-test="client-row-cursor"]')
    expect(row.exists()).toBe(true)
    expect(row.text()).toContain('Not installed')
    expect(wrapper.find('[data-test="connect-cursor"]').exists()).toBe(false)
  })

  // Backup lines are session-scoped: reopening the wizard must not replay
  // backup rows from connects performed in a previous wizard session.
  it('clears backup rows when the wizard is closed and reopened', async () => {
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

    const wrapper = await openClientsTab(pinia)
    await wrapper.find('[data-test="connect-cursor"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="client-backup-cursor"]').exists()).toBe(true)

    // Close and reopen the wizard: a fresh session must start clean.
    await wrapper.setProps({ show: false })
    await flushPromises()
    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(wrapper.find('[data-test="client-backup-cursor"]').exists()).toBe(false)
  })
})
