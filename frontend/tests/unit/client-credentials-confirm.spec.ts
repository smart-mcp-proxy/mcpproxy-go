import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ClientCredentials from '@/components/clients/ClientCredentials.vue'
import api from '@/services/api'

// Spec 134 PR 7 review (Sol round 2 and 3): a revoke confirmation always names the
// credential the operator chose and is never retargeted by a refresh.

vi.mock('@/services/api', () => ({ default: { revokeClientLogin: vi.fn(), revokeClientAPIKey: vi.fn() } }))

const key = (prefix: string) => ({ kind: 'api_key', prefix, state: 'active', last_used_at: null })
const login = (id: string, over: Record<string, unknown> = {}) => ({
  kind: 'browser_login', grant_id: id, state: 'active', approved_at: '2026-11-20T10:00:00Z', last_used_at: null,
  reported_name: 'Cursor IDE', redirect_host: '127.0.0.1', observed_name: 'Cursor IDE', name_mismatch: false, ...over,
})
const client = (credentials: unknown[]) => ({ id: 'cursor', display_name: 'Cursor', credentials })

describe('revoke confirmations (section=revoke)', () => {
  beforeEach(() => { vi.clearAllMocks(); (api.revokeClientAPIKey as any).mockResolvedValue({}); (api.revokeClientLogin as any).mockResolvedValue({}) })

  it('keeps an open confirmation when ANOTHER credential changes', async () => {
    const w = mount(ClientCredentials, { props: { client: client([login('A'), login('B')]) as any, section: 'revoke' } })
    await w.get('[data-test="revoke-login-A"]').trigger('click')
    expect(w.find('[data-test="revoke-credential-dialog"]').exists()).toBe(true)
    await w.setProps({ client: client([login('A'), login('B', { state: 'revoked' }), login('C')]) as any })
    await flushPromises()
    expect(w.get('[data-test="revoke-credential-confirm"]').exists()).toBe(true)
    await w.get('[data-test="revoke-credential-confirm"]').trigger('click')
    await flushPromises()
    expect(api.revokeClientLogin).toHaveBeenCalledWith('cursor', 'A')
  })

  it('drops the confirmation when the credential it names changes, and never revokes a replacement key', async () => {
    const w = mount(ClientCredentials, { props: { client: client([key('mcp_cli_old')]) as any, section: 'revoke' } })
    await w.get('[data-test="revoke-api-key"]').trigger('click')
    await w.setProps({ client: client([key('mcp_cli_new')]) as any })
    await flushPromises()
    expect(w.find('[data-test="revoke-credential-confirm"]').exists()).toBe(false)
    expect(api.revokeClientAPIKey).not.toHaveBeenCalled()
  })

  it('the revoke rows say which grant they act on', () => {
    const w = mount(ClientCredentials, { props: { client: client([login('A', { approved_at: '2026-11-01T10:00:00Z' }), login('B')]) as any, section: 'revoke' } })
    expect(w.get('[data-test="credential-facts-A"]').text()).toContain('Approved')
    expect(w.get('[data-test="credential-facts-A"]').text()).toContain('127.0.0.1')
    expect(w.get('[data-test="credential-facts-A"]').text()).not.toBe(w.get('[data-test="credential-facts-B"]').text())
  })
})
