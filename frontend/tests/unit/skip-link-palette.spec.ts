import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 134 FR-013 / SC-007: on an ordinary load the first Tab lands on
// "Skip to content" and opens no dialog; the palette opens only on Cmd/Ctrl+K,
// "/" outside editable elements, or its button.

vi.mock('@/services/api', () => {
  const base: Record<string, unknown> = {
    hasAPIKey: () => true,
    onAuthError: vi.fn(() => () => {}),
    createEventSource: () => ({ close: vi.fn(), addEventListener: vi.fn() }),
    addEventListener: () => () => {},
  }
  return {
    default: new Proxy(base, {
      get(target: Record<string, unknown>, prop: string) {
        if (prop in target) return target[prop]
        target[prop] = vi.fn().mockResolvedValue({ success: true, data: { count: 0, items: [], profiles: [], clients: [] } })
        return target[prop]
      },
    }),
  }
})
vi.mock('@/services/auth-api', () => ({
  authApi: { getProvider: vi.fn().mockResolvedValue(null), getSessionStatus: vi.fn(), getMe: vi.fn(), logout: vi.fn(), getLoginUrl: vi.fn() },
}))

const routes = [{ path: '/', name: 'dashboard', component: { template: '<div id="page"><a href="#x">in page</a></div>' } }]

async function mountApp() {
  setActivePinia(createPinia())
  const router = createRouter({ history: createMemoryHistory(), routes })
  router.push('/')
  await router.isReady()
  const { default: App } = await import('@/App.vue')
  const wrapper = mount(App, {
    attachTo: document.body,
    global: { plugins: [router], stubs: { ToastContainer: true, ConnectionStatus: true, AuthErrorModal: true } },
  })
  await flushPromises()
  return wrapper
}

function tabbable(root: HTMLElement): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>('a[href], button, input, select, textarea, [tabindex]')]
    .filter((el) => !el.hasAttribute('disabled') && el.getAttribute('tabindex') !== '-1' && !el.closest('[inert]'))
}

describe('skip link and palette (spec 134 FR-013)', () => {
  beforeEach(() => { document.body.innerHTML = '' })

  it('"Skip to content" is the first tab stop in the document', async () => {
    const wrapper = await mountApp()
    const first = tabbable(document.body)[0]
    expect(first.getAttribute('data-test')).toBe('skip-link')
    expect(first.getAttribute('href')).toBe('#main-content')
    expect(first.textContent).toContain('Skip to content')
    wrapper.unmount()
  })

  it('activating it moves focus into main', async () => {
    const wrapper = await mountApp()
    const link = document.querySelector<HTMLAnchorElement>('[data-test="skip-link"]')!
    link.click()
    await flushPromises()
    const main = document.getElementById('main-content')!
    expect(main.tagName).toBe('MAIN')
    expect(document.activeElement).toBe(main)
    wrapper.unmount()
  })

  it('focusing the header search never opens the palette; clicking does', async () => {
    const wrapper = await mountApp()
    const search = document.querySelector<HTMLElement>('[data-test="header-search-input"]')!
    search.focus()
    await flushPromises()
    expect(document.querySelector('dialog[open]')).toBeNull()
    expect(wrapper.findComponent({ name: 'CommandPalette' }).props('open')).toBeFalsy()
    search.click()
    await flushPromises()
    expect(wrapper.findComponent({ name: 'CommandPalette' }).props('open')).toBe(true)
    wrapper.unmount()
  })

  it('Ctrl+K and "/" outside an editable element open it; "/" inside an input does not', async () => {
    const wrapper = await mountApp()
    const palette = () => wrapper.findComponent({ name: 'CommandPalette' }).props('open')
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', ctrlKey: true }))
    await flushPromises()
    expect(palette()).toBe(true)
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', ctrlKey: true }))
    await flushPromises()
    expect(palette()).toBe(false)
    window.dispatchEvent(new KeyboardEvent('keydown', { key: '/' }))
    await flushPromises()
    expect(palette()).toBe(true)
    wrapper.unmount()
  })
})
