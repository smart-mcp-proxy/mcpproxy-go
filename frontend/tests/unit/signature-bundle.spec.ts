import { describe, it, expect } from 'vitest'
import { formatSignatureBundle } from '@/utils/signatureBundle'

// GH #938 finding 2: no surface answered "which signatures is my proxy running,
// and how old are they?". The Security tab now renders the security overview's
// `signature_bundle` descriptor; this pins the formatting rules.
describe('formatSignatureBundle (#938)', () => {
  it('returns null when the daemon does not report a bundle', () => {
    expect(formatSignatureBundle(undefined)).toBeNull()
    expect(formatSignatureBundle(null)).toBeNull()
    expect(formatSignatureBundle({})).toBeNull()
  })

  it('summarizes an embedded corpus', () => {
    const out = formatSignatureBundle({
      source: 'embedded',
      bundle_version: '0.1.0',
      signature_count: 6,
      runnable_rules: 6,
      skipped_rules: 4,
      declared_skipped: 3,
      fingerprint: 'abc123def456',
    })
    expect(out).not.toBeNull()
    expect(out!.value).toBe('6')
    expect(out!.title).toContain('Signatures')
    expect(out!.detail).toContain('embedded')
    expect(out!.detail).toContain('0.1.0')
    expect(out!.tone).toBe('')
  })

  it('names the configured file and its freshness stamp', () => {
    const out = formatSignatureBundle({
      source: 'file',
      path: '/opt/tpa/scanner-bundle.json',
      bundle_version: '0.1.0',
      runnable_rules: 12,
      generated_at: '2026-07-30T12:00:00Z',
      fingerprint: 'deadbeef1234',
    })
    expect(out!.detail).toContain('/opt/tpa/scanner-bundle.json')
    expect(out!.tooltip).toContain('2026-07-30')
    expect(out!.tooltip).toContain('deadbeef1234')
  })

  it('flags a failed configured-bundle load as a warning', () => {
    const out = formatSignatureBundle({
      source: 'embedded',
      bundle_version: '0.1.0',
      runnable_rules: 6,
      load_error: 'read scanner bundle /opt/tpa.json: no such file or directory',
    })
    expect(out!.tone).toBe('text-warning')
    expect(out!.detail).toContain('load failed')
    expect(out!.tooltip).toContain('/opt/tpa.json')
  })
})
