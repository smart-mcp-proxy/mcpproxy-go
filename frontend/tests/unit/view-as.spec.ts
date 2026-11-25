import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { readFileSync } from 'fs'
import { resolve } from 'path'
import ViewAs from '@/components/ViewAs.vue'
import api from '@/services/api'
import { setAvailableFeatures } from '@/composables/useScopeQuery'
import { useClientsStore } from '@/stores/clients'
import { WORK_FULL, WORK_RO, makeClient } from './fixtures/profiles108i'

// Spec 134 FR-016: the Viewing filter leaves the header and becomes a "View as"
// control in the filter row of Servers, Tools, Activity and Usage, under the
// same visibility rule, with the same URL parameters.

vi.mock('@/services/api', () => ({
  default: { hasAPIKey: vi.fn(() => true), getProfiles: vi.fn(), getClients: vi.fn() },
}))

const stub = { template: '<div />' }
const makeRouter = () => createRouter({
  history: createMemoryHistory(),
  routes: ['activity', 'tools', 'servers', 'usage', 'settings', 'clients'].map(name => ({ path: `/${name}`, name, component: stub })),
})

async function mountAt(url: string, profiles: any[] = [WORK_RO, WORK_FULL]) {
  ;(api.getProfiles as any).mockResolvedValue({ profiles })
  const router = makeRouter()
  await router.push(url)
  await router.isReady()
  const wrapper = mount(ViewAs, { global: { plugins: [router] } })
  useClientsStore().clients = [makeClient('cursor')] as any
  await flushPromises()
  return { wrapper, router }
}

describe('View as control (spec 134 FR-016)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    setAvailableFeatures(['scope_filters'])
  })

  it('is labelled "View as" and offers Profile and Client selects', async () => {
    const { wrapper } = await mountAt('/servers')
    const button = wrapper.get('[data-test="viewing-filter-button"]')
    expect(button.text()).toContain('View as')
    await button.trigger('click')
    expect(wrapper.find('[data-test="viewing-profile-select"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="viewing-client-select"]').exists()).toBe(true)
  })

  it('writes ?profile= and keeps other params (same URL contract as before)', async () => {
    const { wrapper, router } = await mountAt('/tools?q=abc')
    await wrapper.get('[data-test="viewing-filter-button"]').trigger('click')
    await wrapper.get('[data-test="viewing-profile-select"]').setValue(WORK_RO.name)
    await flushPromises()
    expect(router.currentRoute.value.query.profile).toBe(WORK_RO.name)
    expect(router.currentRoute.value.query.q).toBe('abc')
  })

  it('names the narrowing next to the control when a profile is set', async () => {
    const { wrapper } = await mountAt(`/usage?profile=${WORK_RO.name}`)
    expect(wrapper.get('[data-test="view-as-note"]').text()).toContain('Viewing as')
    expect(wrapper.get('[data-test="view-as-note"]').text()).toContain(WORK_RO.title || WORK_RO.name)
  })

  it('does not claim narrowing for the unattributed marker "-"', async () => {
    const { wrapper } = await mountAt('/servers?profile=-')
    expect(wrapper.find('[data-test="view-as-note"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="viewing-filter-button"]').attributes('aria-label')).toContain('unattributed')
  })

  it('shows no narrowing note when the page reports a profile/client conflict', async () => {
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [WORK_RO] })
    const router = makeRouter()
    await router.push(`/tools?profile=${WORK_RO.name}&client=cursor`)
    await router.isReady()
    const wrapper = mount(ViewAs, { props: { conflict: true }, global: { plugins: [router] } })
    useClientsStore().clients = [makeClient('cursor')] as any
    await flushPromises()
    expect(wrapper.find('[data-test="view-as-note"]').exists()).toBe(false)
  })

  it('stays hidden for a tenant-like state with nothing to filter by', async () => {
    const { wrapper } = await mountAt('/servers', [])
    useClientsStore().clients = [] as any
    await flushPromises()
    expect(wrapper.find('[data-test="viewing-filter"]').exists()).toBe(false)
  })

  it('the clear action removes profile and client', async () => {
    const { wrapper, router } = await mountAt(`/activity?profile=${WORK_RO.name}&client=cursor`)
    await wrapper.get('[data-test="viewing-filter-clear"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.profile).toBeUndefined()
    expect(router.currentRoute.value.query.client).toBeUndefined()
  })
})

describe('View as is placed in the four filter rows (spec 134 FR-016)', () => {
  const view = (name: string) => readFileSync(resolve(__dirname, `../../src/views/${name}.vue`), 'utf8')
  for (const page of ['Servers', 'Tools', 'Activity', 'Usage']) {
    it(`${page}.vue renders <ViewAs />`, () => {
      expect(view(page)).toMatch(/<ViewAs\b/)
    })
  }
  it('App.vue no longer passes a viewing slot to the header', () => {
    expect(readFileSync(resolve(__dirname, '../../src/App.vue'), 'utf8')).not.toMatch(/ViewingFilter|#viewing/)
  })
})
