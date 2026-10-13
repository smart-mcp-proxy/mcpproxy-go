import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import { Bar } from 'vue-chartjs'
import type { ChartOptions } from 'chart.js'
import CallHistogram from '@/components/usage/CallHistogram.vue'
import Timeline from '@/components/usage/Timeline.vue'

// Live QA finding on 109-k-activity-scope-filters, T119: "T119 ... is not
// implemented: `useScopeQuery` does not appear in frontend/src/views/
// Usage.vue, and neither frontend/src/components/usage/Timeline.vue nor
// CallHistogram.vue have any click handler or router link — Usage/Home chart
// bars do not link to the calls behind the bar as FR-082 and the quickstart's
// 'Usage bar click' recipe require." url-filter-contract.md link map: "Usage
// chart bar (tool x bucket)" -> "the calls behind the bar" ->
// `/activity?view=calls&tool=<server:tool>&from=<bucket start>&to=<bucket
// end>[&status=<s>]`.
//
// Chart.js does not run in jsdom (no canvas 2D context) — chart.js itself
// catches that (`if (!context || !canvas) { console.error(...); return }`,
// chart.js's Chart constructor) rather than throwing, so the real <Bar> still
// mounts and still receives its `options` prop; finding it by its actual
// import (rather than trying to stub it — a `<script setup>` template's
// direct component reference does not go through the string-keyed
// `global.stubs` resolution vue-chartjs's Bar would need) is what lets this
// call `options.onClick` exactly as chart.js would, with no real canvas
// needed at all.

describe('CallHistogram — bar click (Spec 109-k T119)', () => {
  it('emits select-tool with the clicked bar\'s tool', () => {
    const tools = [
      { server: 'filesystem', tool: 'read', calls: 10, errors: 0 },
      { server: 'github', tool: 'create_issue', calls: 3, errors: 1 },
    ]
    const wrapper = mount(CallHistogram, { props: { tools } })
    const options = wrapper.findComponent(Bar).props('options') as ChartOptions<'bar'>
    options.onClick!({ native: { target: document.createElement('canvas') } } as never, [{ index: 1 } as never], null as never)
    expect(wrapper.emitted('select-tool')).toBeTruthy()
    expect(wrapper.emitted('select-tool')![0][0]).toEqual(tools[1])
  })

  it('does nothing when the click misses every bar', () => {
    const tools = [{ server: 'filesystem', tool: 'read', calls: 10, errors: 0 }]
    const wrapper = mount(CallHistogram, { props: { tools } })
    const options = wrapper.findComponent(Bar).props('options') as ChartOptions<'bar'>
    options.onClick!({ native: {} } as never, [], null as never)
    expect(wrapper.emitted('select-tool')).toBeFalsy()
  })
})

describe('Timeline — bucket click (Spec 109-k T119)', () => {
  it('emits select-bucket with [this bucket\'s start, next bucket\'s start)', () => {
    const buckets = [
      { start: '2026-09-20T00:00:00.000Z', calls: 5, errors: 0 },
      { start: '2026-09-20T01:00:00.000Z', calls: 3, errors: 1 },
      { start: '2026-09-20T02:00:00.000Z', calls: 2, errors: 0 },
    ]
    const wrapper = mount(Timeline, { props: { buckets, window: '24h' } })
    const options = wrapper.findComponent(Bar).props('options') as ChartOptions<'bar'>
    options.onClick!({ native: {} } as never, [{ index: 0 } as never], null as never)
    expect(wrapper.emitted('select-bucket')![0][0]).toEqual({
      start: '2026-09-20T00:00:00.000Z',
      end: '2026-09-20T01:00:00.000Z',
    })
  })

  it('extrapolates the last bucket\'s end from the previous gap', () => {
    const buckets = [
      { start: '2026-09-20T00:00:00.000Z', calls: 5, errors: 0 },
      { start: '2026-09-20T01:00:00.000Z', calls: 3, errors: 1 },
    ]
    const wrapper = mount(Timeline, { props: { buckets, window: '24h' } })
    const options = wrapper.findComponent(Bar).props('options') as ChartOptions<'bar'>
    options.onClick!({ native: {} } as never, [{ index: 1 } as never], null as never)
    const emitted = wrapper.emitted('select-bucket')![0][0] as { start: string; end: string }
    expect(emitted.start).toBe('2026-09-20T01:00:00.000Z')
    expect(emitted.end).toBe('2026-09-20T02:00:00.000Z') // same 1h gap as bucket 0 -> 1
  })
})

vi.mock('@/services/api', () => {
  const ok = (data: unknown = null) => Promise.resolve({ success: true, data })
  return {
    default: {
      getActivityUsage: vi.fn(() =>
        ok({
          window: '24h',
          generated_at: '2026-09-26T12:00:00Z',
          freshness_ms: 100,
          token_source: 'bytes',
          tokens_saved: 100,
          tokens_saved_percentage: 10,
          tokens_saved_estimated: false,
          tools: [{ server: 'filesystem', tool: 'read', calls: 10, errors: 0 }],
          timeline: [{ start: '2026-09-20T00:00:00.000Z', calls: 5, errors: 0 }],
          total_calls: 10,
          total_errors: 0,
        })
      ),
      hasAPIKey: vi.fn(() => true),
      onAuthError: vi.fn(() => () => {}),
    },
  }
})

describe('Usage page — chart bar click navigates to the calls behind it (Spec 109-k T119)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  async function mountUsageWithRouter() {
    const Usage = (await import('@/views/Usage.vue')).default
    const router = createRouter({
      history: createWebHistory(),
      routes: [
        { path: '/', name: 'home', component: Usage },
        { path: '/activity', name: 'activity', component: { template: '<div/>' } },
      ],
    })
    await router.push('/')
    await router.isReady()
    const wrapper = mount(Usage, { global: { plugins: [createPinia(), router] } })
    await flushPromises()
    await flushPromises()
    return { wrapper, router }
  }

  it('a CallHistogram bar click navigates to /activity?view=calls&tool=<server:tool>&from=-24h', async () => {
    const { wrapper, router } = await mountUsageWithRouter()
    await wrapper.findComponent(CallHistogram).vm.$emit('select-tool', { server: 'filesystem', tool: 'read', calls: 10, errors: 0 })
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/activity')
    expect(router.currentRoute.value.query).toMatchObject({ view: 'calls', tool: 'filesystem:read', from: '-24h' })
  })

  it('a Timeline bucket click navigates to /activity?view=calls&from=<start>&to=<end>', async () => {
    const { wrapper, router } = await mountUsageWithRouter()
    await wrapper.findComponent(Timeline).vm.$emit('select-bucket', {
      start: '2026-09-20T00:00:00.000Z',
      end: '2026-09-20T01:00:00.000Z',
    })
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/activity')
    expect(router.currentRoute.value.query).toMatchObject({
      view: 'calls',
      from: '2026-09-20T00:00:00.000Z',
      to: '2026-09-20T01:00:00.000Z',
    })
  })
})
