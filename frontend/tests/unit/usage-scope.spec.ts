import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import CallHistogram from '@/components/usage/CallHistogram.vue'

// Spec 109-k T113: url-filter-contract.md `from`/`to` row ("Usage: window")
// — a deep link that carries `from`/`to` (a server card's stats line, a
// future Clients-row link, a shared URL) must move the window picker, not
// just chart-bar links built FROM the page. `?from=-7d` -> `window=7d`;
// `?from=-3d` (not one of the three presets) -> `window=all`, rendered as a
// disabled "not applied on Usage" chip, never a request the backend would
// silently default to 24h under.

const usageDataFor = (window: string) => ({
  window,
  tokens_saved: 0,
  tokens_saved_percentage: 0,
  tools: [{ server: 'filesystem', tool: 'read', calls: 3, errors: 0, resp_bytes: 100, p95_ms: 5 }],
  timeline: [],
})

let getActivityUsageMock: ReturnType<typeof vi.fn>

vi.mock('@/services/api', () => {
  const ok = (data: unknown) => Promise.resolve({ success: true, data })
  return {
    default: {
      getActivityUsage: vi.fn((params?: { window?: string }) => ok(usageDataFor(params?.window ?? '24h'))),
    },
  }
})

async function mountUsageAt(path: string) {
  const api = (await import('@/services/api')).default
  getActivityUsageMock = api.getActivityUsage as unknown as ReturnType<typeof vi.fn>
  const Usage = (await import('@/views/Usage.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/usage', name: 'usage', component: Usage },
      { path: '/activity', name: 'activity', component: { template: '<div/>' } },
    ],
  })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(Usage, { global: { plugins: [createPinia(), router] } })
  await flushPromises()
  await flushPromises()
  return { wrapper, router }
}

describe('Usage — window hydration from the URL contract (Spec 109-k T113)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('no from/to defaults to window=24h (the picker\'s own default) and no "not applied" chip', async () => {
    const { wrapper } = await mountUsageAt('/usage')
    expect(getActivityUsageMock).toHaveBeenCalledWith(expect.objectContaining({ window: '24h' }))
    expect(wrapper.find('[data-test="usage-range-not-applied-chip"]').exists()).toBe(false)
  })

  it('?from=-24h maps to window=24h', async () => {
    const { wrapper } = await mountUsageAt('/usage?from=-24h')
    expect(getActivityUsageMock).toHaveBeenCalledWith(expect.objectContaining({ window: '24h' }))
    expect(wrapper.find('[data-test="usage-window-24h"]').classes()).toContain('btn-primary')
    expect(wrapper.find('[data-test="usage-range-not-applied-chip"]').exists()).toBe(false)
  })

  it('?from=-7d maps to window=7d', async () => {
    const { wrapper } = await mountUsageAt('/usage?from=-7d')
    expect(getActivityUsageMock).toHaveBeenCalledWith(expect.objectContaining({ window: '7d' }))
    expect(wrapper.find('[data-test="usage-window-7d"]').classes()).toContain('btn-primary')
    expect(wrapper.find('[data-test="usage-range-not-applied-chip"]').exists()).toBe(false)
  })

  it('?from=-3d (not one of the three presets) issues window=all, never the backend\'s silent 24h default, and shows the disabled chip', async () => {
    const { wrapper } = await mountUsageAt('/usage?from=-3d')
    expect(getActivityUsageMock).toHaveBeenCalledWith(expect.objectContaining({ window: 'all' }))
    expect(wrapper.find('[data-test="usage-window-all"]').classes()).toContain('btn-primary')
    expect(wrapper.find('[data-test="usage-range-not-applied-chip"]').exists()).toBe(true)
  })

  it('an explicit ?to= (never one of the three presets) also issues window=all with the chip shown', async () => {
    const { wrapper } = await mountUsageAt('/usage?from=-24h&to=2026-01-01T00:00:00Z')
    expect(getActivityUsageMock).toHaveBeenCalledWith(expect.objectContaining({ window: 'all' }))
    expect(wrapper.find('[data-test="usage-range-not-applied-chip"]').exists()).toBe(true)
  })

  it('a CallHistogram bar click links to Activity with the active window\'s from + the clicked tool', async () => {
    const { wrapper, router } = await mountUsageAt('/usage?from=-7d')
    await flushPromises()
    // CallHistogram emits 'select-tool' on a bar click (usage-chart-bar-links.spec.ts
    // exercises chart.js's own onClick -> emit path in isolation); driving the
    // emit directly here is what actually exercises Usage.vue's onSelectTool.
    const callHistogram = wrapper.findComponent(CallHistogram)
    expect(callHistogram.exists()).toBe(true)
    callHistogram.vm.$emit('select-tool', { server: 'filesystem', tool: 'read', calls: 3, errors: 0 })
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toContain('/activity')
    expect(router.currentRoute.value.query).toEqual(
      expect.objectContaining({ view: 'calls', tool: 'filesystem:read', from: '-7d' })
    )
  })
})

// zcode review round 1, F1: a contradictory `?server=`/`?tool=` pair (rule 8
// of url-filter-contract.md) has no REST request that can express both — the
// page must issue no request and show the conflict empty state, never fall
// back to the window's unfiltered aggregate.
describe('Usage — contradictory server/tool (Spec 109-k rule 8, zcode F1)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('a disagreeing server/tool pair issues no request and shows the conflict empty state', async () => {
    const { wrapper } = await mountUsageAt('/usage?server=notes&tool=github:search')
    expect(getActivityUsageMock).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="usage-conflict-empty-state"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="usage-empty-state"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="usage-charts"]').exists()).toBe(false)
  })

  it('an agreeing server/tool pair (same server) still issues the scoped request', async () => {
    const { wrapper } = await mountUsageAt('/usage?server=filesystem&tool=filesystem:read')
    expect(getActivityUsageMock).toHaveBeenCalledWith(
      expect.objectContaining({ server: 'filesystem', tool: 'read' })
    )
    expect(wrapper.find('[data-test="usage-conflict-empty-state"]').exists()).toBe(false)
  })
})
