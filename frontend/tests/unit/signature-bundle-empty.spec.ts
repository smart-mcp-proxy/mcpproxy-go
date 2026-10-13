import { describe, it, expect } from 'vitest'
import { formatSignatureBundle } from '@/utils/signatureBundle'

// A corpus with ZERO runnable rules means offline TPA coverage is OFF. Rendering
// a bare "0" in the default tone let an operator read a switched-off scanner as
// a healthy one — the same class of "the runtime is right but the operator is
// misled" bug #938 set out to close.
describe('formatSignatureBundle — zero runnable rules', () => {
  it('renders zero runnable signatures as an error, not a neutral count', () => {
    const out = formatSignatureBundle({
      source: 'file',
      path: '/opt/tpa/scanner-bundle.json',
      bundle_version: '0.1.0',
      runnable_rules: 0,
      skipped_rules: 4,
    })
    expect(out).not.toBeNull()
    expect(out!.value).toBe('0')
    expect(out!.tone).toBe('text-error')
    expect(out!.detail.toLowerCase()).toContain('no signatures running')
  })

  it('keeps a load failure visible when the count is also zero', () => {
    const out = formatSignatureBundle({
      source: 'embedded',
      runnable_rules: 0,
      load_error: 'scanner bundle: no runnable rules',
    })
    expect(out!.tone).toBe('text-error')
    expect(out!.tooltip).toContain('no runnable rules')
  })
})
