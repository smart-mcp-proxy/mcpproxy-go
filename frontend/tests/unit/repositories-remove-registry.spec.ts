import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import Repositories from '@/views/Repositories.vue'
import api from '@/services/api'

// MCP-1064: the Repositories view gains a Remove affordance for
// custom/unverified registries (the matching front-end for the MCP-1057
// DELETE /api/v1/registries/{id} backend path). Built-in official/trusted
// registries must NOT offer removal. Removal is confirmed first, refreshes the
// list on success, and maps the cross-surface error codes to friendly text.

vi.mock('@/services/api', () => ({
  default: {
    listRegistries: vi.fn(),
    searchRegistryServers: vi.fn(),
    addRegistrySource: vi.fn(),
    addServerFromRegistry: vi.fn(),
    removeRegistrySource: vi.fn(),
  },
}))

const globalStubs = {
  CollapsibleHintsPanel: { template: '<div />' },
}

function mountView() {
  return mount(Repositories, {
    global: { plugins: [createPinia()], stubs: globalStubs },
  })
}

const officialRegistry = {
  id: 'official',
  name: 'Official MCP Registry',
  description: 'The official registry',
  url: 'https://registry.modelcontextprotocol.io/',
  provenance: 'official/trusted',
  trusted: true,
}

const customRegistry = {
  id: 'acme',
  name: 'Acme Registry',
  description: 'A custom source',
  url: 'https://acme.example/registry',
  provenance: 'custom/unverified',
  trusted: false,
}

describe('Repositories — remove custom registry (MCP-1064)', () => {
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
    ;(api.removeRegistrySource as any).mockReset()
  })

  it('offers a Remove action on a custom registry but NOT on an official one', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-test="registry-remove-acme"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="registry-remove-official"]').exists()).toBe(false)
  })

  it('confirms before removing: clicking Remove opens a dialog and does NOT call the API yet', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="registry-remove-acme"]').trigger('click')
    await flushPromises()

    expect((wrapper.find('[data-test="registry-remove-dialog"]').element as HTMLDialogElement).hasAttribute('open')).toBe(true)
    expect(api.removeRegistrySource).not.toHaveBeenCalled()
  })

  it('removes the registry on confirm, calls the API with the id, and refreshes the list', async () => {
    ;(api.removeRegistrySource as any).mockResolvedValue({
      success: true,
      registry: { id: 'acme', name: 'Acme Registry', provenance: 'custom/unverified', trusted: false },
    })

    const wrapper = mountView()
    await flushPromises()
    ;(api.listRegistries as any).mockClear()

    await wrapper.find('[data-test="registry-remove-acme"]').trigger('click')
    await wrapper.find('[data-test="registry-remove-confirm"]').trigger('click')
    await flushPromises()

    expect(api.removeRegistrySource).toHaveBeenCalledTimes(1)
    expect(api.removeRegistrySource).toHaveBeenCalledWith('acme')
    // List is refreshed so the removed entry disappears.
    expect(api.listRegistries).toHaveBeenCalledTimes(1)
    // Dialog closed.
    expect((wrapper.find('[data-test="registry-remove-dialog"]').element as HTMLDialogElement).hasAttribute('open')).toBe(false)
  })

  it('maps registry_shadows_builtin to a built-in-cannot-be-removed message', async () => {
    ;(api.removeRegistrySource as any).mockResolvedValue({
      success: false,
      code: 'registry_shadows_builtin',
      error: 'built-in',
    })

    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="registry-remove-acme"]').trigger('click')
    await wrapper.find('[data-test="registry-remove-confirm"]').trigger('click')
    await flushPromises()

    const err = wrapper.find('[data-test="registry-remove-error"]')
    expect(err.exists()).toBe(true)
    expect(err.text().toLowerCase()).toContain('built-in')
  })

  it('maps registries_locked to a friendly locked message', async () => {
    ;(api.removeRegistrySource as any).mockResolvedValue({
      success: false,
      code: 'registries_locked',
      error: 'locked',
    })

    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="registry-remove-acme"]').trigger('click')
    await wrapper.find('[data-test="registry-remove-confirm"]').trigger('click')
    await flushPromises()

    const err = wrapper.find('[data-test="registry-remove-error"]')
    expect(err.exists()).toBe(true)
    expect(err.text().toLowerCase()).toContain('locked')
  })
})
