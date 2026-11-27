import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setAvailableFeatures } from '@/composables/useScopeQuery'
import { WORK_RO, makeClient } from './fixtures/profiles108i'

// Spec 134 FR-055/FR-056 with View as (Spec 108-j): the grouped rows keep the
// verdict and "Why?" (in Details), the read-only guard and the subject banner.
const getGlobalToolsMock = vi.hoisted(() => vi.fn())
const explainAccessMock = vi.hoisted(() => vi.fn())

vi.mock('@/services/api', () => ({
  default: {
    getGlobalTools: getGlobalToolsMock,
    getStatus: vi.fn(() => Promise.resolve({ success: true, data: { features: { scope_filters: ['profile', 'client', 'token'] } } })),
    explainAccess: explainAccessMock,
    getProfiles: vi.fn(() => Promise.resolve({ profiles: [] })),
    getClients: vi.fn(() => Promise.resolve({ success: true, data: { clients: [] } })),
    getToolApprovals: vi.fn(() => Promise.resolve({ success: true, data: { approvals: [] } })),
  },
}))

const stub = { template: '<div />' }
const row = (name: string, server: string, access: { visible: boolean; callable: boolean; reason?: string }) => ({
  name, server_name: server, description: 'd', tier: 'write', approval_status: 'approved', disabled: false, config_denied: false, usage: 0, access,
})

async function mountAt(path: string) {
  window.localStorage.removeItem('mcpproxy.tools.layout')
  const Tools = (await import('@/views/Tools.vue')).default
  const { useProfilesStore } = await import('@/stores/profiles')
  const { useClientsStore } = await import('@/stores/clients')
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: stub }, { path: '/tools', component: Tools }, { path: '/servers', component: stub },
      { path: '/servers/:serverName', component: stub }, { path: '/activity', name: 'activity', component: stub },
      { path: '/clients', name: 'clients', component: stub }, { path: '/review', component: stub }, { path: '/settings', component: stub },
    ],
  })
  await router.push(path)
  await router.isReady()
  useProfilesStore().profiles = [WORK_RO] as any
  useProfilesStore().loaded = true
  useClientsStore().clients = [makeClient('cursor', { profile: 'work-ro' } as any)] as any
  const wrapper = mount(Tools, { attachTo: document.body, global: { plugins: [router] } })
  await flushPromises()
  await flushPromises()
  return wrapper
}

describe('Tools grouped under View as', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    setAvailableFeatures(['profile', 'client', 'token'])
    const tools = [row('create_issue', 'github', { visible: false, callable: false, reason: 'above_tier_cap' }), row('list', 'github', { visible: true, callable: true })]
    getGlobalToolsMock.mockImplementation(() => Promise.resolve({ success: true, data: { tools, stats: { total: 2, enabled: 2, disabled: 0, pending_approval: 0 } } }))
    explainAccessMock.mockResolvedValue({ subject: { kind: 'client', name: 'cursor' }, tool: 'github:create_issue', profile: { name: 'work-ro', source: 'pin' }, steps: [], verdict: 'hidden', first_failure: 'tier_cap', fixes: [] })
  })

  it('keeps the View as select, the banner and the conflict-free sections open', async () => {
    const wrapper = await mountAt('/tools?client=cursor')
    expect(wrapper.find('[data-test="tools-view-as-select"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="tools-view-as-summary"]').text()).toContain('1 visible')
    expect(wrapper.find('[data-test="tools-section-github"] [data-test="tools-section-body"]').isVisible()).toBe(true)
    expect(getGlobalToolsMock).toHaveBeenCalledWith({ client: 'cursor' })
  })

  it('a not callable row says so; Details has the verdict, the reason and Why', async () => {
    const wrapper = await mountAt('/tools?client=cursor')
    const r = wrapper.findAll('[data-test="tool-row"]').find(x => x.text().includes('create_issue'))!
    expect(r.find('[data-test="tool-not-callable"]').exists()).toBe(true)
    expect(r.attributes('data-not-callable')).toBe('true')
    await r.find('[data-test="tool-details-button"]').trigger('click')
    await flushPromises()
    const access = wrapper.find('[data-test="tool-details-access"]')
    expect(access.text()).toContain('Hidden')
    await access.find('[data-test="tool-details-why"]').trigger('click')
    await flushPromises()
    expect(explainAccessMock).toHaveBeenCalledWith({ tool: 'github:create_issue', client: 'cursor' })
    expect(wrapper.find('[data-test="access-explainer"]').exists()).toBe(true)
  })

  it('selection is read-only while viewing as someone', async () => {
    const wrapper = await mountAt('/tools?client=cursor')
    expect((wrapper.find('[data-test="tools-select-all"]').element as HTMLInputElement).disabled).toBe(true)
    expect((wrapper.find('[data-test="tool-select"]').element as HTMLInputElement).disabled).toBe(true)
  })

  it('the Callable filter lives in More filters, narrows the rows and counts as an advanced filter', async () => {
    const wrapper = await mountAt('/tools?client=cursor')
    const panel = wrapper.find('[data-test="tools-more-filters"]')
    expect(panel.find('[data-test="tools-show-filter"]').exists()).toBe(true)
    await wrapper.find('[data-test="tools-more-filters-toggle"]').trigger('click')
    await panel.find('[data-test="tools-show-callable"]').trigger('click')
    expect(wrapper.findAll('[data-test="tool-row"]')).toHaveLength(1)
    expect(wrapper.find('[data-test="tools-more-filters-toggle"]').text()).toContain('1')
    await panel.find('[data-test="tools-show-not_callable"]').trigger('click')
    expect(wrapper.find('[data-test="tool-name"]').text()).toBe('create_issue')
  })

  it('a Show choice is named by an Active filters chip and Clear Filters resets it, even with More filters closed', async () => {
    const wrapper = await mountAt('/tools?client=cursor')
    await wrapper.find('[data-test="tools-more-filters-toggle"]').trigger('click')
    await wrapper.find('[data-test="tools-show-not_callable"]').trigger('click')
    await wrapper.find('[data-test="tools-more-filters-toggle"]').trigger('click')
    expect(wrapper.text()).toContain('Show: Not callable')
    const clear = wrapper.findAll('button').find(b => b.text() === 'Clear Filters')!
    await clear.trigger('click')
    expect(wrapper.text()).not.toContain('Show: Not callable')
    expect(wrapper.findAll('[data-test="tool-row"]')).toHaveLength(2)
  })
})
