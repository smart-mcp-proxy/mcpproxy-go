import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import Repositories from '@/views/Repositories.vue'
import api from '@/services/api'

vi.mock('@/services/api', () => ({
  default: {
    listRegistries: vi.fn(),
    searchRegistryServers: vi.fn(),
    addRegistrySource: vi.fn(),
    editRegistrySource: vi.fn(),
    removeRegistrySource: vi.fn(),
    addServerFromRegistry: vi.fn(),
  },
}))

const globalStubs = { CollapsibleHintsPanel: { template: '<div />' } }

function mountView() {
  return mount(Repositories, { global: { plugins: [createPinia()], stubs: globalStubs } })
}

const officialRegistry = {
  id: 'official',
  name: 'Official MCP Registry',
  description: 'The official registry',
  url: 'https://registry.modelcontextprotocol.io/',
  provenance: 'official',
  trusted: true,
}

const customRegistry = {
  id: 'acme',
  name: 'Acme Registry',
  description: 'A custom source',
  url: 'https://acme.example/registry',
  servers_url: 'https://acme.example/registry/v0.1/servers',
  provenance: 'custom',
  trusted: false,
}

/**
 * Audit F17: three prominent registry cards sat directly above a "Choose
 * registries…" dropdown and a disabled search box, while the body said "Select a
 * Registry". The cards looked exactly like the selector and were not clickable —
 * a false affordance. They are the selector now.
 */
describe('Repositories — registry cards are the selector (audit F17)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    ;(api.listRegistries as any).mockResolvedValue({
      success: true,
      data: { registries: [officialRegistry, customRegistry], total: 2 },
    })
    ;(api.searchRegistryServers as any).mockResolvedValue({
      success: true,
      data: { registry_id: 'official', servers: [], total: 0 },
    })
  })

  it('starts with nothing selected and says how to enable search', async () => {
    const wrapper = mountView()
    await flushPromises()

    const card = wrapper.find('[data-test="registry-card-official"]')
    expect(card.attributes('aria-pressed')).toBe('false')
    expect(card.attributes('role')).toBe('button')

    expect(wrapper.find('[data-test="registry-search-input"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-test="registry-search-hint"]').text())
      .toContain('Click a registry card above')
  })

  it('selects a registry when its card is clicked, and shows the selected state', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="registry-card-official"]').trigger('click')
    await flushPromises()

    const card = wrapper.find('[data-test="registry-card-official"]')
    expect(card.attributes('aria-pressed')).toBe('true')
    expect(wrapper.find('[data-test="registry-card-selected-official"]').exists()).toBe(true)

    // The search box and button come alive, and the hint retires.
    expect(wrapper.find('[data-test="registry-search-input"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.find('[data-test="registry-search-button"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.find('[data-test="registry-search-hint"]').exists()).toBe(false)
  })

  it('is keyboard-operable', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="registry-card-acme"]').trigger('keydown.enter')
    await flushPromises()

    expect(wrapper.find('[data-test="registry-card-acme"]').attributes('aria-pressed')).toBe('true')
  })

  it('toggles a selected card off again', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="registry-card-official"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="registry-card-official"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="registry-card-official"]').attributes('aria-pressed')).toBe('false')
  })

  it('managing a custom source does not double as selecting it', async () => {
    const wrapper = mountView()
    await flushPromises()

    // The kebab lives inside the card; its click must not reach the card.
    await wrapper.find('[data-test="registry-kebab-acme"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="registry-card-acme"]').attributes('aria-pressed')).toBe('false')
  })
})
