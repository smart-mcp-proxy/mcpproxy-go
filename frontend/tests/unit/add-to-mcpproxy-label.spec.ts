import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import Repositories from '@/views/Repositories.vue'
import api from '@/services/api'

// Spec 109 FR-063: the add action reads "Add to MCPProxy" (Web/macOS; CLI
// prints "Added <name> to MCPProxy (quarantined for review)"), and after a
// successful add the same control becomes "Added ✓ · Open".

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

const officialRegistry = {
  id: 'official',
  name: 'Official MCP Registry',
  description: 'The official registry',
  url: 'https://registry.modelcontextprotocol.io/',
  provenance: 'official',
  trusted: true,
}

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/repositories', name: 'repositories', component: Repositories },
      { path: '/servers/:serverName', name: 'server-detail', component: { template: '<div/>' } },
    ],
  })
}

async function mountView() {
  const router = makeRouter()
  router.push('/repositories')
  await router.isReady()
  const wrapper = mount(Repositories, {
    global: { plugins: [createPinia(), router], stubs: globalStubs },
  })
  await flushPromises()
  return { wrapper, router }
}

describe('Repositories "Add to MCPProxy" (Spec 109 FR-063)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    ;(api.listRegistries as any).mockResolvedValue({
      success: true,
      data: { registries: [officialRegistry], total: 1 },
    })
    ;(api.searchRegistryServers as any).mockResolvedValue({
      success: true,
      data: {
        registry_id: 'official',
        servers: [{ id: 'gh-server', registry: 'official', name: 'github-server', description: 'GitHub tools' }],
        total: 1,
      },
    })
  })

  async function searchAndGetAddButton(wrapper: Awaited<ReturnType<typeof mountView>>['wrapper']) {
    await wrapper.find('[data-test="registry-card-official"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="registry-search-button"]').trigger('click')
    await flushPromises()
    return wrapper.find('[data-test="registry-add-gh-server"]')
  }

  it('reads "Add to MCPProxy" on the result button', async () => {
    const { wrapper } = await mountView()
    const button = await searchAndGetAddButton(wrapper)
    expect(button.exists()).toBe(true)
    expect(button.text()).toContain('Add to MCPProxy')
    expect(button.text()).not.toBe('Add to MCP')
  })

  it('flips to "Added ✓ · Open" after a successful add', async () => {
    ;(api.addServerFromRegistry as any).mockResolvedValue({
      success: true,
      server: { name: 'github-server' },
    })
    const { wrapper } = await mountView()
    const button = await searchAndGetAddButton(wrapper)

    await button.trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="registry-add-gh-server"]').exists()).toBe(false)
    const added = wrapper.find('[data-test="registry-added-gh-server"]')
    expect(added.exists()).toBe(true)
    expect(added.text()).toContain('Added ✓ · Open')
  })

  it('opens the added server on click', async () => {
    ;(api.addServerFromRegistry as any).mockResolvedValue({
      success: true,
      server: { name: 'github-server' },
    })
    const { wrapper, router } = await mountView()
    const button = await searchAndGetAddButton(wrapper)
    await button.trigger('click')
    await flushPromises()

    await wrapper.find('[data-test="registry-added-gh-server"]').trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.path).toBe('/servers/github-server')
  })
})
