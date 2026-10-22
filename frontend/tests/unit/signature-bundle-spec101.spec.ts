import { describe, it, expect } from 'vitest'
import { formatSignatureBundle, describeDegradedReason } from '@/utils/signatureBundle'

const FULL = 'a'.repeat(12) + 'b'.repeat(52)

// Spec 101 FR-020 / contracts/bundle-status-surface.md: the status surface must
// answer "am I current and verified, or stuck on last-known-good, and why?".
describe('formatSignatureBundle — Spec 101 identity fields', () => {
  it('exposes short fingerprint, full digest, sequence, authority, epoch, verified, staleness', () => {
    const out = formatSignatureBundle({
      source: 'file',
      path: '/opt/tpa/b.json',
      runnable_rules: 12,
      fingerprint: FULL.slice(0, 12),
      full_digest: FULL,
      sequence: '42',
      authority: 'operator',
      trust_epoch: 3,
      signature_verified: true,
      staleness_days: 9,
    })!
    expect(out.shortFingerprint).toBe(FULL.slice(0, 12))
    expect(out.fullDigest).toBe(FULL)
    const byLabel = Object.fromEntries(out.identity.map((r) => [r.label, r.value]))
    expect(byLabel['Sequence']).toBe('42')
    expect(byLabel['Authority']).toBe('operator')
    expect(byLabel['Trust epoch']).toBe('3')
    expect(byLabel['Signature']).toBe('verified')
    expect(byLabel['Age']).toBe('9 days')
  })

  it('reports an unverified signature and singular/unknown staleness plainly', () => {
    const a = formatSignatureBundle({ source: 'embedded', runnable_rules: 6, staleness_days: 1 })!
    const b = formatSignatureBundle({ source: 'embedded', runnable_rules: 6 })!
    const ar = Object.fromEntries(a.identity.map((r) => [r.label, r.value]))
    const br = Object.fromEntries(b.identity.map((r) => [r.label, r.value]))
    expect(ar['Signature']).toBe('not verified')
    expect(ar['Age']).toBe('1 day')
    expect(br['Age']).toBeUndefined()
  })

  it('derives the short fingerprint from the full digest when only that is sent', () => {
    const out = formatSignatureBundle({ source: 'embedded', runnable_rules: 6, full_digest: FULL })!
    expect(out.shortFingerprint).toBe(FULL.slice(0, 12))
  })

  it('has empty identity extras for a legacy descriptor', () => {
    const out = formatSignatureBundle({ source: 'embedded', runnable_rules: 6, fingerprint: 'abc123def456' })!
    expect(out.fullDigest).toBe('')
    expect(out.degraded).toBeNull()
    expect(out.lastRejection).toBeNull()
    expect(out.pinned).toBe(false)
  })
})

describe('formatSignatureBundle — degraded coverage', () => {
  it.each([
    ['unsigned_external_active', /unsigned/i],
    ['embedded_below_watermark', /older/i],
    ['operator_bundle_missing', /missing|no longer/i],
    ['operator_authority_unbaselined', /baseline/i],
  ])('explains %s and says auto-approval is suspended', (reason, re) => {
    const out = formatSignatureBundle({
      source: 'embedded',
      runnable_rules: 6,
      coverage_degraded: true,
      degraded_reason: reason,
    })!
    expect(out.degraded).not.toBeNull()
    expect(out.degraded!.reason).toBe(reason)
    expect(out.degraded!.explanation).toMatch(re)
    expect(out.degraded!.explanation.toLowerCase()).toContain('auto-approval')
    expect(out.degraded!.explanation.toLowerCase()).toContain('review')
    expect(out.tone).toBe('text-warning')
  })

  it('still shows a banner for an unknown future reason, with the raw code', () => {
    const out = formatSignatureBundle({
      source: 'embedded', runnable_rules: 6, coverage_degraded: true, degraded_reason: 'some_new_reason',
    })!
    expect(out.degraded!.explanation).toContain('some_new_reason')
    expect(out.degraded!.explanation.toLowerCase()).toContain('auto-approval')
  })

  it('shows a banner even when degraded with no reason', () => {
    const out = formatSignatureBundle({ source: 'embedded', runnable_rules: 6, coverage_degraded: true })!
    expect(out.degraded).not.toBeNull()
  })

  it('has NO banner when not degraded, even with a reason string left behind', () => {
    const out = formatSignatureBundle({
      source: 'embedded', runnable_rules: 6, coverage_degraded: false, degraded_reason: 'unsigned_external_active',
    })!
    expect(out.degraded).toBeNull()
    expect(out.tone).toBe('')
  })

  it('does not downgrade the zero-runnable error tone', () => {
    const out = formatSignatureBundle({ source: 'embedded', runnable_rules: 0, coverage_degraded: true, degraded_reason: 'unsigned_external_active' })!
    expect(out.tone).toBe('text-error')
  })

  it('describeDegradedReason is exported for direct use', () => {
    expect(describeDegradedReason('unsigned_external_active')).toMatch(/unsigned/i)
  })
})

describe('formatSignatureBundle — last rejection and pins', () => {
  it('surfaces reason, detail and time of the last rejection', () => {
    const out = formatSignatureBundle({
      source: 'embedded',
      runnable_rules: 6,
      last_rejection: { reason: 'downgrade', detail: 'sequence 3 below watermark 5', at: '2026-10-19T10:00:00Z' },
    })!
    expect(out.lastRejection).toEqual({
      reason: 'downgrade',
      label: expect.stringMatching(/downgrade/i),
      detail: 'sequence 3 below watermark 5',
      at: '2026-10-19T10:00:00Z',
    })
    // A rejection alone is informational: last-known-good is still serving.
    expect(out.degraded).toBeNull()
    expect(out.tone).toBe('')
  })

  it('ignores an empty last_rejection object', () => {
    const out = formatSignatureBundle({ source: 'embedded', runnable_rules: 6, last_rejection: { reason: '', at: '' } })!
    expect(out.lastRejection).toBeNull()
  })

  it('reports pinned and rollback flags', () => {
    const out = formatSignatureBundle({ source: 'file', runnable_rules: 3, pinned: true, rollback: true })!
    expect(out.pinned).toBe(true)
    expect(out.rollback).toBe(true)
  })
})
