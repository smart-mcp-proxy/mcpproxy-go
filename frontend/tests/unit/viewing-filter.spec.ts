import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { h } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import TopHeader from '@/components/TopHeader.vue'
import ViewingFilter from '@/components/ViewingFilter.vue'
import api from '@/services/api'
import { setAvailableFeatures, useScopeQuery } from '@/composables/useScopeQuery'
import { useAuthStore } from '@/stores/auth'
import { useClientsStore } from '@/stores/clients'
import { WORK_FULL, WORK_RO, makeClient } from './fixtures/profiles108i'

vi.mock('@/services/api', () => ({
  default: {
    hasAPIKey: vi.fn(() => true),
    getProfiles: vi.fn(),
    getClients: vi.fn(),
    getAttention: vi.fn().mockResolvedValue({ success: true, data: { count: 0, items: [] } }),
  },
}))

const stub = { template: '<div />' }
const TOOLTIP = 'Filters what this page shows. It does not change what any client or agent can access.'

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: ['activity', 'tools', 'servers', 'settings', 'clients', 'home'].map(name => ({ path: name === 'home' ? '/' : `/${name}`, name, component: stub })),
  })
}

async function mountChip(url = '/activity', options: { profiles?: any[]; clients?: any[] } = {}) {
  ;(api.getProfiles as any).mockResolvedValue({ profiles: options.profiles ?? [WORK_RO, WORK_FULL] })
  const router = makeRouter()
  await router.push(url)
  await router.isReady()
  const wrapper = mount(ViewingFilter, { global: { plugins: [router] } })
  useClientsStore().clients = (options.clients ?? [makeClient('cursor')]) as any
  await flushPromises()
  return { wrapper, router }
}

describe('Viewing chip (Spec 108-i T095, FR-044)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    setAvailableFeatures(['scope_filters'])
    ;(api.hasAPIKey as any).mockReturnValue(true)
  })

  it('the ProfileSwitcher component is gone and the header has no profile-switcher test id', async () => {
    expect(Object.keys(import.meta.glob('../../src/components/ProfileSwitcher.vue'))).toEqual([])
    const router = makeRouter()
    await router.push('/')
    await router.isReady()
    const header = mount(TopHeader, { global: { plugins: [router], stubs: { StatusPill: true, AddMenu: true, CommandPalette: true } } })
    await flushPromises()
    expect(header.find('[data-test="profile-switcher"]').exists()).toBe(false)
    expect(header.text()).not.toContain('Profile:')
  })

  it('renders inside the header viewing slot', async () => {
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [WORK_RO] })
    const router = makeRouter()
    await router.push('/activity')
    await router.isReady()
    const header = mount(TopHeader, {
      slots: { viewing: () => h(ViewingFilter) },
      global: { plugins: [router], stubs: { StatusPill: true, AddMenu: true, CommandPalette: true } },
    })
    await flushPromises()
    expect(header.find('[data-test="header-viewing-slot"] [data-test="viewing-filter"]').exists()).toBe(true)
  })

  it('shows "Viewing: all" with a popover holding Profile and Client selects and the tooltip', async () => {
    const { wrapper } = await mountChip()
    const button = wrapper.get('[data-test="viewing-filter-button"]')
    expect(wrapper.get('[data-test="viewing-filter-text"]').text()).toBe('Viewing: all')
    expect(button.attributes('title')).toBe(TOOLTIP)
    expect(wrapper.find('[data-test="viewing-filter-clear"]').exists()).toBe(false)
    await button.trigger('click')
    expect(wrapper.get('[data-test="viewing-filter-help"]').text()).toBe(TOOLTIP)
    expect(wrapper.get('[data-test="viewing-profile-select"]').findAll('option').map(option => option.text())).toEqual(['All', 'Work', 'Work Full'])
    expect(wrapper.get('[data-test="viewing-client-select"]').findAll('option').map(option => option.text())).toEqual(['All', 'Cursor'])
    expect(button.attributes('aria-expanded')).toBe('true')
  })

  it('writes profile and client to the URL through the scope query, and clears them', async () => {
    const { wrapper, router } = await mountChip()
    await wrapper.get('[data-test="viewing-filter-button"]').trigger('click')
    await wrapper.get('[data-test="viewing-profile-select"]').setValue('work-ro')
    await flushPromises()
    await wrapper.get('[data-test="viewing-client-select"]').setValue('cursor')
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ profile: 'work-ro', client: 'cursor' })
    expect(wrapper.get('[data-test="viewing-filter-text"]').text()).toBe('Viewing: Work · Cursor')
    await wrapper.get('[data-test="viewing-filter-clear"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({})
    expect(wrapper.get('[data-test="viewing-filter-text"]').text()).toBe('Viewing: all')
    expect(wrapper.get('[data-test="viewing-filter-button"]').attributes('aria-label')).toContain('Viewing: all')
  })

  it('reads the URL, survives navigation through the link map, and Back restores it', async () => {
    const { wrapper, router } = await mountChip('/activity?profile=work-ro')
    expect(wrapper.get('[data-test="viewing-filter-text"]').text()).toBe('Viewing: Work')
    // A page link built with linkTo carries the sticky value.
    let link: any
    const Host = { setup() { link = useScopeQuery('activity').linkTo('tools'); return () => h('div') } }
    mount(Host, { global: { plugins: [router] } })
    await router.push(link)
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/tools')
    expect(router.currentRoute.value.query.profile).toBe('work-ro')
    expect(wrapper.get('[data-test="viewing-filter-text"]').text()).toBe('Viewing: Work')
    router.back()
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/activity')
    expect(router.currentRoute.value.query.profile).toBe('work-ro')
    expect(wrapper.get('[data-test="viewing-filter-text"]').text()).toBe('Viewing: Work')
  })

  it('never calls a binding route: it only changes the URL', async () => {
    const { wrapper } = await mountChip()
    await wrapper.get('[data-test="viewing-filter-button"]').trigger('click')
    await wrapper.get('[data-test="viewing-profile-select"]').setValue('work-full')
    await flushPromises()
    const calls = Object.entries(api as any).filter(([name, fn]) => /Binding|setActive/.test(name) && (fn as any).mock?.calls.length)
    expect(calls).toEqual([])
  })

  it('is hidden when scope_filters is not advertised', async () => {
    setAvailableFeatures([])
    const { wrapper } = await mountChip()
    expect(wrapper.find('[data-test="viewing-filter"]').exists()).toBe(false)
  })

  it('is hidden with no profiles and no client holding a credential', async () => {
    const none = await mountChip('/activity', { profiles: [], clients: [makeClient('codex', { credential_state: 'admin_key' })] })
    expect(none.wrapper.find('[data-test="viewing-filter"]').exists()).toBe(false)
    const credentialed = await mountChip('/activity', { profiles: [], clients: [makeClient('cursor')] })
    expect(credentialed.wrapper.find('[data-test="viewing-filter"]').exists()).toBe(true)
  })

  it('is hidden for a tenant and never reads the admin profile list', async () => {
    ;(api.hasAPIKey as any).mockReturnValue(false)
    const auth = useAuthStore()
    auth.isTeamsEdition = true
    auth.loading = false
    auth.authResolvedSuccessfully = true
    auth.user = { id: 'u', email: 'u@x', display_name: 'U', role: 'user', provider: 'oidc', created_at: '', last_login_at: '' } as any
    const { wrapper } = await mountChip()
    expect(wrapper.find('[data-test="viewing-filter"]').exists()).toBe(false)
    expect(api.getProfiles).not.toHaveBeenCalled()
  })

  it('collapses to an icon below 1100px whose aria-label names the current value', async () => {
    const { wrapper } = await mountChip('/activity?profile=work-ro&client=cursor')
    // The text is hidden below the breakpoint and the button keeps its name.
    expect(wrapper.get('[data-test="viewing-filter-text"]').classes()).toContain('hidden')
    expect(wrapper.get('[data-test="viewing-filter-text"]').classes()).toContain('min-[1100px]:inline')
    expect(wrapper.get('[data-test="viewing-filter-button"]').attributes('aria-label')).toBe('Viewing: Work · Cursor. Filters what this page shows')
    expect(wrapper.get('[data-test="viewing-filter-clear"]').attributes('aria-label')).toBe('Clear view filter')
  })

  it('dims a parameter the current page does not register, with the reason', async () => {
    // Servers registers profile but not client.
    const { wrapper } = await mountChip('/servers?profile=work-ro&client=cursor')
    expect(wrapper.get('[data-test="viewing-profile"]').classes()).not.toContain('opacity-50')
    const client = wrapper.get('[data-test="viewing-client"]')
    expect(client.classes()).toContain('opacity-50')
    expect(client.attributes('title')).toBe('Not applied on this page')
    // A page outside the contract applies neither.
    const settings = await mountChip('/settings?profile=work-ro')
    expect(settings.wrapper.get('[data-test="viewing-profile"]').attributes('title')).toBe('Not applied on this page')
  })
})
