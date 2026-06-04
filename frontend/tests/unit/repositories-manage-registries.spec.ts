import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import Repositories from '@/views/Repositories.vue'
import api from '@/services/api'

// MCP-1073: the Repositories view manages registry *sources* as cards.
// - Custom registries get a kebab (⋮) menu with Edit + Delete.
// - Official (built-in) registries are read-only: no kebab, a neutral
//   "Built-in" tag, and a neutral Official/Custom badge (no alarming copy).
// - Edit reuses the Add-Registry dialog, pre-filled, with a read-only id.
// - Delete shows a destructive confirmation modal before calling the API.

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
  servers_url: 'https://acme.example/registry/v0.1/servers',
  provenance: 'custom',
  trusted: false,
}

describe('Repositories — manage registry sources (MCP-1073)', () => {
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
    ;(api.editRegistrySource as any).mockReset()
    ;(api.removeRegistrySource as any).mockReset()
  })

  it('renders a manageable card per registry with a neutral Official/Custom badge', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-test="registry-card-official"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="registry-card-acme"]').exists()).toBe(true)

    // Neutral badge — no alarming words anywhere on the official card.
    const officialCard = wrapper.find('[data-test="registry-card-official"]')
    expect(officialCard.text()).toContain('Official')
    expect(officialCard.text().toLowerCase()).not.toContain('quarantine')
    expect(officialCard.text().toLowerCase()).not.toContain('unverified')

    const customCard = wrapper.find('[data-test="registry-card-acme"]')
    expect(customCard.text()).toContain('Custom')
  })

  it('shows no alarming warning banner or one-time third-party warning gate', async () => {
    const wrapper = mountView()
    await flushPromises()

    // The old third-party warning dialog and custom-quarantine note are gone.
    expect(wrapper.find('[data-test="registry-third-party-warning"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="registry-custom-quarantine-note"]').exists()).toBe(false)
    expect(wrapper.html().toLowerCase()).not.toContain('always quarantined')
    expect(wrapper.html().toLowerCase()).not.toContain('can never skip')
  })

  it('exposes a kebab Edit/Delete only on custom registries; official is read-only', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-test="registry-kebab-acme"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="registry-edit-acme"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="registry-delete-acme"]').exists()).toBe(true)

    // Official: no kebab, no edit/delete, has a "Built-in" tag.
    expect(wrapper.find('[data-test="registry-kebab-official"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="registry-edit-official"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="registry-delete-official"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="registry-card-official"]').text()).toContain('Built-in')
  })

  it('Edit opens the dialog pre-filled with a read-only id and PUTs the changes', async () => {
    ;(api.editRegistrySource as any).mockResolvedValue({
      success: true,
      registry: { id: 'acme', name: 'Acme Renamed', provenance: 'custom', trusted: false },
    })

    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="registry-edit-acme"]').trigger('click')
    await flushPromises()

    // Dialog open + pre-filled.
    expect((wrapper.find('[data-test="registry-add-source-dialog"]').element as HTMLDialogElement).hasAttribute('open')).toBe(true)
    const urlInput = wrapper.find('[data-test="registry-add-url-input"]').element as HTMLInputElement
    const nameInput = wrapper.find('[data-test="registry-add-name-input"]').element as HTMLInputElement
    expect(urlInput.value).toBe('https://acme.example/registry')
    expect(nameInput.value).toBe('Acme Registry')

    // The id is shown read-only in edit mode.
    const idInput = wrapper.find('[data-test="registry-edit-id"]')
    expect(idInput.exists()).toBe(true)
    expect((idInput.element as HTMLInputElement).readOnly).toBe(true)

    // Change the name and submit → PUT, not POST.
    await wrapper.find('[data-test="registry-add-name-input"]').setValue('Acme Renamed')
    await wrapper.find('[data-test="registry-add-form"]').trigger('submit')
    await flushPromises()

    expect(api.editRegistrySource).toHaveBeenCalledTimes(1)
    expect(api.addRegistrySource).not.toHaveBeenCalled()
    const [id, opts] = (api.editRegistrySource as any).mock.calls[0]
    expect(id).toBe('acme')
    expect(opts.name).toBe('Acme Renamed')
  })

  it('Delete shows a destructive confirmation modal and only calls the API on confirm', async () => {
    ;(api.removeRegistrySource as any).mockResolvedValue({ success: true })

    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="registry-delete-acme"]').trigger('click')
    await flushPromises()

    const confirm = wrapper.find('[data-test="registry-delete-dialog"]')
    expect(confirm.exists()).toBe(true)
    expect((confirm.element as HTMLDialogElement).hasAttribute('open')).toBe(true)
    expect(api.removeRegistrySource).not.toHaveBeenCalled()

    await wrapper.find('[data-test="registry-delete-confirm"]').trigger('click')
    await flushPromises()

    expect(api.removeRegistrySource).toHaveBeenCalledWith('acme')
  })
})
