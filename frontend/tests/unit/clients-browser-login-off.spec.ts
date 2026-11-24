import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative, resolve } from 'node:path'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import Clients from '@/views/Clients.vue'
import api from '@/services/api'
import { useSystemStore } from '@/stores/system'
import { setAvailableFeatures } from '@/composables/useScopeQuery'

// Spec 132 FR-002 / US1: while browser login is off and was never used, nothing
// but the Settings switch mentions it. The core adds no new field to the
// clients payload then, and the Clients page must read exactly as before.

vi.mock('@/services/api', () => ({
  default: {
    hasAPIKey: vi.fn(() => true),
    getClients: vi.fn(),
    getRouting: vi.fn(),
    getClient: vi.fn(),
    revokeClientLogin: vi.fn(),
    revokeClientAPIKey: vi.fn(),
  },
}))

const legacy = { id: 'cursor', display_name: 'Cursor', kind: 'supported', state: 'connected_seen', active_sessions: 0, last_seen: null, credential_state: 'client', token_name: 'client-cursor' }
const used = {
  ...legacy,
  credentials: [
    { kind: 'api_key', prefix: 'mcp_cli_ab12', state: 'active' },
    { kind: 'browser_login', grant_id: 'grantAAAA', state: 'active', approved_at: '2026-11-20T10:00:00Z', reported_name: 'Cursor IDE', redirect_host: '127.0.0.1', name_mismatch: false },
  ],
  browser_login_disabled: true,
}

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/clients', name: 'clients', component: Clients },
      { path: '/activity', name: 'activity', component: { template: '<div />' } },
    ],
  })
}

async function mountPage(row: Record<string, unknown>) {
  ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [row] } })
  ;(api.getClient as any).mockResolvedValue({ success: true, data: { ...row, sessions: [] } })
  const router = makeRouter()
  await router.push('/clients')
  await router.isReady()
  const wrapper = mount(Clients, { global: { plugins: [router], stubs: { ClientConnectList: true, AgentTokens: true, ModeSwitcher: true, ClientBindingControls: true, ProfileChip: true } } })
  await flushPromises()
  await wrapper.get('[data-test="client-expand-cursor"]').trigger('click')
  await flushPromises()
  return wrapper
}

describe('Clients page with browser login off and never used', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    setAvailableFeatures([])
    ;(api.getRouting as any).mockResolvedValue({ success: true, data: { endpoints: { default: '/mcp' }, routing_mode: 'retrieve_tools', available_modes: ['retrieve_tools'] } })
    useSystemStore().$patch({ status: { listen_addr: '127.0.0.1:18081' } as any })
  })

  it('shows no credentials block and no mention of browser login or sign-in', async () => {
    const wrapper = await mountPage(legacy)
    expect(wrapper.find('[data-test="client-credentials-cursor"]').exists()).toBe(false)
    expect(wrapper.text()).not.toMatch(/browser login|sign.?in|login/i)
  })

  it('shows the credentials of a client once the payload carries them, with the disabled label', async () => {
    const wrapper = await mountPage(used)
    const block = wrapper.get('[data-test="client-credentials-cursor"]')
    expect(block.text()).toContain('mcp_cli_ab12')
    expect(block.get('[data-test="credential-reported-name"]').text()).toBe('Cursor IDE')
    expect(block.get('[data-test="browser-login-disabled"]').exists()).toBe(true)
  })

  it('refreshes the roster after a login is revoked', async () => {
    ;(api.revokeClientLogin as any).mockResolvedValue({ changed: true })
    const wrapper = await mountPage(used)
    const before = (api.getClients as any).mock.calls.length
    await wrapper.get('[data-test="revoke-login-grantAAAA"]').trigger('click')
    await wrapper.get('[data-test="revoke-credential-confirm"]').trigger('click')
    await flushPromises()
    expect(api.revokeClientLogin).toHaveBeenCalledWith('cursor', 'grantAAAA')
    expect((api.getClients as any).mock.calls.length).toBeGreaterThan(before)
  })
})

// The one place that guards the rule for every other view: a new mention of
// browser login in the web UI sources has to be added here on purpose.
describe('browser login is mentioned only where the spec allows it', () => {
  const root = resolve(__dirname, '../../src')
  const allowed = new Set([
    'components/clients/ClientCredentials.vue', // hidden until the payload carries credentials
    'components/ClientConnectList.vue', // the Connect choice, drawn only while the core says browser_login_available (connect-auth-choice.spec.ts)
    'views/AuthorizeRequest.vue', // the consent page
    'views/settings/fields.ts', // the one Settings switch
    'router/index.ts', // the consent route
    'services/api.ts',
    'types/api.ts',
  ])
  function walk(dir: string): string[] {
    return readdirSync(dir).flatMap(name => {
      const full = join(dir, name)
      return statSync(full).isDirectory() ? walk(full) : [full]
    })
  }
  it('has no other view, component or store that names it', () => {
    const hits = walk(root)
      .filter(file => /\.(vue|ts)$/.test(file))
      .filter(file => /browser login|browser_login|mcp_oauth/i.test(readFileSync(file, 'utf8')))
      .map(file => relative(root, file))
      .filter(file => !allowed.has(file))
    expect(hits).toEqual([])
  })
})
