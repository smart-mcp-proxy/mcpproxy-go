import { describe, it, expect } from 'vitest'
import { ADVANCED_ACCORDIONS, buildPartial, validateField, type SettingField } from '../../src/views/settings/fields'

// Spec 124 FR-070: the work-session retention window sits beside its sibling
// retention settings, is a number of at least one day, and needs a restart
// (the backend reads it at start, like the other retention settings).
const KEY = 'session_retention_days'

function activityFields(): SettingField[] {
  return ADVANCED_ACCORDIONS.find((a) => a.id === 'activity')?.fields ?? []
}

describe('Settings — session_retention_days (spec 124)', () => {
  it('lives in the activity retention accordion, next to the record cap', () => {
    const keys = activityFields().map((f) => f.key)
    expect(keys).toContain(KEY)
    expect(keys.indexOf(KEY)).toBe(keys.indexOf('activity_max_records') + 1)
  })

  it('is a number of at least one day', () => {
    const f = activityFields().find((x) => x.key === KEY)!
    expect(f.control).toBe('number')
    expect(f.min).toBe(1)
    expect(validateField(f, 7)).toBeNull()
    expect(validateField(f, 1)).toBeNull()
    expect(validateField(f, 0)).not.toBeNull()
  })

  it('is marked as needing a restart', () => {
    expect(activityFields().find((x) => x.key === KEY)!.restart).toBe(true)
  })

  it('is saved under its own top-level key', () => {
    expect(buildPartial({ [KEY]: 14 }, [KEY])).toEqual({ [KEY]: 14 })
  })
})
