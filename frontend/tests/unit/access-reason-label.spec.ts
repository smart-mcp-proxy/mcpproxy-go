import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { accessReasonLabel, accessStateLabel } from '@/utils/accessReason'

// Spec 108-j J7 / R9: every access reason the backend can return has a
// non-empty, human word, so a new reason added to profile.AccessReasons fails
// here instead of rendering a blank badge. The Go enum is the source of truth
// (contracts.ts only exports the four FR-010 reasons).

const GO_SRC = readFileSync(resolve(__dirname, '../../../internal/profile/access.go'), 'utf8')

function goAccessReasons(): string[] {
  return [...GO_SRC.matchAll(/AccessReason[A-Za-z]+\s+AccessReason = "([a-z_]+)"/g)].map(m => m[1])
}

describe('accessReasonLabel (Spec 108-j J7)', () => {
  it('has a word for every reason the backend lists', () => {
    const reasons = goAccessReasons()
    expect(reasons.length).toBeGreaterThanOrEqual(11)
    for (const reason of reasons) {
      const label = accessReasonLabel(reason)
      expect(label, reason).toBeTruthy()
      expect(label, `${reason} must not fall through to the raw slug`).not.toBe(reason.replaceAll('_', ' '))
    }
  })

  it('tool_approval reads Disabled for an operator-disabled tool and Awaiting approval otherwise', () => {
    expect(accessReasonLabel('tool_approval', { disabled: true })).toBe('Disabled')
    expect(accessReasonLabel('tool_approval', { disabled: false })).toBe('Awaiting approval')
    expect(accessReasonLabel('tool_approval')).toBe('Awaiting approval')
  })

  it('names the words US3-3 asks for', () => {
    expect(accessReasonLabel('server_not_in_profile')).toBe('Server not in profile')
    expect(accessReasonLabel('denied_by_rule')).toBe('Denied by rule')
    expect(accessReasonLabel('above_tier_cap')).toBe('Above tier cap')
  })

  it('accessStateLabel: Hidden when not visible, Not callable when listed but not callable', () => {
    expect(accessStateLabel({ visible: false, callable: false })).toBe('Hidden')
    expect(accessStateLabel({ visible: true, callable: false })).toBe('Not callable')
    expect(accessStateLabel({ visible: true, callable: true })).toBe('Callable')
  })
})
