import { describe, it, expect, vi, beforeEach } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { mount, flushPromises } from '@vue/test-utils'
import { createRouter, createWebHistory } from 'vue-router'
import CatalogSearch from '@/components/CatalogSearch.vue'

// Spec 109 D35 (T160, demo finding #1): the Web leg of the cached-fallback
// chain. The REST golden internal/registries/testdata/catalog_cached_fallback_order.json
// (written by the Go httpapi test from the real handler while one source is
// down) is served to Add Server -> Catalog: the rows render in the golden
// order, only cached rows carry "From cached list", and the notice says what
// happened and what is shown.

const golden = JSON.parse(
  readFileSync(resolve(__dirname, '../../../internal/registries/testdata/catalog_cached_fallback_order.json'), 'utf8'),
) as {
  query: string
  ids: string[]
  results: Array<Record<string, unknown> & { source: string; id: string; title: string; from_cache?: boolean }>
  unavailable: Array<{ source: string; fallback: string }>
}

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

async function mountAndSearch(unavailable: unknown[]) {
  vi.mocked(api.getConfigSecrets).mockResolvedValue({
    success: true,
    data: { secrets: [], environment_vars: [], total_secrets: 0, total_env_vars: 0, keyring_available: true },
  })
  vi.mocked(api.catalogSearch).mockImplementation(async (params: { q?: string }) => ({
    success: true,
    data: params.q === golden.query
      ? { query: golden.query, results: golden.results, sections: null, unavailable }
      : { query: params.q ?? '', results: [], sections: { official: [], popular: [] }, unavailable: [] },
  }) as never)
  const wrapper = mount(CatalogSearch, { global: { plugins: [router] } })
  await flushPromises()
  await wrapper.find('[data-test="catalog-search-input"]').setValue(golden.query)
  await flushPromises()
  await new Promise(r => setTimeout(r, 400)) // the input is debounced
  await flushPromises()
  return wrapper
}

describe('catalog cached fallback on the Web UI (T160)', () => {
  beforeEach(() => {
    vi.mocked(api.catalogSearch).mockReset()
    vi.mocked(api.getConfigSecrets).mockReset()
  })

  const unavailable = () => golden.unavailable.map(u => ({
    source: u.source,
    reason: 'timeout after 5s',
    fallback: u.fallback,
    cached_at: '2026-10-02T10:00:00Z',
  }))

  it('renders the golden order, badges only the cached rows', async () => {
    const wrapper = await mountAndSearch(unavailable())
    const titles = wrapper.findAll('[data-test="catalog-result-title"]').map(t => t.text())
    expect(titles).toEqual(golden.results.map(r => r.title))

    for (const r of golden.results) {
      const badge = wrapper.find(`[data-test="catalog-from-cache-${r.source}-${r.id}"]`)
      expect(badge.exists(), `${r.source}:${r.id}`).toBe(Boolean(r.from_cache))
      if (r.from_cache) expect(badge.text()).toBe('From cached list')
    }
    expect(wrapper.text()).toContain('From cached list')
  })

  it('says which source is down and that matches come from its cached list', async () => {
    const wrapper = await mountAndSearch(unavailable())
    const notice = wrapper.find('[data-test="catalog-unavailable-notice"]')
    expect(notice.exists()).toBe(true)
    expect(notice.text()).toBe('slowreg: live search unavailable (timeout after 5s); showing matches from its cached list')
  })

  it('keeps today\'s wording for a source with no cached listing', async () => {
    const wrapper = await mountAndSearch([{ source: 'slowreg', reason: 'timeout after 5s' }])
    expect(wrapper.find('[data-test="catalog-unavailable-notice"]').text()).toBe('slowreg (timeout after 5s) unavailable')
  })

  it('never marks a live row as cached', async () => {
    const wrapper = await mountAndSearch(unavailable())
    const live = golden.results.find(r => !r.from_cache)!
    expect(wrapper.find(`[data-test="catalog-from-cache-${live.source}-${live.id}"]`).exists()).toBe(false)
  })
})
