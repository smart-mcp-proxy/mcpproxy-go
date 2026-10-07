import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ClientConnectList from '@/components/ClientConnectList.vue'
import api from '@/services/api'

// Follow-up to Spec 109-b: the Connect modal should show the home-shortened
// display_path everywhere it shows a config path, and surface the reload hint
// returned by a disconnect the same way it does after a connect.

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

const FULL = '/Users/test/.cursor/mcp.json'
const SHORT = '~/.cursor/mcp.json'

function connectedRow() {
  return {
    id: 'cursor',
    name: 'Cursor',
    config_path: FULL,
    display_path: SHORT,
    exists: true,
    connected: true,
    supported: true,
    icon: 'cursor',
  }
}

async function openModal(pinia: any) {
  const wrapper = mount(ClientConnectList, { props: { show: false }, global: { plugins: [pinia] } })
  await wrapper.setProps({ show: true })
  await flushPromises()
  return wrapper
}

describe('ConnectModal display_path and disconnect reload hint', () => {
  let pinia: any

  beforeEach(() => {
    pinia = createPinia()
    setActivePinia(pinia)
    for (const fn of Object.values(api) as any[]) fn.mockReset()
    ;(api.getOnboardingState as any).mockResolvedValue({ success: true, data: null })
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: [connectedRow()] })
  })

  it('shows display_path in the row and keeps the full path as its title', async () => {
    const wrapper = await openModal(pinia)
    const path = wrapper.find('[data-test="client-path-cursor"]')
    expect(path.text()).toBe(SHORT)
    expect(path.attributes('title')).toBe(FULL)
  })

  it('falls back to config_path when display_path is absent', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      data: [{ ...connectedRow(), display_path: undefined }],
    })
    const wrapper = await openModal(pinia)
    expect(wrapper.find('[data-test="client-path-cursor"]').text()).toBe(FULL)
  })

  it('shows display_path in the disconnect confirmation and the reload hint afterwards', async () => {
    ;(api.disconnectClient as any).mockResolvedValue({
      success: true,
      data: {
        success: true,
        client: 'cursor',
        config_path: FULL,
        display_path: SHORT,
        server_name: 'mcpproxy',
        action: 'removed',
        message: 'MCPProxy removed from Cursor',
        reload_hint: 'Reload the Cursor window to unload MCPProxy',
      },
    })

    const wrapper = await openModal(pinia)
    await wrapper.find('[data-test="connect-disconnect-cursor"]').trigger('click')
    await flushPromises()

    const confirmPath = wrapper.find('[data-test="connect-disconnect-path"]')
    expect(confirmPath.text()).toBe(SHORT)
    expect(confirmPath.attributes('title')).toBe(FULL)
    expect(wrapper.find('[data-test="connect-reload-hint"]').exists()).toBe(false)

    await wrapper.find('[data-test="connect-disconnect-confirm-button"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="connect-reload-hint"]').text()).toBe(
      'Reload the Cursor window to unload MCPProxy',
    )
    expect(wrapper.emitted('updated')).toHaveLength(1)
  })

  it('does not claim success when the disconnect left the credential live', async () => {
    ;(api.disconnectClient as any).mockResolvedValue({
      success: true,
      data: {
        success: true,
        client: 'cursor',
        config_path: FULL,
        server_name: 'mcpproxy',
        action: 'removed',
        message: 'MCPProxy removed from Cursor',
        credential_revoke_error: 'storage busy',
      },
    })
    const wrapper = await openModal(pinia)
    await wrapper.find('[data-test="connect-disconnect-cursor"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="connect-disconnect-confirm-button"]').trigger('click')
    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('storage busy')
    expect(text).toContain('mcpproxy client forget cursor')
  })

  it('emits a native-list refresh after a successful undo', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: [{ ...connectedRow(), connected: false }] })
    ;(api.getConnectPreview as any).mockResolvedValue({
      success: true,
      data: { client: 'cursor', config_path: FULL, entry_exists: false, entry_text: '{}', access_state: 'accessible' },
    })
    ;(api.connectClient as any).mockResolvedValue({
      success: true,
      data: { success: true, client: 'cursor', config_path: FULL, action: 'created', message: 'Connected' },
    })
    ;(api.undoConnectClient as any).mockResolvedValue({
      success: true,
      data: { success: true, client: 'cursor', config_path: FULL, action: 'deleted', message: 'Undone' },
    })

    const wrapper = await openModal(pinia)
    await wrapper.find('[data-test="connect-cursor"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="client-preview-confirm-cursor"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="connect-undo"]').trigger('click')
    await wrapper.find('[data-test="connect-undo-confirm"]').trigger('click')
    await flushPromises()

    expect(api.undoConnectClient).toHaveBeenCalledWith('cursor', 'mcpproxy', null)
    expect(wrapper.emitted('updated')).toHaveLength(2)
  })
})
