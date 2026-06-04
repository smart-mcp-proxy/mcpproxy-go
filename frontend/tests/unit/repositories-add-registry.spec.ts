import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import Repositories from '@/views/Repositories.vue'
import api from '@/services/api'

// MCP-867 + MCP-1073: the Repositories view exposes an add-registry affordance
// and surfaces provenance neutrally. MCP-1073 removed the alarming one-time
// third-party warning gate and the "always quarantined" copy — adding a custom
// source now goes straight through, and trust is surfaced as a neutral
// Official/Custom badge.

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

// Stub the hints child so we don't drag its dependencies into the mount.
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
  provenance: 'official',
  trusted: true,
}

const customRegistry = {
  id: 'acme',
  name: 'Acme Registry',
  description: 'A custom source',
  url: 'https://acme.example/registry',
  provenance: 'custom',
  trusted: false,
}

describe('Repositories — add registry + neutral provenance (MCP-867/MCP-1073)', () => {
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
    ;(api.addRegistrySource as any).mockReset()
  })

  it('renders an Add Registry affordance', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.find('[data-test="registry-add-source-button"]').exists()).toBe(true)
  })

  it('surfaces trust neutrally: custom flagged "custom" in the multiselect, official without it; no alarming banner', async () => {
    const wrapper = mountView()
    await flushPromises()

    const acmeOpt = wrapper.find('[data-test="registry-option-acme"]')
    const officialOpt = wrapper.find('[data-test="registry-option-official"]')
    expect(acmeOpt.exists()).toBe(true)
    expect(officialOpt.exists()).toBe(true)
    expect((acmeOpt.element as HTMLElement).closest('label')?.textContent).toContain('custom')
    expect((officialOpt.element as HTMLElement).closest('label')?.textContent).not.toContain('custom')

    // No alarming copy anywhere, and the old warning gate is gone.
    expect(wrapper.find('[data-test="registry-third-party-warning"]').exists()).toBe(false)
    expect(wrapper.html().toLowerCase()).not.toContain('unverified')
    expect(wrapper.html().toLowerCase()).not.toContain('always quarantined')
  })

  it('multiselect: toggling a registry searches it; selecting a second searches across both (R1)', async () => {
    const wrapper = mountView()
    await flushPromises()
    ;(api.searchRegistryServers as any).mockClear()

    await wrapper.find('[data-test="registry-option-official"]').setValue(true)
    await flushPromises()
    await wrapper.find('[data-test="registry-option-acme"]').setValue(true)
    await flushPromises()

    const searched = (api.searchRegistryServers as any).mock.calls.map((c: any[]) => c[0])
    expect(searched).toContain('official')
    expect(searched).toContain('acme')
  })

  it('adds a custom source directly — no warning gate, no acknowledgement persisted', async () => {
    ;(api.addRegistrySource as any).mockResolvedValue({
      success: true,
      registry: { id: 'acme', name: 'Acme Registry', provenance: 'custom', trusted: false },
    })

    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="registry-add-source-button"]').trigger('click')
    await wrapper.find('[data-test="registry-add-url-input"]').setValue('https://acme.example/registry')
    await wrapper.find('[data-test="registry-add-form"]').trigger('submit')
    await flushPromises()

    // The add goes straight through; no warning dialog, no localStorage ack.
    expect(api.addRegistrySource).toHaveBeenCalledTimes(1)
    expect(api.addRegistrySource).toHaveBeenCalledWith('https://acme.example/registry', {
      protocol: 'modelcontextprotocol/registry',
      name: undefined,
    })
    expect(localStorage.getItem('mcpproxy-thirdparty-registry-ack')).toBe(null)
    expect(wrapper.find('[data-test="registry-third-party-warning"]').exists()).toBe(false)
  })

  it('surfaces a friendly message for a locked-registries error', async () => {
    ;(api.addRegistrySource as any).mockResolvedValue({
      success: false,
      code: 'registries_locked',
      error: 'locked',
    })

    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="registry-add-source-button"]').trigger('click')
    await wrapper.find('[data-test="registry-add-url-input"]').setValue('https://acme.example/registry')
    await wrapper.find('[data-test="registry-add-form"]').trigger('submit')
    await flushPromises()

    const err = wrapper.find('[data-test="registry-add-error"]')
    expect(err.exists()).toBe(true)
    expect(err.text().toLowerCase()).toContain('locked')
  })
})
