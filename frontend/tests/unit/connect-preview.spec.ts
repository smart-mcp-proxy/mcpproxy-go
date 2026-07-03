import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ConnectModal from '@/components/ConnectModal.vue'
import OnboardingWizard from '@/components/OnboardingWizard.vue'
import api from '@/services/api'

// Spec 078 US1: clicking Connect must first show the exact change (target file,
// entry contents, masked API key, create-vs-overwrite) and only write the file
// when the user confirms. Cancel writes nothing.

vi.mock('@/services/api', () => ({
  default: {
    getConnectStatus: vi.fn(),
    getConnectClientStatus: vi.fn(),
    getConnectPreview: vi.fn(),
    connectClient: vi.fn(),
    disconnectClient: vi.fn(),
    getOnboardingState: vi.fn(),
    // Wizard lifecycle deps:
    markOnboardingState: vi.fn(),
    getActivities: vi.fn(),
    getConfig: vi.fn(),
    getDockerStatus: vi.fn(),
    getCanonicalConfigPaths: vi.fn(),
  },
}))

function cursorStatus() {
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

function previewFor(overrides: Record<string, unknown> = {}) {
  return {
    success: true,
    data: {
      client: 'cursor',
      config_path: '/Users/test/.cursor/mcp.json',
      format: 'json',
      server_key: 'mcpServers',
      server_name: 'mcpproxy',
      entry: { type: 'sse', url: 'http://127.0.0.1:8080/mcp?apikey=••••' },
      entry_text:
        '{\n  "mcpproxy": {\n    "url": "http://127.0.0.1:8080/mcp?apikey=••••",\n    "type": "sse"\n  }\n}',
      entry_exists: false,
      contains_api_key: true,
      access_state: 'accessible',
      ...overrides,
    },
  }
}

function connectResult() {
  return {
    success: true,
    data: {
      success: true,
      client: 'cursor',
      config_path: '/Users/test/.cursor/mcp.json',
      backup_path: '/Users/test/.cursor/mcp.json.bak.20260702-101530',
      server_name: 'mcpproxy',
      action: 'updated',
      message: 'MCPProxy registered in Cursor as mcpproxy',
    },
  }
}

describe('ConnectModal preview (Spec 078 US1)', () => {
  let pinia: any

  beforeEach(() => {
    pinia = createPinia()
    setActivePinia(pinia)
    vi.clearAllMocks()
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: [cursorStatus()] })
    ;(api.getOnboardingState as any).mockResolvedValue({ success: true, data: null })
    ;(api.getConnectPreview as any).mockResolvedValue(previewFor())
    ;(api.connectClient as any).mockResolvedValue(connectResult())
  })

  async function open() {
    const wrapper = mount(ConnectModal, { props: { show: false }, global: { plugins: [pinia] } })
    await wrapper.setProps({ show: true })
    await flushPromises()
    return wrapper
  }

  it('shows the preview (path, masked entry, api-key note) before writing', async () => {
    const wrapper = await open()

    await wrapper.find('[data-test="connect-start-cursor"]').trigger('click')
    await flushPromises()

    // The write must NOT have happened yet — only the preview was fetched.
    expect(api.getConnectPreview).toHaveBeenCalledWith('cursor')
    expect(api.connectClient).not.toHaveBeenCalled()

    const panel = wrapper.find('[data-test="connect-preview-cursor"]')
    expect(panel.exists()).toBe(true)
    expect(panel.text()).toContain('/Users/test/.cursor/mcp.json')
    expect(panel.text()).toContain('Everything else in the file stays untouched')

    const entry = wrapper.find('[data-test="connect-preview-entry-cursor"]')
    expect(entry.exists()).toBe(true)
    // Masked key visible, real key absent.
    expect(entry.text()).toContain('••••')
    expect(entry.text()).not.toContain('apikey=real')
    expect(wrapper.find('[data-test="connect-preview-apikey-cursor"]').exists()).toBe(true)
  })

  it('confirm writes the file; cancel writes nothing', async () => {
    const wrapper = await open()

    // Cancel path first.
    await wrapper.find('[data-test="connect-start-cursor"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="connect-preview-cancel-cursor"]').trigger('click')
    await flushPromises()
    expect(api.connectClient).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="connect-preview-cursor"]').exists()).toBe(false)

    // Confirm path writes.
    await wrapper.find('[data-test="connect-start-cursor"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="connect-preview-confirm-cursor"]').trigger('click')
    await flushPromises()
    expect(api.connectClient).toHaveBeenCalledTimes(1)
    // A create (entry_exists=false) confirms with force=false.
    expect(api.connectClient).toHaveBeenCalledWith('cursor', 'mcpproxy', false)
  })

  it('shows no credential line when the entry is keyless (require_mcp_auth off)', async () => {
    // Spec 078 security fix: default (auth off) writes a clean, keyless entry.
    ;(api.getConnectPreview as any).mockResolvedValue(
      previewFor({
        contains_api_key: false,
        entry: { type: 'sse', url: 'http://127.0.0.1:8080/mcp' },
        entry_text: '{\n  "mcpproxy": {\n    "url": "http://127.0.0.1:8080/mcp",\n    "type": "sse"\n  }\n}',
      }),
    )
    const wrapper = await open()

    await wrapper.find('[data-test="connect-start-cursor"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="connect-preview-cursor"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="connect-preview-apikey-cursor"]').exists()).toBe(false)
    const entry = wrapper.find('[data-test="connect-preview-entry-cursor"]')
    expect(entry.text()).not.toContain('apikey')
    expect(entry.text()).not.toContain('••••')
  })

  it('an existing entry shows the overwrite warning and confirm implies force=true', async () => {
    ;(api.getConnectPreview as any).mockResolvedValue(previewFor({ entry_exists: true }))
    const wrapper = await open()

    await wrapper.find('[data-test="connect-start-cursor"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="connect-preview-overwrite-cursor"]').exists()).toBe(true)

    await wrapper.find('[data-test="connect-preview-confirm-cursor"]').trigger('click')
    await flushPromises()
    expect(api.connectClient).toHaveBeenCalledWith('cursor', 'mcpproxy', true)
  })

  it('malformed config: confirm is disabled and the copy does not promise a write', async () => {
    // The write parses the same bytes and would fail, so connecting is blocked
    // until the file is fixed — the preview must not claim it writes the entry.
    ;(api.getConnectPreview as any).mockResolvedValue(previewFor({ access_state: 'malformed' }))
    const wrapper = await open()

    await wrapper.find('[data-test="connect-start-cursor"]').trigger('click')
    await flushPromises()

    const warn = wrapper.find('[data-test="connect-preview-malformed-cursor"]')
    expect(warn.exists()).toBe(true)
    expect(warn.text()).toContain('connecting would fail')

    const confirm = wrapper.find('[data-test="connect-preview-confirm-cursor"]')
    expect(confirm.attributes('disabled')).toBeDefined()
    await confirm.trigger('click')
    await flushPromises()
    expect(api.connectClient).not.toHaveBeenCalled()
  })
})

describe('OnboardingWizard preview (Spec 078 US1)', () => {
  let pinia: any

  function onboardingState() {
    return {
      success: true,
      data: {
        has_connected_client: false,
        has_configured_server: true,
        connected_client_count: 0,
        connected_client_ids: [],
        configured_server_count: 1,
        state: { engaged: false },
        should_show_wizard: true,
        first_mcp_client_ever: false,
        mcp_clients_seen_ever: [],
        incomplete_tab_count: 0,
      },
    }
  }

  beforeEach(() => {
    pinia = createPinia()
    setActivePinia(pinia)
    vi.clearAllMocks()
    ;(api.getActivities as any).mockResolvedValue({ success: true, data: { activities: [] } })
    ;(api.getConfig as any).mockResolvedValue({ success: true, data: {} })
    ;(api.getDockerStatus as any).mockResolvedValue({ success: true, data: { available: false } })
    ;(api.getCanonicalConfigPaths as any).mockResolvedValue({ success: true, data: { paths: [] } })
    ;(api.getOnboardingState as any).mockResolvedValue(onboardingState())
    ;(api.markOnboardingState as any).mockResolvedValue(onboardingState())
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: [cursorStatus()] })
    ;(api.getConnectPreview as any).mockResolvedValue(previewFor())
    ;(api.connectClient as any).mockResolvedValue(connectResult())
  })

  async function openClientsTab() {
    const wrapper = mount(OnboardingWizard, { props: { show: false }, global: { plugins: [pinia] } })
    await wrapper.setProps({ show: true })
    await flushPromises()
    await wrapper.find('[data-test="tab-clients"]').trigger('click')
    await flushPromises()
    return wrapper
  }

  it('previews before writing; cancel writes nothing, confirm proceeds', async () => {
    const wrapper = await openClientsTab()

    await wrapper.find('[data-test="connect-cursor"]').trigger('click')
    await flushPromises()
    expect(api.getConnectPreview).toHaveBeenCalledWith('cursor')
    expect(api.connectClient).not.toHaveBeenCalled()

    const panel = wrapper.find('[data-test="client-preview-cursor"]')
    expect(panel.exists()).toBe(true)
    expect(panel.text()).toContain('Everything else in the file stays untouched')
    expect(wrapper.find('[data-test="client-preview-entry-cursor"]').text()).toContain('••••')

    // Cancel writes nothing and dismisses the panel.
    await wrapper.find('[data-test="client-preview-cancel-cursor"]').trigger('click')
    await flushPromises()
    expect(api.connectClient).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="client-preview-cursor"]').exists()).toBe(false)

    // Re-open and confirm.
    await wrapper.find('[data-test="connect-cursor"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="client-preview-confirm-cursor"]').trigger('click')
    await flushPromises()
    expect(api.connectClient).toHaveBeenCalledWith('cursor', 'mcpproxy', false)
  })

  it('overwrite case confirms with force=true', async () => {
    ;(api.getConnectPreview as any).mockResolvedValue(previewFor({ entry_exists: true }))
    const wrapper = await openClientsTab()

    await wrapper.find('[data-test="connect-cursor"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="client-preview-overwrite-cursor"]').exists()).toBe(true)

    await wrapper.find('[data-test="client-preview-confirm-cursor"]').trigger('click')
    await flushPromises()
    expect(api.connectClient).toHaveBeenCalledWith('cursor', 'mcpproxy', true)
  })
})
