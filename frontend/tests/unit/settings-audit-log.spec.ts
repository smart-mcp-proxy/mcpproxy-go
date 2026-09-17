import { describe, it, expect } from 'vitest'
import { AUDIT_LOG_FIELDS, ADVANCED_ACCORDIONS, allCatalogFields } from '../../src/views/settings/fields'

// Spec 107 PR-D (T110a/T110, US3): the `audit_log.*` accordion rows of
// `contracts/config-keys.md` — every row is restart-pinned ("audit_log is
// bound at sink construction") and no row is a secret control (there is no
// audit_log secret key; a `secret` control here would be a mistake).
describe('Settings audit-log accordion (Spec 107 T110)', () => {
  const keys = () => AUDIT_LOG_FIELDS.map((f) => f.key)
  const byKey = (k: string) => AUDIT_LOG_FIELDS.find((f) => f.key === k)

  it('exposes exactly the contract row set, in catalogue order', () => {
    expect(keys()).toEqual([
      'audit_log.enabled',
      'audit_log.stdout',
      'audit_log.path',
      'audit_log.max_size_mb',
      'audit_log.max_backups',
      'audit_log.max_age_days',
      'audit_log.compress',
    ])
  })

  it('marks every audit_log row restart-pinned', () => {
    for (const f of AUDIT_LOG_FIELDS) expect(f.restart, f.key).toBe(true)
  })

  it('never uses a secret control (audit_log has no secret key)', () => {
    for (const f of AUDIT_LOG_FIELDS) expect(f.control, f.key).not.toBe('secret')
  })

  it('types the toggle rows', () => {
    expect(byKey('audit_log.enabled')?.control).toBe('toggle')
    expect(byKey('audit_log.stdout')?.control).toBe('toggle')
    expect(byKey('audit_log.compress')?.control).toBe('toggle')
  })

  it('types the path row as text', () => {
    const path = byKey('audit_log.path')
    expect(path?.control).toBe('text')
  })

  it('types the rotation rows as number', () => {
    expect(byKey('audit_log.max_size_mb')?.control).toBe('number')
    expect(byKey('audit_log.max_backups')?.control).toBe('number')
    expect(byKey('audit_log.max_age_days')?.control).toBe('number')
  })

  it('is wired into the Advanced accordions so it reaches the catalogue', () => {
    const accordion = ADVANCED_ACCORDIONS.find((a) => a.id === 'audit-log')
    expect(accordion).toBeDefined()
    expect(accordion?.fields).toBe(AUDIT_LOG_FIELDS)
    for (const k of keys()) {
      expect(allCatalogFields().map((f) => f.key)).toContain(k)
    }
  })
})
