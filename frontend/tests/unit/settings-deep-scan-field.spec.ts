import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { SECURITY_FIELDS, validateField, type SettingField } from '../../src/views/settings/fields'

// Spec 088 US4 / FR-018: deep scan must be switchable from Settings → Security
// like any other setting. Before this, the only way to turn it on was to
// hand-edit the `security.deep_scan.enabled` config key, whose name was leaked
// to users in a code snippet on the Security page's info alert.
//
// The settings registry gives every control a *systematic* data-test id derived
// from the field key (`SettingField.vue`: `setting-toggle-${field.key}`), so the
// toggle's test hook is `setting-toggle-security.deep_scan.enabled` — a FieldDef
// carries no explicit data-test property in this registry.
const DEEP_SCAN_KEY = 'security.deep_scan.enabled'

function deepScanField(): SettingField | undefined {
  return SECURITY_FIELDS.find((f) => f.key === DEEP_SCAN_KEY)
}

describe('Settings deep-scan toggle (spec 088 US4, FR-018)', () => {
  it('exposes a security.deep_scan.enabled toggle in the Security & Access section', () => {
    const field = deepScanField()
    expect(field, `SECURITY_FIELDS must contain a "${DEEP_SCAN_KEY}" field`).toBeDefined()
    expect(field!.control).toBe('toggle')
  })

  it('labels it "Deep scan (Docker scanners)"', () => {
    expect(deepScanField()!.label).toBe('Deep scan (Docker scanners)')
  })

  it('explains that it layers Docker scanners on top of the always-on offline baseline', () => {
    const help = deepScanField()!.help ?? ''
    expect(help).not.toBe('')
    expect(help).toMatch(/docker/i)
    expect(help).toMatch(/baseline/i)
    // The always-on, offline nature of the baseline is the point of the copy:
    // enabling deep scan adds to it, it does not switch scanning on.
    expect(help).toMatch(/offline/i)
    expect(help).toMatch(/always/i)
  })

  it('is hot-reloadable — no restart flag and no danger confirmation', () => {
    const field = deepScanField()!
    expect(field.restart).toBeUndefined()
    expect(field.danger).toBeUndefined()
  })

  it('validates as a plain boolean toggle (no value-kind constraints)', () => {
    const field = deepScanField()!
    expect(field.valueKind).toBeUndefined()
    expect(validateField(field, true)).toBeNull()
    expect(validateField(field, false)).toBeNull()
  })

  it('actually renders as a toggle with the registry-systematic data-test id', async () => {
    // Mounts the real control — a string-equality mirror of the binding would
    // pass even if the toggle were never rendered (Codex impl-review F5).
    const { mount } = await import('@vue/test-utils')
    const SettingField = (await import('@/components/settings/SettingField.vue')).default
    const wrapper = mount(SettingField, {
      props: { field: deepScanField()!, modelValue: false },
    })
    const toggle = wrapper.find('[data-test="setting-toggle-security.deep_scan.enabled"]')
    expect(toggle.exists()).toBe(true)
    expect((toggle.element as HTMLInputElement).type).toBe('checkbox')
    await toggle.setValue(true)
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([true])
  })
})

describe('Security page info alert no longer instructs a raw config edit (FR-018)', () => {
  const source = readFileSync(resolve(__dirname, '../../src/views/Security.vue'), 'utf-8')

  it('does not name the raw config key in the baseline/deep-scan alert', () => {
    expect(source).not.toContain('security.deep_scan.enabled')
  })

  it('points users at the Settings → Security toggle instead', () => {
    expect(source).toMatch(/Settings\s*→\s*Security/)
  })
})
