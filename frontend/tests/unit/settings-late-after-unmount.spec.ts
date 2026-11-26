import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 134 PR 8, round 23: a Settings instance that has been left must not act
// when its config request (or a pending router write) finishes late. The focus
// link of the CURRENT route would otherwise be applied by the dead instance,
// and its `router.replace({ tab })` would overwrite the page the user is on.

const gate = vi.hoisted(() => ({ release: (() => {}) as (v: unknown) => void }))

vi.mock('@/services/api', () => ({
  default: {
    getConfig: vi.fn(
      () => new Promise((resolve) => { gate.release = resolve }),
    ),
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

describe('Settings: late continuation after unmount', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('a config load that finishes after unmount does not write ?tab= over the current page', async () => {
    const router = makeRouter('/settings?focus=routing_mode')
    await router.isReady()
    const wrapper = mount(Settings, { global: { plugins: [router], stubs: { VueMonacoEditor: true } } })
    await flushPromises()

    wrapper.unmount()
    await router.push('/settings?tab=advanced&focus=routing_mode')
    await router.push('/settings?focus=routing_mode')
    expect(router.currentRoute.value.query.tab).toBeUndefined()

    gate.release({ success: true, data: { config: { server_edition: { enabled: false } } } })
    await flushPromises()

    expect(router.currentRoute.value.query.tab).toBeUndefined()
  })
})
