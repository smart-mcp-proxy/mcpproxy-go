import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createRouter, createWebHistory } from 'vue-router'
import CatalogSearch from '@/components/CatalogSearch.vue'

// Spec 109 fix-catalog-rank T173 (D36.6, FR-061): a catalog card shows Verified
// (not a per-card "Official" badge: the Official section carries that meaning),
// the publisher, and a popularity signal.

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

function result(overrides: Record<string, unknown> = {}) {
  return {
    source: 'official',
    id: 'io.github.github/github-mcp-server',
    title: 'GitHub',
    publisher: 'github',
    verified: true,
    official: true,
    description: 'GitHub from your MCP client',
    transport: 'http',
    install: { url: 'https://api.githubcopilot.com/mcp/' },
    added: false,
    ...overrides,
  }
}

async function search(results: Record<string, unknown>[]) {
  vi.mocked(api.getConfigSecrets).mockResolvedValue({
    success: true,
    data: { secrets: [], environment_vars: [], total_secrets: 0, total_env_vars: 0, keyring_available: true },
  })
  vi.mocked(api.catalogSearch).mockImplementation(async (params: { q?: string }) => ({
    success: true,
    data: params.q === 'github'
      ? { query: 'github', results, sections: null, unavailable: [] }
      : { query: params.q ?? '', results: [], sections: { official: [], popular: [] }, unavailable: [] },
  }) as never)
  const wrapper = mount(CatalogSearch, { global: { plugins: [router] } })
  await flushPromises()
  await wrapper.find('[data-test="catalog-search-input"]').setValue('github')
  await flushPromises()
  await new Promise(r => setTimeout(r, 400)) // the input is debounced
  await flushPromises()
  return wrapper
}

describe('catalog card signals (FR-061)', () => {
  beforeEach(() => {
    vi.mocked(api.catalogSearch).mockReset()
    vi.mocked(api.getConfigSecrets).mockReset()
  })

  it('shows no Official badge on a card, even when official is true', async () => {
    const wrapper = await search([result()])
    expect(wrapper.text()).not.toContain('Official')
  })

  it('shows Verified when the publisher is verified, and not otherwise', async () => {
    const verified = await search([result()])
    expect(verified.text()).toContain('Verified')
    const unverified = await search([result({ verified: false })])
    expect(unverified.text()).not.toContain('Verified')
  })

  it('shows the publisher line "by github"', async () => {
    const wrapper = await search([result()])
    expect(wrapper.find('[data-test="catalog-result-publisher"]').text()).toBe('by github')
  })

  it('shows no publisher line when the hit has none', async () => {
    const wrapper = await search([result({ publisher: undefined })])
    expect(wrapper.find('[data-test="catalog-result-publisher"]').exists()).toBe(false)
  })

  it('renders the publisher as text, never as markup (D19)', async () => {
    const wrapper = await search([result({ publisher: '<img src=x onerror=alert(1)>' })])
    const line = wrapper.find('[data-test="catalog-result-publisher"]')
    expect(line.text()).toBe('by <img src=x onerror=alert(1)>')
    expect(line.find('img').exists()).toBe(false)
  })

  it('shows stars as "★ 21k" and installs as "1.2M installs"', async () => {
    const stars = await search([result({ popularity: { stars: 21345 } })])
    expect(stars.find('[data-test="catalog-result-popularity"]').text()).toBe('★ 21k')
    const installs = await search([result({ popularity: { installs: 1234567 } })])
    expect(installs.find('[data-test="catalog-result-popularity"]').text()).toBe('1.2M installs')
  })

  it('prefers stars when both signals are present, and formats small counts as-is', async () => {
    const both = await search([result({ popularity: { stars: 950, installs: 5 } })])
    expect(both.find('[data-test="catalog-result-popularity"]').text()).toBe('★ 950')
    const thousands = await search([result({ popularity: { stars: 1234 } })])
    expect(thousands.find('[data-test="catalog-result-popularity"]').text()).toBe('★ 1.2k')
  })

  it('shows no popularity element when there is no signal', async () => {
    const none = await search([result()])
    expect(none.find('[data-test="catalog-result-popularity"]').exists()).toBe(false)
    const empty = await search([result({ popularity: {} })])
    expect(empty.find('[data-test="catalog-result-popularity"]').exists()).toBe(false)
  })
})
