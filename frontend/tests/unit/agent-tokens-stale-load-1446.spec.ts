import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import AgentTokens from '@/views/AgentTokens.vue'
import api from '@/services/api'
import { setAvailableFeatures } from '@/composables/useScopeQuery'

// Issue #1446 item 8: an older GET /tokens must not overwrite a newer one.
// Item 17: the Profile column keeps a minimum width and its chip never wraps.

vi.mock('@/services/api', () => ({
  default: {
    hasAPIKey: vi.fn(() => true),
    listAgentTokens: vi.fn(),
    getServers: vi.fn(),
    getProfiles: vi.fn(),
  },
}))

const future = '2030-01-01T00:00:00Z'
const tok = (name: string) => ({ name, token_prefix: 'mcp_agt_aa', allowed_servers: ['*'], permissions: ['read'], expires_at: future, created_at: future, last_used_at: null, revoked: false, kind: 'agent', legacy_scope: false })
const stub = { template: '<div />' }

describe('Agent tokens list (#1446)', () => {
  const original = (HTMLDialogElement.prototype as any).showModal
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(HTMLDialogElement.prototype as any).showModal = vi.fn()
    setAvailableFeatures(['scope_filters'])
    ;(api.getServers as any).mockResolvedValue({ success: true, data: { servers: [] } })
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [] })
  })
  afterEach(() => { ;(HTMLDialogElement.prototype as any).showModal = original })

  it('the latest load wins when responses resolve out of order', async () => {
    let resolveFirst!: (v: any) => void
    ;(api.listAgentTokens as any)
      .mockReturnValueOnce(new Promise(r => { resolveFirst = r }))
      .mockResolvedValue({ success: true, data: { tokens: [tok('newer')] } })
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/clients', name: 'clients', component: stub }, { path: '/profiles', name: 'profiles', component: stub }] })
    await router.push('/clients?tab=tokens')
    await router.isReady()
    const wrapper = mount(AgentTokens, { global: { plugins: [router] }, attachTo: document.body })
    await vi.waitFor(() => expect((api.listAgentTokens as any).mock.calls.length).toBeGreaterThanOrEqual(1))
    const refresh = (wrapper.vm as any).refreshTokens ?? (wrapper.vm as any).loadTokens
    if (refresh) {
      await refresh()
    } else {
      await router.push('/clients?tab=tokens&profile=x')
    }
    await flushPromises()
    resolveFirst({ success: true, data: { tokens: [tok('older')] } })
    await flushPromises()
    expect(wrapper.find('[data-test="token-row-newer"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="token-row-older"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
