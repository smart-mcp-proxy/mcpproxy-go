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

// Spec 078 US1: a generic accessible preview (create case). Individual tests
// override entry_exists / access_state as needed.
function previewOk(overrides: Record<string, unknown> = {}) {
  return {
    success: true,
    data: {
      client: 'cursor',
      config_path: '/Users/test/.cursor/mcp.json',
      format: 'json',
      server_key: 'mcpServers',
      server_name: 'mcpproxy',
      entry: { type: 'http', url: 'http://127.0.0.1:8080/mcp' },
      entry_text: '{\n  "mcpproxy": {\n    "type": "http",\n    "url": "http://127.0.0.1:8080/mcp"\n  }\n}',
      entry_exists: false,
      contains_api_key: false,
      access_state: 'accessible',
      ...overrides,
    },
  }
}

describe('ConnectModal', () => {
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
    // Default: no content-resolved connections (most tests exercise the
    // stat-only listing only). Individual tests override as needed.
    ;(api.getOnboardingState as any).mockResolvedValue({ success: true, data: null })
    // Spec 078 US1: Connect now previews first; default to an accessible create.
    ;(api.getConnectPreview as any).mockResolvedValue(previewOk())
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

  // Spec 075 (MCP-2833) US1: the stat-only listing reports access_state=unknown
  // for installed clients. The view must NOT eagerly read content, so an
  // installed+unknown client shows a neutral Connect affordance and an explicit
  // "Check access" action — never a denial banner.
  it('renders installed+unknown neutrally with a Check-access action and no denial banner', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      data: [{
        id: 'cursor',
        name: 'Cursor',
        config_path: '/Users/test/.cursor/mcp.json',
        exists: true,
        connected: false,
        supported: true,
        icon: 'cursor',
        access_state: 'unknown',
      }],
    })

    const wrapper = mount(ConnectModal, {
      props: { show: false },
      global: { plugins: [pinia] },
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    // Neutral: no denial banner for an unverified client.
    expect(wrapper.find('[data-test="connect-denied-banner"]').exists()).toBe(false)
    // Explicit, no-eager-read affordance to verify access on demand.
    expect(wrapper.find('[data-test="connect-check-access"]').exists()).toBe(true)
    // Connect remains offered.
    expect(wrapper.find('button.btn-primary.btn-xs').text()).toContain('Connect')
  })

  // Spec 075 US2: a permission-denied client surfaces a distinct, actionable
  // remediation banner naming the tccutil reset command — not "not connected".
  it('renders an actionable remediation banner for an access_state=denied client', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      data: [{
        id: 'claude-code',
        name: 'Claude Code',
        config_path: '/Users/test/.claude.json',
        exists: true,
        connected: false,
        supported: true,
        icon: 'claude-code',
        access_state: 'denied',
        remediation:
          "macOS blocked mcpproxy from reading Claude Code's configuration (Privacy & Security ▸ App Data).\n" +
          'Fix: System Settings ▸ Privacy & Security ▸ App Data ▸ enable mcpproxy,\n' +
          'or run: tccutil reset SystemPolicyAppData com.smartmcpproxy.mcpproxy\n' +
          '(dev builds: com.smartmcpproxy.mcpproxy.dev)',
      }],
    })

    const wrapper = mount(ConnectModal, {
      props: { show: false },
      global: { plugins: [pinia] },
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    const banner = wrapper.find('[data-test="connect-denied-banner"]')
    expect(banner.exists()).toBe(true)
    expect(banner.text()).toContain('tccutil reset SystemPolicyAppData com.smartmcpproxy.mcpproxy')
    expect(banner.text()).toContain('macOS blocked')
    // A denied client must not present a plain Connect button as if it were fine.
    expect(wrapper.find('[data-test="connect-blocked-badge"]').exists()).toBe(true)
    // The exact reset command is one-click copyable.
    expect(wrapper.find('[data-test="connect-copy-tccutil"]').exists()).toBe(true)
  })

  // Spec 075 FR-003: a malformed config is reported distinctly from a denial.
  it('renders a distinct malformed badge (not a denial banner) for access_state=malformed', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      data: [{
        id: 'cursor',
        name: 'Cursor',
        config_path: '/Users/test/.cursor/mcp.json',
        exists: true,
        connected: false,
        supported: true,
        icon: 'cursor',
        access_state: 'malformed',
      }],
    })

    const wrapper = mount(ConnectModal, {
      props: { show: false },
      global: { plugins: [pinia] },
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(wrapper.find('[data-test="connect-malformed-badge"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="connect-denied-banner"]').exists()).toBe(false)
  })

  // Spec 078 US2 / FR-006: a successful connect must surface the timestamped
  // backup path returned by the API — the strongest "you can undo this" signal.
  it('surfaces the timestamped backup path after a successful connect', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      data: [{
        id: 'cursor',
        name: 'Cursor',
        config_path: '/Users/test/.cursor/mcp.json',
        exists: true,
        connected: false,
        supported: true,
        icon: 'cursor',
      }],
    })
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

    const wrapper = mount(ConnectModal, {
      props: { show: false },
      global: { plugins: [pinia] },
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    // Spec 078 US1: click Connect → preview panel → confirm writes.
    await wrapper.find('[data-test="connect-start-cursor"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="connect-preview-confirm-cursor"]').trigger('click')
    await flushPromises()

    const backup = wrapper.find('[data-test="connect-backup-path"]')
    expect(backup.exists()).toBe(true)
    expect(backup.text()).toContain('A backup of your previous config was saved to')
    expect(backup.text()).toContain('/Users/test/.cursor/mcp.json.bak.20260702-101530')
    // The "no prior file" variant must not render simultaneously.
    expect(wrapper.find('[data-test="connect-no-backup"]').exists()).toBe(false)

    // One-click copy of the backup path (same affordance pattern as tccutil).
    const copyBtn = wrapper.find('[data-test="connect-copy-backup"]')
    expect(copyBtn.exists()).toBe(true)
    await copyBtn.trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith('/Users/test/.cursor/mcp.json.bak.20260702-101530')
  })

  // Spec 078 US2 acceptance 2: a connect that created a brand-new config file
  // has nothing to back up — say so explicitly, never show a blank path.
  it('states that there was no prior file to back up when connect created the config', async () => {
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
        icon: 'claude-desktop',
      }],
    })
    ;(api.connectClient as any).mockResolvedValue({
      success: true,
      data: {
        success: true,
        client: 'claude-desktop',
        config_path: '/Users/test/Library/Application Support/Claude/claude_desktop_config.json',
        // no backup_path: the file did not exist before the write
        server_name: 'mcpproxy',
        action: 'added',
        message: 'MCPProxy registered in Claude Desktop as mcpproxy',
      },
    })

    const wrapper = mount(ConnectModal, {
      props: { show: false },
      global: { plugins: [pinia] },
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    // Spec 078 US1: preview then confirm for the bridge (no-prior-file) case.
    await wrapper.find('[data-test="connect-start-claude-desktop"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="connect-preview-confirm-claude-desktop"]').trigger('click')
    await flushPromises()

    const noBackup = wrapper.find('[data-test="connect-no-backup"]')
    expect(noBackup.exists()).toBe(true)
    expect(noBackup.text()).toContain('No prior config file existed, so no backup was needed.')
    expect(wrapper.find('[data-test="connect-backup-path"]').exists()).toBe(false)
  })

  // Spec 078 US2: disconnect also writes a backup first — surface it too.
  it('surfaces the backup path after a successful disconnect', async () => {
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
      }],
    })
    ;(api.disconnectClient as any).mockResolvedValue({
      success: true,
      data: {
        success: true,
        client: 'opencode',
        config_path: '/Users/test/.config/opencode/opencode.json',
        backup_path: '/Users/test/.config/opencode/opencode.json.bak.20260702-110000',
        server_name: 'mcpproxy',
        action: 'removed',
        message: 'MCPProxy removed from OpenCode',
      },
    })

    const wrapper = mount(ConnectModal, {
      props: { show: false },
      global: { plugins: [pinia] },
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    await wrapper.find('button.btn-ghost.text-error').trigger('click')
    await flushPromises()

    const backup = wrapper.find('[data-test="connect-backup-path"]')
    expect(backup.exists()).toBe(true)
    expect(backup.text()).toContain('opencode.json.bak.20260702-110000')
  })

  // Spec 078 US2 / SC-005: Connect All must surface EVERY successful client's
  // backup outcome, not just the last one — 100% of successful connects that
  // modified an existing config display their backup path.
  it('Connect All surfaces a per-client backup line for every successful connect', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      data: [
        {
          id: 'cursor',
          name: 'Cursor',
          config_path: '/Users/test/.cursor/mcp.json',
          exists: true,
          connected: false,
          supported: true,
          icon: 'cursor',
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
        {
          // Bridge client with no config file: connect creates it → no backup.
          id: 'claude-desktop',
          name: 'Claude Desktop',
          config_path: '/Users/test/Library/Application Support/Claude/claude_desktop_config.json',
          exists: false,
          connected: false,
          supported: true,
          bridge: true,
          icon: 'claude-desktop',
        },
      ],
    })
    ;(api.connectClient as any).mockImplementation((id: string) => {
      const backups: Record<string, string | undefined> = {
        cursor: '/Users/test/.cursor/mcp.json.bak.20260702-101530',
        codex: '/Users/test/.codex/config.toml.bak.20260702-101531',
        'claude-desktop': undefined,
      }
      return Promise.resolve({
        success: true,
        data: {
          success: true,
          client: id,
          config_path: '',
          backup_path: backups[id],
          server_name: 'mcpproxy',
          action: 'added',
          message: `MCPProxy registered in ${id}`,
        },
      })
    })
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.assign(navigator, { clipboard: { writeText } })

    const wrapper = mount(ConnectModal, {
      props: { show: false },
      global: { plugins: [pinia] },
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    const connectAllBtn = wrapper
      .findAll('button')
      .find(b => b.text().includes('Connect All'))!
    expect(connectAllBtn).toBeTruthy()
    await connectAllBtn.trigger('click')
    await flushPromises()

    expect(api.connectClient).toHaveBeenCalledTimes(3)

    // Every modified-config client shows ITS backup path.
    const cursorRow = wrapper.find('[data-test="connect-bulk-backup-cursor"]')
    expect(cursorRow.exists()).toBe(true)
    expect(cursorRow.text()).toContain('/Users/test/.cursor/mcp.json.bak.20260702-101530')
    const codexRow = wrapper.find('[data-test="connect-bulk-backup-codex"]')
    expect(codexRow.exists()).toBe(true)
    expect(codexRow.text()).toContain('/Users/test/.codex/config.toml.bak.20260702-101531')
    // The no-prior-file client states its case explicitly.
    const desktopRow = wrapper.find('[data-test="connect-bulk-backup-claude-desktop"]')
    expect(desktopRow.exists()).toBe(true)
    expect(desktopRow.text()).toContain('No prior config file existed')

    // The single-result backup line must not duplicate the last client's path.
    expect(wrapper.find('[data-test="connect-backup-path"]').exists()).toBe(false)

    // Per-row copy copies THAT client's path.
    await wrapper.find('[data-test="connect-bulk-copy-cursor"]').trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith('/Users/test/.cursor/mcp.json.bak.20260702-101530')
  })

  // Spec 075 US1/US2: the explicit per-client "Check access" action reads one
  // client's config on demand (the only privacy-prompt-eligible call) and
  // resolves its access_state in-band, surfacing a denial without a full connect.
  it('resolves a denial via the explicit Check-access action (per-client GET)', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      data: [{
        id: 'codex',
        name: 'Codex CLI',
        config_path: '/Users/test/.codex/config.toml',
        exists: true,
        connected: false,
        supported: true,
        icon: 'codex',
        access_state: 'unknown',
      }],
    })
    ;(api.getConnectClientStatus as any).mockResolvedValue({
      success: true,
      data: {
        id: 'codex',
        name: 'Codex CLI',
        config_path: '/Users/test/.codex/config.toml',
        exists: true,
        connected: false,
        supported: true,
        icon: 'codex',
        access_state: 'denied',
        remediation: 'macOS blocked mcpproxy ... tccutil reset SystemPolicyAppData com.smartmcpproxy.mcpproxy',
      },
    })

    const wrapper = mount(ConnectModal, {
      props: { show: false },
      global: { plugins: [pinia] },
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    // Before checking: neutral, no banner.
    expect(wrapper.find('[data-test="connect-denied-banner"]').exists()).toBe(false)

    await wrapper.find('[data-test="connect-check-access"]').trigger('click')
    await flushPromises()

    expect(api.getConnectClientStatus).toHaveBeenCalledWith('codex')
    const banner = wrapper.find('[data-test="connect-denied-banner"]')
    expect(banner.exists()).toBe(true)
    expect(banner.text()).toContain('tccutil reset SystemPolicyAppData')
  })
})
