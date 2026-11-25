import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 134 FR-011 / FR-012 / SC-002: header = search button, online indicator,
// "N need attention" popover (every item's fix selectable, also the sixth),
// "Add server" next to the Add menu. Viewing no longer lives here (FR-016).

const attention = vi.hoisted(() => vi.fn())

vi.mock('@/services/api', () => {
  const base: Record<string, unknown> = {
    getAttention: attention,
    hasAPIKey: vi.fn(() => true),
    onAuthError: vi.fn(() => () => {}),
  }
  return {
    default: new Proxy(base, {
      get(target: Record<string, unknown>, prop: string) {
        if (prop in target) return target[prop]
        target[prop] = vi.fn().mockResolvedValue({ success: true, data: null })
        return target[prop]
      },
    }),
  }
})

import TopHeader from '@/components/TopHeader.vue'

const stub = { template: '<div />' }

function item(id: string) {
  return {
    id: `sign_in_required:server:${id}`, kind: 'sign_in_required', rank: 10,
    subject: { type: 'server', id, name: id }, summary: `${id}: sign in required`,
    fix: { verb: 'login', label: 'Sign in', target: `/servers/${id}` }, since: '2026-09-25T06:00:00Z',
  }
}

async function mountHeader() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: stub }, { path: '/servers/:n', component: stub }, { path: '/add-server', component: stub }],
  })
  router.push('/')
  await router.isReady()
  const pinia = createPinia()
  setActivePinia(pinia)
  const wrapper = mount(TopHeader, {
    attachTo: document.body,
    global: { plugins: [pinia, router], stubs: { HeaderStatus: true, AddMenu: true, CommandPalette: true } },
  })
  await flushPromises()
  return { wrapper, router }
}

describe('shell header (spec 134 FR-011)', () => {
  beforeEach(() => {
    attention.mockReset().mockResolvedValue({ success: true, data: { count: 0, items: [] } })
  })

  it('search is a real button, not a text field that opens on focus', async () => {
    const { wrapper } = await mountHeader()
    const search = wrapper.get('[data-test="header-search-input"]')
    expect(search.element.tagName).toBe('BUTTON')
    expect(search.attributes('aria-keyshortcuts')).toContain('Control+K')
    wrapper.unmount()
  })

  it('has no Viewing slot or filter (moved to page filter rows, FR-016)', async () => {
    const { wrapper } = await mountHeader()
    expect(wrapper.find('[data-test="header-viewing-slot"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('shows a direct "Add server" link to /add-server beside the Add menu', async () => {
    const { wrapper } = await mountHeader()
    const add = wrapper.get('[data-test="header-add-server"]')
    expect(add.attributes('href')).toBe('/add-server')
    expect(add.text()).toContain('Add server')
    expect(add.classes()).toContain('btn-primary')
    wrapper.unmount()
  })

  it('attention button says "N need attention" and the popover lists every item including the sixth', async () => {
    const items = Array.from({ length: 7 }, (_, i) => item(`s${i}`))
    attention.mockResolvedValue({ success: true, data: { count: 7, items } })
    const { wrapper } = await mountHeader()
    const button = wrapper.get('[data-test="header-attention-pill-button"]')
    expect(button.attributes('aria-label')).toBe('7 need attention')
    expect(button.attributes('aria-expanded')).toBe('false')
    await button.trigger('click')
    expect(button.attributes('aria-expanded')).toBe('true')
    const rows = wrapper.findAll('[data-test^="header-attention-item-"]')
    expect(rows).toHaveLength(7)
    expect(rows[5].attributes('href')).toBe('/servers/s5')
    expect(wrapper.find('[data-test="header-attention-see-all"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('closes the popover when focus leaves it, so the backdrop never stays up', async () => {
    attention.mockResolvedValue({ success: true, data: { count: 1, items: [item('a')] } })
    const { wrapper } = await mountHeader()
    await wrapper.get('[data-test="header-attention-pill-button"]').trigger('click')
    const outside = document.createElement('button')
    document.body.appendChild(outside)
    await wrapper.get('[data-test="header-attention-pill"]').trigger('focusout', { relatedTarget: outside })
    expect(wrapper.find('[data-test="header-attention-popover"]').exists()).toBe(false)
    outside.remove()
    wrapper.unmount()
  })

  it('Escape closes the popover and returns focus to the attention button', async () => {
    attention.mockResolvedValue({ success: true, data: { count: 1, items: [item('a')] } })
    const { wrapper } = await mountHeader()
    const button = wrapper.get('[data-test="header-attention-pill-button"]')
    await button.trigger('click')
    expect(wrapper.find('[data-test="header-attention-popover"]').exists()).toBe(true)
    await wrapper.get('[data-test="header-attention-popover"]').trigger('keydown', { key: 'Escape' })
    expect(wrapper.find('[data-test="header-attention-popover"]').exists()).toBe(false)
    expect(document.activeElement).toBe(button.element)
    wrapper.unmount()
  })
})
