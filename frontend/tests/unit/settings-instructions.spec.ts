import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import SettingField from '@/components/settings/SettingField.vue'
import { ADVANCED_ACCORDIONS, validateField, type SettingField as Field } from '@/views/settings/fields'

// MCP-2175 — editable `instructions` textarea in Advanced settings.
// The built-in default (Go: defaultInstructions) is shown as the placeholder
// and clearing the box must persist "" (Go maps "" -> default), never whitespace.

const instrField: Field = {
  key: 'instructions',
  label: 'Server instructions',
  control: 'textarea',
  optional: true,
  placeholder: 'BUILT-IN DEFAULT',
}

describe('SettingField textarea control', () => {
  it('renders a <textarea> with the default bound as placeholder', () => {
    const w = mount(SettingField, { props: { field: instrField, modelValue: '' } })
    const ta = w.find('[data-test="setting-textarea-instructions"]')
    expect(ta.exists()).toBe(true)
    expect(ta.element.tagName).toBe('TEXTAREA')
    expect(ta.attributes('placeholder')).toBe('BUILT-IN DEFAULT')
  })

  it('emits "" when cleared to whitespace-only (empty-means-default)', async () => {
    const w = mount(SettingField, { props: { field: instrField, modelValue: 'old custom' } })
    const ta = w.find('[data-test="setting-textarea-instructions"]')
    await ta.setValue('   \n  ')
    const emits = w.emitted('update:modelValue')
    expect(emits).toBeTruthy()
    expect(emits![emits!.length - 1][0]).toBe('')
  })

  it('preserves real multi-line content verbatim', async () => {
    const w = mount(SettingField, { props: { field: instrField, modelValue: '' } })
    const ta = w.find('[data-test="setting-textarea-instructions"]')
    await ta.setValue('Use retrieve_tools first.\nThen call_tool_read.')
    const emits = w.emitted('update:modelValue')
    expect(emits![emits!.length - 1][0]).toBe('Use retrieve_tools first.\nThen call_tool_read.')
  })
})

describe('instructions field catalogue (MCP-2175)', () => {
  it('exposes an Advanced "mcp" accordion with an optional textarea instructions field', () => {
    const mcp = ADVANCED_ACCORDIONS.find((a) => a.id === 'mcp')
    expect(mcp, 'expected an Advanced accordion with id "mcp"').toBeTruthy()
    const f = mcp!.fields.find((x) => x.key === 'instructions')
    expect(f, 'expected an "instructions" field in the mcp accordion').toBeTruthy()
    expect(f!.control).toBe('textarea')
    expect(f!.optional).toBe(true)
  })

  it('treats any blank/whitespace instructions value as valid', () => {
    const f: Field = { key: 'instructions', label: 'x', control: 'textarea', optional: true }
    expect(validateField(f, '')).toBeNull()
    expect(validateField(f, '   ')).toBeNull()
    expect(validateField(f, 'custom instructions text')).toBeNull()
  })
})
