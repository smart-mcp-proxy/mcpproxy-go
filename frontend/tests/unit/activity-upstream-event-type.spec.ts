import { describe, it, expect } from 'vitest'
import { ACTIVITY_TYPE_LABELS, activityViewTypes, formatType, getTypeIcon } from '../../src/utils/activity'

// Spec 124 FR-024: upstream events appear in the System view and can be
// filtered by type. The System view is derived from ACTIVITY_TYPE_LABELS, so
// registering the type there is what makes the rows visible.
describe('activity type upstream_event (spec 124)', () => {
  it('has a label and an icon', () => {
    expect(ACTIVITY_TYPE_LABELS.upstream_event).toBe('Upstream Event')
    expect(formatType('upstream_event')).toBe('Upstream Event')
    expect(getTypeIcon('upstream_event')).not.toBe('')
  })

  it('belongs to the System view and not to the calls view', () => {
    expect(activityViewTypes('system')).toContain('upstream_event')
    expect(activityViewTypes('calls')).not.toContain('upstream_event')
  })
})
