import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import ClientConnectList from '@/components/ClientConnectList.vue'
import api from '@/services/api'
import { useClientsStore } from '@/stores/clients'
import { GUARD_REFUSAL, WORK_FULL, WORK_RO, apiError, makeClient } from './fixtures/profiles108i'

// Spec 108-i T042w: the connect dialog's profile and mode fields, the masked
// client credential, and the D5 management notice.

vi.mock('@/services/api', () => ({
  default: {
    hasAPIKey: vi.fn(() => true),
    getConnectStatus: vi.fn(),
    getConnectClientStatus: vi.fn(),
    getConnectPreview: vi.fn(),
    connectClient: vi.fn(),
    getOnboardingState: vi.fn(),
    getConfig: vi.fn(),
    getProfiles: vi.fn(),
    getClients: vi.fn(),
  },
}))

const NOTICE = 'This client can no longer add, change or restart servers; manage servers from the Web UI, the macOS app or the CLI'
const stub = { template: '<div />' }

function cursorStatus() {
  return { id: 'cursor', name: 'Cursor', config_path: '/Users/test/.cursor/mcp.json', display_path: '~/.cursor/mcp.json', exists: true, connected: false, supported: true, icon: 'cursor' }
}

function preview(overrides: Record<string, unknown> = {}) {
  return {
    success: true,
    data: {
      client: 'cursor', config_path: '/Users/test/.cursor/mcp.json', display_path: '~/.cursor/mcp.json', format: 'json', server_key: 'mcpServers', server_name: 'mcpproxy',
      entry: {}, entry_text: '{"mcpproxy":{"headers":{"X-API-Key":"mcp_cli_••••"}}}', entry_exists: false, contains_api_key: false,
      credential: 'mcp_cli_••••', profile: '', mode: 'switchable', precondition_token: 'pre-1', access_state: 'accessible', ...overrides,
    },
  }
}

async function open(props: Record<string, unknown> = {}) {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: stub }, { path: '/settings', name: 'settings', component: stub }] })
  await router.push('/')
  await router.isReady()
  const wrapper = mount(ClientConnectList, { props: { show: false, ...props }, global: { plugins: [router] } })
  await wrapper.setProps({ show: true })
  await flushPromises()
  return { wrapper, router }
}

async function startCursor(wrapper: ReturnType<typeof mount>) {
  await wrapper.get('[data-test="connect-cursor"]').trigger('click')
  await flushPromises()
}

describe('ClientConnectList profile and mode fields (Spec 108-i T042w)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: [cursorStatus()] })
    ;(api.getOnboardingState as any).mockResolvedValue({ success: true, data: null })
    ;(api.getConfig as any).mockResolvedValue({ success: true, data: { config: { require_mcp_auth: true } } })
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [WORK_RO, WORK_FULL] })
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [], warnings: [] } })
    ;(api.getConnectPreview as any).mockResolvedValue(preview())
    ;(api.connectClient as any).mockResolvedValue({ success: true, data: { success: true, client: 'cursor', config_path: '/Users/test/.cursor/mcp.json', server_name: 'mcpproxy', action: 'created', message: 'ok', reload_hint: 'Restart Cursor' } })
  })

  it('defaults the profile to All servers with the lock switch off and disabled', async () => {
    const { wrapper } = await open()
    await startCursor(wrapper)
    const select = wrapper.get('[data-test="connect-profile-select-cursor"]')
    expect((select.element as HTMLSelectElement).value).toBe('')
    expect(select.findAll('option').map(option => option.text())).toEqual(['All servers', 'Work (work-ro)', 'Work Full (work-full)'])
    const lock = wrapper.get('[data-test="connect-lock-switch-cursor"]')
    expect(lock.attributes('disabled')).toBeDefined()
    expect((lock.element as HTMLInputElement).checked).toBe(false)
  })

  it('choosing a profile turns the lock on and refetches the preview with profile and mode', async () => {
    const { wrapper } = await open()
    await startCursor(wrapper)
    expect(api.getConnectPreview).toHaveBeenCalledWith('cursor')
    ;(api.getConnectPreview as any).mockResolvedValue(preview({ profile: 'work-ro', mode: 'locked' }))
    await wrapper.get('[data-test="connect-profile-select-cursor"]').setValue('work-ro')
    await flushPromises()
    expect(api.getConnectPreview).toHaveBeenLastCalledWith('cursor', { profile: 'work-ro', mode: 'locked' })
    const lock = wrapper.get('[data-test="connect-lock-switch-cursor"]')
    expect((lock.element as HTMLInputElement).checked).toBe(true)
    expect(lock.attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-test="connect-binding-summary-cursor"]').text()).toBe('Profile: Work · Mode: locked')

    await lock.setValue(false)
    await flushPromises()
    expect(api.getConnectPreview).toHaveBeenLastCalledWith('cursor', { profile: 'work-ro', mode: 'switchable' })
  })

  it('shows the masked client credential and no API-key notice', async () => {
    const { wrapper } = await open()
    await startCursor(wrapper)
    expect(wrapper.get('[data-test="connect-credential-cursor"]').text()).toBe('Credential: mcp_cli_•••• (client credential)')
    expect(wrapper.get('[data-test="connect-credential-notice-cursor"]').text()).toBe('MCPProxy writes a client credential for Cursor. The admin API key is never written.')
    expect(wrapper.find('[data-test="client-preview-apikey-cursor"]').exists()).toBe(false)
  })

  it('shows the D5 notice for All servers and for a profile without management tools, not with them', async () => {
    const { wrapper } = await open()
    await startCursor(wrapper)
    expect(wrapper.get('[data-test="connect-mgmt-notice-cursor"]').text()).toBe(NOTICE)
    await wrapper.get('[data-test="connect-profile-select-cursor"]').setValue('work-ro')
    await flushPromises()
    expect(wrapper.get('[data-test="connect-mgmt-notice-cursor"]').text()).toBe(NOTICE)
    await wrapper.get('[data-test="connect-profile-select-cursor"]').setValue('work-full')
    await flushPromises()
    expect(wrapper.find('[data-test="connect-mgmt-notice-cursor"]').exists()).toBe(false)
  })

  it('confirm sends {server_name, force, profile, mode, precondition_token}', async () => {
    const { wrapper } = await open()
    await startCursor(wrapper)
    ;(api.getConnectPreview as any).mockResolvedValue(preview({ profile: 'work-ro', mode: 'locked', precondition_token: 'pre-2' }))
    await wrapper.get('[data-test="connect-profile-select-cursor"]').setValue('work-ro')
    await flushPromises()
    await wrapper.get('[data-test="client-preview-confirm-cursor"]').trigger('click')
    await flushPromises()
    expect(api.connectClient).toHaveBeenCalledWith('cursor', 'mcpproxy', false, { profile: 'work-ro', mode: 'locked', precondition_token: 'pre-2' })
  })

  it('an untouched dialog keeps a reconnect\'s binding: the body carries no profile or mode', async () => {
    const { wrapper } = await open()
    await startCursor(wrapper)
    await wrapper.get('[data-test="client-preview-confirm-cursor"]').trigger('click')
    await flushPromises()
    // Only the preview's precondition token rides along.
    expect(api.connectClient).toHaveBeenCalledWith('cursor', 'mcpproxy', false, { precondition_token: 'pre-1' })
  })

  it('starts from the current binding of a client that already holds a client credential', async () => {
    useClientsStore().clients = [makeClient('cursor', { profile: 'work-ro', profile_mode: 'locked', profile_source: 'pin' })] as any
    const { wrapper } = await open()
    await startCursor(wrapper)
    expect((wrapper.get('[data-test="connect-profile-select-cursor"]').element as HTMLSelectElement).value).toBe('work-ro')
    expect((wrapper.get('[data-test="connect-lock-switch-cursor"]').element as HTMLInputElement).checked).toBe(true)
  })

  it('offers keyless only with require_mcp_auth off, under Advanced, and it hides the profile fields', async () => {
    const { wrapper: authOn } = await open()
    await startCursor(authOn)
    expect(authOn.find('[data-test="connect-advanced-cursor"]').exists()).toBe(false)

    ;(api.getConfig as any).mockResolvedValue({ success: true, data: { config: { require_mcp_auth: false } } })
    const { wrapper } = await open()
    await startCursor(wrapper)
    const advanced = wrapper.get('[data-test="connect-advanced-cursor"]')
    expect(advanced.get('summary').text()).toBe('Advanced')
    ;(api.getConnectPreview as any).mockResolvedValue(preview({ keyless: true, credential: '' }))
    await advanced.get('[data-test="connect-keyless-cursor"]').setValue(true)
    await flushPromises()
    expect(advanced.text()).toContain('Connect without a credential (unidentified client)')
    expect(api.getConnectPreview).toHaveBeenLastCalledWith('cursor', { keyless: true })
    expect(wrapper.find('[data-test="connect-profile-select-cursor"]').exists()).toBe(false)
    await wrapper.get('[data-test="client-preview-confirm-cursor"]').trigger('click')
    await flushPromises()
    expect(api.connectClient).toHaveBeenCalledWith('cursor', 'mcpproxy', false, { keyless: true, precondition_token: 'pre-1' })
  })

  it('shows a 409 guard refusal with its fixes and keeps the panel open', async () => {
    ;(api.connectClient as any).mockRejectedValue(apiError(GUARD_REFUSAL.error, GUARD_REFUSAL))
    const { wrapper } = await open()
    await startCursor(wrapper)
    await wrapper.get('[data-test="connect-profile-select-cursor"]').setValue('work-ro')
    await flushPromises()
    await wrapper.get('[data-test="client-preview-confirm-cursor"]').trigger('click')
    await flushPromises()
    const panel = wrapper.get('[data-test="client-preview-cursor"]')
    expect(panel.get('[data-test="guard-refusal"]').text()).toContain('reachable without authentication')
    expect(panel.findAll('[data-test^="guard-fix-"]')).toHaveLength(2)
  })

  it('shows the remediation of a token-name conflict', async () => {
    ;(api.connectClient as any).mockRejectedValue(apiError('token name client-cursor is held by a regular agent token', { status: 409, conflicting_token: 'client-cursor' }))
    const { wrapper } = await open()
    await startCursor(wrapper)
    await wrapper.get('[data-test="client-preview-confirm-cursor"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="connect-conflict-cursor"]').text()).toContain('Revoke or delete token client-cursor')
  })

  it('the bulk preview uses one profile and mode for every selected client', async () => {
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: [cursorStatus(), { ...cursorStatus(), id: 'codex', name: 'Codex' }] })
    const { wrapper } = await open()
    await wrapper.get('[data-test="connect-all"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="connect-bulk-profile-select"]').setValue('work-full')
    await flushPromises()
    expect(api.getConnectPreview).toHaveBeenLastCalledWith('codex', { profile: 'work-full', mode: 'locked' })
    expect(wrapper.find('[data-test="connect-bulk-mgmt-notice"]').exists()).toBe(false)
    await wrapper.get('[data-test="connect-bulk-preview-confirm"]').trigger('click')
    await flushPromises()
    const calls = (api.connectClient as any).mock.calls
    expect(calls.map((call: any[]) => call[0]).sort()).toEqual(['codex', 'cursor'])
    for (const call of calls) expect(call[3]).toMatchObject({ profile: 'work-full', mode: 'locked' })
  })

  it('keeps Spec 109 display_path and reload_hint rendering', async () => {
    const { wrapper } = await open()
    expect(wrapper.get('[data-test="client-path-cursor"]').text()).toBe('~/.cursor/mcp.json')
    await startCursor(wrapper)
    await wrapper.get('[data-test="client-preview-confirm-cursor"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="connect-reload-hint"]').text()).toBe('Restart Cursor')
  })

  it('opens the focused client\'s preview at once (the row call to action)', async () => {
    const { wrapper } = await open({ focusClient: 'cursor' })
    expect(wrapper.find('[data-test="client-preview-cursor"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="connect-profile-select-cursor"]').exists()).toBe(true)
  })
})
