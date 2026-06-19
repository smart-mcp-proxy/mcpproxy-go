import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ConnectModal from '@/components/ConnectModal.vue'
import api from '@/services/api'

vi.mock('@/services/api', () => ({
  default: {
    getConnectStatus: vi.fn(),
    connectClient: vi.fn(),
    disconnectClient: vi.fn(),
    getOnboardingState: vi.fn(),
  },
}))

describe('ConnectModal', () => {
  let pinia: any

  beforeEach(() => {
    pinia = createPinia()
    setActivePinia(pinia)
    ;(api.getConnectStatus as any).mockReset()
    ;(api.connectClient as any).mockReset()
    ;(api.disconnectClient as any).mockReset()
    ;(api.getOnboardingState as any).mockReset()
    // Default: no content-resolved connections (most tests exercise the
    // stat-only listing only). Individual tests override as needed.
    ;(api.getOnboardingState as any).mockResolvedValue({ success: true, data: null })
  })

  it('renders an OpenCode row', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      data: [{
        id: 'opencode',
        name: 'OpenCode',
        config_path: '/Users/test/.config/opencode/opencode.json',
        exists: true,
        connected: false,
        supported: true,
        icon: 'opencode',
      }],
    })

    const wrapper = mount(ConnectModal, {
      props: { show: false },
      global: { plugins: [pinia] },
    })

    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(wrapper.text()).toContain('OpenCode')
    expect(wrapper.text()).toContain('/Users/test/.config/opencode/opencode.json')
  })

  it('renders emoji icons for OpenCode, Gemini, and Codex', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      data: [
        {
          id: 'opencode',
          name: 'OpenCode',
          config_path: '/Users/test/.config/opencode/opencode.json',
          exists: true,
          connected: false,
          supported: true,
          icon: 'opencode',
        },
        {
          id: 'gemini',
          name: 'Gemini CLI',
          config_path: '/Users/test/.gemini/settings.json',
          exists: true,
          connected: false,
          supported: true,
          icon: 'gemini',
        },
        {
          id: 'codex',
          name: 'Codex CLI',
          config_path: '/Users/test/.codex/config.toml',
          exists: true,
          connected: false,
          supported: true,
          icon: 'codex',
        },
      ],
    })

    const wrapper = mount(ConnectModal, {
      props: { show: false },
      global: { plugins: [pinia] },
    })

    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(wrapper.text()).toContain('⚡')
    expect(wrapper.text()).toContain('♊')
    expect(wrapper.text()).toContain('⌘')
  })

  it('renders a Connect button and bridge note for Claude Desktop', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      data: [{
        id: 'claude-desktop',
        name: 'Claude Desktop',
        config_path: '/Users/test/Library/Application Support/Claude/claude_desktop_config.json',
        exists: true,
        connected: false,
        supported: true,
        note: 'Connects via an mcp-remote stdio bridge (npx -y mcp-remote). Requires Node.js.',
        icon: 'claude-desktop',
      }],
    })

    const wrapper = mount(ConnectModal, {
      props: { show: false },
      global: { plugins: [pinia] },
    })

    await wrapper.setProps({ show: true })
    await flushPromises()

    // A real one-click Connect button must be offered (not greyed out).
    const connectButton = wrapper.find('button.btn-primary.btn-xs')
    expect(connectButton.exists()).toBe(true)
    expect(connectButton.text()).toContain('Connect')

    // The bridge note must be surfaced to the user.
    expect(wrapper.text()).toContain('mcp-remote stdio bridge')
  })

  it('shows Connect for a bridge client even when its config file does not exist', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      data: [{
        id: 'claude-desktop',
        name: 'Claude Desktop',
        config_path: '/Users/test/Library/Application Support/Claude/claude_desktop_config.json',
        exists: false,
        connected: false,
        supported: true,
        bridge: true,
        note: 'Connects via an mcp-remote stdio bridge (npx -y mcp-remote). Requires Node.js.',
        icon: 'claude-desktop',
      }],
    })

    const wrapper = mount(ConnectModal, {
      props: { show: false },
      global: { plugins: [pinia] },
    })

    await wrapper.setProps({ show: true })
    await flushPromises()

    // Fresh install: no config file yet, but the bridge Connect must still appear.
    const connectButton = wrapper.find('button.btn-primary.btn-xs')
    expect(connectButton.exists()).toBe(true)
    expect(connectButton.text()).toContain('Connect')
    expect(wrapper.text()).not.toContain('Config not found')
  })

  it('disconnect uses server_name alias when OpenCode status is adopted', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      data: [{
        id: 'opencode',
        name: 'OpenCode',
        config_path: '/Users/test/.config/opencode/opencode.json',
        exists: true,
        connected: true,
        supported: true,
        icon: 'opencode',
        server_name: 'proxy-alt',
      }],
    })
    const disconnectSpy = vi.spyOn(api, 'disconnectClient').mockResolvedValue({
      success: true,
      data: {
        success: true,
        client: 'opencode',
        config_path: '',
        server_name: 'proxy-alt',
        action: 'removed',
        message: 'ok',
      },
    } as any)

    const wrapper = mount(ConnectModal, {
      props: { show: false },
      global: { plugins: [pinia] },
    })

    await wrapper.setProps({ show: true })
    await flushPromises()

    const button = wrapper.find('button.btn-ghost')
    expect(button.exists()).toBe(true)
    await button.trigger('click')
    await flushPromises()

    expect(disconnectSpy).toHaveBeenCalledWith('opencode', 'proxy-alt')
  })

  // MCP-2952: GetAllStatus() is stat-only (#706) and always reports
  // connected=false. The wizard/modal must merge the content-resolved
  // connected_client_ids from the onboarding state so already-connected
  // clients render Disconnect, not a fresh Connect button.
  it('renders Disconnect for a client present in connected_client_ids despite connected=false', async () => {
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
    ;(api.getOnboardingState as any).mockResolvedValue({
      success: true,
      data: {
        has_connected_client: true,
        has_configured_server: false,
        connected_client_count: 1,
        connected_client_ids: ['codex'],
        configured_server_count: 0,
        state: { engaged: false },
        should_show_wizard: true,
        first_mcp_client_ever: false,
        mcp_clients_seen_ever: [],
        incomplete_tab_count: 0,
      },
    })

    const wrapper = mount(ConnectModal, {
      props: { show: false },
      global: { plugins: [pinia] },
    })

    await wrapper.setProps({ show: true })
    await flushPromises()

    // codex resolved as connected -> Disconnect button.
    const codexRow = wrapper.find('button.btn-ghost.text-error')
    expect(codexRow.exists()).toBe(true)
    expect(codexRow.text()).toContain('Disconnect')

    // cursor is genuinely not connected -> Connect button still offered.
    const connectButtons = wrapper.findAll('button.btn-primary.btn-xs')
    expect(connectButtons.length).toBe(1)
    expect(connectButtons[0].text()).toContain('Connect')
  })
})
