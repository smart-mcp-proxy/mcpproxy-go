import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 134 FR-014 / FR-013: below 768 px (phone widths, where the sidebar is
// an overlay) the menu has a real button, Escape closes it and returns focus,
// and while closed nothing inside it is focusable. Theme choices are buttons.

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
        target[prop] = vi.fn().mockResolvedValue({ success: true, data: { count: 0, items: [], profiles: [], clients: [], stats: { total: 0 } } })
        return target[prop]
      },
    }),
  }
})
vi.mock('@/services/auth-api', () => ({
  authApi: { getProvider: vi.fn().mockResolvedValue(null), getSessionStatus: vi.fn(), getMe: vi.fn(), logout: vi.fn(), getLoginUrl: vi.fn() },
}))

function phone(matches: boolean) {
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches, media: query, addEventListener: vi.fn(), removeEventListener: vi.fn(),
    addListener: vi.fn(), removeListener: vi.fn(), onchange: null, dispatchEvent: vi.fn(),
  })) as unknown as typeof window.matchMedia
}

async function mountApp() {
  setActivePinia(createPinia())
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', name: 'dashboard', component: { template: '<div />' } }] })
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

describe('phone overlay sidebar (spec 134 FR-014)', () => {
  beforeEach(() => { document.body.innerHTML = '' })

  it('the menu toggle is a button with aria-expanded and aria-controls', async () => {
    phone(true)
    const wrapper = await mountApp()
    const toggle = wrapper.get('[data-test="header-drawer-toggle"]')
    expect(toggle.element.tagName).toBe('BUTTON')
    expect(toggle.attributes('aria-expanded')).toBe('false')
    expect(toggle.attributes('aria-controls')).toBeTruthy()
    await toggle.trigger('click')
    expect(toggle.attributes('aria-expanded')).toBe('true')
    wrapper.unmount()
  })

  it('closed on a phone: the sidebar is inert (nothing focusable); open: it is not', async () => {
    phone(true)
    const wrapper = await mountApp()
    const side = wrapper.get('[data-test="sidebar-overlay"]')
    expect(side.attributes('inert')).toBeDefined()
    await wrapper.get('[data-test="header-drawer-toggle"]').trigger('click')
    expect(side.attributes('inert')).toBeUndefined()
    wrapper.unmount()
  })

  it('open on a phone: the page behind the overlay is inert so focus cannot leave the menu', async () => {
    phone(true)
    const wrapper = await mountApp()
    const content = wrapper.get('.drawer-content')
    expect(content.attributes('inert')).toBeUndefined()
    await wrapper.get('[data-test="header-drawer-toggle"]').trigger('click')
    expect(content.attributes('inert')).toBeDefined()
    wrapper.unmount()
  })

  it('on desktop widths the sidebar is never inert', async () => {
    phone(false)
    const wrapper = await mountApp()
    expect(wrapper.get('[data-test="sidebar-overlay"]').attributes('inert')).toBeUndefined()
    wrapper.unmount()
  })

  it('Escape closes the menu and returns focus to the toggle', async () => {
    phone(true)
    const wrapper = await mountApp()
    const toggle = wrapper.get('[data-test="header-drawer-toggle"]')
    ;(toggle.element as HTMLElement).focus()
    await toggle.trigger('click')
    await flushPromises()
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await flushPromises()
    expect(toggle.attributes('aria-expanded')).toBe('false')
    expect(document.activeElement).toBe(toggle.element)
    wrapper.unmount()
  })

  it('an Escape already handled by a layer above (palette) does not also close the menu', async () => {
    phone(true)
    const wrapper = await mountApp()
    const toggle = wrapper.get('[data-test="header-drawer-toggle"]')
    await toggle.trigger('click')
    const handled = new KeyboardEvent('keydown', { key: 'Escape', cancelable: true })
    handled.preventDefault()
    window.dispatchEvent(handled)
    await flushPromises()
    expect(toggle.attributes('aria-expanded')).toBe('true')
    expect(wrapper.get('[data-test="skip-link"]').attributes('inert')).toBeDefined()
    wrapper.unmount()
  })

  it('theme choices are buttons that a keyboard can activate', async () => {
    phone(false)
    const wrapper = await mountApp()
    const system = wrapper.get('[data-test="theme-option-system"]')
    expect(system.element.tagName).toBe('BUTTON')
    const dark = wrapper.get('[data-test="theme-option-dark"]')
    expect(dark.element.tagName).toBe('BUTTON')
    wrapper.unmount()
  })
})
