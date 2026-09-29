import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createRouter, createWebHistory } from 'vue-router'
import CatalogSearch from '@/components/CatalogSearch.vue'

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

const router = createRouter({ history: createWebHistory(), routes: [{ path: '/', component: { template: '<div/>' } }, { path: '/servers/:serverName', component: { template: '<div/>' } }] })

function githubResult(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    source: 'official',
    id: 'io.github.github/github-mcp-server',
    title: 'GitHub',
    publisher: 'github',
    verified: true,
    official: true,
    description: 'Access GitHub from your MCP client. <img src=x onerror=alert(1)>',
    transport: 'http',
    install: { url: 'https://api.githubcopilot.com/mcp/' },
    required_inputs: [],
    added: false,
    ...overrides,
  }
}

async function mountCatalog(keyringAvailable = true) {
  vi.mocked(api.getConfigSecrets).mockResolvedValue({
    success: true,
    data: { secrets: [], environment_vars: [], total_secrets: 0, total_env_vars: 0, keyring_available: keyringAvailable },
  })
  const wrapper = mount(CatalogSearch, { global: { plugins: [router] } })
  await flushPromises()
  return wrapper
}

describe('CatalogSearch', () => {
  beforeEach(() => {
    vi.mocked(api.catalogSearch).mockReset()
    vi.mocked(api.getConfigSecrets).mockReset()
    vi.mocked(api.addServerFromRegistry).mockReset()
    vi.mocked(api.getServers).mockReset()
  })

  it('defaults to the Catalog source (empty query) and renders sections', async () => {
    vi.mocked(api.catalogSearch).mockResolvedValue({
      success: true,
      data: { query: '', results: [], sections: { official: [githubResult()], popular: [] }, unavailable: [] },
    })
    const wrapper = await mountCatalog()
    expect(api.catalogSearch).toHaveBeenCalledWith(expect.objectContaining({ q: '' }))
    expect(wrapper.find('[data-test="catalog-section-official"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('GitHub')
    expect(wrapper.text()).toContain('io.github.github/github-mcp-server')
  })

  it('shows title/id/publisher and the "Add to MCPProxy" action, flipping to "Added ✓ · Open"', async () => {
    vi.mocked(api.catalogSearch).mockResolvedValue({
      success: true,
      data: { query: '', results: [], sections: { official: [githubResult()], popular: [] }, unavailable: [] },
    })
    vi.mocked(api.addServerFromRegistry).mockResolvedValue({ success: true, server: { name: 'github' } as never })
    const wrapper = await mountCatalog()

    const button = wrapper.find('[data-test="catalog-add-official-io.github.github/github-mcp-server"]')
    expect(button.text()).toBe('Add to MCPProxy')
    await button.trigger('click')
    await flushPromises()

    const updated = wrapper.find('[data-test="catalog-add-official-io.github.github/github-mcp-server"]')
    expect(updated.text()).toContain('Added ✓')
  })

  it('resolves a prior-session Added/Open card only by visible source and install target', async () => {
    vi.mocked(api.catalogSearch).mockResolvedValue({
      success: true,
      data: { query: '', results: [], sections: { official: [githubResult({ added: true })], popular: [] }, unavailable: [] },
    })
    vi.mocked(api.getServers).mockResolvedValue({
      success: true,
      data: { servers: [{ name: 'installed-github', source_registry_id: 'official', url: 'https://api.githubcopilot.com/mcp/', protocol: 'http' }] },
    })
    const wrapper = await mountCatalog()
    await wrapper.find('[data-test="catalog-add-official-io.github.github/github-mcp-server"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/servers/installed-github')
  })

  it('opens a prior-session credential-bearing Added/Open card from the server-authoritative name', async () => {
    vi.mocked(api.catalogSearch).mockResolvedValue({
      success: true,
      data: {
        query: '',
        results: [],
        sections: { official: [githubResult({
          added: true,
          added_server_name: 'installed-github-with-secret',
          install: { url: 'https://api.githubcopilot.com/mcp/?access_token=catalog-value' },
        })], popular: [] },
        unavailable: [],
      },
    })
    const wrapper = await mountCatalog()
    await wrapper.find('[data-test="catalog-add-official-io.github.github/github-mcp-server"]').trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.path).toBe('/servers/installed-github-with-secret')
    // GET /servers redacts credential-bearing URLs, so it is not a safe join
    // source. The authoritative catalog name makes no second request needed.
    expect(api.getServers).not.toHaveBeenCalled()
  })

  it('does not navigate an ambiguous prior-session Added/Open card', async () => {
    vi.mocked(api.catalogSearch).mockResolvedValue({
      success: true,
      data: { query: '', results: [], sections: { official: [githubResult({ added: true })], popular: [] }, unavailable: [] },
    })
    vi.mocked(api.getServers).mockResolvedValue({
      success: true,
      data: { servers: [
        { name: 'github-a', source_registry_id: 'official', url: 'https://api.githubcopilot.com/mcp/', protocol: 'http' },
        { name: 'github-b', source_registry_id: 'official', url: 'https://api.githubcopilot.com/mcp/', protocol: 'http' },
      ] },
    })
    const wrapper = await mountCatalog()
    await wrapper.find('[data-test="catalog-add-official-io.github.github/github-mcp-server"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="catalog-add-error"]').text()).toContain('More than one')
    expect(wrapper.find('[data-test="catalog-section-official"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('GitHub')
    expect(router.currentRoute.value.path).not.toMatch(/github-[ab]/)
  })

  it('renders a description containing <img onerror> and markdown INERT — no element is created (D19)', async () => {
    vi.mocked(api.catalogSearch).mockResolvedValue({
      success: true,
      data: {
        query: '',
        results: [],
        sections: {
          official: [githubResult({ description: '<img src=x onerror="window.__pwned = true">[click me](javascript:alert(1))' })],
          popular: [],
        },
        unavailable: [],
      },
    })
    const wrapper = await mountCatalog()
    expect(wrapper.find('img').exists()).toBe(false)
    expect((window as unknown as { __pwned?: boolean }).__pwned).toBeUndefined()
    expect(wrapper.find('a').exists()).toBe(false)
    expect(wrapper.text()).toContain('<img src=x onerror=')
  })

  it('surfaces unavailable sources without failing the whole search', async () => {
    vi.mocked(api.catalogSearch).mockResolvedValue({
      success: true,
      data: { query: 'x', results: [githubResult()], sections: null, unavailable: [{ source: 'smithery', reason: 'timeout after 5s' }] },
    })
    const wrapper = await mountCatalog()
    await wrapper.find('[data-test="catalog-search-input"]').setValue('x')
    await new Promise((r) => setTimeout(r, 300))
    await flushPromises()
    expect(wrapper.find('[data-test="catalog-unavailable-notice"]').text()).toContain('smithery')
  })

  // Review round 1: confirmAdd() unconditionally closed the secrets dialog
  // after addResult(), even when the POST /registries/.../add call failed —
  // addResult() never throws (api.ts always resolves {success:false}), so
  // the failure fell straight through to closeSecretsDialog(), which wiped
  // the error it had just set one line earlier and silently orphaned the
  // keyring entry the dialog had just written.
  it('keeps the secrets dialog open, shows the error, and rolls back the just-written secret when the add fails after a successful secret write', async () => {
    vi.mocked(api.catalogSearch).mockResolvedValue({
      success: true,
      data: {
        query: '',
        results: [],
        sections: {
          official: [githubResult({ required_inputs: [{ name: 'GITHUB_TOKEN', secret_like: true }] })],
          popular: [],
        },
        unavailable: [],
      },
    })
    vi.mocked(api.getSecretRefs).mockResolvedValue({ success: true, data: { refs: [] } })
    vi.mocked(api.setSecret).mockResolvedValue({ success: true, data: { name: 'github-env-github-token', type: 'keyring' } })
    vi.mocked(api.deleteSecret).mockResolvedValue({ success: true, data: { name: 'github-env-github-token', type: 'keyring' } })
    vi.mocked(api.addServerFromRegistry).mockResolvedValue({ success: false, error: 'a server named "github" already exists' })

    const wrapper = await mountCatalog()
    await wrapper.find('[data-test="catalog-add-official-io.github.github/github-mcp-server"]').trigger('click')
    await flushPromises()

    const dialog = wrapper.find('[data-test="catalog-secrets-dialog"]')
    expect(dialog.exists()).toBe(true)

    await wrapper.find('[data-test="secret-toggle-value-input"]').setValue('ghp_xxx')
    await wrapper.find('[data-test="catalog-secrets-confirm"]').trigger('click')
    await flushPromises()

    // The write happened (Secret mode is the default for a secret_like input).
    expect(api.setSecret).toHaveBeenCalledWith('github-env-github-token', 'ghp_xxx')

    // The dialog must stay open with the error visible, not silently close.
    expect(wrapper.find('[data-test="catalog-secrets-dialog"]').attributes('open')).toBeDefined()
    expect(wrapper.find('[data-test="catalog-add-error"]').text()).toContain('already exists')

    // The secret this failed attempt wrote must be rolled back so a retry
    // doesn't orphan a keyring entry or get a -2-suffixed name.
    expect(api.deleteSecret).toHaveBeenCalledWith('github-env-github-token')
  })

  // #1398 F-N: with the keyring already known-unavailable, a secret-like
  // input still defaults to Secret mode; Add must fail closed (disabled) and
  // recover only once the user switches that input to Value.
  it('disables the secrets dialog Add button while a Secret-mode input cannot use an unavailable keyring', async () => {
    vi.mocked(api.catalogSearch).mockResolvedValue({
      success: true,
      data: {
        query: '',
        results: [],
        sections: {
          official: [githubResult({ required_inputs: [{ name: 'GITHUB_TOKEN', secret_like: true }] })],
          popular: [],
        },
        unavailable: [],
      },
    })
    const wrapper = await mountCatalog(false)
    await wrapper.find('[data-test="catalog-add-official-io.github.github/github-mcp-server"]').trigger('click')
    await flushPromises()

    await wrapper.find('[data-test="secret-toggle-value-input"]').setValue('ghp_xxx')
    const confirm = () => wrapper.find('[data-test="catalog-secrets-confirm"]').element as HTMLButtonElement
    expect(confirm().disabled).toBe(true)

    await wrapper.find('[data-test="secret-toggle-mode-value"]').trigger('click')
    await flushPromises()
    expect(confirm().disabled).toBe(false)
  })
})
