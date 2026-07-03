import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ConnectModal from '@/components/ConnectModal.vue'
import api from '@/services/api'

// Spec 078 US3: the standalone Connect modal offers a session-scoped one-click
// Undo on the post-connect result area, mirroring the wizard row. The revert
// panel shows the change to be reverted (FR-009) before anything runs.

vi.mock('@/services/api', () => ({
  default: {
    getConnectStatus: vi.fn(),
    getConnectClientStatus: vi.fn(),
    getConnectPreview: vi.fn(),
    connectClient: vi.fn(),
    disconnectClient: vi.fn(),
    undoConnectClient: vi.fn(),
    getOnboardingState: vi.fn(),
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

function cursorClient(connected = false) {
  return {
    id: 'cursor',
    name: 'Cursor',
    config_path: '/Users/test/.cursor/mcp.json',
    exists: true,
    connected,
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

async function openModal(pinia: any) {
  const wrapper = mount(ConnectModal, {
    props: { show: false },
    global: { plugins: [pinia] },
  })
  await wrapper.setProps({ show: true })
  await flushPromises()
  return wrapper
}

async function connectViaPreview(wrapper: any, clientId: string) {
  await wrapper.find(`[data-test="connect-start-${clientId}"]`).trigger('click')
  await flushPromises()
  await wrapper.find(`[data-test="connect-preview-confirm-${clientId}"]`).trigger('click')
  await flushPromises()
}

describe('ConnectModal one-click undo (Spec 078 US3)', () => {
  let pinia: any

  beforeEach(() => {
    pinia = createPinia()
    setActivePinia(pinia)
    vi.clearAllMocks()
    ;(api.getOnboardingState as any).mockResolvedValue({ success: true, data: null })
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: [cursorClient()] })
    ;(api.getConnectPreview as any).mockResolvedValue(previewOk())
    ;(api.connectClient as any).mockResolvedValue(connectOk(BACKUP))
  })

  it('offers Undo after a connect and reverts on the panel confirm', async () => {
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

    const wrapper = await openModal(pinia)
    expect(wrapper.find('[data-test="connect-undo"]').exists()).toBe(false)

    await connectViaPreview(wrapper, 'cursor')

    // Undo offer appears alongside the backup-path result.
    expect(wrapper.find('[data-test="connect-backup-path"]').exists()).toBe(true)
    const undoBtn = wrapper.find('[data-test="connect-undo"]')
    expect(undoBtn.exists()).toBe(true)

    // FR-009: the change to be reverted is shown BEFORE anything runs.
    await undoBtn.trigger('click')
    await flushPromises()
    expect(api.undoConnectClient).not.toHaveBeenCalled()
    const panel = wrapper.find('[data-test="connect-undo-panel"]')
    expect(panel.exists()).toBe(true)
    expect(panel.text()).toContain('will be reverted')
    expect(wrapper.find('[data-test="connect-undo-entry"]').text()).toContain('http://127.0.0.1:8080/mcp')

    await wrapper.find('[data-test="connect-undo-confirm"]').trigger('click')
    await flushPromises()

    expect(api.undoConnectClient).toHaveBeenCalledWith('cursor', 'mcpproxy', BACKUP)
    expect(wrapper.text()).toContain('Restored /Users/test/.cursor/mcp.json from backup')
    // The undo affordance is consumed: session offers it once per connect.
    expect(wrapper.find('[data-test="connect-undo"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="connect-undo-panel"]').exists()).toBe(false)
  })

  it('Keep closes the panel without calling undo', async () => {
    const wrapper = await openModal(pinia)
    await connectViaPreview(wrapper, 'cursor')

    await wrapper.find('[data-test="connect-undo"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="connect-undo-cancel"]').trigger('click')
    await flushPromises()

    expect(api.undoConnectClient).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="connect-undo-panel"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="connect-undo"]').exists()).toBe(true)
  })

  it('states the file removal for the no-prior-file case and passes null backup', async () => {
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

    const wrapper = await openModal(pinia)
    await connectViaPreview(wrapper, 'cursor')

    expect(wrapper.find('[data-test="connect-no-backup"]').exists()).toBe(true)
    await wrapper.find('[data-test="connect-undo"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="connect-undo-panel"]').text()).toContain('Undo removes')

    await wrapper.find('[data-test="connect-undo-confirm"]').trigger('click')
    await flushPromises()

    expect(api.undoConnectClient).toHaveBeenCalledWith('cursor', 'mcpproxy', null)
    expect(wrapper.text()).toContain('Removed /Users/test/.cursor/mcp.json')
  })

  it('surfaces a drift refusal (409) honestly without pretending it undid', async () => {
    ;(api.undoConnectClient as any).mockResolvedValue({
      success: false,
      error:
        '/Users/test/.cursor/mcp.json changed since mcpproxy connected; refusing to restore the backup over your edits. Use disconnect to remove just the mcpproxy entry.',
    })

    const wrapper = await openModal(pinia)
    await connectViaPreview(wrapper, 'cursor')

    await wrapper.find('[data-test="connect-undo"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="connect-undo-confirm"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('changed since mcpproxy connected')
  })

  it('does not carry the undo offer across modal sessions', async () => {
    const wrapper = await openModal(pinia)
    await connectViaPreview(wrapper, 'cursor')
    expect(wrapper.find('[data-test="connect-undo"]').exists()).toBe(true)

    await wrapper.setProps({ show: false })
    await flushPromises()
    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(wrapper.find('[data-test="connect-undo"]').exists()).toBe(false)
  })
})
