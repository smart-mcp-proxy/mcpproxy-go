import { describe, it, expect } from 'vitest'
import { validateField, type SettingField } from '../../src/views/settings/fields'

// Mirrors the native tray's SettingsDiscoveryFieldsTests: an optional duration
// left blank means "inherit the default" (tri-state nil) and must validate,
// otherwise the Save button is stuck disabled.
describe('validateField — optional duration (spec 074)', () => {
  const optional: SettingField = { key: 'tool_discovery_interval', label: 'X', control: 'duration', optional: true }
  const required: SettingField = { key: 'x', label: 'X', control: 'duration' }

  it('treats a blank optional duration as valid (inherit default)', () => {
    expect(validateField(optional, '')).toBeNull()
    expect(validateField(optional, null)).toBeNull()
    expect(validateField(optional, undefined)).toBeNull()
  })

  it('still rejects a blank required duration', () => {
    expect(validateField(required, '')).not.toBeNull()
  })

  it('rejects a malformed duration regardless of optional', () => {
    expect(validateField(optional, 'abc')).not.toBeNull()
  })

  it('accepts valid durations including 0s (disabled)', () => {
    expect(validateField(optional, '30s')).toBeNull()
    expect(validateField(optional, '1h30m')).toBeNull()
    expect(validateField(optional, '0s')).toBeNull()
  })
})
