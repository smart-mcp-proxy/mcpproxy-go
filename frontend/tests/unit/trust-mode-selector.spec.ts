import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import TrustModeSelector from '@/components/TrustModeSelector.vue'
import { TRUST_MODES, type TrustMode } from '@/utils/trustMode'

// Spec 088 US1 (FR-001/FR-002/FR-003): the tri-mode trust selector replaces the
// legacy binary auto-approve toggle. It must
//  - render all three modes with the DUAL-behavior copy (tool changes + add-time
//    admission) sourced from utils/trustMode.ts,
//  - present the fail-closed derivation for display: unset → manual selected and
//    flagged as the default; an unrecognized raw value → raw + effective manual
//    shown, never hidden or silently rewritten,
//  - gate the least-safe mode (`auto`) behind an inline confirmation that names
//    BOTH risks, emitting only after the operator acknowledges it.
// The component is presentational: it emits the chosen mode string and never
// persists anything itself (the PATCH lives in the parent view, FR-004).

const OPTION = (mode: TrustMode) => `[data-test="trust-mode-option-${mode}"]`
const RADIO = (mode: TrustMode) => `${OPTION(mode)} input[type="radio"]`

const meta = (mode: TrustMode) => {
  const m = TRUST_MODES.find((x) => x.mode === mode)
  if (!m) throw new Error(`missing TRUST_MODES entry for ${mode}`)
  return m
}

function mountSelector(modelValue?: string) {
  return mount(TrustModeSelector, { props: { modelValue } })
}

function isChecked(wrapper: ReturnType<typeof mountSelector>, mode: TrustMode) {
  return (wrapper.find(RADIO(mode)).element as HTMLInputElement).checked
}

/** Click a radio card the way an operator would. */
async function select(wrapper: ReturnType<typeof mountSelector>, mode: TrustMode) {
  await wrapper.find(RADIO(mode)).setValue(true)
}

describe('TrustModeSelector — rendering (FR-002)', () => {
  it('renders the selector root and exactly the three recognized modes, in TRUST_MODES order', () => {
    const wrapper = mountSelector('manual')
    expect(wrapper.find('[data-test="trust-mode-selector"]').exists()).toBe(true)
    const options = wrapper.findAll('[data-test^="trust-mode-option-"]')
    expect(options).toHaveLength(3)
    expect(options.map((o) => o.attributes('data-test'))).toEqual([
      'trust-mode-option-auto',
      'trust-mode-option-scan',
      'trust-mode-option-manual',
    ])
  })

  it.each(TRUST_MODES.map((m) => m.mode))(
    'option %s renders its label, tool-change description AND add-time admission note',
    (mode) => {
      const wrapper = mountSelector('manual')
      const text = wrapper.find(OPTION(mode)).text()
      expect(text).toContain(meta(mode).label)
      expect(text).toContain(meta(mode).description)
      expect(text).toContain(meta(mode).admissionNote)
    }
  )

  it('groups the radios under one name so only one mode can be selected', () => {
    const wrapper = mountSelector('manual')
    const names = TRUST_MODES.map((m) => wrapper.find(RADIO(m.mode)).attributes('name'))
    expect(new Set(names).size).toBe(1)
    expect(names[0]).toBeTruthy()
  })
})

describe('TrustModeSelector — current selection from the raw value (FR-001)', () => {
  it('unset (field omitted) selects manual and indicates it is the default', () => {
    const wrapper = mountSelector(undefined)
    expect(isChecked(wrapper, 'manual')).toBe(true)
    expect(isChecked(wrapper, 'auto')).toBe(false)
    expect(isChecked(wrapper, 'scan')).toBe(false)
    const note = wrapper.find('[data-test="trust-mode-default-note"]')
    expect(note.exists()).toBe(true)
    expect(note.text().toLowerCase()).toContain('default')
    expect(wrapper.find('[data-test="trust-mode-invalid-note"]').exists()).toBe(false)
  })

  it('empty string is also treated as unset (default manual)', () => {
    const wrapper = mountSelector('')
    expect(isChecked(wrapper, 'manual')).toBe(true)
    expect(wrapper.find('[data-test="trust-mode-default-note"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="trust-mode-invalid-note"]').exists()).toBe(false)
  })

  it.each(['auto', 'scan', 'manual'] as TrustMode[])(
    'explicit %s is shown as the current selection with no default/invalid note',
    (mode) => {
      const wrapper = mountSelector(mode)
      expect(isChecked(wrapper, mode)).toBe(true)
      expect(wrapper.find('[data-test="trust-mode-default-note"]').exists()).toBe(false)
      expect(wrapper.find('[data-test="trust-mode-invalid-note"]').exists()).toBe(false)
    }
  )

  it('a migrated legacy value is displayed as-is with no provenance labeling', () => {
    // The backend normalizes legacy flags INTO trust_mode at load time, so
    // "auto" here is indistinguishable from an explicitly configured one.
    const wrapper = mountSelector('auto')
    expect(isChecked(wrapper, 'auto')).toBe(true)
    expect(wrapper.text().toLowerCase()).not.toContain('legacy')
    expect(wrapper.text().toLowerCase()).not.toContain('migrated')
  })

  it('an unrecognized raw value shows the raw value AND the effective fail-closed mode', () => {
    const wrapper = mountSelector('bogus')
    const note = wrapper.find('[data-test="trust-mode-invalid-note"]')
    expect(note.exists()).toBe(true)
    expect(note.text()).toContain('bogus')
    expect(note.text()).toContain('manual')
    // Effective mode drives the selection; nothing is silently rewritten.
    expect(isChecked(wrapper, 'manual')).toBe(true)
    expect(wrapper.find('[data-test="trust-mode-default-note"]').exists()).toBe(false)
  })

  it.each(['Scan', ' scan '])(
    'mis-cased / untrimmed value %j is invalid (case-sensitive, untrimmed like the backend)',
    (raw) => {
      const wrapper = mountSelector(raw)
      const note = wrapper.find('[data-test="trust-mode-invalid-note"]')
      expect(note.exists()).toBe(true)
      expect(note.text()).toContain(raw.trim())
      expect(isChecked(wrapper, 'manual')).toBe(true)
    }
  )

  it('reflects a raw value changed by the parent (re-load / SSE refresh)', async () => {
    const wrapper = mountSelector('manual')
    await wrapper.setProps({ modelValue: 'scan' })
    expect(isChecked(wrapper, 'scan')).toBe(true)
    expect(isChecked(wrapper, 'manual')).toBe(false)
  })
})

describe('TrustModeSelector — selecting a safe mode emits immediately', () => {
  it('selecting scan emits update:modelValue with exactly the mode string', async () => {
    const wrapper = mountSelector('manual')
    await select(wrapper, 'scan')
    const emitted = wrapper.emitted('update:modelValue')
    expect(emitted).toHaveLength(1)
    expect(emitted![0]).toEqual(['scan'])
    expect(typeof emitted![0][0]).toBe('string')
    expect(wrapper.find('[data-test="trust-mode-auto-confirm"]').exists()).toBe(false)
  })

  it('selecting manual (from auto) emits immediately with no confirmation', async () => {
    const wrapper = mountSelector('auto')
    await select(wrapper, 'manual')
    expect(wrapper.emitted('update:modelValue')![0]).toEqual(['manual'])
    expect(wrapper.find('[data-test="trust-mode-auto-confirm"]').exists()).toBe(false)
  })

  it('re-selecting the mode already in effect does not emit', async () => {
    const wrapper = mountSelector('scan')
    await select(wrapper, 'scan')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })
})

describe('TrustModeSelector — auto requires an acknowledged warning (FR-003)', () => {
  it('does not render the confirmation until auto is selected', () => {
    const wrapper = mountSelector('manual')
    expect(wrapper.find('[data-test="trust-mode-auto-confirm"]').exists()).toBe(false)
  })

  it('a server already configured as auto shows no confirmation on mount', () => {
    const wrapper = mountSelector('auto')
    expect(wrapper.find('[data-test="trust-mode-auto-confirm"]').exists()).toBe(false)
  })

  it('selecting auto opens the confirmation and emits nothing yet', async () => {
    const wrapper = mountSelector('manual')
    await select(wrapper, 'auto')
    expect(wrapper.find('[data-test="trust-mode-auto-confirm"]').exists()).toBe(true)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it('the confirmation warns about BOTH unscanned tool changes and unscanned admission', async () => {
    const wrapper = mountSelector('manual')
    await select(wrapper, 'auto')
    const text = wrapper.find('[data-test="trust-mode-auto-confirm"]').text()
    // Dual-behavior copy, sourced from TRUST_MODES so it cannot drift.
    expect(text).toContain(meta('auto').description)
    expect(text).toContain(meta('auto').admissionNote)
    expect(text.toLowerCase()).toContain('rug-pull')
    expect(text.toLowerCase()).toContain('quarantine')
  })

  it('acknowledging the warning emits auto exactly once and closes the confirmation', async () => {
    const wrapper = mountSelector('manual')
    await select(wrapper, 'auto')
    await wrapper.find('[data-test="trust-mode-auto-confirm-accept"]').trigger('click')
    const emitted = wrapper.emitted('update:modelValue')
    expect(emitted).toHaveLength(1)
    expect(emitted![0]).toEqual(['auto'])
    expect(wrapper.find('[data-test="trust-mode-auto-confirm"]').exists()).toBe(false)
  })

  it('cancelling emits nothing and reverts the selection to the mode in effect', async () => {
    const wrapper = mountSelector('manual')
    await select(wrapper, 'auto')
    await wrapper.find('[data-test="trust-mode-auto-confirm-cancel"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.find('[data-test="trust-mode-auto-confirm"]').exists()).toBe(false)
    expect(isChecked(wrapper, 'manual')).toBe(true)
    expect(isChecked(wrapper, 'auto')).toBe(false)
  })

  it('cancelling on an invalid raw value reverts to the effective (manual) selection', async () => {
    const wrapper = mountSelector('bogus')
    await select(wrapper, 'auto')
    await wrapper.find('[data-test="trust-mode-auto-confirm-cancel"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(isChecked(wrapper, 'manual')).toBe(true)
    expect(wrapper.find('[data-test="trust-mode-invalid-note"]').exists()).toBe(true)
  })

  it('switching to a safe mode while the auto confirmation is open closes it and emits that mode', async () => {
    const wrapper = mountSelector('manual')
    await select(wrapper, 'auto')
    await select(wrapper, 'scan')
    expect(wrapper.find('[data-test="trust-mode-auto-confirm"]').exists()).toBe(false)
    const emitted = wrapper.emitted('update:modelValue')
    expect(emitted).toHaveLength(1)
    expect(emitted![0]).toEqual(['scan'])
  })

  it('cancel then re-select auto re-opens the confirmation (no sticky acknowledgement)', async () => {
    const wrapper = mountSelector('manual')
    await select(wrapper, 'auto')
    await wrapper.find('[data-test="trust-mode-auto-confirm-cancel"]').trigger('click')
    await select(wrapper, 'auto')
    expect(wrapper.find('[data-test="trust-mode-auto-confirm"]').exists()).toBe(true)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })
})
