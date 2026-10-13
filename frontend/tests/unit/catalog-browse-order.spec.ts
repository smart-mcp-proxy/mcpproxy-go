import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createRouter, createWebHistory } from 'vue-router'
import CatalogSearch from '@/components/CatalogSearch.vue'

// Spec 109 D35 (T164, demo finding #6): with an empty query the catalog lists
// Popular first when it has entries (popularity if available), then Official
// (curated first). With no popularity signal only Official shows, so the
// landing is never led by an empty section.

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

function result(id: string, title: string) {
  return {
    source: 'official', id, title, publisher: 'p', verified: true, official: true, description: 'd',
    transport: 'stdio', install: { command: 'npx', args: ['-y', id] }, required_inputs: [], added: false,
  }
}

async function mountWith(sections: { official: unknown[]; popular: unknown[] }) {
  vi.mocked(api.getConfigSecrets).mockResolvedValue({
    success: true,
    data: { secrets: [], environment_vars: [], total_secrets: 0, total_env_vars: 0, keyring_available: true },
  })
  vi.mocked(api.catalogSearch).mockResolvedValue({
    success: true,
    data: { query: '', results: [], sections, unavailable: [] },
  } as never)
  const wrapper = mount(CatalogSearch, { global: { plugins: [router] } })
  await flushPromises()
  return wrapper
}

describe('catalog browse section order (T164)', () => {
  beforeEach(() => {
    vi.mocked(api.catalogSearch).mockReset()
    vi.mocked(api.getConfigSecrets).mockReset()
  })

  it('renders Popular before Official when Popular is non-empty', async () => {
    const wrapper = await mountWith({ official: [result('ref/filesystem', 'filesystem')], popular: [result('io.github.x/hot', 'hot server')] })
    const sections = wrapper.findAll('[data-test^="catalog-section-"]').map(s => s.attributes('data-test'))
    expect(sections).toEqual(['catalog-section-popular', 'catalog-section-official'])
    const text = wrapper.text()
    expect(text.indexOf('hot server')).toBeLessThan(text.indexOf('filesystem'))
  })

  it('renders only Official when Popular is empty', async () => {
    const wrapper = await mountWith({ official: [result('ref/filesystem', 'filesystem')], popular: [] })
    const sections = wrapper.findAll('[data-test^="catalog-section-"]').map(s => s.attributes('data-test'))
    expect(sections).toEqual(['catalog-section-official'])
  })
})
