import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import ClientConnectList from '@/components/ClientConnectList.vue'
import api from '@/services/api'

// Spec 132 PR-3 (T067): the Connect screen offers "API key" or "Sign in with
// browser" only when browser login is on AND the client supports it. With the
// feature off the screen must be exactly what it was before.

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

const stub = { template: '<div />' }

function claudeStatus(extra: Record<string, unknown> = {}) {
  return { id: 'claude-code', name: 'Claude Code', config_path: '/Users/test/.claude.json', display_path: '~/.claude.json', exists: true, connected: false, supported: true, icon: 'claude-code', ...extra }
}
function cursorStatus(extra: Record<string, unknown> = {}) {
  return { id: 'cursor', name: 'Cursor', config_path: '/Users/test/.cursor/mcp.json', display_path: '~/.cursor/mcp.json', exists: true, connected: false, supported: true, icon: 'cursor', ...extra }
}

function preview(overrides: Record<string, unknown> = {}) {
  return {
    success: true,
    data: {
      client: 'claude-code', config_path: '/Users/test/.claude.json', display_path: '~/.claude.json', format: 'json', server_key: 'mcpServers', server_name: 'mcpproxy',
      entry: {}, entry_text: '{"mcpproxy":{"type":"http","url":"http://127.0.0.1:8080/mcp","headers":{"X-API-Key":"mcp_cli_••••"}}}', entry_exists: false, contains_api_key: false,
      credential: 'mcp_cli_••••', profile: '', mode: 'switchable', precondition_token: 'pre-key', access_state: 'accessible', ...overrides,
    },
  }
}
function oauthPreview(overrides: Record<string, unknown> = {}) {
  return preview({
    entry_text: '{"mcpproxy":{"type":"http","url":"http://127.0.0.1:8080/mcp/oauth/claude-code"}}',
    credential: undefined, profile: '', mode: '', auth: 'oauth', browser_login_available: true,
    auth_note: 'Connect does not revoke this client\'s existing API key: it keeps working until you revoke it ("Revoke API key" on the Clients page).',
    precondition_token: 'pre-oauth', ...overrides,
  })
}

async function open() {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: stub }, { path: '/settings', name: 'settings', component: stub }] })
  await router.push('/')
  await router.isReady()
  const wrapper = mount(ClientConnectList, { props: { show: false }, global: { plugins: [router] } })
  await wrapper.setProps({ show: true })
  await flushPromises()
  return wrapper
}

async function startClaude(wrapper: ReturnType<typeof mount>) {
  await wrapper.get('[data-test="connect-claude-code"]').trigger('click')
  await flushPromises()
}

// Vue leaves `<!--v-if-->` placeholders for a false v-if; they are not content,
// and removing one from the pretty-printed output leaves a blank line. The
// snapshot beside this file was recorded from the component as it was before
// Spec 132 PR-3, with the same normalization.
function html(wrapper: ReturnType<typeof mount>): string {
  return wrapper.html().replace(/<!--[\s\S]*?-->/g, '').replace(/^[ \t]*\n/gm, '')
}

describe('Connect screen: choice of how the client signs in (Spec 132 PR-3)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.getOnboardingState as any).mockResolvedValue({ success: true, data: null })
    ;(api.getConfig as any).mockResolvedValue({ success: true, data: { config: { require_mcp_auth: true } } })
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [] })
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [], warnings: [] } })
    ;(api.getConnectPreview as any).mockResolvedValue(preview())
    ;(api.connectClient as any).mockResolvedValue({ success: true, data: { success: true, client: 'claude-code', config_path: '/Users/test/.claude.json', server_name: 'mcpproxy', action: 'created', message: 'ok', reload_hint: 'Run /mcp' } })
  })

  describe('feature off', () => {
    // A client that supports browser login still gets no choice while the
    // switch is off: the payload may name the capability, the screen ignores it.
    const off = { supports_browser_login: true }

    it('renders exactly the screen it rendered before (closed row)', async () => {
      ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: [claudeStatus(off), cursorStatus()] })
      const wrapper = await open()
      expect(wrapper.find('[data-test^="connect-auth"]').exists()).toBe(false)
      expect(html(wrapper)).toMatchSnapshot()
    })

    it('renders exactly the screen it rendered before (preview open) and sends the same requests', async () => {
      ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: [claudeStatus(off), cursorStatus()] })
      const wrapper = await open()
      await startClaude(wrapper)
      expect(wrapper.find('[data-test^="connect-auth"]').exists()).toBe(false)
      expect(html(wrapper)).toMatchSnapshot()
      expect(api.getConnectPreview).toHaveBeenCalledWith('claude-code')
      await wrapper.get('[data-test="client-preview-confirm-claude-code"]').trigger('click')
      await flushPromises()
      expect(api.connectClient).toHaveBeenCalledWith('claude-code', 'mcpproxy', false, { precondition_token: 'pre-key' })
    })

    it('does not offer it for a client without support even when others have it', async () => {
      ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: [claudeStatus({ supports_browser_login: true, browser_login_available: true }), cursorStatus()] })
      const wrapper = await open()
      ;(api.getConnectPreview as any).mockResolvedValue(preview({ client: 'cursor' }))
      await wrapper.get('[data-test="connect-cursor"]').trigger('click')
      await flushPromises()
      expect(wrapper.find('[data-test="connect-auth-choice-cursor"]').exists()).toBe(false)
      expect(wrapper.find('[data-test="client-preview-credential-cursor"]').exists()).toBe(true)
    })
  })

  describe('feature on, client supports it', () => {
    const on = { supports_browser_login: true, browser_login_available: true }

    beforeEach(() => {
      ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: [claudeStatus(on), cursorStatus()] })
    })

    it('shows the choice with "API key" selected, after the diff and backup notice', async () => {
      const wrapper = await open()
      await startClaude(wrapper)
      const choice = wrapper.get('[data-test="connect-auth-choice-claude-code"]')
      expect((wrapper.get('[data-test="connect-auth-key-claude-code"]').element as HTMLInputElement).checked).toBe(true)
      expect((wrapper.get('[data-test="connect-auth-oauth-claude-code"]').element as HTMLInputElement).checked).toBe(false)
      expect(choice.text()).toContain('API key')
      expect(choice.text()).toContain('Sign in with browser')
      // the first preview is the default one: nothing about auth is requested
      expect(api.getConnectPreview).toHaveBeenCalledWith('claude-code')
      // diff + backup notice are in the same panel as the choice and the Connect button
      const panel = wrapper.get('[data-test="client-preview-claude-code"]')
      expect(panel.text()).toContain('a timestamped backup is created first')
      expect(panel.get('[data-test="client-preview-entry-claude-code"]').text()).toContain('mcp_cli_••••')
      const order = panel.html()
      expect(order.indexOf('client-preview-entry-claude-code')).toBeLessThan(order.indexOf('connect-auth-choice-claude-code'))
      expect(order.indexOf('connect-auth-choice-claude-code')).toBeLessThan(order.indexOf('client-preview-confirm-claude-code'))
      // the default still connects exactly as before
      await wrapper.get('[data-test="client-preview-confirm-claude-code"]').trigger('click')
      await flushPromises()
      expect(api.connectClient).toHaveBeenCalledWith('claude-code', 'mcpproxy', false, { precondition_token: 'pre-key' })
    })

    it('is not offered for a client that does not support it', async () => {
      const wrapper = await open()
      ;(api.getConnectPreview as any).mockResolvedValue(preview({ client: 'cursor' }))
      await wrapper.get('[data-test="connect-cursor"]').trigger('click')
      await flushPromises()
      expect(wrapper.find('[data-test="connect-auth-choice-cursor"]').exists()).toBe(false)
    })

    it('choosing the browser re-previews with auth=oauth and shows an address with no credential', async () => {
      const wrapper = await open()
      await startClaude(wrapper)
      ;(api.getConnectPreview as any).mockResolvedValue(oauthPreview())
      await wrapper.get('[data-test="connect-auth-oauth-claude-code"]').setValue(true)
      await flushPromises()
      expect(api.getConnectPreview).toHaveBeenLastCalledWith('claude-code', { auth: 'oauth' })
      const panel = wrapper.get('[data-test="client-preview-claude-code"]')
      expect(panel.get('[data-test="client-preview-entry-claude-code"]').text()).toContain('/mcp/oauth/claude-code')
      expect(panel.text()).not.toContain('mcp_cli_')
      expect(panel.find('[data-test="client-preview-credential-claude-code"]').exists()).toBe(false)
      expect(panel.find('[data-test="connect-profile-select-claude-code"]').exists()).toBe(false)
      expect(panel.find('[data-test="connect-advanced-claude-code"]').exists()).toBe(false)
      const note = panel.get('[data-test="connect-auth-oauth-notice-claude-code"]').text()
      expect(note).toContain('No credential is written')
      expect(note).toContain('does not revoke')
      expect(panel.text()).toContain('a timestamped backup is created first')
    })

    it('confirm sends auth=oauth with the preview token and no binding', async () => {
      const wrapper = await open()
      await startClaude(wrapper)
      ;(api.getConnectPreview as any).mockResolvedValue(oauthPreview())
      await wrapper.get('[data-test="connect-auth-oauth-claude-code"]').setValue(true)
      await flushPromises()
      await wrapper.get('[data-test="client-preview-confirm-claude-code"]').trigger('click')
      await flushPromises()
      expect(api.connectClient).toHaveBeenCalledWith('claude-code', 'mcpproxy', false, { auth: 'oauth', precondition_token: 'pre-oauth' })
    })

    it('switching back to the API key re-previews without auth', async () => {
      const wrapper = await open()
      await startClaude(wrapper)
      ;(api.getConnectPreview as any).mockResolvedValue(oauthPreview())
      await wrapper.get('[data-test="connect-auth-oauth-claude-code"]').setValue(true)
      await flushPromises()
      ;(api.getConnectPreview as any).mockResolvedValue(preview())
      await wrapper.get('[data-test="connect-auth-key-claude-code"]').setValue(true)
      await flushPromises()
      expect(api.getConnectPreview).toHaveBeenLastCalledWith('claude-code')
      expect(wrapper.find('[data-test="client-preview-credential-claude-code"]').exists()).toBe(true)
      await wrapper.get('[data-test="client-preview-confirm-claude-code"]').trigger('click')
      await flushPromises()
      expect(api.connectClient).toHaveBeenLastCalledWith('claude-code', 'mcpproxy', false, { precondition_token: 'pre-key' })
    })

    it('replacing an existing entry says so, and overwrites with force', async () => {
      const wrapper = await open()
      await startClaude(wrapper)
      ;(api.getConnectPreview as any).mockResolvedValue(oauthPreview({ entry_exists: true }))
      await wrapper.get('[data-test="connect-auth-oauth-claude-code"]').setValue(true)
      await flushPromises()
      expect(wrapper.get('[data-test="client-preview-overwrite-claude-code"]').text()).toContain('overwrite')
      await wrapper.get('[data-test="client-preview-confirm-claude-code"]').trigger('click')
      await flushPromises()
      expect(api.connectClient).toHaveBeenLastCalledWith('claude-code', 'mcpproxy', true, { auth: 'oauth', precondition_token: 'pre-oauth' })
    })

    it('a refused choice keeps the panel open and shows the reason', async () => {
      const wrapper = await open()
      await startClaude(wrapper)
      ;(api.getConnectPreview as any).mockRejectedValue(Object.assign(new Error('browser login is off: set mcp_oauth_enabled to true first'), { status: 400 }))
      await wrapper.get('[data-test="connect-auth-oauth-claude-code"]').setValue(true)
      await flushPromises()
      expect(wrapper.get('[data-test="connect-preview-error-claude-code"]').text()).toContain('mcp_oauth_enabled')
      expect((wrapper.get('[data-test="client-preview-confirm-claude-code"]').element as HTMLButtonElement).disabled).toBe(true)
    })

    it('reopening a row after choosing the browser drops the old preview and cannot confirm it', async () => {
      const wrapper = await open()
      await startClaude(wrapper)
      ;(api.getConnectPreview as any).mockResolvedValue(oauthPreview())
      await wrapper.get('[data-test="connect-auth-oauth-claude-code"]').setValue(true)
      await flushPromises()
      expect(wrapper.find('[data-test="client-preview-confirm-claude-code"]').exists()).toBe(true)

      // click the row again; the key preview is still in flight
      let resolveKey: (v: unknown) => void = () => {}
      ;(api.getConnectPreview as any).mockReturnValue(new Promise(resolve => { resolveKey = resolve }))
      await wrapper.get('[data-test="connect-claude-code"]').trigger('click')
      await flushPromises()
      // nothing of the old OAuth preview is left to confirm
      const confirm = wrapper.find('[data-test="client-preview-confirm-claude-code"]')
      expect(confirm.exists() && !(confirm.element as HTMLButtonElement).disabled).toBe(false)
      expect(wrapper.find('[data-test="client-preview-entry-claude-code"]').exists()).toBe(false)
      if (confirm.exists()) await confirm.trigger('click')
      await flushPromises()
      expect(api.connectClient).not.toHaveBeenCalled()

      // the key preview arrives: the form is on "API key" and sends its own token
      resolveKey(preview())
      await flushPromises()
      expect((wrapper.get('[data-test="connect-auth-key-claude-code"]').element as HTMLInputElement).checked).toBe(true)
      await wrapper.get('[data-test="client-preview-confirm-claude-code"]').trigger('click')
      await flushPromises()
      expect(api.connectClient).toHaveBeenCalledTimes(1)
      expect(api.connectClient).toHaveBeenCalledWith('claude-code', 'mcpproxy', false, { precondition_token: 'pre-key' })
    })

    it('a fresh Connect starts on "API key" again', async () => {
      const wrapper = await open()
      await startClaude(wrapper)
      ;(api.getConnectPreview as any).mockResolvedValue(oauthPreview())
      await wrapper.get('[data-test="connect-auth-oauth-claude-code"]').setValue(true)
      await flushPromises()
      await wrapper.get('[data-test="client-preview-cancel-claude-code"]').trigger('click')
      ;(api.getConnectPreview as any).mockResolvedValue(preview())
      await startClaude(wrapper)
      expect((wrapper.get('[data-test="connect-auth-key-claude-code"]').element as HTMLInputElement).checked).toBe(true)
    })
  })
})
