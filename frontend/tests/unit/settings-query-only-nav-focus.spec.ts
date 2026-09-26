import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 109 PR-a review round 9 finding (medium): Settings.vue's FR-016
// `?tab=` sync and the pre-existing `?focus=` deep link were both applied
// only in onMounted. The header's ModeSwitcher (rendered on every page,
// including /settings) links to `/settings?focus=routing_mode`; clicking it
// while already on /settings is a query-only navigation to the SAME route
// component, which Vue Router (and App.vue's <router-view>, keyed on the
// auth epoch rather than the route) reuses instead of remounting —
// onMounted never reruns, so the click did nothing. Same class of bug this
// PR already fixed for ServerDetail.vue (round-8 finding) via a route-query
// watcher calling readTabFromQuery() from a serverName watch.

const emptyConfig = vi.hoisted(() => ({ server_edition: { enabled: false } }))

vi.mock('@/services/api', () => ({
  default: {
    getConfig: vi.fn().mockResolvedValue({ success: true, data: { config: emptyConfig } }),
    getStatus: vi.fn().mockResolvedValue({ success: true, data: {} }),
    validateConfig: vi.fn(),
    applyConfig: vi.fn(),
  },
}))

import Settings from '@/views/Settings.vue'

function makeRouter(path: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/settings', name: 'settings', component: Settings, meta: { title: 'Settings' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div/>' } },
    ],
  })
  router.push(path)
  return router
}

async function mountSettings(path: string) {
  const router = makeRouter(path)
  await router.isReady()
  const wrapper = mount(Settings, {
    global: {
      plugins: [router],
      stubs: { VueMonacoEditor: true, ConnectModal: true },
    },
  })
  return { wrapper, router }
}

describe('Settings reacts to a query-only navigation while already mounted (review round 9)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('switches tab and applies ?focus= when the query changes without a remount', async () => {
    const { wrapper, router } = await mountSettings('/settings')
    await flushPromises()

    // Precondition: default tab is Security, not General (routing_mode lives
    // in the General section).
    expect(wrapper.find('[data-test="settings-tab-security"]').classes()).toContain('tab-active')

    // Simulate the ModeSwitcher RouterLink click while already on /settings:
    // same route component, query-only change.
    await router.push('/settings?focus=routing_mode')
    await flushPromises()
    await flushPromises()

    expect(wrapper.find('[data-test="settings-tab-general"]').classes()).toContain('tab-active')
    expect(wrapper.find('[data-test="setting-row-routing_mode"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('switches tab when ?tab= changes via a query-only navigation', async () => {
    const { wrapper, router } = await mountSettings('/settings')
    await flushPromises()

    expect(wrapper.find('[data-test="settings-tab-security"]').classes()).toContain('tab-active')

    await router.push('/settings?tab=raw')
    await flushPromises()

    expect(wrapper.find('[data-test="settings-tab-raw"]').classes()).toContain('tab-active')
    wrapper.unmount()
  })

  // zcode review finding on the fix above: comparing only `focus !==
  // prevFocus` left a REPEAT click of the same ModeSwitcher link a dead
  // click once the user had manually switched to a different tab in
  // between — the exact class of bug this fix targets, just one click
  // later. ModeSwitcher's RouterLink is a bare `/settings?focus=...` with
  // no `tab`, so following it always replaces the whole query (`tab`
  // reverts to undefined), unlike watch(activeTab)'s own `{ ...route.query,
  // tab }` writeback, which never unsets `tab`.
  it('re-applies ?focus= on a repeat click after the user manually switched tabs (same focus value)', async () => {
    const { wrapper, router } = await mountSettings('/settings')
    await flushPromises()

    // First click: lands on General (routing_mode's home tab).
    await router.push('/settings?focus=routing_mode')
    await flushPromises()
    await flushPromises()
    expect(wrapper.find('[data-test="settings-tab-general"]').classes()).toContain('tab-active')

    // User manually browses to Raw JSON — the URL keeps `focus=routing_mode`
    // (watch(activeTab) preserves other query params) but now also carries
    // `tab=raw`.
    await wrapper.find('[data-test="settings-tab-raw"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="settings-tab-raw"]').classes()).toContain('tab-active')
    expect(router.currentRoute.value.query.focus).toBe('routing_mode')

    // Re-clicking the SAME ModeSwitcher link: same `focus` value as before,
    // but it must still land back on General.
    await router.push('/settings?focus=routing_mode')
    await flushPromises()
    await flushPromises()

    expect(wrapper.find('[data-test="settings-tab-general"]').classes()).toContain('tab-active')
    wrapper.unmount()
  })
})
