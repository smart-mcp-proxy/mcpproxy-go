import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { reactive } from 'vue'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// Spec 109 D35 (T162, demo finding #3): the Settings header claimed "Changes
// save instantly" while every section has a "Save changes" button and an
// unsaved-changes counter. The header now names the button by interpolating the
// same constant the button renders, so the two cannot drift.

vi.mock('@/services/api', () => ({ default: { patchConfig: vi.fn() } }))
vi.mock('@/stores/system', () => ({ useSystemStore: () => ({ addToast: vi.fn() }) }))

import SettingsSection from '@/components/settings/SettingsSection.vue'
import { SAVE_CHANGES_LABEL, type SettingField } from '@/views/settings/fields'

describe('Settings header copy matches behaviour', () => {
  const settingsVue = readFileSync(resolve(__dirname, '../../src/views/Settings.vue'), 'utf8')

  it('names the save button by interpolating SAVE_CHANGES_LABEL and never says "instantly"', () => {
    expect(settingsVue).not.toMatch(/save instantly/i)
    expect(settingsVue).toContain('{{ SAVE_CHANGES_LABEL }}')
  })

  it('SAVE_CHANGES_LABEL is the section save button text', () => {
    const field: SettingField = { key: 'tool_response_limit', label: 'Limit', control: 'number', min: 0 }
    const w = mount(SettingsSection, {
      props: { sectionId: 'general', fields: [field], working: reactive({ tool_response_limit: 1 }), original: reactive({ tool_response_limit: 1 }) },
    })
    expect(w.find('[data-test="settings-apply-general"]').text()).toBe(SAVE_CHANGES_LABEL)
    expect(SAVE_CHANGES_LABEL).toBe('Save changes')
  })
})
