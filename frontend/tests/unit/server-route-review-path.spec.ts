import { describe, it, expect } from 'vitest'
import { reviewPath } from '@/utils/serverRoute'
import { TOOL_APPROVAL_LABELS, toolApprovalLabel } from '@/utils/toolQuarantine'

// Spec 109 T093: one builder for /review/<server>[?change=<state>].
describe('reviewPath', () => {
  it('percent-encodes a slash in the server name (MCP-1112 rule)', () => {
    expect(reviewPath('a/b')).toBe('/review/a%2Fb')
  })

  it('appends the change scope when given', () => {
    expect(reviewPath('gh', 'changed')).toBe('/review/gh?change=changed')
    expect(reviewPath('gh', 'pending')).toBe('/review/gh?change=pending')
  })
})

describe('toolApprovalLabel (FR-027)', () => {
  it('maps the raw approval states to the shared labels', () => {
    expect(TOOL_APPROVAL_LABELS).toEqual({
      approved: 'Approved',
      pending: 'New, needs review',
      changed: 'Changed, needs review',
    })
    expect(toolApprovalLabel('approved')).toBe('Approved')
    expect(toolApprovalLabel('pending')).toBe('New, needs review')
    expect(toolApprovalLabel('changed')).toBe('Changed, needs review')
  })

  it('falls back to the raw value for an unknown state', () => {
    expect(toolApprovalLabel('mystery')).toBe('mystery')
  })
})
