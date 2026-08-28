import { describe, it, expect } from 'vitest'
import { ACTIVITY_TYPE_LABELS, formatType, getTypeIcon, humaniseType } from '../../src/utils/activity'

// Issue #1065 defect 2: the Activity "Type" column mixed humanised labels with
// raw snake_case, because the label map was hand-maintained and fell through
// `|| type` for anything it did not know.
describe('activity type labels', () => {
  it('never renders a raw snake_case value, mapped or not', () => {
    for (const type of [...Object.keys(ACTIVITY_TYPE_LABELS), 'a_type_from_the_future']) {
      expect(formatType(type), `formatType(${type}) leaked the raw enum`).not.toMatch(/_/)
    }
  })

  it('labels the four types issue #1065 was filed for', () => {
    expect(formatType('tool_quarantine_change')).toBe('Tool Quarantine Change')
    expect(formatType('security_scan')).toBe('Security Scan')
    expect(formatType('credential_broker')).toBe('Credential Broker')
    expect(formatType('prompt_get')).toBe('Prompt Fetch')
  })

  it('keeps server-level and tool-level quarantine distinct', () => {
    expect(formatType('quarantine_change')).toBe('Quarantine Change')
    expect(formatType('tool_quarantine_change')).toBe('Tool Quarantine Change')
    const labels = Object.values(ACTIVITY_TYPE_LABELS)
    expect(new Set(labels).size, 'two activity types share a label').toBe(labels.length)
  })

  it('gives every labelled type its own icon, not the fallback', () => {
    const types = Object.keys(ACTIVITY_TYPE_LABELS)
    const icons = types.map(getTypeIcon)
    types.forEach((type, i) => {
      expect(icons[i], `${type} falls back to the generic glyph`).not.toBe('📋')
    })
    expect(new Set(icons).size, 'two activity types share an icon').toBe(icons.length)
  })

  it('humanises snake_case', () => {
    expect(humaniseType('tool_quarantine_change')).toBe('Tool Quarantine Change')
    expect(humaniseType('preflight')).toBe('Preflight')
    expect(humaniseType('')).toBe('')
  })
})
