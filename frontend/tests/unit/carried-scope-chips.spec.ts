import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { readFileSync } from 'fs'
import { resolve } from 'path'
import CarriedScopeChips from '@/components/scope/CarriedScopeChips.vue'
import { setAvailableFeatures } from '@/composables/useScopeQuery'

// Spec 134 FR-016: a view-as value carried in the URL onto a page that applies
// no scope (Home, Settings, Review, a server, the Clients Endpoint tab) is shown
// as a removable chip marked "not applied on this page", so it can always be
// seen and cleared.

vi.mock('@/services/api', () => ({ default: { hasAPIKey: vi.fn(() => true), getProfiles: vi.fn(), getClients: vi.fn() } }))

const stub = { template: '<div />' }
const router = () => createRouter({
  history: createMemoryHistory(),
  routes: [
    { path: '/', name: 'dashboard', component: stub },
    { path: '/settings', name: 'settings', component: stub },
    { path: '/tools', name: 'tools', component: stub },
    { path: '/clients', name: 'clients', component: stub },
  ],
})

async function mountAt(url: string) {
  setActivePinia(createPinia())
  const r = router()
  await r.push(url)
  await r.isReady()
  const wrapper = mount(CarriedScopeChips, { global: { plugins: [r] } })
  await flushPromises()
  return { wrapper, r }
}

describe('carried view-as chips on pages without a scope (spec 134 FR-016)', () => {
  beforeEach(() => setAvailableFeatures(['scope_filters']))

  it('shows each carried value as a removable "not applied on this page" chip', async () => {
    const { wrapper, r } = await mountAt('/settings?profile=work&client=cursor&tab=general')
    const chips = wrapper.findAll('[data-test^="carried-scope-chip-"]')
    expect(chips.map((c) => c.attributes('data-test'))).toEqual(['carried-scope-chip-profile', 'carried-scope-chip-client'])
    for (const chip of chips) expect(chip.text()).toContain('not applied on this page')
    await wrapper.get('[data-test="carried-scope-remove-profile"]').trigger('click')
    await flushPromises()
    expect(r.currentRoute.value.query.profile).toBeUndefined()
    expect(r.currentRoute.value.query.client).toBe('cursor')
    expect(r.currentRoute.value.query.tab).toBe('general')
  })

  it('renders nothing on a page that applies a scope (its own chips show there)', async () => {
    const { wrapper } = await mountAt('/tools?profile=work')
    expect(wrapper.find('[data-test="carried-scope-chips"]').exists()).toBe(false)
  })

  it('renders on the Clients Endpoint tab, which applies no scope', async () => {
    const { wrapper } = await mountAt('/clients?tab=endpoint&profile=work')
    expect(wrapper.find('[data-test="carried-scope-chip-profile"]').exists()).toBe(true)
  })

  it('renders nothing without values; a value the build does not list is still shown and removable', async () => {
    expect((await mountAt('/settings')).wrapper.find('[data-test="carried-scope-chips"]').exists()).toBe(false)
    setAvailableFeatures([])
    expect((await mountAt('/settings?token=ci')).wrapper.find('[data-test="carried-scope-chip-token"]').exists()).toBe(true)
  })

  it('App mounts it above the page, and Clients hosts View as and scope chips on its Clients and Tokens tabs', () => {
    const src = (f: string) => readFileSync(resolve(__dirname, `../../src/${f}`), 'utf8')
    expect(src('App.vue')).toMatch(/<CarriedScopeChips\b/)
    expect(src('views/Clients.vue')).toMatch(/<ViewAs\b/)
    expect(src('views/Clients.vue')).toMatch(/<ScopeChips[^>]*page="clients"/)
    expect(src('views/AgentTokens.vue')).toMatch(/<ScopeChips[^>]*page="tokens"/)
  })
})
