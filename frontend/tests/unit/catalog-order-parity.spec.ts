import { describe, it, expect, vi, beforeEach } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { mount, flushPromises } from '@vue/test-utils'
import { createRouter, createWebHistory } from 'vue-router'
import CatalogSearch from '@/components/CatalogSearch.vue'

// Spec 109-m SC-008 (T145b, M12): the Web leg of the catalog order parity
// chain. The REST golden internal/registries/testdata/catalog_github_order.json
// (written by the Go httpapi test from the real handler over the fixture
// registries) is served to Add Server -> Catalog; the results must render in
// exactly that order, the official, verified GitHub server first.

const golden = JSON.parse(
  readFileSync(resolve(__dirname, '../../../internal/registries/testdata/catalog_github_order.json'), 'utf8'),
) as { query: string; ids: string[]; results: Record<string, unknown>[] }

vi.mock('@/services/api', () => ({
  default: {
    catalogSearch: vi.fn(),
    getConfigSecrets: vi.fn(),
    addServerFromRegistry: vi.fn(),
    getSecretRefs: vi.fn(),
    setSecret: vi.fn(),
    deleteSecret: vi.fn(),
    getServers: vi.fn(),
  },
}))
import api from '@/services/api'

const router = createRouter({
  history: createWebHistory(),
  routes: [{ path: '/', component: { template: '<div/>' } }, { path: '/servers/:serverName', component: { template: '<div/>' } }],
})

describe('catalog order parity on the Web UI (SC-008)', () => {
  beforeEach(() => {
    vi.mocked(api.catalogSearch).mockReset()
    vi.mocked(api.getConfigSecrets).mockReset()
  })

  it('renders the REST results in the served order, official GitHub first', async () => {
    expect(golden.results.map(r => `${r.source}:${r.id}`)).toEqual(golden.ids)
    vi.mocked(api.getConfigSecrets).mockResolvedValue({
      success: true,
      data: { secrets: [], environment_vars: [], total_secrets: 0, total_env_vars: 0, keyring_available: true },
    })
    vi.mocked(api.catalogSearch).mockImplementation(async (params: { q?: string }) => ({
      success: true,
      data: params.q === golden.query
        ? { query: golden.query, results: golden.results, sections: null, unavailable: [] }
        : { query: params.q ?? '', results: [], sections: { official: [], popular: [] }, unavailable: [] },
    }) as never)

    const wrapper = mount(CatalogSearch, { global: { plugins: [router] } })
    await flushPromises()
    await wrapper.find('[data-test="catalog-search-input"]').setValue(golden.query)
    await flushPromises()
    await new Promise(r => setTimeout(r, 400)) // the input is debounced
    await flushPromises()

    const titles = wrapper.findAll('[data-test="catalog-result-title"]').map(t => t.text())
    expect(titles).toEqual(golden.results.map(r => r.title))
    expect(titles[0]).toBe('GitHub')
    expect(wrapper.findAll('[data-test^="catalog-result-"]').filter(n => n.attributes('data-test') !== 'catalog-result-title').length).toBe(golden.ids.length)
  })
})
