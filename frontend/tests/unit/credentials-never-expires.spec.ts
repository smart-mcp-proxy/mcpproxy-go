import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import CredentialLifecycle from '@/components/clients/CredentialLifecycle.vue'
import { expiresText, expiringSoon, isLease, isNeverExpires, stateBadge } from '@/utils/credentials'

// A token minted with `--expires never` carries Go's zero time on the wire. It
// is never expired, never a lease, never "expiring soon", and reads "Never".
const ZERO = '0001-01-01T00:00:00Z'
const NOW = Date.parse('2026-10-10T12:00:00Z')

describe('never-expiring credentials', () => {
  it('recognises the zero time and nothing else', () => {
    expect(isNeverExpires(ZERO)).toBe(true)
    expect(isNeverExpires('0001-01-01T00:00:00.000Z')).toBe(true)
    expect(isNeverExpires('2026-10-10T12:00:00Z')).toBe(false)
    expect(isNeverExpires(null)).toBe(false)
    expect(isNeverExpires('')).toBe(false)
  })

  it('is active, not a lease, never expiring soon', () => {
    const c = { expires_at: ZERO, created_at: '2026-10-10T11:00:00Z' }
    expect(stateBadge(c, NOW).label).toBe('Active')
    expect(isLease(c)).toBe(false)
    expect(expiringSoon(c, NOW)).toBe(false)
  })

  it('expiry text says Never', () => {
    expect(expiresText(ZERO)).toBe('Never')
    expect(expiresText('2026-10-10T12:00:00Z')).not.toBe('Never')
  })

  it('the lifecycle chip says "Never expires" rather than Expired', () => {
    const w = mount(CredentialLifecycle, { props: { id: 't', kind: 'token', profile: 'p', lease: false, expiresAt: ZERO, showState: true, now: NOW } })
    expect(w.text()).toContain('Never expires')
    expect(w.text()).not.toContain('Expired')
  })

  it('a client credential with a zero expiry is not "Never" and not active', () => {
    expect(isNeverExpires(ZERO, 'client')).toBe(false)
    expect(isNeverExpires(ZERO, 'agent')).toBe(true)
    expect(expiresText(ZERO, 'client')).not.toBe('Never')
    const c = { expires_at: ZERO, created_at: '2026-10-10T11:00:00Z', kind: 'client' }
    expect(stateBadge(c, NOW).label).not.toBe('Active')
    const w = mount(CredentialLifecycle, { props: { id: 'c', kind: 'client', profile: 'p', lease: false, expiresAt: ZERO, showState: true, now: NOW } })
    expect(w.text()).not.toContain('Never expires')
  })
})
