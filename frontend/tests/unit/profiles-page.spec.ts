import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import Profiles from '@/views/Profiles.vue'
import api from '@/services/api'
import { setAvailableFeatures } from '@/composables/useScopeQuery'
import { useAuthStore } from '@/stores/auth'
import { makeClient, makeProfile, apiError } from './fixtures/profiles108i'

vi.mock('@/services/api', () => ({
  default: {
    hasAPIKey: vi.fn(() => true),
    getProfiles: vi.fn(),
    createProfile: vi.fn(),
    getServers: vi.fn(),
    getClients: vi.fn(),
    deleteProfile: vi.fn(),
  },
}))

const stub = { template: '<div />' }

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/profiles', name: 'profiles', component: Profiles },
      { path: '/profiles/:name', name: 'profile-editor', component: stub },
      { path: '/tools', name: 'tools', component: stub },
      { path: '/activity', name: 'activity', component: stub },
      { path: '/clients', name: 'clients', component: stub },
      { path: '/tokens', name: 'tokens', component: stub },
      { path: '/settings', name: 'settings', component: stub },
    ],
  })
}

async function mountAt(url = '/profiles') {
  const router = makeRouter()
  await router.push(url)
  await router.isReady()
  const wrapper = mount(Profiles, { global: { plugins: [router] } })
  await flushPromises()
  return { wrapper, router }
}

const work = makeProfile('work-ro', {
  title: 'Work',
  max_tier: 'read',
  tool_counts: { read: 4, write: 2, destructive: 1, unannotated_hidden: 3 },
  calls_24h: 12,
  blocked_24h: 2,
  used_by: { clients: [{ id: 'cursor', mode: 'locked' }, { id: 'claude-code', mode: 'switchable' }], tokens: ['ci', 'bot'], anonymous_profile: false },
})
const legacy = makeProfile('legacy', { is_legacy: true })

describe('Profiles page (Spec 108-i T090, FR-040)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.hasAPIKey as any).mockReturnValue(true)
    setAvailableFeatures(['scope_filters'])
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [work, legacy], anonymous_profile: 'work-ro' })
    ;(api.getServers as any).mockResolvedValue({ success: true, data: { servers: [{ name: 'github' }, { name: 'notion' }] } })
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [makeClient('cursor'), makeClient('claude-code', { display_name: 'Claude Code' })], warnings: [] } })
  })

  it('renders cards with title, tier counts, unannotated hidden, used-by and calls/blocked', async () => {
    const { wrapper } = await mountAt()
    const card = wrapper.get('[data-test="profile-card-work-ro"]')
    expect(card.text()).toContain('Work')
    expect(card.text()).toContain('work-ro')
    expect(card.get('[data-test="profile-counts"]').text()).toMatch(/Read\s*4.*Write\s*2.*Destructive\s*1/)
    expect(card.get('[data-test="profile-unannotated"]').text()).toBe('3 unannotated hidden')
    expect(card.get('[data-test="profile-used-by"]').text()).toContain('Cursor')
    expect(card.get('[data-test="profile-used-by"]').text()).toContain('\u{1F512}')
    expect(card.get('[data-test="profile-used-by"]').text()).toContain('Claude Code')
    expect(card.get('[data-test="profile-used-by"]').text()).toContain('2 tokens')
    expect(card.get('[data-test="profile-stats"]').text()).toContain('12 calls')
    expect(card.get('[data-test="profile-stats"]').text()).toContain('2 blocked')
    expect(wrapper.get('[data-test="profile-legacy-legacy"]').text()).toBe('Servers only')
  })

  it('omits the Used by line when used_by is absent (non-administrator)', async () => {
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [makeProfile('work-ro')] })
    const { wrapper } = await mountAt()
    expect(wrapper.find('[data-test="profile-used-by"]').exists()).toBe(false)
  })

  it('links carry ?profile= through linkTo to the tools, activity, clients and tokens routes', async () => {
    const { wrapper, router } = await mountAt()
    const card = wrapper.get('[data-test="profile-card-work-ro"]')
    const hrefs = Object.fromEntries(['tools', 'activity', 'clients', 'tokens'].map(page => [page, card.get(`[data-test="profile-link-${page}"]`).attributes('href')]))
    expect(hrefs).toEqual({
      tools: '/tools?profile=work-ro',
      activity: '/activity?profile=work-ro',
      clients: '/clients?profile=work-ro',
      tokens: '/tokens?profile=work-ro',
    })
    expect(router.resolve({ name: 'tokens', query: { profile: 'work-ro' } }).name).toBe('tokens')
  })

  it('shows the All servers card first, with the anonymous status', async () => {
    const { wrapper } = await mountAt()
    const cards = wrapper.findAll('article')
    expect(cards[0].attributes('data-test')).toBe('profile-card-all-servers')
    expect(cards[0].text()).toContain('reach every enabled server')
    expect(wrapper.get('[data-test="profile-anonymous-status"]').text()).toContain('Work')
    expect(wrapper.get('[data-test="profile-anonymous-link"]').attributes('href')).toContain('focus=anonymous_profile')
  })

  it('says unconfined when no anonymous profile is set', async () => {
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [work] })
    const { wrapper } = await mountAt()
    expect(wrapper.get('[data-test="profile-anonymous-status"]').text()).toContain('unconfined')
  })

  it('?create=1 opens the create dialog and strips the param', async () => {
    const { wrapper, router } = await mountAt('/profiles?create=1&profile=keep')
    expect(wrapper.get('[data-test="profile-create-dialog"]').attributes('open')).toBeDefined()
    expect(router.currentRoute.value.query).toEqual({ profile: 'keep' })
  })

  it('the create dialog posts the chosen starting point tier, then opens the editor', async () => {
    ;(api.createProfile as any).mockResolvedValue({ profile: makeProfile('work-ro'), warnings: [] })
    const { wrapper, router } = await mountAt()
    await wrapper.get('[data-test="profiles-add"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="profile-create-name"]').setValue('e2e-work-ro')
    await wrapper.get('[data-test="profile-create-title"]').setValue('E2E')
    await wrapper.get('[data-test="profile-create-server-github"]').setValue(true)
    await wrapper.get('[data-test="profile-create-start-read"]').setValue(true)
    await wrapper.get('[data-test="profile-create-submit"]').trigger('submit')
    await flushPromises()
    expect(api.createProfile).toHaveBeenCalledWith({ name: 'e2e-work-ro', title: 'E2E', servers: ['github'], max_tier: 'read' })
    expect(router.currentRoute.value.name).toBe('profile-editor')

    // Custom sets no tier.
    ;(api.createProfile as any).mockClear()
    await router.push('/profiles')
    await wrapper.get('[data-test="profiles-add"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="profile-create-name"]').setValue('custom')
    await wrapper.get('[data-test="profile-create-start-custom"]').setValue(true)
    await wrapper.get('[data-test="profile-create-submit"]').trigger('submit')
    await flushPromises()
    expect((api.createProfile as any).mock.calls[0][0].max_tier).toBeUndefined()
  })

  it('marks the field a validator 400 names', async () => {
    ;(api.createProfile as any).mockRejectedValue(apiError('invalid profile name', { field: 'name', status: 400 }))
    const { wrapper } = await mountAt()
    await wrapper.get('[data-test="profiles-add"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="profile-create-name"]').setValue('Bad Name')
    await wrapper.get('[data-test="profile-create-submit"]').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[data-test="profile-create-name-error"]').text()).toBe('invalid profile name')
    expect(wrapper.get('[data-test="profile-create-name"]').classes()).toContain('input-error')
  })

  it('renders the loading, empty and error states', async () => {
    let resolve!: (value: unknown) => void
    ;(api.getProfiles as any).mockReturnValue(new Promise(r => { resolve = r }))
    const { wrapper } = await mountAt()
    expect(wrapper.get('[data-test="profiles-skeleton"]').attributes('aria-busy')).toBe('true')
    resolve({ profiles: [] })
    await flushPromises()
    expect(wrapper.get('[data-test="profiles-empty"]').text()).toContain('No profiles yet')
    expect(wrapper.find('[data-test="profile-card-all-servers"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="profiles-create-empty"]').exists()).toBe(true)
  })

  it('shows an error with Retry, and the evaluation-unavailable text for a 503', async () => {
    ;(api.getProfiles as any).mockRejectedValueOnce(apiError('boom', { status: 503 }))
    const { wrapper } = await mountAt()
    expect(wrapper.get('[data-test="profiles-error"]').text()).toContain('Profile evaluation is unavailable; retry in a moment')
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [work] })
    await wrapper.get('[data-test="profiles-retry"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="profiles-error"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="profile-card-work-ro"]').exists()).toBe(true)
  })

  it('"Create token with this profile" routes to the token dialog preset', async () => {
    const { wrapper } = await mountAt()
    const link = wrapper.get('[data-test="profile-create-token-work-ro"]')
    expect(link.attributes('href')).toBe('/clients?tab=tokens&create=1&profile=work-ro')
  })

  it('is read only for a tenant: no create, assign, delete or used-by links', async () => {
    ;(api.hasAPIKey as any).mockReturnValue(false)
    const auth = useAuthStore()
    auth.isTeamsEdition = true
    auth.loading = false
    auth.authResolvedSuccessfully = true
    auth.user = { id: 'u', email: 'u@x', display_name: 'U', role: 'user', provider: 'oidc', created_at: '', last_login_at: '' } as any
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [makeProfile('work-ro')] })
    const { wrapper } = await mountAt()
    expect(wrapper.find('[data-test="profiles-add"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="profile-more-work-ro"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="profile-link-clients"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="profile-edit-work-ro"]').text()).toBe('View')
  })
})
