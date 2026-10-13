import { describe, it, expect } from 'vitest'
import { ADVANCED_ACCORDIONS, validateField, type SettingField } from '../../src/views/settings/fields'

// Spec 096 T014: `code_execution_max_parallel` (default concurrency for
// call_tools() batches) must be reachable from Settings like its code-execution
// siblings. The 1–32 bound is not cosmetic — it mirrors the Go validator
// (internal/config/config.go), so an out-of-range value the form accepts would
// be rejected on save.
const KEY = 'code_execution_max_parallel'

function field(): SettingField | undefined {
  return ADVANCED_ACCORDIONS.find((a) => a.id === 'code-execution')?.fields.find((f) => f.key === KEY)
}

describe('Settings — code_execution_max_parallel (spec 096)', () => {
  it('lives in the code-execution accordion as a number control', () => {
    const f = field()
    expect(f, `the "code-execution" accordion must contain a "${KEY}" field`).toBeDefined()
    expect(f!.control).toBe('number')
    expect(f!.label).not.toBe('')
  })

  it('bounds the value to 1–32, matching the backend validator', () => {
    const f = field()!
    expect(f.min).toBe(1)
    expect(f.max).toBe(32)
    expect(validateField(f, 8)).toBeNull()
    expect(validateField(f, 1)).toBeNull()
    expect(validateField(f, 32)).toBeNull()
    expect(validateField(f, 0)).not.toBeNull()
    expect(validateField(f, 33)).not.toBeNull()
  })

  it('is hot-reloadable — no restart flag and no danger confirmation', () => {
    const f = field()!
    expect(f.restart).toBeUndefined()
    expect(f.danger).toBeUndefined()
  })
})
