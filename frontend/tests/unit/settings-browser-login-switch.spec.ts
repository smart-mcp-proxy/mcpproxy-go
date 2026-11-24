import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import SettingFieldView from '@/components/settings/SettingField.vue'
import { SECURITY_FIELDS, allCatalogFields } from '../../src/views/settings/fields'

// Spec 132 FR-001: one Settings switch, off by default, marked "Experimental",
// applied without a restart.

const KEY = 'mcp_oauth_enabled'

function field() {
  return allCatalogFields().find(f => f.key === KEY)
}

describe('Settings: browser login switch (Spec 132)', () => {
  it('is one toggle in Security & Access, marked Experimental, hot, off by default', () => {
    const f = field()
    expect(f, `${KEY} must be in the catalogue`).toBeDefined()
    expect(SECURITY_FIELDS.some(x => x.key === KEY)).toBe(true)
    expect(f!.control).toBe('toggle')
    expect(f!.badge).toBe('Experimental')
    expect(f!.restart).toBeUndefined()
    expect(f!.defaultValue ?? false).toBe(false)
  })

  it('renders the badge beside the label', () => {
    const wrapper = mount(SettingFieldView, { props: { field: field()!, modelValue: false } })
    const badge = wrapper.get(`[data-test="setting-badge-${KEY}"]`)
    expect(badge.text()).toBe('Experimental')
    expect(wrapper.find('input[type="checkbox"]').exists()).toBe(true)
  })

  it('renders no badge for a field without one', () => {
    const other = SECURITY_FIELDS.find(f => f.key === 'require_mcp_auth')!
    const wrapper = mount(SettingFieldView, { props: { field: other, modelValue: false } })
    expect(wrapper.find('[data-test^="setting-badge-"]').exists()).toBe(false)
  })

  it('says what the switch does in plain words and mentions no other surface', () => {
    const help = field()!.help ?? ''
    expect(help).toMatch(/browser/i)
    expect(help).toMatch(/consent|approve/i)
  })
})
