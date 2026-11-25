import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import StatusPill from '@/components/StatusPill.vue'
import StateTag from '@/components/StateTag.vue'
import { healthTone } from '@/utils/healthTone'
import { HEALTH_STATUS_LABELS, type HealthStatusValue } from '@/types/contracts'

// Spec 134 FR-003 / FR-004 / SC-003: one component prints server status text.
// Known values print the Spec 109 label; unknown prints the raw value in the
// neutral tone; a missing status renders nothing of its own (the caller keeps
// its present fallback).

const TONES: Record<HealthStatusValue, string> = {
  ready: 'ok',
  needs_review: 'review',
  connecting: 'busy',
  disabled: 'off',
  sign_in_required: 'error',
  needs_secret: 'error',
  needs_config: 'error',
  error: 'error',
}

describe('StatusPill (spec 134 FR-003)', () => {
  for (const [status, tone] of Object.entries(TONES)) {
    it(`${status} prints its Spec 109 label in the ${tone} tone`, () => {
      const wrapper = mount(StatusPill, { props: { status } })
      const pill = wrapper.get('[data-test="status-pill"]')
      expect(pill.text()).toBe(HEALTH_STATUS_LABELS[status as HealthStatusValue])
      expect(pill.attributes('data-tone')).toBe(tone)
      expect(healthTone(status)).toBe(tone)
    })
  }

  it('covers all eight Spec 109 statuses', () => {
    expect(Object.keys(TONES).sort()).toEqual(Object.keys(HEALTH_STATUS_LABELS).sort())
  })

  it('prints an unknown status verbatim in the neutral tone', () => {
    const wrapper = mount(StatusPill, { props: { status: 'quantum_flux' } })
    const pill = wrapper.get('[data-test="status-pill"]')
    expect(pill.text()).toBe('quantum_flux')
    expect(pill.attributes('data-tone')).toBe('neutral')
  })

  it('renders nothing when the status is missing', () => {
    expect(mount(StatusPill, { props: { status: undefined } }).find('[data-test="status-pill"]').exists()).toBe(false)
    expect(mount(StatusPill, { props: { status: '' } }).find('[data-test="status-pill"]').exists()).toBe(false)
  })

  it('does not rely on colour alone: the tone dot is hidden from assistive tech', () => {
    const wrapper = mount(StatusPill, { props: { status: 'error' } })
    expect(wrapper.get('[data-test="status-pill"] svg, [data-test="status-pill"] [data-pill-dot]').attributes('aria-hidden')).toBe('true')
  })
})

describe('StateTag', () => {
  it('prints slot text in a status tone', () => {
    const wrapper = mount(StateTag, { props: { tone: 'review' }, slots: { default: 'Awaiting review' } })
    const tag = wrapper.get('[data-test="state-tag"]')
    expect(tag.text()).toBe('Awaiting review')
    expect(tag.attributes('data-tone')).toBe('review')
  })
})
