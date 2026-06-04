import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import Repositories from '@/views/Repositories.vue'
import api from '@/services/api'

// MCP-867: the Repositories view gains an add-registry affordance, provenance
// surfacing, and a one-time third-party warning. These tests lock the gating
// (warning shown before the first custom add; skipped after acknowledgement)
// and the provenance badge selection.

vi.mock('@/services/api', () => ({
  default: {
    listRegistries: vi.fn(),
    searchRegistryServers: vi.fn(),
    addRegistrySource: vi.fn(),
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

describe('Repositories — add registry + provenance + third-party warning', () => {
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

  it('flags a custom registry as unverified in the multiselect and an official one without that suffix', async () => {
    // R4: provenance banner removed; trust surfaced inline in the registry
    // multiselect (R1) — the option label carries "— unverified" for
    // third-party registries. R1: it is a multiselect (checkboxes), not a
    // single <select>.
    const wrapper = mountView()
    await flushPromises()

    const acmeOpt = wrapper.find('[data-test="registry-option-acme"]')
    const officialOpt = wrapper.find('[data-test="registry-option-official"]')
    expect(acmeOpt.exists()).toBe(true)
    expect(officialOpt.exists()).toBe(true)
    expect((acmeOpt.element as HTMLElement).closest('label')?.textContent).toContain('unverified')
    expect((officialOpt.element as HTMLElement).closest('label')?.textContent).not.toContain('unverified')

    // The old prominent banner / quarantine-note block is gone.
    expect(wrapper.find('[data-test="registry-provenance-badge-custom"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="registry-custom-quarantine-note"]').exists()).toBe(false)
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

  it('shows the one-time third-party warning before the first add and does NOT call the API yet', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="registry-add-source-button"]').trigger('click')
    await wrapper.find('[data-test="registry-add-url-input"]').setValue('https://acme.example/registry')
    await wrapper.find('[data-test="registry-add-form"]').trigger('submit')
    await flushPromises()

    // Warning is shown; the add request has not gone out.
    expect((wrapper.find('[data-test="registry-third-party-warning"]').element as HTMLDialogElement).hasAttribute('open')).toBe(true)
    expect(api.addRegistrySource).not.toHaveBeenCalled()
  })

  it('proceeds with the add after acknowledging, and persists acknowledgement so the warning is skipped next time', async () => {
    ;(api.addRegistrySource as any).mockResolvedValue({
      success: true,
      registry: { id: 'acme', name: 'Acme Registry', provenance: 'custom/unverified', trusted: false },
    })

    const wrapper = mountView()
    await flushPromises()

    // First add → warning → acknowledge → API called once.
    await wrapper.find('[data-test="registry-add-source-button"]').trigger('click')
    await wrapper.find('[data-test="registry-add-url-input"]').setValue('https://acme.example/registry')
    await wrapper.find('[data-test="registry-add-form"]').trigger('submit')
    await flushPromises()
    await wrapper.find('[data-test="registry-third-party-acknowledge"]').trigger('click')
    await flushPromises()

    expect(api.addRegistrySource).toHaveBeenCalledTimes(1)
    expect(api.addRegistrySource).toHaveBeenCalledWith('https://acme.example/registry', {
      protocol: 'modelcontextprotocol/registry',
      name: undefined,
    })
    expect(localStorage.getItem('mcpproxy-thirdparty-registry-ack')).toBe('true')

    // Second add → no warning, API called directly.
    await wrapper.find('[data-test="registry-add-source-button"]').trigger('click')
    await wrapper.find('[data-test="registry-add-url-input"]').setValue('https://other.example/registry')
    await wrapper.find('[data-test="registry-add-form"]').trigger('submit')
    await flushPromises()

    expect((wrapper.find('[data-test="registry-third-party-warning"]').element as HTMLDialogElement).hasAttribute('open')).toBe(false)
    expect(api.addRegistrySource).toHaveBeenCalledTimes(2)
  })

  it('surfaces a friendly message for a locked-registries error', async () => {
    localStorage.setItem('mcpproxy-thirdparty-registry-ack', 'true')
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
