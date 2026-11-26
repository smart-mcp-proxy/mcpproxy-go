import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import Clients from '@/views/Clients.vue'
import api from '@/services/api'
import { setAvailableFeatures } from '@/composables/useScopeQuery'
import { WORK_FULL, WORK_RO, makeClient } from './fixtures/profiles108i'

// Spec 134 FR-060: the Clients table (name, state, reach, last seen, one button), the
// Manage side panel with its four groups, the fold of undetected supported apps, and
// `?focus=` opening the panel.

vi.mock('@/services/api', () => ({
  default: {
    hasAPIKey: vi.fn(() => true),
    getClients: vi.fn(),
    getRouting: vi.fn(),
    getClient: vi.fn(),
    getProfiles: vi.fn(),
    getGlobalTools: vi.fn(),
    explainAccess: vi.fn(),
    setClientBinding: vi.fn(),
    forgetClient: vi.fn(),
    rotateClient: vi.fn(),
    getConnectPreview: vi.fn(),
    revokeClientAPIKey: vi.fn(),
    revokeClientLogin: vi.fn(),
  },
}))

const stub = { template: '<div />' }
const cursor = makeClient('cursor', { profile: 'work-ro', profile_title: 'Work', profile_mode: 'locked', profile_source: 'pin', token_name: 'client-cursor', connection_unverified: true, display_path: '~/.cursor/mcp.json', config_path: '/Users/x/.cursor/mcp.json', sessions: [{ id: 's1', work_session_id: 'w1', started_at: '', last_activity: '' }] as any, reload_hint: 'Restart Cursor' })
const codex = makeClient('codex', { state: 'not_installed', installed: false, connected: false, credential_state: 'none' })
const adminHolder = makeClient('windsurf', { credential_state: 'admin_key' })
const zed = makeClient('other:zed', { kind: 'other', state: 'other', installed: false, connected: false, credential_state: 'unknown', display_name: 'Zed' })
const custom = makeClient('dev-laptop', { kind: 'custom', state: 'connected_seen', credential_state: 'client', token_name: 'client-dev-laptop', profile: 'gone', profile_missing: true, profile_mode: 'locked', profile_source: 'pin' })

function router() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/clients', name: 'clients', component: Clients },
      ...['activity', 'tools', 'usage', 'tokens', 'settings', 'review'].map(name => ({ path: `/${name}`, name, component: stub })),
    ],
  })
}

async function mountClients(clients: any[], query = '') {
  ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients, warnings: [] } })
  ;(api.getClient as any).mockImplementation(async (id: string) => ({ success: true, data: clients.find(c => c.id === id) }))
  const r = router()
  await r.push(`/clients${query}`)
  await r.isReady()
  const wrapper = mount(Clients, {
    global: { plugins: [r], stubs: { ClientConnectList: { name: 'ClientConnectList', template: '<div data-test="connect-stub" />', props: ['show', 'revealClient'] }, AgentTokens: true, ModeSwitcher: true } },
    attachTo: document.body,
  })
  await flushPromises()
  return { wrapper, r }
}

describe('Clients table and Manage panel (Spec 134 FR-060)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    document.body.innerHTML = ''
    setAvailableFeatures(['scope_filters'])
    ;(api.getRouting as any).mockResolvedValue({ success: true, data: { endpoints: {}, routing_mode: 'retrieve_tools', description: '', available_modes: [] } })
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [WORK_RO, WORK_FULL] })
  })

  it('every client is one row: name, state, reach, last seen and exactly one button', async () => {
    const { wrapper } = await mountClients([cursor, adminHolder, zed, custom])
    const rows = wrapper.findAll('[data-test="clients-table"] tbody tr')
    expect(rows).toHaveLength(4)
    for (const row of rows) expect(row.findAll('button')).toHaveLength(1)
    expect(wrapper.get('[data-test="client-state-cursor"]').text()).toContain('installed')
    expect(wrapper.get('[data-test="client-unverified-cursor"]').text()).toBe('unverified')
    // Reach keeps today's words: a bound profile, "All servers" for a credential that cannot be bound,
    // a missing profile as deny-all, and a dash (never "All servers") when there is no record.
    expect(wrapper.get('[data-test="client-reach-cursor"]').text()).toContain('Work · Read-only · locked by credential')
    expect(wrapper.get('[data-test="client-reach-windsurf"]').text()).toContain('All servers')
    expect(wrapper.get('[data-test="client-reach-windsurf"]').text()).toContain('Admin key')
    expect(wrapper.get('[data-test="client-reach-dev-laptop"]').text()).toContain('gone (missing — deny-all)')
    const unknown = wrapper.get('[data-test="client-reach-other:zed"]')
    expect(unknown.text()).not.toContain('All servers')
    expect(unknown.text()).toContain('Reach unknown')
  })

  it('folds only supported apps that are positively not detected, and reveals a focused one', async () => {
    const { wrapper } = await mountClients([cursor, codex, zed, adminHolder])
    const fold = wrapper.get('[data-test="clients-undetected"]')
    expect(fold.attributes('open')).toBeUndefined()
    expect(wrapper.get('[data-test="clients-undetected-summary"]').text()).toBe('1 supported app not detected')
    expect(fold.find('[data-test="client-manage-codex"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="clients-table"]').find('[data-test="client-manage-codex"]').exists()).toBe(false)
    // Observed, connected and credentialed rows are never folded.
    for (const id of ['cursor', 'other:zed', 'windsurf']) expect(wrapper.get('[data-test="clients-table"]').find(`[data-test="client-manage-${id}"]`).exists()).toBe(true)
    wrapper.unmount()

    const focused = await mountClients([cursor, codex], '?focus=codex')
    expect(focused.wrapper.find('[data-test="clients-undetected"]').exists()).toBe(false)
    expect(focused.wrapper.get('[data-test="clients-table"]').find('[data-test="client-manage-codex"]').exists()).toBe(true)
    focused.wrapper.unmount()
  })

  it('the panel has four groups in order, links and sessions on top, and a closed "Disconnect or remove"', async () => {
    const { wrapper } = await mountClients([cursor])
    await wrapper.get('[data-test="client-manage-cursor"]').trigger('click')
    await flushPromises()
    const panel = wrapper.get('[data-test="client-manage-panel-cursor"]')
    const html = panel.html()
    const at = (needle: string) => { const i = html.indexOf(needle); expect(i, needle).toBeGreaterThan(-1); return i }
    const order = [
      'client-manage-top-cursor', 'clients-row-link-activity-cursor', 'clients-row-link-sessions-cursor', 'clients-row-link-tools-cursor', 'clients-row-link-usage-cursor',
      'href="/activity?view=sessions&amp;session=w1"',
      'client-manage-use-cursor', 'client-manage-connect-cursor', 'client-manage-key-cursor', 'client-manage-remove-cursor',
    ].map(at)
    expect(order).toEqual([...order].sort((a, b) => a - b))
    expect(panel.findAll('h3').map(h => h.text())).toEqual(['What it can use', 'How it connects', 'Key'])
    expect(panel.get('summary').text()).toBe('Disconnect or remove')
    expect(panel.get('[data-test="client-manage-remove-cursor"]').attributes('open')).toBeUndefined()
    // Group contents.
    expect(panel.get('[data-test="client-manage-use-cursor"]').find('[data-test="client-profile-chip-cursor"]').exists()).toBe(true)
    expect(panel.get('[data-test="client-manage-use-cursor"]').find('[data-test="client-explain-cursor"]').exists()).toBe(true)
    const how = panel.get('[data-test="client-manage-connect-cursor"]')
    expect(how.get('[data-test="client-config-path"]').text()).toContain('~/.cursor/mcp.json')
    expect(how.find('[data-test="check-client-connection"]').exists()).toBe(true)
    expect(how.find('[data-test="client-connect-open-cursor"]').exists()).toBe(true)
    expect(panel.get('[data-test="client-manage-key-cursor"]').find('[data-test="client-rotate-cursor"]').exists()).toBe(true)
    const remove = panel.get('[data-test="client-manage-remove-cursor"]')
    expect(remove.find('[data-test="client-forget-cursor"]').exists()).toBe(true)
    expect(remove.find('[data-test="client-disconnect-cursor"]').exists()).toBe(true)
    expect(remove.findAll('p').length).toBeGreaterThanOrEqual(2)
    expect(panel.find('[data-test="client-reload-hint"]').text()).toBe('Restart Cursor')
    wrapper.unmount()
  })

  it('copies the config path', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const { wrapper } = await mountClients([cursor])
    await wrapper.get('[data-test="client-manage-cursor"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="client-copy-path"]').trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith('~/.cursor/mcp.json')
    wrapper.unmount()
  })

  it('Connect and Disconnect hand the client to the shared connect list without starting a write', async () => {
    const { wrapper } = await mountClients([cursor])
    await wrapper.get('[data-test="client-manage-cursor"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="client-connect-open-cursor"]').trigger('click')
    const list = wrapper.getComponent({ name: 'ClientConnectList' })
    expect(list.props('show')).toBe(true)
    expect(list.props('revealClient')).toBe('cursor')
    expect(api.getConnectPreview).not.toHaveBeenCalled()
    wrapper.get('[data-test="client-manage-remove-cursor"]').element.setAttribute('open', '')
    await list.vm.$emit('close')
    await flushPromises()
    expect(list.props('show')).toBe(false)
    await wrapper.get('[data-test="client-disconnect-cursor"]').trigger('click')
    expect(list.props('show')).toBe(true)
    expect(list.props('revealClient')).toBe('cursor')
    wrapper.unmount()
  })

  it('?focus=<id> opens the panel on the Clients tab; a focus outside the roster opens nothing and requests nothing', async () => {
    const opened = await mountClients([cursor], '?focus=cursor')
    expect(opened.wrapper.find('[data-test="client-manage-panel-cursor"]').exists()).toBe(true)
    expect(opened.wrapper.get('[data-test="focused-client-row"]').text()).toContain('Cursor')
    opened.wrapper.unmount()

    vi.clearAllMocks()
    const missing = await mountClients([cursor], '?focus=nobody')
    expect(missing.wrapper.find('[data-test="client-manage-root"]').exists()).toBe(false)
    expect(api.getClient).not.toHaveBeenCalled()
    missing.wrapper.unmount()

    // An explicit Tokens tab keeps its present behaviour: no panel.
    const tokens = await mountClients([cursor], '?tab=tokens&focus=cursor')
    expect(tokens.wrapper.find('[data-test="client-manage-root"]').exists()).toBe(false)
    tokens.wrapper.unmount()
  })

  it('?focus=<id>&move=1 opens the panel with the profile menu open', async () => {
    const { wrapper } = await mountClients([cursor], '?focus=cursor&move=1')
    expect(wrapper.find('[data-test="client-profile-menu-cursor"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('Escape closes the panel and focus returns to Manage; the backdrop and the close button also close it', async () => {
    const { wrapper } = await mountClients([cursor, adminHolder])
    const manage = wrapper.get('[data-test="client-manage-windsurf"]')
    ;(manage.element as HTMLElement).focus()
    await manage.trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="client-manage-panel-windsurf"]').trigger('keydown', { key: 'Escape' })
    await flushPromises()
    expect(wrapper.find('[data-test="client-manage-root"]').exists()).toBe(false)
    expect(document.activeElement).toBe(manage.element)
    for (const closer of ['client-manage-close', 'client-manage-backdrop']) {
      await manage.trigger('click')
      await flushPromises()
      await wrapper.get(`[data-test="${closer}"]`).trigger('click')
      await flushPromises()
      expect(wrapper.find('[data-test="client-manage-root"]').exists(), closer).toBe(false)
    }
    wrapper.unmount()
  })

  it('Forget keeps its confirmation, its result dialog stays until dismissed, and focus goes to the toolbar when the row is gone', async () => {
    ;(api.forgetClient as any).mockResolvedValue({ revoked: true, disconnected: false })
    const { wrapper } = await mountClients([custom])
    await wrapper.get('[data-test="client-manage-dev-laptop"]').trigger('click')
    await flushPromises()
    wrapper.get('[data-test="client-manage-remove-dev-laptop"]').element.setAttribute('open', '')
    await wrapper.get('[data-test="client-forget-dev-laptop"]').trigger('click')
    await flushPromises()
    expect(api.forgetClient).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="forget-client-dialog"]').exists()).toBe(true)
    // The row leaves the roster after the forget: the panel closes, the result stays.
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [], warnings: [] } })
    await wrapper.get('[data-test="forget-confirm"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="client-manage-root"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="forget-result"]').exists()).toBe(true)
    expect(document.activeElement).toBe(wrapper.get('[data-test="open-client-connect"]').element)
    wrapper.unmount()
  })

  it('Escape inside a nested dialog does not close the panel', async () => {
    const { wrapper } = await mountClients([cursor])
    await wrapper.get('[data-test="client-manage-cursor"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="client-rotate-cursor"]').trigger('click')
    await flushPromises()
    const dialog = wrapper.get('[data-test="rotate-client-dialog"]')
    await dialog.trigger('keydown', { key: 'Escape' })
    expect(wrapper.find('[data-test="client-manage-panel-cursor"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('keeps the active-session count, and Disconnect only for a connected supported client', async () => {
    const idle = makeClient('claude-code', { connected: false, state: 'installed', active_sessions: 0, credential_state: 'none' })
    const { wrapper } = await mountClients([{ ...cursor, active_sessions: 3 }, idle])
    await wrapper.get('[data-test="client-manage-cursor"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="client-active-sessions-cursor"]').text()).toBe('3 active sessions')
    expect(wrapper.find('[data-test="client-disconnect-cursor"]').exists()).toBe(true)
    await wrapper.get('[data-test="client-manage-close"]').trigger('click')
    await wrapper.get('[data-test="client-manage-claude-code"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="client-disconnect-claude-code"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('a click on the row opens the panel as the old row toggled its details; the button is not double-handled', async () => {
    const { wrapper } = await mountClients([cursor])
    await wrapper.get('[data-test="client-name-cursor"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="client-manage-panel-cursor"]').exists()).toBe(true)
    expect(api.getClient).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('shows the profile and lock facts for a client whose credential cannot be bound', async () => {
    const adminWithProfile = makeClient('windsurf', { credential_state: 'admin_key', profile: 'work-ro', profile_mode: 'locked' })
    const { wrapper } = await mountClients([adminWithProfile])
    await wrapper.get('[data-test="client-manage-windsurf"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="client-manage-use-windsurf"]').text()).toContain('locked')
    wrapper.unmount()
  })

  it('closes the panel when the tab changes, and a different client gets a fresh panel (no retargeted dialog)', async () => {
    const other = makeClient('codex', { profile: 'work-ro', token_name: 'client-codex' })
    const { wrapper } = await mountClients([cursor, other])
    await wrapper.get('[data-test="client-manage-cursor"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="client-rotate-cursor"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="rotate-confirm"]').exists()).toBe(true)
    await wrapper.get('[data-test="client-manage-close"]').trigger('click')
    await wrapper.get('[data-test="client-manage-codex"]').trigger('click')
    await flushPromises()
    // The new panel starts with no open rotate dialog.
    expect(wrapper.find('[data-test="client-manage-panel-codex"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="rotate-confirm"]').exists()).toBe(false)
    await wrapper.get('[data-test="clients-tabs"] button:nth-child(3)').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="client-manage-root"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('keeps the panel of a client whose rotation is in flight, then opens the queued one', async () => {
    const dev = makeClient('dev-laptop', { kind: 'custom', state: 'connected_seen', credential_state: 'client', token_name: 'client-dev-laptop', profile: 'work-ro', profile_mode: 'locked', profile_source: 'pin' })
    let finish!: (value: unknown) => void
    ;(api.rotateClient as any).mockReturnValue(new Promise(resolve => { finish = resolve }))
    const { wrapper } = await mountClients([dev, cursor])
    await wrapper.get('[data-test="client-manage-dev-laptop"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="client-rotate-dev-laptop"]').trigger('click')
    await wrapper.get('[data-test="rotate-confirm"]').trigger('click')
    await flushPromises()
    // Another client is asked for while the rotation is pending: the panel stays.
    await wrapper.get('[data-test="client-manage-cursor"]').trigger('click').catch(() => {})
    await flushPromises()
    expect(wrapper.find('[data-test="client-manage-panel-dev-laptop"]').exists()).toBe(true)
    // Cancel cannot close the dialog mid-flight, so the secret has somewhere to arrive.
    expect(wrapper.get('[data-test="rotate-confirm"]').attributes('disabled')).toBeDefined()
    finish({ client: dev, credential: 'mcp_cli_0123456789abcdef0123456789abcdef', snippet: null, rotation: { state: 'pending' } })
    await flushPromises()
    expect(wrapper.find('[data-test="credential-once-dialog"]').exists()).toBe(true)
    wrapper.unmount()
  })
})
