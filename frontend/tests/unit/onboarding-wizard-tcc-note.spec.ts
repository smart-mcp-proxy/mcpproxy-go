import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import OnboardingWizard from '@/components/OnboardingWizard.vue'
import api from '@/services/api'

// Spec 078 US4 / FR-011: on macOS, the wizard must explain the App-Data privacy
// prompt BEFORE the first read that can trigger it. The per-client preview read
// behind "Review & connect" is that first read, and it only prompts for configs
// under another app's protected data (~/Library/Application Support/…), so the
// forewarning is keyed off the client's config_path — inherently macOS-only and
// scoped to exactly the clients whose connect can fire the prompt.

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

// Claude Desktop: config lives under another app's protected data on macOS —
// the exact shape whose preview read fires the App-Data prompt.
function protectedClient(overrides: Record<string, unknown> = {}) {
  return {
    id: 'claude-desktop',
    name: 'Claude Desktop',
    config_path: '/Users/test/Library/Application Support/Claude/claude_desktop_config.json',
    exists: true,
    connected: false,
    supported: true,
    bridge: true,
    icon: 'claude-desktop',
    ...overrides,
  }
}

// Cursor: dot-directory config, no TCC protection — must never show the note
// (including on Linux/Windows daemons, where no path ever matches).
function unprotectedClient(overrides: Record<string, unknown> = {}) {
  return {
    id: 'cursor',
    name: 'Cursor',
    config_path: '/Users/test/.cursor/mcp.json',
    exists: true,
    connected: false,
    supported: true,
    icon: 'cursor',
    ...overrides,
  }
}

function previewOk(overrides: Record<string, unknown> = {}) {
  return {
    success: true,
    data: {
      client: 'claude-desktop',
      config_path: '/Users/test/Library/Application Support/Claude/claude_desktop_config.json',
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

describe('OnboardingWizard pre-emptive macOS TCC note (Spec 078 US4 / FR-011)', () => {
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
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      data: [protectedClient(), unprotectedClient()],
    })
    ;(api.getConnectPreview as any).mockResolvedValue(previewOk())
  })

  it('forewarns about the macOS prompt on a protected-path client before any action', async () => {
    const wrapper = await openClientsTab(pinia)

    const note = wrapper.find('[data-test="client-tcc-note-claude-desktop"]')
    expect(note.exists()).toBe(true)
    // Plain-language: names the OS prompt and tells the user what to choose.
    expect(note.text()).toContain('macOS may ask for permission')
    expect(note.text()).toContain('access data from other apps')
    expect(note.text()).toContain('Allow')
  })

  it('never shows the note for clients whose config is not under protected app data', async () => {
    const wrapper = await openClientsTab(pinia)

    expect(wrapper.find('[data-test="client-tcc-note-cursor"]').exists()).toBe(false)
  })

  it('does not show the note for an already-connected client', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      data: [protectedClient({ connected: true })],
    })

    const wrapper = await openClientsTab(pinia)

    expect(wrapper.find('[data-test="client-row-claude-desktop"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="client-tcc-note-claude-desktop"]').exists()).toBe(false)
  })

  it('drops the note once the preview is open (the protected read already succeeded)', async () => {
    const wrapper = await openClientsTab(pinia)

    expect(wrapper.find('[data-test="client-tcc-note-claude-desktop"]').exists()).toBe(true)

    await wrapper.find('[data-test="connect-claude-desktop"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="client-preview-claude-desktop"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="client-tcc-note-claude-desktop"]').exists()).toBe(false)
  })

  it('does not show the note where no connect is offered (unsupported / not installed)', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({
      success: true,
      data: [
        protectedClient({ id: 'vscode', name: 'VS Code', bridge: false, exists: false }),
        protectedClient({ id: 'other', name: 'Other', supported: false, reason: 'Not supported' }),
      ],
    })

    const wrapper = await openClientsTab(pinia)

    expect(wrapper.find('[data-test="client-tcc-note-vscode"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="client-tcc-note-other"]').exists()).toBe(false)
  })
})
