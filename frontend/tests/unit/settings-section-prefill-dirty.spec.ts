import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { reactive } from 'vue'

// MCP-2484 (Codex REQUEST_CHANGES on #685): the instructions prefill writes the
// built-in default directly onto `state.working` in Settings.vue. SettingsSection
// PATCHes only its dirty keys, so a value set OUTSIDE a control must still be
// treated as dirty — otherwise "Save without editing" after a prefill persists
// nothing. SettingsSection now derives dirtiness from working≠original.

const patchConfig = vi.fn(() =>
  Promise.resolve({ success: true, data: { changed_fields: ['instructions'], requires_restart: false } })
)

vi.mock('@/services/api', () => ({
  default: { patchConfig: (...args: unknown[]) => patchConfig(...args) },
}))
vi.mock('@/stores/system', () => ({ useSystemStore: () => ({ addToast: vi.fn() }) }))

import SettingsSection from '@/components/settings/SettingsSection.vue'
import type { SettingField } from '@/views/settings/fields'

const instructionsField: SettingField = {
  key: 'instructions',
  label: 'Server instructions',
  control: 'textarea',
  optional: true,
  resetDefault: 'BUILT-IN DEFAULT',
}

function mountSection(working: Record<string, unknown>, original: Record<string, unknown>) {
  return mount(SettingsSection, {
    props: { sectionId: 'mcp', fields: [instructionsField], working: reactive(working), original: reactive(original) },
  })
}

describe('SettingsSection prefill-is-dirty (MCP-2484)', () => {
  beforeEach(() => patchConfig.mockClear())

  it('treats an externally-prefilled value (working≠original) as dirty and saves it', async () => {
    // working got the prefilled default written directly; original is still the
    // saved (empty) value — exactly the post-prefill state from Settings.vue.
    const w = mountSection({ instructions: 'BUILT-IN DEFAULT' }, { instructions: '' })

    const save = w.find('[data-test="settings-apply-mcp"]')
    expect(save.exists()).toBe(true)
    expect((save.element as HTMLButtonElement).disabled).toBe(false)

    // the field itself is flagged dirty in the UI
    expect(w.find('[data-test="setting-row-instructions"]').classes()).toContain('border-l-warning')

    await save.trigger('click')
    await flushPromises()

    expect(patchConfig).toHaveBeenCalledTimes(1)
    expect(patchConfig.mock.calls[0][0]).toMatchObject({ instructions: 'BUILT-IN DEFAULT' })
  })

  it('is NOT dirty when working equals original (no spurious save)', () => {
    const w = mountSection({ instructions: 'same' }, { instructions: 'same' })
    expect((w.find('[data-test="settings-apply-mcp"]').element as HTMLButtonElement).disabled).toBe(true)
  })

  it('clears dirty after a save commits original = working', async () => {
    const working = reactive({ instructions: 'BUILT-IN DEFAULT' })
    const original = reactive({ instructions: '' })
    const w = mount(SettingsSection, {
      props: { sectionId: 'mcp', fields: [instructionsField], working, original },
    })
    await w.find('[data-test="settings-apply-mcp"]').trigger('click')
    await flushPromises()
    // doSave copies working → original on success; section is no longer dirty
    expect((w.find('[data-test="settings-apply-mcp"]').element as HTMLButtonElement).disabled).toBe(true)
  })
})
