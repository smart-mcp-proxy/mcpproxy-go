import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import Repositories from '@/views/Repositories.vue'
import api from '@/services/api'

// Review round 8, finding 4: `addedServers`/`addingServerId` (Spec 109
// FR-063's "Added ✓ · Open") are keyed by the bare catalog entry id, but
// catalog entry ids collide across registries (MCP-866) — the same bug the
// macOS half of this PR (ServerBrowseView.swift's `addedKey`) was already
// fixed for, by keying on `registry::id` instead. Two cards from different
// registries sharing the same `id` must be tracked independently: adding
// one must not flip the OTHER (never-added) card to "Added ✓ · Open", show
// its spinner, or hand its "Open" click to the wrong server.

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

const customRegistry = {
  id: 'custom-registry',
  name: 'Custom Registry',
  description: 'A third-party registry',
  url: 'https://example.com/registry',
  provenance: 'custom',
  trusted: false,
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

describe('Repositories "Add to MCPProxy" across colliding registry ids (review round 8, finding 4)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    ;(api.listRegistries as any).mockResolvedValue({
      success: true,
      data: { registries: [officialRegistry, customRegistry], total: 2 },
    })
    ;(api.searchRegistryServers as any).mockImplementation((id: string) => {
      if (id === 'official') {
        return Promise.resolve({
          success: true,
          data: {
            registry_id: 'official',
            servers: [
              { id: 'gh-server', registry: 'official', name: 'official-github-server', description: 'Official GitHub tools' },
            ],
            total: 1,
          },
        })
      }
      return Promise.resolve({
        success: true,
        data: {
          registry_id: 'custom-registry',
          servers: [
            { id: 'gh-server', registry: 'custom-registry', name: 'custom-github-server', description: 'A different server, same bare id' },
          ],
          total: 1,
        },
      })
    })
  })

  async function selectBothAndSearch(wrapper: Awaited<ReturnType<typeof mountView>>['wrapper']) {
    await wrapper.find('[data-test="registry-card-official"]').trigger('click')
    await wrapper.find('[data-test="registry-card-custom-registry"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="registry-search-button"]').trigger('click')
    await flushPromises()
  }

  it('keeps both colliding-id cards, and adding one does not flip the other to "Added"', async () => {
    ;(api.addServerFromRegistry as any).mockResolvedValue({
      success: true,
      server: { name: 'official-github-server' },
    })
    const { wrapper } = await mountView()
    await selectBothAndSearch(wrapper)

    // Both cards render (not deduped away, since they come from different
    // registries) — sanity check before exercising the collision itself.
    expect(wrapper.text()).toContain('official-github-server')
    expect(wrapper.text()).toContain('custom-github-server')

    const addButtons = wrapper.findAll('[data-test="registry-add-gh-server"]')
    expect(addButtons).toHaveLength(2)

    // Add the FIRST card (whichever the DOM order puts first — we assert by
    // content below rather than assuming official is first).
    await addButtons[0].trigger('click')
    await flushPromises()

    const stillAddButtons = wrapper.findAll('[data-test="registry-add-gh-server"]')
    const addedButtons = wrapper.findAll('[data-test="registry-added-gh-server"]')

    // Exactly one card must have flipped to "Added ✓ · Open"; the other must
    // still read "Add to MCPProxy" and be clickable.
    expect(addedButtons).toHaveLength(1)
    expect(stillAddButtons).toHaveLength(1)
    expect(stillAddButtons[0].attributes('disabled')).toBeFalsy()
  })

  it('opens the correct server when two colliding-id cards both got added', async () => {
    ;(api.addServerFromRegistry as any)
      .mockResolvedValueOnce({ success: true, server: { name: 'official-github-server' } })
      .mockResolvedValueOnce({ success: true, server: { name: 'custom-github-server' } })

    const { wrapper, router } = await mountView()
    await selectBothAndSearch(wrapper)

    let addButtons = wrapper.findAll('[data-test="registry-add-gh-server"]')
    await addButtons[0].trigger('click')
    await flushPromises()

    addButtons = wrapper.findAll('[data-test="registry-add-gh-server"]')
    expect(addButtons).toHaveLength(1)
    await addButtons[0].trigger('click')
    await flushPromises()

    // Both are now "Added ✓ · Open" — each must navigate to ITS OWN server,
    // not whichever the bare id happened to resolve to first.
    const addedButtons = wrapper.findAll('[data-test="registry-added-gh-server"]')
    expect(addedButtons).toHaveLength(2)

    await addedButtons[0].trigger('click')
    await flushPromises()
    const firstTarget = router.currentRoute.value.path

    await addedButtons[1].trigger('click')
    await flushPromises()
    const secondTarget = router.currentRoute.value.path

    expect(firstTarget).not.toBe(secondTarget)
    expect([firstTarget, secondTarget].sort()).toEqual(
      ['/servers/custom-github-server', '/servers/official-github-server'].sort()
    )
  })
})
