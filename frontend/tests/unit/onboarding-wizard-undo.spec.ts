import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import OnboardingWizard from '@/components/OnboardingWizard.vue'
import api from '@/services/api'

// Spec 078 US3: after a connect performed in this wizard session, the client
// row offers a one-click Undo next to the backup line. Clicking it first shows
// the change to be reverted (FR-009); confirming restores the pre-connect file
// and returns the row to its not-connected state.

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
    undoConnectClient: vi.fn(),
  },
}))

const BACKUP = '/Users/test/.cursor/mcp.json.bak.20260703-101530'

function previewOk(overrides: Record<string, unknown> = {}) {
  return {
    success: true,
    data: {
      client: 'cursor',
      config_path: '/Users/test/.cursor/mcp.json',
      format: 'json',
      server_key: 'mcpServers',
      server_name: 'mcpproxy',
      entry: { type: 'sse', url: 'http://127.0.0.1:8080/mcp' },
      entry_text: '{\n  "mcpproxy": {\n    "url": "http://127.0.0.1:8080/mcp",\n    "type": "sse"\n  }\n}',
      entry_exists: false,
      contains_api_key: false,
      access_state: 'accessible',
      ...overrides,
    },
  }
}

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

function connectOk(backupPath?: string) {
  return {
    success: true,
    data: {
      success: true,
      client: 'cursor',
      config_path: '/Users/test/.cursor/mcp.json',
      ...(backupPath ? { backup_path: backupPath } : {}),
      server_name: 'mcpproxy',
      action: 'created',
      message: 'MCPProxy registered in Cursor as mcpproxy',
    },
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

async function connectViaPreview(wrapper: any, clientId: string) {
  await wrapper.find(`[data-test="connect-${clientId}"]`).trigger('click')
  await flushPromises()
  await wrapper.find(`[data-test="client-preview-confirm-${clientId}"]`).trigger('click')
  await flushPromises()
}

describe('OnboardingWizard one-click undo (Spec 078 US3)', () => {
  let pinia: any

  beforeEach(() => {
    pinia = createPinia()
    setActivePinia(pinia)
    vi.clearAllMocks()
    ;(api.getActivities as any).mockResolvedValue({ success: true, data: { activities: [] } })
    ;(api.getConfig as any).mockResolvedValue({ success: true, data: {} })
    ;(api.getDockerStatus as any).mockResolvedValue({ success: true, data: { available: false } })
    ;(api.getCanonicalConfigPaths as any).mockResolvedValue({ success: true, data: { paths: [] } })
    ;(api.getOnboardingState as any).mockResolvedValue(onboardingState([]))
    ;(api.markOnboardingState as any).mockResolvedValue(onboardingState([]))
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: [cursorClient()] })
    ;(api.getConnectPreview as any).mockResolvedValue(previewOk())
    ;(api.connectClient as any).mockResolvedValue(connectOk(BACKUP))
  })

  it('offers Undo on the row after a connect, shows the revert panel, and restores on confirm', async () => {
    ;(api.undoConnectClient as any).mockResolvedValue({
      success: true,
      data: {
        success: true,
        client: 'cursor',
        config_path: '/Users/test/.cursor/mcp.json',
        backup_path: '/Users/test/.cursor/mcp.json.bak.20260703-101545',
        server_name: 'mcpproxy',
        action: 'restored',
        message: `Restored /Users/test/.cursor/mcp.json from backup ${BACKUP}`,
      },
    })

    const wrapper = await openClientsTab(pinia)

    // No undo affordance before any connect in this session.
    expect(wrapper.find('[data-test="client-undo-cursor"]').exists()).toBe(false)

    await connectViaPreview(wrapper, 'cursor')

    const undoBtn = wrapper.find('[data-test="client-undo-cursor"]')
    expect(undoBtn.exists()).toBe(true)

    // FR-009: undo first shows the change to be reverted — nothing is called yet.
    await undoBtn.trigger('click')
    await flushPromises()
    expect(api.undoConnectClient).not.toHaveBeenCalled()
    const panel = wrapper.find('[data-test="client-undo-panel-cursor"]')
    expect(panel.exists()).toBe(true)
    expect(panel.text()).toContain('will be reverted')
    expect(wrapper.find('[data-test="client-undo-entry-cursor"]').text()).toContain('http://127.0.0.1:8080/mcp')

    await wrapper.find('[data-test="client-undo-confirm-cursor"]').trigger('click')
    await flushPromises()

    // The undo passes the exact backup path this session's connect returned.
    expect(api.undoConnectClient).toHaveBeenCalledWith('cursor', 'mcpproxy', BACKUP)
    // Row returns to its pre-connect state: backup line + panel gone, honest result shown.
    expect(wrapper.find('[data-test="client-backup-cursor"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="client-undo-panel-cursor"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('Restored /Users/test/.cursor/mcp.json from backup')
    // Review & connect is available again.
    expect(wrapper.find('[data-test="connect-cursor"]').exists()).toBe(true)
  })

  it('Keep dismisses the revert panel without calling undo', async () => {
    const wrapper = await openClientsTab(pinia)
    await connectViaPreview(wrapper, 'cursor')

    await wrapper.find('[data-test="client-undo-cursor"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="client-undo-cancel-cursor"]').trigger('click')
    await flushPromises()

    expect(api.undoConnectClient).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="client-undo-panel-cursor"]').exists()).toBe(false)
    // The backup line (and thus the Undo affordance) is still there.
    expect(wrapper.find('[data-test="client-backup-cursor"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="client-undo-cursor"]').exists()).toBe(true)
  })

  it('passes null backup for the no-prior-file case and states the file removal honestly', async () => {
    ;(api.connectClient as any).mockResolvedValue(connectOk(undefined))
    ;(api.undoConnectClient as any).mockResolvedValue({
      success: true,
      data: {
        success: true,
        client: 'cursor',
        config_path: '/Users/test/.cursor/mcp.json',
        backup_path: '/Users/test/.cursor/mcp.json.bak.20260703-101545',
        server_name: 'mcpproxy',
        action: 'deleted',
        message: 'Removed /Users/test/.cursor/mcp.json — it did not exist before mcpproxy connected',
      },
    })

    const wrapper = await openClientsTab(pinia)
    await connectViaPreview(wrapper, 'cursor')

    await wrapper.find('[data-test="client-undo-cursor"]').trigger('click')
    await flushPromises()
    const panel = wrapper.find('[data-test="client-undo-panel-cursor"]')
    expect(panel.text()).toContain('Undo removes')

    await wrapper.find('[data-test="client-undo-confirm-cursor"]').trigger('click')
    await flushPromises()

    expect(api.undoConnectClient).toHaveBeenCalledWith('cursor', 'mcpproxy', null)
    expect(wrapper.text()).toContain('Removed /Users/test/.cursor/mcp.json')
  })

  it('surfaces a drift refusal honestly and keeps the row state', async () => {
    ;(api.undoConnectClient as any).mockResolvedValue({
      success: false,
      error:
        '/Users/test/.cursor/mcp.json changed since mcpproxy connected; refusing to restore the backup over your edits. Use disconnect to remove just the mcpproxy entry.',
    })

    const wrapper = await openClientsTab(pinia)
    await connectViaPreview(wrapper, 'cursor')

    await wrapper.find('[data-test="client-undo-cursor"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="client-undo-confirm-cursor"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('changed since mcpproxy connected')
    // The refusal does not pretend the undo happened: the backup line stays.
    expect(wrapper.find('[data-test="client-backup-cursor"]').exists()).toBe(true)
  })

  it('does not replay undo state in a new wizard session', async () => {
    const wrapper = await openClientsTab(pinia)
    await connectViaPreview(wrapper, 'cursor')
    expect(wrapper.find('[data-test="client-undo-cursor"]').exists()).toBe(true)

    await wrapper.setProps({ show: false })
    await flushPromises()
    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(wrapper.find('[data-test="client-undo-cursor"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="client-undo-panel-cursor"]').exists()).toBe(false)
  })
})
