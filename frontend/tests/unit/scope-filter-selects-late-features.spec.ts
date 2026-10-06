import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { defineComponent, h } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import ScopeFilterSelects from '@/components/scope/ScopeFilterSelects.vue'
import { setAvailableFeatures, useScopeQuery } from '@/composables/useScopeQuery'
import { useClientsStore } from '@/stores/clients'
import { makeClient } from './fixtures/profiles108i'

const listTokensMock = vi.hoisted(() => vi.fn())

vi.mock('@/services/api', () => ({
  default: {
    getProfiles: vi.fn(() => Promise.resolve({ profiles: [] })),
    getClients: vi.fn(() => Promise.resolve({ success: true, data: { clients: [] } })),
    listAgentTokens: listTokensMock,
  },
}))

// Spec 108-j review F2.3: the pickers are mounted before GET /status has answered
// (the filter panel is open from a remembered state), so the Token list must load
// when the build's scope filters become known, not only in onMounted.
describe('ScopeFilterSelects when the scope features arrive after mount', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    setAvailableFeatures([])
    listTokensMock.mockResolvedValue({ success: true, data: { tokens: [{ name: 'ro-bot', kind: 'agent' }, { name: 'client-cursor', kind: 'client' }] } })
  })

  it('loads the token names (and the client list) once the features are known', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/activity', name: 'activity', component: { template: '<div />' } }],
    })
    await router.push('/activity')
    await router.isReady()
    const clients = useClientsStore()
    const refreshPresence = vi.spyOn(clients, 'refreshPresence').mockImplementation(async () => {
      clients.clients = [makeClient('cursor')] as any
    })
    const Host = defineComponent({
      setup() {
        const scopeQuery = useScopeQuery('activity')
        return () => h(ScopeFilterSelects, { page: 'activity', scopeQuery })
      },
    })
    const wrapper = mount(Host, { global: { plugins: [router] } })
    await flushPromises()
    expect(wrapper.find('[data-test="scope-select-token"]').exists()).toBe(false)
    expect(listTokensMock).not.toHaveBeenCalled()

    setAvailableFeatures(['profile', 'client', 'token'])
    await flushPromises()
    await flushPromises()

    expect(listTokensMock).toHaveBeenCalledTimes(1)
    expect(refreshPresence).toHaveBeenCalledTimes(1)
    const options = wrapper.get('[data-test="scope-select-token"]').findAll('option').map(option => option.text())
    expect(options).toEqual(['All tokens', 'ro-bot'])
  })
})
