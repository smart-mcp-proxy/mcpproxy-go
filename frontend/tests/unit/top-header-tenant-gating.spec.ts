import { describe, it, expect, beforeEach, vi } from 'vitest'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 109 FR-057: ProfileSwitcher now also requires profilesStore.hasProfiles
// (fetched by TopHeader on mount), so a real profile must be mocked here for
// the "still renders for an admin principal" case below to hold.
vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  const base: Record<string, unknown> = {
    getProfiles: vi.fn(() => ok({ profiles: [{ name: 'work', servers: ['alpha'], tool_count: 3 }] })),
    getActiveProfile: vi.fn(() => ok({ active_profile: '' })),
    // authStore.principalKind calls this SYNCHRONOUSLY and branches on
    // truthiness — the Proxy fallback below returns a (truthy) Promise for
    // any undeclared method, which would silently reclassify every tenant
    // test here as 'api_key'.
    hasAPIKey: vi.fn(() => false),
  }
  return {
    default: new Proxy(base, {
      get(target: Record<string, unknown>, prop: string) {
        if (prop in target) return target[prop]
        target[prop] = vi.fn(() => ok())
        return target[prop]
      },
    }),
  }
})

// Spec 107 PR-C cross-review round 2, chunk 4 (P1): ModeSwitcher was
// unconditionally rendered in TopHeader for every principal kind, including
// a tenant session. routing_mode lives behind GET /routing and PATCH /config
// — both admin-only core doors (named must-refuse, rest-endpoints.md §8) —
// so a tenant who opened the panel and picked anything drew a fixed 403,
// contradicting FR-041's "hidden rather than issued-and-403'd" for
// tenant-inapplicable controls. TopHeader.vue must hide the control
// entirely for a tenant principal, not merely suppress its own fetch.

import TopHeader from '@/components/TopHeader.vue'
import { useAuthStore } from '@/stores/auth'
import { useProfilesStore } from '@/stores/profiles'
import api from '@/services/api'

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', name: 'dashboard', component: { template: '<div />' } }],
  })
}

async function mountTopHeaderAs(role: 'user' | 'admin') {
  const router = makeRouter()
  router.push('/')
  await router.isReady()

  const authStore = useAuthStore()
  authStore.isTeamsEdition = true
  authStore.loading = false
  authStore.authResolvedSuccessfully = true
  authStore.user = {
    id: 'u1',
    email: 'u1@example.com',
    display_name: 'U1',
    role,
    provider: 'oidc',
    created_at: '',
    last_login_at: '',
  }

  const wrapper = shallowMount(TopHeader, {
    global: {
      plugins: [router],
      stubs: { RouterLink: true },
    },
  })
  await flushPromises()
  return wrapper
}

async function mountTopHeaderForProfileStartup(options: {
  teamsEdition: boolean
  loading: boolean
  resolved: boolean
  role?: 'user' | 'admin'
}) {
  const router = makeRouter()
  router.push('/')
  await router.isReady()

  const authStore = useAuthStore()
  authStore.isTeamsEdition = options.teamsEdition
  authStore.loading = options.loading
  authStore.authResolvedSuccessfully = options.resolved
  authStore.user = options.role
    ? {
        id: 'u1', email: 'u1@example.com', display_name: 'U1', role: options.role,
        provider: 'oidc', created_at: '', last_login_at: '',
      }
    : null

  const wrapper = shallowMount(TopHeader, {
    global: { plugins: [router], stubs: { RouterLink: true } },
  })
  await flushPromises()
  return wrapper
}

describe('TopHeader tenant gating (Spec 107 FR-041, cross-review round 2 P1)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    // Each mount is an independent startup state. Do not let a prior header's
    // profile requests make a skip assertion vacuously pass or fail.
    vi.clearAllMocks()
  })

  // Spec 109 FR-057: profile population is a startup side effect, not merely
  // a rendering detail. These direct-header cases deliberately include the
  // personal branch: changing the guard to `canLoadCore && isAdmin` would
  // still pass the administrator case but must stop this one from fetching.
  it('populates the profile switcher for confirmed personal startup', async () => {
    const wrapper = await mountTopHeaderForProfileStartup({
      teamsEdition: false, loading: false, resolved: true,
    })

    expect(api.getProfiles).toHaveBeenCalledTimes(1)
    expect(api.getActiveProfile).toHaveBeenCalledTimes(1)
    expect(useProfilesStore().hasProfiles).toBe(true)
    expect(wrapper.findComponent({ name: 'ProfileSwitcher' }).exists()).toBe(true)
  })

  it('populates profiles for a confirmed administrator startup', async () => {
    await mountTopHeaderForProfileStartup({
      teamsEdition: true, loading: false, resolved: true, role: 'admin',
    })

    expect(api.getProfiles).toHaveBeenCalledTimes(1)
    expect(api.getActiveProfile).toHaveBeenCalledTimes(1)
    expect(useProfilesStore().hasProfiles).toBe(true)
  })

  it.each([
    ['tenant', { teamsEdition: true, loading: false, resolved: true, role: 'user' as const }],
    ['pending', { teamsEdition: true, loading: true, resolved: false, role: 'admin' as const }],
    ['bootstrap-failed', { teamsEdition: true, loading: false, resolved: false }],
    ['signed-out', { teamsEdition: true, loading: false, resolved: true }],
  ])('skips profile population for %s server startup', async (_state, options) => {
    await mountTopHeaderForProfileStartup(options)

    expect(api.getProfiles).not.toHaveBeenCalled()
    expect(api.getActiveProfile).not.toHaveBeenCalled()
    expect(useProfilesStore().hasProfiles).toBe(false)
  })

  // Spec 109-i FR-053: the mode switcher left the header for Clients ->
  // Endpoint & mode, so it is absent for every principal. The tenant concern
  // (routing_mode is admin-only, Spec 107 FR-041) is now satisfied by the
  // header simply not carrying it; Clients hides its own tenant surface.
  it('never renders the mode switcher (admin or tenant)', async () => {
    for (const role of ['user', 'admin'] as const) {
      const wrapper = await mountTopHeaderAs(role)
      expect(wrapper.find('[data-test="mode-switcher"]').exists()).toBe(false)
      expect(wrapper.findComponent({ name: 'ModeSwitcher' }).exists()).toBe(false)
    }
  })

  // Spec 107 PR-C cross-review round 3, chunk 4 (P2): adding a server from the
  // header submitted through /api/v1/tools/call, a core dispatch door the
  // tenant-session allowlist refuses with 403 (rest-endpoints.md §8). The
  // header "+ Add" menu (Spec 109-i FR-052, formerly the Add Server button)
  // must therefore be absent for a tenant, not merely mislabeled.
  it('hides the add menu for a tenant principal', async () => {
    const wrapper = await mountTopHeaderAs('user')
    expect(wrapper.find('[data-test="header-add-menu"]').exists()).toBe(false)
    expect(wrapper.findComponent({ name: 'AddMenu' }).exists()).toBe(false)
  })

  it('still renders the add menu for an admin principal', async () => {
    const wrapper = await mountTopHeaderAs('admin')
    expect(wrapper.findComponent({ name: 'AddMenu' }).exists()).toBe(true)
  })

  it('hides the status pill for a tenant (their servers store is never loaded)', async () => {
    const wrapper = await mountTopHeaderAs('user')
    expect(wrapper.findComponent({ name: 'StatusPill' }).exists()).toBe(false)
  })

  // Spec 107 PR-C cross-review round 3, chunk 4 (P2): selecting a profile in
  // ProfileSwitcher calls PUT /api/v1/profiles/active, which the
  // tenant-session allowlist also refuses with 403 (only GET /profiles* is
  // tenant-reachable) — an enabled control that always fails to act.
  it('hides the profile switcher for a tenant principal', async () => {
    const wrapper = await mountTopHeaderAs('user')
    expect(wrapper.find('[data-test="profile-switcher"]').exists()).toBe(false)
    expect(wrapper.findComponent({ name: 'ProfileSwitcher' }).exists()).toBe(false)
  })

  it('still renders the profile switcher for an admin principal', async () => {
    const wrapper = await mountTopHeaderAs('admin')
    expect(wrapper.findComponent({ name: 'ProfileSwitcher' }).exists()).toBe(true)
  })
})
