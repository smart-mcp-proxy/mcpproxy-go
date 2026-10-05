import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { defineComponent, h } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import ScopeFilterSelects from '@/components/scope/ScopeFilterSelects.vue'
import { setAvailableFeatures, useScopeQuery } from '@/composables/useScopeQuery'
import { useProfilesStore } from '@/stores/profiles'
import { WORK_RO } from './fixtures/profiles108i'

vi.mock('@/services/api', () => ({
  default: {
    getProfiles: vi.fn(() => Promise.resolve({ profiles: [] })),
    getClients: vi.fn(() => Promise.resolve({ success: true, data: { clients: [] } })),
    listAgentTokens: vi.fn(() => Promise.resolve({ success: true, data: { tokens: [] } })),
  },
}))

// Element ids must be unique per mounted instance (label `for` targets), and
// must not come from Math.random (CodeQL js/insecure-randomness).
describe('ScopeFilterSelects ids', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    setAvailableFeatures(['profile', 'client', 'token'])
  })

  it('two mounted instances get different ids, and each label targets its own select', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/usage', name: 'usage', component: { template: '<div />' } }],
    })
    await router.push('/usage')
    await router.isReady()
    useProfilesStore().profiles = [WORK_RO] as any
    const Host = defineComponent({
      setup() {
        const scopeQuery = useScopeQuery('usage')
        return () => h('div', [
          h(ScopeFilterSelects, { page: 'usage', scopeQuery }),
          h(ScopeFilterSelects, { page: 'usage', scopeQuery }),
        ])
      },
    })
    const wrapper = mount(Host, { global: { plugins: [router] }, attachTo: document.body })
    await flushPromises()
    const selects = wrapper.findAll('[data-test="scope-select-profile"]')
    expect(selects).toHaveLength(2)
    const ids = selects.map(s => s.attributes('id'))
    expect(ids[0]).toBeTruthy()
    expect(ids[0]).not.toBe(ids[1])
    for (const s of selects) {
      expect(wrapper.find(`label[for="${s.attributes('id')}"]`).exists()).toBe(true)
    }
    wrapper.unmount()
  })
})
