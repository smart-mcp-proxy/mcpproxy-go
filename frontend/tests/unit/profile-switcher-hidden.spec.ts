import { describe, it, expect, beforeEach, vi } from 'vitest'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 109 FR-057 (H2 interim, until Spec 108 removes the header
// ProfileSwitcher entirely): "Profile:" looked like agent scoping while only
// setting a UI default, so it is hidden when no profiles exist yet.

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  const base: Record<string, unknown> = {
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

import TopHeader from '@/components/TopHeader.vue'
import api from '@/services/api'

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', name: 'dashboard', component: { template: '<div />' } }],
  })
}

async function mountTopHeader() {
  const router = makeRouter()
  router.push('/')
  await router.isReady()
  const wrapper = shallowMount(TopHeader, {
    global: { plugins: [router], stubs: { RouterLink: true } },
  })
  await flushPromises()
  return wrapper
}

describe('header ProfileSwitcher visibility (Spec 109 FR-057)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.hasAPIKey as any).mockReturnValue(false)
  })

  it('renders no ProfileSwitcher when profilesStore.hasProfiles is false', async () => {
    ;(api.getProfiles as any).mockResolvedValue({ success: true, data: { profiles: [] } })
    const wrapper = await mountTopHeader()

    expect(wrapper.findComponent({ name: 'ProfileSwitcher' }).exists()).toBe(false)
    expect(wrapper.find('[data-test="profile-switcher"]').exists()).toBe(false)
  })

  it('renders no ProfileSwitcher when the profiles fetch fails', async () => {
    ;(api.getProfiles as any).mockResolvedValue({ success: false, error: 'boom' })
    const wrapper = await mountTopHeader()

    expect(wrapper.findComponent({ name: 'ProfileSwitcher' }).exists()).toBe(false)
  })

  it('renders the ProfileSwitcher once at least one profile exists', async () => {
    ;(api.getProfiles as any).mockResolvedValue({
      success: true,
      data: { profiles: [{ name: 'work', servers: ['alpha'], tool_count: 3 }] },
    })
    const wrapper = await mountTopHeader()

    expect(wrapper.findComponent({ name: 'ProfileSwitcher' }).exists()).toBe(true)
  })
})
