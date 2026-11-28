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

  async function mountLoaded() {
    const router = makeRouter('/settings')
    await router.isReady()
    const wrapper = mount(Settings, { global: { plugins: [router], stubs: { VueMonacoEditor: true } }, attachTo: document.body })
    gate.release({ success: true, data: { config: { server_edition: { enabled: false } } } })
    await flushPromises()
    return wrapper
  }

  it('the arrow-key focus callback does nothing once the page is left', async () => {
    const wrapper = await mountLoaded()
    const stray = document.createElement('button')
    stray.setAttribute('data-test', 'settings-tab-security')
    document.body.appendChild(stray)
    wrapper.get('[data-test="settings-tab-general"]').element.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true }))
    wrapper.unmount()
    await flushPromises()
    expect(document.activeElement).not.toBe(stray)
    stray.remove()
  })

  it('the arrow-key focus callback does nothing when the section changed again before it ran', async () => {
    const wrapper = await mountLoaded()
    const next = wrapper.get('[data-test="settings-tab-security"]').element as HTMLElement
    const advanced = wrapper.get('[data-test="settings-tab-advanced"]').element as HTMLElement
    wrapper.get('[data-test="settings-tab-general"]').element.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true }))
    advanced.click()
    await flushPromises()
    expect(document.activeElement).not.toBe(next)
    wrapper.unmount()
  })

  it('the section-change focus callback does nothing once the page is left', async () => {
    const wrapper = await mountLoaded()
    const stale = document.createElement('button')
    stale.setAttribute('role', 'tab')
    stale.setAttribute('data-tab-id', 'x')
    stale.setAttribute('aria-selected', 'false')
    document.body.appendChild(stale)
    stale.focus()
    const target = document.createElement('button')
    target.setAttribute('data-test', 'settings-tab-advanced')
    document.body.appendChild(target)
    ;(wrapper.get('[data-test="settings-tab-advanced"]').element as HTMLElement).click()
    // The watcher has run and queued its focus callback; the page is left before it fires.
    await Promise.resolve()
    wrapper.unmount()
    await flushPromises()
    expect(document.activeElement).toBe(stale)
    stale.remove()
    target.remove()
  })
})
