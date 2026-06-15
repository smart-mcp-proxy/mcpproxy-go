import { describe, it, expect } from 'vitest'
import { isBlankInstructions, type SettingField } from '../../src/views/settings/fields'

// MCP-2484: instructions textarea prefill + reset-to-default

// Unit: SettingField interface accepts resetDefault (compile-time test via import)
describe('SettingField.resetDefault (MCP-2484)', () => {
  it('allows a resetDefault property on SettingField without TS error', () => {
    const f: SettingField = {
      key: 'instructions',
      label: 'Server instructions',
      control: 'textarea',
      optional: true,
      resetDefault: 'built-in default text',
    }
    expect(f.resetDefault).toBe('built-in default text')
  })

  it('resetDefault is optional — omitting it does not cause a TS error', () => {
    const f: SettingField = {
      key: 'instructions',
      label: 'Server instructions',
      control: 'textarea',
      optional: true,
    }
    expect(f.resetDefault).toBeUndefined()
  })
})

// Unit: isBlankInstructions helper
describe('isBlankInstructions (MCP-2484)', () => {
  it('treats unset/empty/whitespace as blank (eligible for prefill)', () => {
    expect(isBlankInstructions(undefined)).toBe(true)
    expect(isBlankInstructions(null)).toBe(true)
    expect(isBlankInstructions('')).toBe(true)
    expect(isBlankInstructions('  \n ')).toBe(true)
  })

  it('treats a real saved value as non-blank (never overwrite)', () => {
    expect(isBlankInstructions('my custom instructions')).toBe(false)
  })
})

// Unit: maybePrefillInstructions logic (isolated, not mounting the full component)
describe('maybePrefillInstructions logic (MCP-2484)', () => {
  function prefill(working: Record<string, any>, defaultVal: string, isLoaded: boolean) {
    if (!defaultVal) return
    if (!isLoaded) return
    if (isBlankInstructions(working.instructions)) {
      working.instructions = defaultVal
    }
  }

  it('prefills instructions when config is empty and default is available', () => {
    const w: Record<string, any> = { instructions: '' }
    prefill(w, 'built-in default', true)
    expect(w.instructions).toBe('built-in default')
  })

  it('prefills when instructions is null', () => {
    const w: Record<string, any> = { instructions: null }
    prefill(w, 'built-in default', true)
    expect(w.instructions).toBe('built-in default')
  })

  it('prefills when instructions is undefined', () => {
    const w: Record<string, any> = {}
    prefill(w, 'built-in default', true)
    expect(w.instructions).toBe('built-in default')
  })

  it('prefills when instructions is whitespace-only', () => {
    const w: Record<string, any> = { instructions: '  \n  ' }
    prefill(w, 'built-in default', true)
    expect(w.instructions).toBe('built-in default')
  })

  it('does NOT overwrite a saved custom instruction', () => {
    const w: Record<string, any> = { instructions: 'my custom instructions' }
    prefill(w, 'built-in default', true)
    expect(w.instructions).toBe('my custom instructions')
  })

  it('does NOT prefill when config is not yet loaded', () => {
    const w: Record<string, any> = { instructions: '' }
    prefill(w, 'built-in default', false)
    expect(w.instructions).toBe('')
  })

  it('does NOT prefill when defaultInstructions is empty (API not yet resolved)', () => {
    const w: Record<string, any> = { instructions: '' }
    prefill(w, '', true)
    expect(w.instructions).toBe('')
  })
})
