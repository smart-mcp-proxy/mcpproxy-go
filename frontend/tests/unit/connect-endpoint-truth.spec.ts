import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ConnectModal from '@/components/ConnectModal.vue'
import api from '@/services/api'

vi.mock('@/services/api', () => ({
  default: {
    getConnectStatus: vi.fn(),
    getConnectClientStatus: vi.fn(),
    getConnectPreview: vi.fn(),
    connectClient: vi.fn(),
    disconnectClient: vi.fn(),
    getOnboardingState: vi.fn(),
  },
}))

function row(overrides: Record<string, unknown> = {}) {
  return {
    id: 'cursor',
    name: 'Cursor',
    config_path: '/Users/test/.cursor/mcp.json',
    exists: true,
    connected: false,
    supported: true,
    icon: 'cursor',
    proxy_url: 'http://127.0.0.1:8080/mcp',
    ...overrides,
  }
}

async function openModal(pinia: any) {
  const wrapper = mount(ConnectModal, { props: { show: false }, global: { plugins: [pinia] } })
  await wrapper.setProps({ show: true })
  await flushPromises()
  return wrapper
}

/**
 * Audit F18: the modal used to call a row "connected" whenever the client's
 * config held an entry NAMED mcpproxy — including one pointing at a different
 * instance on a different port — and offered three different interaction
 * patterns across rows with no confirmation on the destructive one.
 */
describe('ConnectModal — honest connected state (audit F18)', () => {
  let pinia: any

  beforeEach(() => {
    pinia = createPinia()
    setActivePinia(pinia)
    ;(api.getConnectStatus as any).mockReset()
    ;(api.getConnectClientStatus as any).mockReset()
    ;(api.getConnectPreview as any).mockReset()
    ;(api.connectClient as any).mockReset()
    ;(api.disconnectClient as any).mockReset()
    ;(api.getOnboardingState as any).mockReset()
    ;(api.getOnboardingState as any).mockResolvedValue({ success: true, data: null })
  })

  it('shows the registered endpoint on a row connected to this instance', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      data: [row({
        connected: true,
        registered_url: 'http://127.0.0.1:8080/mcp',
        endpoint_match: 'this',
        access_state: 'accessible',
      })],
    })

    const wrapper = await openModal(pinia)

    const endpoint = wrapper.find('[data-test="connect-endpoint-cursor"]')
    expect(endpoint.exists()).toBe(true)
    expect(endpoint.text()).toContain('http://127.0.0.1:8080/mcp')
    expect(wrapper.find('[data-test="connect-other-instance-cursor"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="connect-disconnect-cursor"]').exists()).toBe(true)
  })

  it('marks a row registered to a different instance instead of claiming it', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      data: [row({
        connected: true,
        registered_url: 'http://127.0.0.1:18412/mcp',
        endpoint_match: 'other',
        access_state: 'accessible',
      })],
    })

    const wrapper = await openModal(pinia)

    expect(wrapper.find('[data-test="connect-other-instance-cursor"]').text())
      .toContain('Connected to another instance')
    expect(wrapper.find('[data-test="connect-endpoint-cursor"]').text())
      .toContain('http://127.0.0.1:18412/mcp')
    // The offered action repoints it here — not a Disconnect of someone else's
    // instance, and not a no-op.
    expect(wrapper.find('[data-test="connect-repoint-cursor"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="connect-disconnect-cursor"]').exists()).toBe(false)
  })

  it('offers Verify endpoint on a row whose connected flag came from the merge, not a read', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      // The stat-only listing never sets connected/endpoint_match.
      data: [row()],
    })
    ;(api.getOnboardingState as any).mockResolvedValue({
      success: true,
      data: {
        has_connected_client: true,
        has_configured_server: false,
        connected_client_count: 1,
        connected_client_ids: ['cursor'],
        configured_server_count: 0,
        state: { engaged: false },
        should_show_wizard: false,
        first_mcp_client_ever: false,
        mcp_clients_seen_ever: [],
        incomplete_tab_count: 0,
      },
    })

    const wrapper = await openModal(pinia)

    const check = wrapper.find('[data-test="connect-check-access"]')
    expect(check.exists()).toBe(true)
    expect(check.text()).toContain('Verify endpoint')
    // Nothing was read, so no endpoint may be asserted.
    expect(wrapper.find('[data-test="connect-endpoint-cursor"]').exists()).toBe(false)
  })

  it('confirms before disconnecting, and cancelling writes nothing', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      data: [row({
        connected: true,
        registered_url: 'http://127.0.0.1:8080/mcp',
        endpoint_match: 'this',
        access_state: 'accessible',
      })],
    })

    const wrapper = await openModal(pinia)

    await wrapper.find('[data-test="connect-disconnect-cursor"]').trigger('click')
    await flushPromises()

    const confirm = wrapper.find('[data-test="connect-disconnect-confirm"]')
    expect(confirm.exists()).toBe(true)
    // The backup notice sits in the dialog, before the button (feedback:
    // preview-then-act).
    expect(confirm.text()).toContain('backup')
    expect(api.disconnectClient).not.toHaveBeenCalled()

    await wrapper.find('[data-test="connect-disconnect-cancel"]').trigger('click')
    await flushPromises()

    expect(api.disconnectClient).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="connect-disconnect-confirm"]').exists()).toBe(false)
  })

  it('Connect All names the number of clients it would change', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      data: [
        row(),
        row({ id: 'codex', name: 'Codex CLI', config_path: '/Users/test/.codex/config.toml' }),
      ],
    })

    const wrapper = await openModal(pinia)

    expect(wrapper.find('[data-test="connect-all"]').text()).toContain('Connect 2 clients')
    expect(wrapper.find('[data-test="connect-all-hint"]').exists()).toBe(false)
  })

  it('Connect All explains itself when there is nothing to connect', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      data: [row({ connected: true, endpoint_match: 'this', registered_url: 'http://127.0.0.1:8080/mcp' })],
    })

    const wrapper = await openModal(pinia)

    const button = wrapper.find('[data-test="connect-all"]')
    expect(button.attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-test="connect-all-hint"]').text())
      .toContain('already connected')
  })

  it('names the paths it searched on a client with no config', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      data: [row({
        id: 'opencode',
        name: 'OpenCode',
        exists: false,
        config_path: '/Users/test/.config/opencode/opencode.json',
        checked_paths: [
          '/Users/test/.config/opencode/opencode.jsonc',
          '/Users/test/.config/opencode/opencode.json',
        ],
      })],
    })

    const wrapper = await openModal(pinia)

    const badge = wrapper.find('[data-test="connect-not-found-opencode"]')
    expect(badge.exists()).toBe(true)
    expect(badge.attributes('title')).toContain('opencode.jsonc')
    expect(badge.attributes('title')).toContain('opencode.json')
  })
})
