import { describe, expect, it } from 'vitest'
import { clientHasNoRecord, clientReach, clientStateLabel, isUndetectedSupported } from '@/utils/clientReach'
import { makeClient } from './fixtures/profiles108i'

const profiles = (name: string) => name === 'work-ro' ? { title: 'Work', max_tier: 'read', tool_counts: { write: 0, destructive: 0 } } : undefined

describe('client reach (Spec 134 FR-060)', () => {
  it('names the profile, its tier and whether the credential locks it', () => {
    const r = clientReach(makeClient('cursor', { profile: 'work-ro', profile_mode: 'locked', profile_source: 'pin' }), profiles)
    expect(r).toMatchObject({ kind: 'profile', text: 'Work · Read-only · locked by credential' })
    expect(clientReach(makeClient('cursor', { profile: 'work-ro', profile_mode: 'switchable', profile_source: 'binding' }), profiles).text).toContain('switchable')
  })

  it('prints "All servers" only where the page showed unrestricted access', () => {
    expect(clientReach(makeClient('a', { credential_state: 'client' }), profiles)).toMatchObject({ kind: 'all', text: 'All servers' })
    expect(clientReach(makeClient('a', { credential_state: 'admin_key' }), profiles)).toMatchObject({ kind: 'all', text: 'All servers', note: 'Admin key' })
    for (const state of ['revoked', 'expired'] as const) expect(clientReach(makeClient('a', { credential_state: state }), profiles).kind).toBe('all')
  })

  it('a row with no record has unknown reach, never "All servers"', () => {
    const observed = makeClient('other:zed', { kind: 'other', credential_state: 'unknown' })
    const gone = makeClient('c', { kind: 'custom', credential_state: 'none' })
    const absent = makeClient('x', { installed: false, connected: false, credential_state: 'none' })
    for (const c of [observed, gone, absent]) {
      expect(clientHasNoRecord(c)).toBe(true)
      expect(clientReach(c, profiles)).toMatchObject({ kind: 'unknown', text: '—' })
    }
  })

  it('a missing profile reads as deny-all', () => {
    expect(clientReach(makeClient('a', { profile: 'gone', profile_missing: true, profile_mode: 'locked' }), profiles)).toMatchObject({ kind: 'missing', text: 'gone (missing — deny-all)' })
  })

  it('folds only supported apps that are positively not detected', () => {
    const undetected = makeClient('codex', { state: 'not_installed', installed: false, connected: false, credential_state: 'none' })
    expect(isUndetectedSupported(undetected)).toBe(true)
    expect(isUndetectedSupported({ ...undetected, kind: 'other' })).toBe(false)
    expect(isUndetectedSupported({ ...undetected, kind: 'custom' })).toBe(false)
    expect(isUndetectedSupported({ ...undetected, connected: true })).toBe(false)
    expect(isUndetectedSupported({ ...undetected, credential_state: 'client' })).toBe(false)
    expect(isUndetectedSupported({ ...undetected, token_name: 'client-codex' })).toBe(false)
    expect(isUndetectedSupported({ ...undetected, state: 'installed' })).toBe(false)
    expect(clientStateLabel('connected_never_seen')).toBe('connected never seen')
  })
})
