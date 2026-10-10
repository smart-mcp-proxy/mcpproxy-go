import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

vi.mock('@/services/api', () => ({
  default: {
    getClients: vi.fn(),
    getClient: vi.fn(),
    getRouting: vi.fn(),
    getProfiles: vi.fn(),
  },
}))

import api from '@/services/api'
import CredentialLifecycle from '@/components/clients/CredentialLifecycle.vue'
import { CREDENTIALS_CHANGED_EVENT, useClientsStore } from '@/stores/clients'

// Spec 115 T062/T064/T067: the lifecycle line of a worker credential and the
// live refresh on credentials.changed.
const NOW = Date.parse('2026-10-10T12:00:00Z')
const at = (min: number) => new Date(NOW + min * 60000).toISOString()

describe('CredentialLifecycle', () => {
  it('shows binding, issuer, lease and an unenforced purpose; a lease carries no warning class', () => {
    const w = mount(CredentialLifecycle, {
      props: {
        id: 'delegated-worker', kind: 'client', profile: 'daily-research', mode: 'locked',
        issuer: { actor_kind: 'api_key', surface: 'mcp' }, lease: true, expiresAt: at(50),
        purpose: '<b>Summarise</b> today; assumes no writes', showState: true, now: NOW,
      },
    })
    expect(w.text()).toContain('Locked to daily-research')
    expect(w.text()).toContain('via MCP · api_key')
    expect(w.text()).toContain('Lease ends in 50 min')
    expect(w.text()).toContain('Active')
    expect(w.text()).toContain('Stated purpose — not enforced')
    // The purpose is text, never markup.
    expect(w.find('[data-test="credential-purpose-text"]').element.innerHTML).toContain('&lt;b&gt;')
    expect(w.find('b').exists()).toBe(false)
    expect(w.html()).not.toContain('text-warning')
    expect(w.html()).not.toMatch(/reconnect/i)
  })

  it('a revoked credential reads "Revoked <time>"; an ended lease is an outcome', () => {
    const revoked = mount(CredentialLifecycle, { props: { id: 't', kind: 'token', profile: 'p', revoked: true, revokedAt: at(-3), lease: true, expiresAt: at(30), showState: true, now: NOW } })
    expect(revoked.find('[data-test="credential-state-t"]').text()).toMatch(/^Revoked \d{4}-/)
    expect(revoked.text()).not.toContain('Lease ends')
    expect(revoked.text()).toContain('Pinned to p')
    const ended = mount(CredentialLifecycle, { props: { id: 'e', kind: 'token', profile: 'p', lease: true, expiresAt: at(-1), showState: true, now: NOW } })
    expect(ended.find('[data-test="credential-state-e"]').text()).toBe('Lease ended')
    expect(ended.html()).not.toContain('text-error')
  })

  it('a dangling pin is shown as deny-all', () => {
    const w = mount(CredentialLifecycle, { props: { id: 'd', kind: 'token', profile: 'gone', profileState: 'dangling', now: NOW } })
    expect(w.text()).toContain('Profile missing — deny-all')
  })
})

describe('live refresh', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('the clients store refetches on credentials.changed (UI-004)', async () => {
    const store = useClientsStore()
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [] } })
    await store.refreshPresence()
    const calls = (api.getClients as any).mock.calls.length
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [{ id: 'w1', display_name: 'w1', kind: 'custom', state: 'other', installed: false, connected: true, active_sessions: 0, calls_24h: 0, credential_state: 'revoked', revoked_at: at(0) }] } })
    window.dispatchEvent(new CustomEvent(CREDENTIALS_CHANGED_EVENT, { detail: { kind: 'client', id: 'w1', change: 'revoke' } }))
    await vi.waitFor(() => expect((api.getClients as any).mock.calls.length).toBeGreaterThan(calls))
    await vi.waitFor(() => expect(store.clients[0]?.credential_state).toBe('revoked'))
  })
})
