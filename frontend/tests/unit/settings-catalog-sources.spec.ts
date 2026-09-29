import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia } from 'pinia'
import CatalogSourcesSettings from '@/components/CatalogSourcesSettings.vue'
import api from '@/services/api'

// Spec 109 FR-062: catalog-source management moved from the retired
// Repositories.vue into Settings → Catalog sources. This ports the essential
// add/edit/delete coverage the old repositories-add-registry.spec.ts and
// repositories-manage-registries.spec.ts pinned.

vi.mock('@/services/api', () => ({
  default: {
    listRegistries: vi.fn(),
    addRegistrySource: vi.fn(),
    editRegistrySource: vi.fn(),
    removeRegistrySource: vi.fn(),
  },
}))

const officialRegistry = {
  id: 'official',
  name: 'Official MCP Registry',
  url: 'https://registry.modelcontextprotocol.io/',
  provenance: 'official',
  trusted: true,
}

const customRegistry = {
  id: 'my-custom',
  name: 'My Custom Registry',
  url: 'https://example.com/registry',
  provenance: 'custom/unverified',
  trusted: false,
}

function mountView() {
  return mount(CatalogSourcesSettings, { global: { plugins: [createPinia()] } })
}

beforeEach(() => {
  vi.mocked(api.listRegistries).mockReset()
  vi.mocked(api.addRegistrySource).mockReset()
  vi.mocked(api.editRegistrySource).mockReset()
  vi.mocked(api.removeRegistrySource).mockReset()
  vi.mocked(api.listRegistries).mockResolvedValue({
    success: true,
    data: { registries: [officialRegistry, customRegistry], total: 2 },
  })
})

describe('CatalogSourcesSettings', () => {
  it('lists registries with Official/Custom badges, and only custom ones get a kebab menu', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-test="registry-provenance-official"]').text()).toBe('Official')
    expect(wrapper.find('[data-test="registry-builtin-official"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="registry-kebab-official"]').exists()).toBe(false)

    expect(wrapper.find('[data-test="registry-provenance-my-custom"]').text()).toBe('Custom')
    expect(wrapper.find('[data-test="registry-kebab-my-custom"]').exists()).toBe(true)
  })

  it('adds a custom registry source', async () => {
    vi.mocked(api.addRegistrySource).mockResolvedValue({ success: true, registry: { id: 'new-reg', name: 'New Registry' } })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="registry-add-source-button"]').trigger('click')
    await wrapper.find('[data-test="registry-add-url-input"]').setValue('https://new.example.com/')
    await wrapper.find('[data-test="registry-add-form"]').trigger('submit')
    await flushPromises()

    expect(api.addRegistrySource).toHaveBeenCalledWith('https://new.example.com/', expect.objectContaining({}))
    expect(wrapper.find('[data-test="registry-add-success"]').exists()).toBe(true)
  })

  it('shows the backend error message when adding fails', async () => {
    vi.mocked(api.addRegistrySource).mockResolvedValue({ success: false, code: 'invalid_registry_url', error: 'nope' })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="registry-add-source-button"]').trigger('click')
    await wrapper.find('[data-test="registry-add-url-input"]').setValue('not-a-url')
    await wrapper.find('[data-test="registry-add-form"]').trigger('submit')
    await flushPromises()

    expect(wrapper.find('[data-test="registry-add-error"]').exists()).toBe(true)
  })

  it('edits a custom registry via the kebab menu', async () => {
    vi.mocked(api.editRegistrySource).mockResolvedValue({ success: true, registry: { id: 'my-custom', name: 'Renamed' } })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="registry-edit-my-custom"]').trigger('click')
    expect(wrapper.find('[data-test="registry-edit-id"]').element.getAttribute('value')).toBe('my-custom')
    await wrapper.find('[data-test="registry-add-name-input"]').setValue('Renamed')
    await wrapper.find('[data-test="registry-add-form"]').trigger('submit')
    await flushPromises()

    expect(api.editRegistrySource).toHaveBeenCalledWith('my-custom', expect.objectContaining({ name: 'Renamed' }))
  })

  it('deletes a custom registry after confirmation', async () => {
    vi.mocked(api.removeRegistrySource).mockResolvedValue({ success: true })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.find('[data-test="registry-delete-my-custom"]').trigger('click')
    await wrapper.find('[data-test="registry-delete-confirm"]').trigger('click')
    await flushPromises()

    expect(api.removeRegistrySource).toHaveBeenCalledWith('my-custom')
  })

  it('links to the Add Server page for browsing, since this tab only manages sources', async () => {
    const wrapper = mount(CatalogSourcesSettings, {
      global: {
        plugins: [createPinia()],
        stubs: { RouterLink: { template: '<a :href="to"><slot /></a>', props: ['to'] } },
      },
    })
    await flushPromises()
    expect(wrapper.find('a[href="/add-server"]').exists()).toBe(true)
  })
})
