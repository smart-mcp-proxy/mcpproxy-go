import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ClientCredentials from '@/components/clients/ClientCredentials.vue'
import api from '@/services/api'

vi.mock('@/services/api', () => ({
  default: {
    revokeClientLogin: vi.fn(),
    revokeClientAPIKey: vi.fn(),
  },
}))

const apiKey = { kind: 'api_key', prefix: 'mcp_cli_ab12', state: 'active', expires_at: '2027-01-01T00:00:00Z', last_used_at: '2026-11-22T10:00:00Z' }
const login = (over: Record<string, unknown> = {}) => ({
  kind: 'browser_login', grant_id: 'grantAAAA', state: 'active', approved_at: '2026-11-20T10:00:00Z', last_used_at: '2026-11-22T11:00:00Z',
  reported_name: 'Cursor IDE', redirect_host: '127.0.0.1', observed_name: 'Cursor IDE', name_mismatch: false, ...over,
})
const client = (credentials: unknown[] | undefined, disabled?: boolean) => ({
  id: 'cursor', display_name: 'Cursor', kind: 'supported', state: 'connected_seen', active_sessions: 0, last_seen: null,
  ...(credentials === undefined ? {} : { credentials }),
  ...(disabled === undefined ? {} : { browser_login_disabled: disabled }),
})
const mountIt = (c: unknown) => mount(ClientCredentials, { props: { client: c as any } })

describe('ClientCredentials (Spec 132 T056)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(api.revokeClientLogin as any).mockResolvedValue({ changed: true })
    ;(api.revokeClientAPIKey as any).mockResolvedValue({ changed: true })
  })

  it('lists the API key and each login with its state, times, reported name and redirect host', () => {
    const w = mountIt(client([apiKey, login(), login({ grant_id: 'grantBBBB', state: 'lapsed' }), login({ grant_id: 'grantCCCC', state: 'revoked' })], false))
    expect(w.find('[data-test="credential-api_key"]').text()).toContain('mcp_cli_ab12')
    const rows = w.findAll('[data-test^="credential-login-"]')
    expect(rows.map(r => r.attributes('data-test'))).toEqual(['credential-login-grantAAAA', 'credential-login-grantBBBB', 'credential-login-grantCCCC'])
    expect(w.get('[data-test="credential-state-grantAAAA"]').text()).toBe('active')
    expect(w.get('[data-test="credential-state-grantBBBB"]').text()).toBe('lapsed')
    expect(w.get('[data-test="credential-state-grantCCCC"]').text()).toBe('revoked')
    const first = rows[0]
    expect(first.get('[data-test="credential-reported-name"]').text()).toBe('Cursor IDE')
    expect(first.get('[data-test="credential-redirect-host"]').text()).toBe('127.0.0.1')
    expect(first.text()).toMatch(/Approved/)
    expect(first.text()).toMatch(/Last used/)
    expect(w.find('[data-test="browser-login-disabled"]').exists()).toBe(false)
  })

  it('renders client-controlled strings as text inside <bdi>, never as HTML', () => {
    const evil = '<img src=x onerror="alert(1)">‮evil'
    const w = mountIt(client([login({ reported_name: evil, redirect_host: '<b>host</b>', observed_name: '<script>x</script>', name_mismatch: true })], false))
    expect(w.find('img').exists()).toBe(false)
    expect(w.find('script').exists()).toBe(false)
    expect(w.find('b').exists()).toBe(false)
    for (const sel of ['credential-reported-name', 'credential-redirect-host', 'credential-observed-name']) {
      const el = w.get(`[data-test="${sel}"]`)
      expect(el.element.tagName).toBe('BDI')
    }
    expect(w.get('[data-test="credential-reported-name"]').text()).toBe(evil)
  })

  it('warns, per login, when the name sent at initialize differs from the registered name', () => {
    const w = mountIt(client([login({ name_mismatch: true, observed_name: 'cursor-vscode' }), login({ grant_id: 'grantBBBB' })], false))
    expect(w.find('[data-test="credential-mismatch-grantAAAA"]').exists()).toBe(true)
    expect(w.get('[data-test="credential-mismatch-grantAAAA"]').text()).toContain('cursor-vscode')
    expect(w.find('[data-test="credential-mismatch-grantBBBB"]').exists()).toBe(false)
  })

  it('shows "browser login disabled" and still lists the logins and their revoke buttons', () => {
    const w = mountIt(client([login()], true))
    expect(w.get('[data-test="browser-login-disabled"]').text()).toMatch(/disabled/i)
    expect(w.find('[data-test="credential-login-grantAAAA"]').exists()).toBe(true)
    expect(w.find('[data-test="revoke-login-grantAAAA"]').exists()).toBe(true)
  })

  it('asks before revoking one login, then revokes only that login', async () => {
    const w = mountIt(client([apiKey, login(), login({ grant_id: 'grantBBBB' })], false))
    await w.get('[data-test="revoke-login-grantAAAA"]').trigger('click')
    expect(api.revokeClientLogin).not.toHaveBeenCalled()
    expect(w.get('[data-test="revoke-credential-dialog"]').text()).toContain('Cursor IDE')
    await w.get('[data-test="revoke-credential-confirm"]').trigger('click')
    await flushPromises()
    expect(api.revokeClientLogin).toHaveBeenCalledTimes(1)
    expect(api.revokeClientLogin).toHaveBeenCalledWith('cursor', 'grantAAAA')
    expect(api.revokeClientAPIKey).not.toHaveBeenCalled()
    expect(w.emitted('changed')).toHaveLength(1)
  })

  it('cancelling the confirmation revokes nothing', async () => {
    const w = mountIt(client([login()], false))
    await w.get('[data-test="revoke-login-grantAAAA"]').trigger('click')
    await w.get('[data-test="revoke-credential-cancel"]').trigger('click')
    expect(api.revokeClientLogin).not.toHaveBeenCalled()
    expect(w.emitted('changed')).toBeUndefined()
  })

  it('revokes the API key alone and says the logins stay', async () => {
    const w = mountIt(client([apiKey, login()], false))
    await w.get('[data-test="revoke-api-key"]').trigger('click')
    expect(w.get('[data-test="revoke-credential-dialog"]').text()).toMatch(/logins .* stay/i)
    await w.get('[data-test="revoke-credential-confirm"]').trigger('click')
    await flushPromises()
    expect(api.revokeClientAPIKey).toHaveBeenCalledWith('cursor')
    expect(api.revokeClientLogin).not.toHaveBeenCalled()
    expect(w.emitted('changed')).toHaveLength(1)
  })

  it('offers no revoke for a login or key that is already revoked', () => {
    const w = mountIt(client([{ ...apiKey, state: 'revoked' }, login({ state: 'revoked' })], false))
    expect(w.find('[data-test="revoke-api-key"]').exists()).toBe(false)
    expect(w.find('[data-test="revoke-login-grantAAAA"]').exists()).toBe(false)
  })

  it('shows a failed revoke and does not report a change', async () => {
    ;(api.revokeClientLogin as any).mockRejectedValue(new Error('login not found'))
    const w = mountIt(client([login()], false))
    await w.get('[data-test="revoke-login-grantAAAA"]').trigger('click')
    await w.get('[data-test="revoke-credential-confirm"]').trigger('click')
    await flushPromises()
    expect(w.get('[data-test="revoke-credential-error"]').text()).toContain('login not found')
    expect(w.emitted('changed')).toBeUndefined()
  })

  it('renders nothing while browser login is off and was never used (no new fields in the payload)', () => {
    const w = mountIt(client(undefined, undefined))
    expect(w.html()).toBe('<!--v-if-->')
  })
})
