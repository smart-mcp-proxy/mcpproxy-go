import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import CallHistogram from '@/components/usage/CallHistogram.vue'
import Timeline from '@/components/usage/Timeline.vue'

// Spec 109 FR-074: count charts use integer ticks (a call is never
// fractional), and the "unresolved" group — names that never completed a
// call, excluded from the ranking (audit F22, #1046) — is titled "Calls to
// unknown tools" rather than left as an unheaded caption.

// Chart.js itself doesn't run in jsdom (no canvas 2D context) and mounting
// vue-chartjs's real <Bar> to inspect the resolved `options` prop just trades
// one flaky dependency for another. The tick config is a literal in each
// component's source, so it is asserted directly — the same pragmatic
// source-text approach z-index-scale.spec.ts uses for a CSS token file.
function readSrc(relative: string): string {
  return readFileSync(resolve(__dirname, relative), 'utf-8')
}

const barStub = {
  name: 'Bar',
  props: ['data', 'options'],
  template: '<div data-test="bar-stub" />',
}

describe('CallHistogram (Spec 109 FR-074)', () => {
  it('forces integer ticks on the calls axis', () => {
    const src = readSrc('../../src/components/usage/CallHistogram.vue')
    expect(src).toMatch(/ticks:\s*\{\s*precision:\s*0/)
  })

  it('titles the excluded group "Calls to unknown tools"', () => {
    const wrapper = mount(CallHistogram, {
      props: {
        tools: [
          { server: 's', tool: 'a', calls: 3, errors: 0 },
          // Never completed a call (all calls errored) -> excluded, "unresolved".
          { server: 's', tool: 'broken', calls: 2, errors: 2 },
        ],
      },
      global: { stubs: { Bar: barStub } },
    })
    const note = wrapper.find('[data-test="usage-call-histogram-unresolved"]')
    expect(note.exists()).toBe(true)
    expect(note.text()).toContain('Calls to unknown tools')
  })

  it('does not show the group when nothing is unresolved', () => {
    const wrapper = mount(CallHistogram, {
      props: { tools: [{ server: 's', tool: 'a', calls: 3, errors: 0 }] },
      global: { stubs: { Bar: barStub } },
    })
    expect(wrapper.find('[data-test="usage-call-histogram-unresolved"]').exists()).toBe(false)
  })
})

describe('Timeline (Spec 109 FR-074)', () => {
  it('forces integer ticks on the calls axis', () => {
    const src = readSrc('../../src/components/usage/Timeline.vue')
    expect(src).toMatch(/ticks:\s*\{\s*precision:\s*0/)
  })

  it('still mounts and renders with real data (sanity: Timeline unaffected by the tick change)', () => {
    const wrapper = mount(Timeline, {
      props: {
        buckets: [{ start: '2026-01-01T00:00:00Z', calls: 3, errors: 1 }],
        window: '24h',
      },
      global: { stubs: { Bar: barStub } },
    })
    expect(wrapper.find('[data-test="usage-timeline"]').exists()).toBe(true)
  })
})
