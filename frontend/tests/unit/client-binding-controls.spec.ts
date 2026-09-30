import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import Clients from '@/views/Clients.vue'
import api from '@/services/api'
import { CONNECT_CLIENT_EVENT } from '@/navigation/navModel'
import { setAvailableFeatures } from '@/composables/useScopeQuery'
import { useClientsStore } from '@/stores/clients'
import { useClientBindingsStore } from '@/stores/clientBindings'
import { useProfilesStore } from '@/stores/profiles'
import { GUARD_REFUSAL, WORK_FULL, WORK_RO, apiError, makeClient } from './fixtures/profiles108i'

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
    bulkAssignClients: vi.fn(),
    createCustomClient: vi.fn(),
    rotateClient: vi.fn(),
    finalizeClientRotation: vi.fn(),
    forgetClient: vi.fn(),
    upgradeAdminKeyHolders: vi.fn(),
    getConnectPreview: vi.fn(),
    patchConfig: vi.fn(),
  },
}))

const stub = { template: '<div />' }
const SECRET = 'mcp_cli_0123456789abcdef0123456789abcdef'

const cursor = makeClient('cursor', { profile: 'work-ro', profile_title: 'Work', profile_mode: 'locked', profile_source: 'pin', token_name: 'client-cursor', expires_at: '2027-09-25T00:00:00Z', blocked_24h: 3 })
const claude = makeClient('claude-code', { display_name: 'Claude Code', profile: 'work-full', profile_mode: 'switchable', profile_source: 'binding' })

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/clients', name: 'clients', component: Clients },
      { path: '/activity', name: 'activity', component: stub },
      { path: '/tools', name: 'tools', component: stub },
      { path: '/usage', name: 'usage', component: stub },
      { path: '/tokens', name: 'tokens', component: stub },
      { path: '/settings', name: 'settings', component: stub },
      { path: '/profiles/:name', name: 'profile-editor', component: stub },
      { path: '/servers/:serverName', name: 'server-detail', component: stub },
      { path: '/review', name: 'review', component: stub },
    ],
  })
}

async function mountClients(clients: any[], warnings: any[] = [], query = '') {
  ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients, warnings } })
  ;(api.getClient as any).mockImplementation(async (id: string) => ({ success: true, data: clients.find(client => client.id === id) }))
  const router = makeRouter()
  await router.push(`/clients${query}`)
  await router.isReady()
  const wrapper = mount(Clients, { global: { plugins: [router], stubs: { ClientConnectList: true, AgentTokens: true, ModeSwitcher: true } }, attachTo: document.body })
  await flushPromises()
  return { wrapper, router }
}

describe('Client binding controls on the Clients page (Spec 108-i T092)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    setAvailableFeatures(['scope_filters'])
    ;(api.getRouting as any).mockResolvedValue({ success: true, data: { endpoints: {}, routing_mode: 'retrieve_tools', description: '', available_modes: [] } })
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [WORK_RO, WORK_FULL] })
    ;(api.setClientBinding as any).mockImplementation(async (id: string, body: any) => ({ client: { ...cursor, id, ...body, profile_mode: body.mode ?? 'locked' }, warnings: [] }))
  })

  it('keeps Spec 109 columns and order, and appends one Profile column', async () => {
    const { wrapper } = await mountClients([cursor])
    expect(wrapper.findAll('thead th').map(th => th.text())).toEqual(['Client', 'State', 'Last seen', 'Sessions', 'Config path', 'Profile'])
  })

  it('the chip lists All servers plus the profiles with title and slug', async () => {
    const { wrapper } = await mountClients([cursor])
    const chip = wrapper.get('[data-test="client-profile-chip-cursor"]')
    expect(chip.attributes('aria-haspopup')).toBe('menu')
    await chip.trigger('click')
    const menu = wrapper.get('[data-test="client-profile-menu-cursor"]')
    const items = menu.findAll('[role="menuitemradio"]')
    expect(items.map(item => item.findAll('span').map(span => span.text()))).toEqual([['All servers'], ['Work', 'work-ro'], ['Work Full', 'work-full']])
    expect(items[1].attributes('aria-checked')).toBe('true')
    expect(chip.attributes('aria-expanded')).toBe('true')
  })

  it('selecting a profile sends PUT binding WITHOUT mode; the lock switch sends {profile, mode}', async () => {
    const { wrapper } = await mountClients([cursor])
    await wrapper.get('[data-test="client-profile-chip-cursor"]').trigger('click')
    await wrapper.get('[data-test="client-profile-option-cursor-work-full"]').trigger('click')
    await flushPromises()
    expect(api.setClientBinding).toHaveBeenCalledWith('cursor', { profile: 'work-full' })

    // The row now shows the server's answer; the lock switch sends {profile, mode}.
    ;(api.setClientBinding as any).mockClear()
    await wrapper.get('[data-test="client-lock-switch-cursor"]').setValue(false)
    await flushPromises()
    expect(api.setClientBinding).toHaveBeenCalledWith('cursor', { profile: 'work-full', mode: 'switchable' })
  })

  it('refreshes the row from the response (no optimistic update)', async () => {
    let resolve!: (value: unknown) => void
    ;(api.setClientBinding as any).mockReturnValue(new Promise(r => { resolve = r }))
    const { wrapper } = await mountClients([cursor])
    await wrapper.get('[data-test="client-profile-chip-cursor"]').trigger('click')
    await wrapper.get('[data-test="client-profile-option-cursor-work-full"]').trigger('click')
    expect(wrapper.get('[data-test="client-profile-chip-cursor"]').text()).toContain('Work')
    expect(wrapper.get('[data-test="client-profile-chip-cursor"]').text()).not.toContain('Work Full')
    resolve({ client: { ...cursor, profile: 'work-full', profile_title: 'Work Full', profile_mode: 'locked' }, warnings: [] })
    await flushPromises()
    expect(wrapper.get('[data-test="client-profile-chip-cursor"]').text()).toContain('Work Full')
  })

  it('labels the source: locked by credential (pin) or switchable (binding)', async () => {
    const { wrapper } = await mountClients([cursor, claude])
    const label = (id: string) => wrapper.get(`[data-test="client-profile-chip-${id}"] .whitespace-normal`).text()
    expect(label('cursor')).toBe('Work · Read-only · locked by credential')
    expect(label('claude-code')).toBe('Work Full · Everything · switchable')
    // The lock is an icon plus text, never the icon alone.
    expect(wrapper.get('[data-test="client-profile-chip-cursor"] .sr-only').text()).toBe('Locked')
    expect(wrapper.find('[data-test="client-profile-chip-claude-code"] .sr-only').exists()).toBe(false)
  })

  it('renders a missing profile as deny-all in the danger style with a Move action', async () => {
    const { wrapper } = await mountClients([makeClient('cursor', { profile: 'gone', profile_missing: true, profile_mode: 'locked', profile_source: 'pin' })])
    const chip = wrapper.get('[data-test="client-profile-chip-cursor"]')
    expect(chip.text()).toContain('gone (missing — deny-all)')
    expect(chip.classes()).toContain('btn-error')
    await wrapper.get('[data-test="client-profile-move-cursor"]').trigger('click')
    expect(wrapper.find('[data-test="client-profile-menu-cursor"]').exists()).toBe(true)
  })

  it.each([
    ['none', 'Upgrade to client credential'],
    ['admin_key', 'Upgrade to client credential'],
    ['unknown', 'Upgrade to client credential'],
    ['revoked', 'Reconnect'],
    ['expired', 'Reconnect'],
  ])('%s: chip and switch disabled, PUT never called, CTA "%s" opens the connect list for the client', async (state, cta) => {
    const { wrapper } = await mountClients([makeClient('codex', { credential_state: state as any })])
    expect(wrapper.get('[data-test="client-profile-chip-codex"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-test="client-lock-switch-codex"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-test="client-profile-chip-codex"]').trigger('click')
    expect(wrapper.find('[data-test="client-profile-menu-codex"]').exists()).toBe(false)
    const events: CustomEvent[] = []
    const listener = (event: Event) => events.push(event as CustomEvent)
    window.addEventListener(CONNECT_CLIENT_EVENT, listener)
    const button = wrapper.get('[data-test="client-credential-cta-codex"]')
    expect(button.text()).toBe(cta)
    await button.trigger('click')
    window.removeEventListener(CONNECT_CLIENT_EVENT, listener)
    expect(events).toHaveLength(1)
    expect(events[0].detail).toEqual({ client: 'codex' })
    expect(api.setClientBinding).not.toHaveBeenCalled()
  })

  it('shows a dash for an observed other client with no credential record', async () => {
    const { wrapper } = await mountClients([makeClient('other:zed', { kind: 'other', credential_state: 'none' })])
    expect(wrapper.find('[data-test="client-profile-none-other:zed"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="client-profile-chip-other:zed"]').exists()).toBe(false)
  })

  it('shows a dash, not a call to action, for a supported client that is not installed and holds no credential', async () => {
    const { wrapper } = await mountClients([makeClient('windsurf', { credential_state: 'none', installed: false, connected: false })])
    expect(wrapper.find('[data-test="client-profile-none-windsurf"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="client-credential-cta-windsurf"]').exists()).toBe(false)
    // A revoked credential is still worth a Reconnect even when the app is gone.
    const revoked = await mountClients([makeClient('windsurf', { credential_state: 'revoked', installed: false, connected: false })])
    expect(revoked.wrapper.get('[data-test="client-credential-cta-windsurf"]').text()).toBe('Reconnect')
  })

  it('a 409 guard renders two fix buttons that navigate and never call PATCH', async () => {
    ;(api.setClientBinding as any).mockRejectedValue(apiError(GUARD_REFUSAL.error, GUARD_REFUSAL))
    const { wrapper, router } = await mountClients([cursor])
    await wrapper.get('[data-test="client-profile-chip-cursor"]').trigger('click')
    await wrapper.get('[data-test="client-profile-option-cursor-work-full"]').trigger('click')
    await flushPromises()
    const refusal = wrapper.get('[data-test="client-row-error-cursor"] [data-test="guard-refusal"]')
    expect(refusal.attributes('role')).toBe('alert')
    expect(refusal.findAll('button')).toHaveLength(2)
    await refusal.get('[data-test="guard-fix-set_anonymous_profile"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/settings')
    expect(router.currentRoute.value.query).toEqual({ tab: 'security', focus: 'anonymous_profile', value: 'work-ro' })
    expect(api.patchConfig).not.toHaveBeenCalled()
  })

  it('the warnings banner renders every code with its severity text and action', async () => {
    const warnings = [
      { code: 'client_holds_admin_key', severity: 'warn', client_id: 'codex', message: 'Codex holds the admin API key', action: { kind: 'upgrade_admin_key_holders' } },
      { code: 'anonymous_denied_by_binding_guard', severity: 'warn', message: 'Anonymous callers are denied', bindings: GUARD_REFUSAL.bindings, fixes: GUARD_REFUSAL.fixes, action: { kind: 'change_setting', target: 'anonymous_profile' } },
      { code: 'client_rotation_pending', severity: 'info', client_id: 'cursor', message: 'Rotation pending for Cursor' },
      { code: 'client_token_name_conflict', severity: 'warn', client_id: 'cursor', message: 'Token client-cursor is held by an agent token', action: { kind: 'edit_token', target: 'client-cursor' } },
      { code: 'client_credential_expiring', severity: 'warn', client_id: 'cursor', message: 'Cursor credential expires soon', action: { kind: 'reconnect_client', target: 'cursor' } },
      { code: 'profile_missing', severity: 'warn', client_id: 'cursor', message: 'Cursor uses a missing profile', action: { kind: 'move_client', target: 'cursor' } },
    ]
    const { wrapper, router } = await mountClients([cursor, makeClient('codex', { credential_state: 'admin_key' })], warnings)
    const banner = wrapper.get('[data-test="clients-warnings-banner"]')
    expect(banner.attributes('role')).toBe('status')
    expect(banner.findAll('[data-test^="clients-warning-"]').filter(node => !node.attributes('data-test')!.includes('action'))).toHaveLength(6)
    expect(banner.get('[data-test="clients-warning-client_rotation_pending"]').text()).toContain('Info')
    expect(banner.get('[data-test="clients-warning-client_holds_admin_key"]').text()).toContain('Warning')
    // The guard warning lists its bindings and both fixes.
    expect(banner.get('[data-test="clients-warning-anonymous_denied_by_binding_guard"]').findAll('[data-test^="guard-fix-"]')).toHaveLength(2)

    // reconnect_client dispatches the connect event for the target.
    const events: CustomEvent[] = []
    const listener = (event: Event) => events.push(event as CustomEvent)
    window.addEventListener(CONNECT_CLIENT_EVENT, listener)
    await banner.get('[data-test="clients-warning-action-client_credential_expiring"]').trigger('click')
    window.removeEventListener(CONNECT_CLIENT_EVENT, listener)
    expect(events[0].detail).toEqual({ client: 'cursor' })

    // move_client expands the row with the chip menu open.
    await banner.get('[data-test="clients-warning-action-profile_missing"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="client-profile-menu-cursor"]').exists()).toBe(true)

    // edit_token goes to the tokens route with the token filter.
    await banner.get('[data-test="clients-warning-action-client_token_name_conflict"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.token).toBe('client-cursor')

    // change_setting focuses the setting.
    await router.push('/clients')
    await banner.get('[data-test="clients-warning-action-anonymous_denied_by_binding_guard"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query).toMatchObject({ tab: 'security', focus: 'anonymous_profile' })
  })

  it('has no banner without warnings', async () => {
    const { wrapper } = await mountClients([cursor])
    expect(wrapper.find('[data-test="clients-warnings-banner"]').exists()).toBe(false)
  })

  it('adds a custom client, shows the credential once, and clears it on close', async () => {
    ;(api.createCustomClient as any).mockResolvedValue({
      client: makeClient('dev-laptop', { display_name: 'Dev laptop', kind: 'custom' }),
      credential: SECRET,
      snippet: { generic_http: '{"headers":{"X-API-Key":"' + SECRET + '"}}', header_name: 'X-API-Key' },
    })
    const { wrapper } = await mountClients([cursor])
    await wrapper.get('[data-test="clients-add-other"]').trigger('click')
    await wrapper.get('[data-test="custom-client-id"]').setValue('dev-laptop')
    await wrapper.get('[data-test="custom-client-profile"]').setValue('work-ro')
    await wrapper.get('[data-test="custom-client-profile"]').trigger('change')
    await wrapper.get('[data-test="custom-client-submit"]').trigger('submit')
    await flushPromises()
    expect(api.createCustomClient).toHaveBeenCalledWith({ id: 'dev-laptop', expires_in: '365d', profile: 'work-ro', mode: 'locked' })
    const dialog = wrapper.get('[data-test="credential-once-dialog"]')
    expect((dialog.get('[data-test="credential-once-secret"]').element as HTMLInputElement).value).toBe(SECRET)
    expect(dialog.text()).toContain('Shown once. MCPProxy stores only a hash.')
    expect(dialog.get('[data-test="credential-once-snippet"]').exists()).toBe(true)

    await dialog.get('[data-test="credential-once-close"]').trigger('click')
    await flushPromises()
    // Nothing of the secret survives: not in the DOM, not in any store, not in the URL.
    expect(wrapper.html()).not.toContain(SECRET)
    expect(JSON.stringify(useClientsStore().$state)).not.toContain('mcp_cli_')
    expect(JSON.stringify(useClientBindingsStore().$state)).not.toContain('mcp_cli_')
    expect(JSON.stringify(useProfilesStore().$state)).not.toContain('mcp_cli_')
    expect((wrapper.vm as any).secret ?? null).toBeNull()
    expect(window.location.href).not.toContain('mcp_cli_')
    expect(JSON.stringify(Object.entries(localStorage))).not.toContain('mcp_cli_')
  })

  it('shows a 400 on the id field inline', async () => {
    ;(api.createCustomClient as any).mockRejectedValue(apiError("client id \"cursor\" is a supported client; use connect instead", { field: 'id', status: 400 }))
    const { wrapper } = await mountClients([cursor])
    await wrapper.get('[data-test="clients-add-other"]').trigger('click')
    await wrapper.get('[data-test="custom-client-id"]').setValue('cursor')
    await wrapper.get('[data-test="custom-client-submit"]').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[data-test="custom-client-id-error"]').text()).toContain('is a supported client')
    expect(wrapper.find('[data-test="credential-once-secret"]').exists()).toBe(false)
  })

  it('shows the remediation for a token-name conflict', async () => {
    ;(api.createCustomClient as any).mockRejectedValue(apiError('token name client-dev is held by a regular agent token', { status: 409, conflicting_token: 'client-dev' }))
    const { wrapper } = await mountClients([cursor])
    await wrapper.get('[data-test="clients-add-other"]').trigger('click')
    await wrapper.get('[data-test="custom-client-id"]').setValue('dev')
    await wrapper.get('[data-test="custom-client-submit"]').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[data-test="custom-client-conflict"]').text()).toContain('Revoke or delete token client-dev')
  })

  it('rotates a supported client through the preview, then rotate with the precondition token', async () => {
    ;(api.getConnectPreview as any).mockResolvedValue({ success: true, data: { client: 'cursor', config_path: '/home/.cursor/mcp.json', entry_text: '{"x":"mcp_cli_••••"}', precondition_token: 'pre-1' } })
    ;(api.rotateClient as any).mockResolvedValue({ client: cursor, connect: {}, rotation: { state: 'finalized' } })
    const { wrapper } = await mountClients([cursor])
    await wrapper.findAll('tbody tr')[0].trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="client-rotate-cursor"]').trigger('click')
    await flushPromises()
    expect(api.getConnectPreview).toHaveBeenCalledWith('cursor')
    expect(wrapper.get('[data-test="rotate-preview-entry"]').text()).toContain('mcp_cli_••••')
    expect(api.rotateClient).not.toHaveBeenCalled()
    await wrapper.get('[data-test="rotate-confirm"]').trigger('click')
    await flushPromises()
    expect(api.rotateClient).toHaveBeenCalledWith('cursor', { precondition_token: 'pre-1' })
    expect(wrapper.get('[data-test="client-action-result-cursor"]').text()).toBe('Rotation finalized.')
  })

  it('reports a rolled-back rotation honestly', async () => {
    ;(api.getConnectPreview as any).mockResolvedValue({ success: true, data: { client: 'cursor', config_path: '/p', entry_text: '{}', precondition_token: 'pre-1' } })
    ;(api.rotateClient as any).mockResolvedValue({ client: cursor, rotation: { state: 'rolled_back' } })
    const { wrapper } = await mountClients([cursor])
    await wrapper.findAll('tbody tr')[0].trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="client-rotate-cursor"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="rotate-confirm"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="client-action-result-cursor"]').text()).toBe('Rolled back (the client config was not changed).')
  })

  it('rotating a custom client shows the new credential once, then a pending badge with Finalize now', async () => {
    const custom = makeClient('dev-laptop', { display_name: 'Dev laptop', kind: 'custom', profile: 'work-ro', profile_mode: 'locked', profile_source: 'pin' })
    ;(api.rotateClient as any).mockResolvedValue({ client: { ...custom, rotation_pending: true }, credential: SECRET, snippet: { generic_http: '{}', header_name: 'X-API-Key' }, rotation: { state: 'pending' } })
    const { wrapper } = await mountClients([custom])
    await wrapper.findAll('tbody tr')[0].trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="client-rotate-dev-laptop"]').trigger('click')
    await wrapper.get('[data-test="rotate-confirm"]').trigger('click')
    await flushPromises()
    expect(api.rotateClient).toHaveBeenCalledWith('dev-laptop', {})
    expect((wrapper.get('[data-test="credential-once-secret"]').element as HTMLInputElement).value).toBe(SECRET)
    await wrapper.get('[data-test="credential-once-close"]').trigger('click')
    expect(wrapper.html()).not.toContain(SECRET)

    // The next row refresh carries rotation_pending: the badge and the finalize button show.
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [{ ...custom, rotation_pending: true }], warnings: [] } })
    ;(api.finalizeClientRotation as any).mockResolvedValue({ client: { ...custom, rotation_pending: false }, rotation: { state: 'finalized' } })
    await useClientsStore().load()
    await flushPromises()
    expect(wrapper.get('[data-test="client-rotation-badge-dev-laptop"]').text()).toBe('Rotation pending')
    expect(wrapper.get('[data-test="client-rotation-pending-dev-laptop"]').text()).toContain('Both secrets work for 24 h')
    await wrapper.get('[data-test="client-finalize-dev-laptop"]').trigger('click')
    await flushPromises()
    expect(api.finalizeClientRotation).toHaveBeenCalledWith('dev-laptop')
  })

  it('forget sends disconnect and shows disconnect_error', async () => {
    ;(api.forgetClient as any).mockResolvedValue({ revoked: 'client-cursor', disconnected: false, disconnect_error: 'config is read-only' })
    const { wrapper } = await mountClients([cursor])
    await wrapper.findAll('tbody tr')[0].trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="client-forget-cursor"]').trigger('click')
    const dialog = wrapper.get('[data-test="forget-client-dialog"]')
    expect((dialog.get('[data-test="forget-disconnect"]').element as HTMLInputElement).checked).toBe(true)
    await dialog.get('[data-test="forget-confirm"]').trigger('click')
    await flushPromises()
    expect(api.forgetClient).toHaveBeenCalledWith('cursor', { disconnect: true })
    expect(wrapper.get('[data-test="forget-result"]').text()).toBe('Credential revoked; the config entry could not be removed: config is read-only')
  })

  it('bulk move calls /clients/bulk-assign and lists skipped clients with their codes', async () => {
    ;(api.bulkAssignClients as any).mockResolvedValue({ moved: ['cursor'], skipped: [{ client_id: 'codex', code: 'no_client_credential' }, { client_id: 'zed', code: 'binding_bypassable_without_auth', error: 'would be bypassable' }] })
    const { wrapper } = await mountClients([cursor, claude])
    await wrapper.get('[data-test="clients-bulk-move"]').trigger('click')
    await wrapper.get('[data-test="bulk-from"]').setValue('work-ro')
    await wrapper.get('[data-test="bulk-to"]').setValue('work-full')
    expect(wrapper.get('[data-test="bulk-preview-line"]').text()).toBe('1 client uses Work')
    await wrapper.get('[data-test="bulk-submit"]').trigger('submit')
    await flushPromises()
    expect(api.bulkAssignClients).toHaveBeenCalledWith({ from_profile: 'work-ro', to_profile: 'work-full' })
    const result = wrapper.get('[data-test="bulk-result"]')
    expect(result.text()).toContain('1 moved')
    expect(result.get('[data-test="bulk-skipped-codex"]').text()).toContain('no client credential')
    expect(result.get('[data-test="bulk-skipped-zed"] [data-test="guard-refusal"]').exists()).toBe(true)
  })

  it('bulk move counts every client on the from-profile, not just the filtered rows (F4.1)', async () => {
    const codex = makeClient('codex', { display_name: 'Codex', profile: 'work-ro', profile_mode: 'locked' })
    const { wrapper } = await mountClients([cursor], [], '?client=cursor')
    // The page is scoped to cursor; the unscoped list holds both clients.
    ;(api.getClients as any).mockImplementation(async (scope: any = {}) => ({
      success: true,
      data: { clients: scope?.client ? [cursor] : [cursor, codex], warnings: [] },
    }))
    await wrapper.get('[data-test="clients-bulk-move"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="bulk-from"]').setValue('work-ro')
    await wrapper.get('[data-test="bulk-to"]').setValue('work-full')
    await flushPromises()
    expect(wrapper.get('[data-test="bulk-preview-line"]').text()).toBe('2 clients use Work')
  })

  it('bulk move sends the chosen mode', async () => {
    ;(api.bulkAssignClients as any).mockResolvedValue({ moved: [], skipped: [] })
    const { wrapper } = await mountClients([cursor])
    await wrapper.get('[data-test="clients-bulk-move"]').trigger('click')
    await wrapper.get('[data-test="bulk-from"]').setValue('work-ro')
    await wrapper.get('[data-test="bulk-to"]').setValue('work-full')
    await wrapper.get('[data-test="bulk-mode-locked"]').setValue(true)
    await wrapper.get('[data-test="bulk-submit"]').trigger('submit')
    await flushPromises()
    expect(api.bulkAssignClients).toHaveBeenCalledWith({ from_profile: 'work-ro', to_profile: 'work-full', mode: 'locked' })
  })

  describe('admin-key upgrade', () => {
    const holder = makeClient('codex', { credential_state: 'admin_key' })
    const warning = [{ code: 'client_holds_admin_key', severity: 'warn', message: 'Codex holds the admin key', action: { kind: 'upgrade_admin_key_holders' } }]
    const row = { client_id: 'codex', display_name: 'Codex', display_path: '~/.codex/config.toml', diff: { before: 'key', after: 'mcp_cli_••••' }, credential: 'mcp_cli_••••', profile: '', mode: 'switchable', precondition_token: 'row-1' }

    it('the toolbar button shows only when a client holds the admin key', async () => {
      const { wrapper } = await mountClients([cursor])
      expect(wrapper.find('[data-test="clients-upgrade-admin-key"]').exists()).toBe(false)
      const holderPage = await mountClients([holder], warning)
      expect(holderPage.wrapper.find('[data-test="clients-upgrade-admin-key"]').exists()).toBe(true)
    })

    it('previews, applies with the precondition token, then shows the Rotate the admin API key panel', async () => {
      ;(api.upgradeAdminKeyHolders as any)
        .mockResolvedValueOnce({ preview: [row], precondition_token: 'combined-1' })
        .mockResolvedValueOnce({ upgraded: ['codex'], failed: [{ client_id: 'zed', error: 'read-only' }], next_step: 'rotate_admin_api_key' })
      const { wrapper } = await mountClients([holder], warning)
      await wrapper.get('[data-test="clients-upgrade-admin-key"]').trigger('click')
      await wrapper.get('[data-test="upgrade-profile"]').setValue('work-ro')
      await wrapper.get('[data-test="upgrade-mode-locked"]').setValue(true)
      await wrapper.get('[data-test="upgrade-preview"]').trigger('submit')
      await flushPromises()
      expect(api.upgradeAdminKeyHolders).toHaveBeenNthCalledWith(1, { profile: 'work-ro', mode: 'locked' })
      const table = wrapper.get('[data-test="upgrade-preview-table"]')
      expect(table.text()).toContain('Codex')
      expect(table.text()).toContain('mcp_cli_••••')
      expect(wrapper.find('[data-test="rotate-admin-key-panel"]').exists()).toBe(false)
      await wrapper.get('[data-test="upgrade-apply"]').trigger('click')
      await flushPromises()
      expect(api.upgradeAdminKeyHolders).toHaveBeenNthCalledWith(2, { profile: 'work-ro', mode: 'locked', apply: true, precondition_token: 'combined-1' })
      expect(wrapper.get('[data-test="upgrade-failed"]').text()).toContain('zed: read-only')
      const panel = wrapper.get('[data-test="rotate-admin-key-panel"]')
      expect(panel.text()).toContain('Rotate the admin API key')
      expect(panel.get('[data-test="rotate-admin-key-docs"]').attributes('href')).toBe('https://docs.mcpproxy.app/configuration/config-file/')
    })

    it('a guard disables Apply and shows the refusal with its fixes', async () => {
      ;(api.upgradeAdminKeyHolders as any).mockResolvedValue({ preview: [row], precondition_token: 't', guard: { code: 'binding_bypassable_without_auth', bindings: GUARD_REFUSAL.bindings, fixes: GUARD_REFUSAL.fixes } })
      const { wrapper } = await mountClients([holder], warning)
      await wrapper.get('[data-test="clients-upgrade-admin-key"]').trigger('click')
      await wrapper.get('[data-test="upgrade-profile"]').setValue('work-ro')
      await wrapper.get('[data-test="upgrade-preview"]').trigger('submit')
      await flushPromises()
      expect(wrapper.get('[data-test="upgrade-apply"]').attributes('disabled')).toBeDefined()
      expect(wrapper.get('[data-test="upgrade-preview-step"] [data-test="guard-refusal"]').findAll('button')).toHaveLength(2)
    })

    it('an empty preview goes straight to the rotate panel', async () => {
      ;(api.upgradeAdminKeyHolders as any).mockResolvedValue({ preview: [], precondition_token: '', next_step: 'rotate_admin_api_key' })
      const { wrapper } = await mountClients([holder], warning)
      await wrapper.get('[data-test="clients-upgrade-admin-key"]').trigger('click')
      await wrapper.get('[data-test="upgrade-preview"]').trigger('submit')
      await flushPromises()
      expect(wrapper.find('[data-test="rotate-admin-key-panel"]').exists()).toBe(true)
    })

    it('a stale precondition offers Preview again', async () => {
      ;(api.upgradeAdminKeyHolders as any)
        .mockResolvedValueOnce({ preview: [row], precondition_token: 't1' })
        .mockRejectedValueOnce(apiError('configs changed', { status: 409, code: 'precondition_failed' }))
        .mockResolvedValueOnce({ preview: [row], precondition_token: 't2' })
      const { wrapper } = await mountClients([holder], warning)
      await wrapper.get('[data-test="clients-upgrade-admin-key"]').trigger('click')
      await wrapper.get('[data-test="upgrade-preview"]').trigger('submit')
      await flushPromises()
      await wrapper.get('[data-test="upgrade-apply"]').trigger('click')
      await flushPromises()
      expect(wrapper.get('[data-test="upgrade-stale"]').text()).toBe('Client configs changed since the preview. Preview again before applying.')
      expect(wrapper.get('[data-test="upgrade-apply"]').attributes('disabled')).toBeDefined()
      await wrapper.get('[data-test="upgrade-preview-again"]').trigger('click')
      await flushPromises()
      expect(wrapper.find('[data-test="upgrade-stale"]').exists()).toBe(false)
    })
  })

  it('Explain access opens the explainer with the client preset and calls GET /access/explain', async () => {
    ;(api.getGlobalTools as any).mockResolvedValue({ success: true, data: { tools: [{ server_name: 'github', name: 'create_issue' }, { server_name: 'github', name: 'list_issues' }] } })
    ;(api.explainAccess as any).mockResolvedValue({ subject: { kind: 'client', name: 'cursor' }, tool: 'github:create_issue', profile: { name: 'work-ro', source: 'pin' }, steps: [], verdict: 'hidden', first_failure: 'tier_cap', fixes: [] })
    const { wrapper } = await mountClients([cursor])
    await wrapper.findAll('tbody tr')[0].trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="client-explain-cursor"]').trigger('click')
    await flushPromises()
    const options = wrapper.findAll('[data-test="explain-tool-options"] option').map(option => option.attributes('value'))
    expect(options).toEqual(['github:create_issue', 'github:list_issues'])
    await wrapper.get('[data-test="explain-tool-input"]').setValue('github:create_issue')
    await wrapper.get('[data-test="explain-run"]').trigger('submit')
    await flushPromises()
    expect(api.explainAccess).toHaveBeenCalledWith({ tool: 'github:create_issue', client: 'cursor' })
    expect(wrapper.get('[data-test="explain-verdict"]').text()).toContain('Hidden')
  })

  it('the credential badge and the blocked link show under the chip', async () => {
    const { wrapper } = await mountClients([cursor])
    expect(wrapper.get('[data-test="client-credential-badge-cursor"]').text()).toBe('Client credential')
    const link = wrapper.get('[data-test="client-blocked-link-cursor"]')
    expect(link.text()).toBe('3 blocked (24h)')
    expect(link.attributes('href')).toBe('/activity?client=cursor&status=blocked')
  })

  it('sends the profile and client scope to GET /clients', async () => {
    await mountClients([cursor], [], '?profile=work-ro&client=cursor')
    expect(api.getClients).toHaveBeenCalledWith({ profile: 'work-ro', client: 'cursor' })
  })
})
