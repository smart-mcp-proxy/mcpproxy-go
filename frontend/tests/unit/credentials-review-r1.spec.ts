import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import CredentialLifecycle from '@/components/clients/CredentialLifecycle.vue'
import ProfileToolTable from '@/components/profiles/ProfileToolTable.vue'
import ProfileChip from '@/components/ProfileChip.vue'

vi.mock('@/services/api', () => ({ default: { getProfiles: vi.fn().mockResolvedValue({ profiles: [] }), getClients: vi.fn(), getRouting: vi.fn() } }))

// Spec 115 review r1 (UI chunk): an open page advances the lease, an ended
// lease offers no Reconnect, and a query-only callable deep link re-filters.
describe('lease countdown advances on an open page', () => {
  beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(Date.parse('2026-10-10T12:00:00Z')) })
  afterEach(() => vi.useRealTimers())

  it('ticks from Lease ends in 2 min to Lease ended without a prop change', async () => {
    const w = mount(CredentialLifecycle, { props: { id: 'w', kind: 'client', profile: 'p', lease: true, expiresAt: '2026-10-10T12:02:00Z', showState: true } })
    expect(w.text()).toContain('Lease ends in 2 min')
    vi.advanceTimersByTime(3 * 60_000)
    await nextTick()
    expect(w.find('[data-test="credential-state-w"]').text()).toBe('Lease ended')
    expect(w.text()).not.toContain('Lease ends in')
  })
})

describe('ProfileChip on an ended lease', () => {
  beforeEach(() => setActivePinia(createPinia()))
  const base = { id: 'w', display_name: 'w', kind: 'custom', state: 'other', installed: false, connected: false, active_sessions: 0, calls_24h: 0 }
  it('offers no Reconnect for a lease that ended, but keeps it for a long-lived credential', () => {
    const lease = mount(ProfileChip, { props: { client: { ...base, credential_state: 'expired', lease: true } as any }, global: { stubs: { RouterLink: true } } })
    expect(lease.find('[data-test="client-credential-cta-w"]').exists()).toBe(false)
    const long = mount(ProfileChip, { props: { client: { ...base, credential_state: 'expired', lease: false } as any }, global: { stubs: { RouterLink: true } } })
    expect(long.find('[data-test="client-credential-cta-w"]').text()).toBe('Reconnect')
    const revokedLease = mount(ProfileChip, { props: { client: { ...base, credential_state: 'revoked', lease: true } as any }, global: { stubs: { RouterLink: true } } })
    expect(revokedLease.find('[data-test="client-credential-cta-w"]').exists()).toBe(false)
  })

  it('a non-lease credential row still shows its expiry (UI-001)', () => {
    const w = mount(CredentialLifecycle, { props: { id: 'n', kind: 'client', profile: 'p', lease: false, expiresAt: '2026-10-12T12:00:00Z', showState: true, now: Date.parse('2026-10-10T12:00:00Z') } })
    expect(w.find('[data-test="credential-expiry-n"]').text()).toMatch(/^Expires 2026-10-1\d/)
    const revoked = mount(CredentialLifecycle, { props: { id: 'r', kind: 'client', profile: 'p', lease: false, revoked: true, expiresAt: '2026-10-12T12:00:00Z', showState: true, now: Date.parse('2026-10-10T12:00:00Z') } })
    expect(revoked.find('[data-test="credential-expiry-r"]').text()).toMatch(/^Expires 2026-10-1\d/)
    const expired = mount(CredentialLifecycle, { props: { id: 'x', kind: 'client', profile: 'p', lease: false, expiresAt: '2026-10-09T12:00:00Z', showState: true, now: Date.parse('2026-10-10T12:00:00Z') } })
    expect(expired.find('[data-test="credential-expiry-x"]').text()).toMatch(/^Expired 2026-10-09/)
    const revokedLease = mount(CredentialLifecycle, { props: { id: 'rl', kind: 'client', profile: 'p', lease: true, revoked: true, expiresAt: '2026-10-10T12:30:00Z', showState: true, now: Date.parse('2026-10-10T12:00:00Z') } })
    expect(revokedLease.find('[data-test="credential-expiry-rl"]').text()).toMatch(/^Lease was to end 2026-10-10/)
    expect(revokedLease.text()).not.toContain('Lease ends in')
  })
})

describe('callable deep link on a mounted table', () => {
  const rows = [
    { server: 's', tool: 'visible_one', intrinsic_tier: 'read', profile_tier: 'read', access: { visible: true, callable: true, reason: '' }, classification_stale: false },
    { server: 's', tool: 'pending_one', intrinsic_tier: 'read', profile_tier: 'read', access: { visible: true, callable: false, reason: 'tool_approval' }, classification_stale: false },
    { server: 's', tool: 'hidden_one', intrinsic_tier: 'write', profile_tier: 'write', access: { visible: false, callable: false, reason: 'above_tier_cap' }, classification_stale: false },
  ]
  it('re-filters when only the query changes', async () => {
    const w = mount(ProfileToolTable, { props: { rows: rows as any, draftRules: undefined, profileLabel: 'p', serversChosen: true, initialReason: '' } })
    expect(w.findAll('[data-test^="profile-tool-row-"]').length).toBe(3)
    await w.setProps({ initialReason: 'callable' })
    expect((w.get('[data-test="tool-filter-reason"]').element as HTMLSelectElement).value).toBe('callable')
    const shown = w.findAll('[data-test^="profile-tool-row-"]')
    expect(shown.length).toBe(1)
    expect(shown[0].text()).toContain('visible_one')
  })
})

describe('a stale client detail cannot undo a lifecycle refresh', () => {
  beforeEach(() => setActivePinia(createPinia()))
  it('discards a detail answered after a newer roster fetch started', async () => {
    const api = (await import('@/services/api')).default as any
    const { useClientsStore } = await import('@/stores/clients')
    const row = { id: 'w1', display_name: 'w1', kind: 'custom', state: 'other', installed: false, connected: true, active_sessions: 0, calls_24h: 0 }
    api.getClients = vi.fn().mockResolvedValue({ success: true, data: { clients: [{ ...row, credential_state: 'client' }] } })
    const store = useClientsStore()
    await store.refreshPresence()
    let resolveDetail: (v: any) => void = () => {}
    api.getClient = vi.fn(() => new Promise(r => { resolveDetail = r }))
    const pending = store.loadDetail('w1')
    api.getClients = vi.fn().mockResolvedValue({ success: true, data: { clients: [{ ...row, credential_state: 'revoked' }] } })
    await store.refreshPresence()
    expect(store.clients[0].credential_state).toBe('revoked')
    // The superseded detail is re-requested and the fresh answer applies.
    api.getClient = vi.fn().mockResolvedValue({ success: true, data: { ...row, credential_state: 'revoked', sessions: [{ id: 's1' }] } })
    resolveDetail({ success: true, data: { ...row, credential_state: 'client' } })
    await pending
    expect(store.clients[0].credential_state).toBe('revoked')
    expect(store.clients[0].sessions).toEqual([{ id: 's1' }])
  })
})
