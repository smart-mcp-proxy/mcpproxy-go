import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import FindingChip from '@/components/FindingChip.vue'

// Phase 1 (TPA inline findings) — the per-tool scan chip.
//
// Two invariants decide whether an operator can trust this surface at all, and
// they are the reason the chip is a component rather than an inline ternary:
//
//   * an UNSCANNED tool is never styled as a safe tool. It renders nothing.
//     A green "clean" badge on a tool no scanner has looked at is a claim the
//     product cannot back, and a "not scanned" nag on every card trains the
//     operator to ignore the chip everywhere, including where it matters.
//   * a FAILED scan is never styled as a threat. The scan could not finish, so
//     there is no verdict — it takes the neutral precaution tone that
//     holdEvidence.reasonPresentation() gives HOLD_REASON_SCAN_COVERAGE
//     ('Scan could not complete'), and must never render badge-error.

function mountChip(props: Record<string, unknown> = {}) {
  return mount(FindingChip, { props })
}

describe('FindingChip', () => {
  it('renders nothing for an unscanned tool (default state)', () => {
    const wrapper = mountChip()
    expect(wrapper.text()).toBe('')
    expect(wrapper.find('span').exists()).toBe(false)
  })

  it('renders nothing when the state is explicitly absent', () => {
    expect(mountChip({ state: 'absent', count: 3 }).text()).toBe('')
  })

  it('renders a dangerous chip in the error tone with an exact count', () => {
    const wrapper = mountChip({ state: 'dangerous', count: 2 })
    const chip = wrapper.get('[data-test="finding-chip-dangerous"]')
    expect(chip.classes()).toContain('badge-error')
    expect(chip.text()).toContain('2 findings')
    // Severity is never colour alone.
    expect(chip.text()).toContain('▲')
  })

  it('singularises the count', () => {
    expect(mountChip({ state: 'dangerous', count: 1 }).text()).toContain('1 finding')
  })

  it('renders a warning chip in the warning tone with its own glyph', () => {
    const chip = mountChip({ state: 'warning', count: 1 }).get(
      '[data-test="finding-chip-warning"]',
    )
    expect(chip.classes()).toContain('badge-warning')
    expect(chip.classes()).not.toContain('badge-error')
    expect(chip.text()).toContain('▮')
  })

  it('renders a FAILED scan in the neutral precaution tone, never red', () => {
    const chip = mountChip({ state: 'failed' }).get('[data-test="finding-chip-failed"]')
    expect(chip.classes()).not.toContain('badge-error')
    expect(chip.text()).toContain('Scan could not complete')
    expect(chip.attributes('title')).toContain('not a threat verdict')
  })

  it('renders a spinner, not a verdict, while a scan is running', () => {
    const chip = mountChip({ state: 'scanning' }).get('[data-test="finding-chip-scanning"]')
    expect(chip.find('.loading').exists()).toBe(true)
    expect(chip.classes()).not.toContain('badge-error')
    expect(chip.text()).toContain('Scanning')
  })

  it('renders a neutral stale chip that says the findings may not apply', () => {
    const chip = mountChip({ state: 'stale' }).get('[data-test="finding-chip-stale"]')
    expect(chip.classes()).toContain('badge-ghost')
    expect(chip.text()).toContain('⟳')
    expect(chip.attributes('title')).toContain('changed since the last scan')
  })

  it('appends a staleness marker to a verdict chip without changing its tone', () => {
    const chip = mountChip({ state: 'dangerous', count: 1, stale: true }).get(
      '[data-test="finding-chip-dangerous"]',
    )
    expect(chip.classes()).toContain('badge-error')
    expect(chip.text()).toContain('⟳')
  })

  it('explains that the finding is informational and does not block the tool', () => {
    const chip = mountChip({ state: 'dangerous', count: 1 }).get(
      '[data-test="finding-chip-dangerous"]',
    )
    expect(chip.attributes('title')).toContain('does not block the tool')
  })
})
