import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

// Profiles v2 (MCP-3243 / T4): the Web UI profile switcher consumes the REST
// surface shipped in MCP-3241 — GET /api/v1/profiles (list + servers + tool
// count), GET /api/v1/profiles/active and PUT /api/v1/profiles/active. The
// switcher must: load on mount, render each profile's membership + tool count,
// highlight the active selection, let the user switch (PUT) and clear back to
// "All servers" (empty slug), and handle the zero-profile default gracefully.

const getProfiles = vi.hoisted(() => vi.fn())
const getActiveProfile = vi.hoisted(() => vi.fn())
const setActiveProfile = vi.hoisted(() => vi.fn())
// Stateful backend stub: a successful PUT persists the active profile so a
// subsequent GET (the switcher refreshes on open) reflects it — mirroring the
// real server-level default in httpapi/profiles.go.
const backend = vi.hoisted(() => ({ active: '' }))

vi.mock('@/services/api', () => ({
  default: {
    getProfiles,
    getActiveProfile,
    setActiveProfile,
  },
}))

import ProfileSwitcher from '@/components/ProfileSwitcher.vue'

const SAMPLE_PROFILES = [
  { name: 'dev', servers: ['github', 'ast-grep'], tool_count: 42 },
  { name: 'solo', servers: ['github'], tool_count: 1 },
]

function mountSwitcher() {
  return mount(ProfileSwitcher, {
    global: { plugins: [createPinia()] },
  })
}

describe('ProfileSwitcher (Profiles v2 T4)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    backend.active = ''
    getProfiles.mockReset().mockResolvedValue({ success: true, data: { profiles: SAMPLE_PROFILES } })
    getActiveProfile.mockReset().mockImplementation(() =>
      Promise.resolve({ success: true, data: { active_profile: backend.active } })
    )
    setActiveProfile.mockReset().mockImplementation((profile: string) => {
      backend.active = profile
      return Promise.resolve({ success: true, data: { active_profile: profile } })
    })
  })

  it('loads profiles + active on mount and defaults to "All servers"', async () => {
    const wrapper = mountSwitcher()
    await flushPromises()

    expect(getProfiles).toHaveBeenCalledTimes(1)
    expect(getActiveProfile).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-test="profile-switcher-active"]').text()).toBe('All servers')
    // Menu is closed until the button is clicked.
    expect(wrapper.find('[data-test="profile-switcher-menu"]').exists()).toBe(false)
  })

  it('opens the menu and renders each profile with server + tool counts', async () => {
    const wrapper = mountSwitcher()
    await flushPromises()

    await wrapper.find('[data-test="profile-switcher-button"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="profile-switcher-menu"]').exists()).toBe(true)
    const dev = wrapper.find('[data-test="profile-option-dev"]')
    expect(dev.exists()).toBe(true)
    expect(dev.text()).toContain('dev')
    expect(dev.text()).toContain('2 servers')
    expect(dev.text()).toContain('42 tools')
    // Singular grammar for the single-server / single-tool profile.
    const solo = wrapper.find('[data-test="profile-option-solo"]')
    expect(solo.text()).toContain('1 server')
    expect(solo.text()).toContain('1 tool')
    // "All servers" is active by default.
    expect(wrapper.find('[data-test="profile-option-all"] [data-test="profile-active-badge"]').exists()).toBe(true)
  })

  it('switches the active profile via PUT and updates the label + badge', async () => {
    const wrapper = mountSwitcher()
    await flushPromises()
    await wrapper.find('[data-test="profile-switcher-button"]').trigger('click')
    await flushPromises()

    await wrapper.find('[data-test="profile-option-dev"]').trigger('click')
    await flushPromises()

    expect(setActiveProfile).toHaveBeenCalledWith('dev')
    expect(wrapper.find('[data-test="profile-switcher-active"]').text()).toBe('dev')
    // Re-open to verify the active badge moved to "dev".
    await wrapper.find('[data-test="profile-switcher-button"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="profile-active-badge-dev"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="profile-option-all"] [data-test="profile-active-badge"]').exists()).toBe(false)
  })

  it('clears back to "All servers" with an empty slug', async () => {
    backend.active = 'dev'
    const wrapper = mountSwitcher()
    await flushPromises()
    expect(wrapper.find('[data-test="profile-switcher-active"]').text()).toBe('dev')

    await wrapper.find('[data-test="profile-switcher-button"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="profile-option-all"]').trigger('click')
    await flushPromises()

    expect(setActiveProfile).toHaveBeenCalledWith('')
    expect(wrapper.find('[data-test="profile-switcher-active"]').text()).toBe('All servers')
  })

  it('renders the empty state when no profiles are configured', async () => {
    getProfiles.mockResolvedValue({ success: true, data: { profiles: [] } })
    const wrapper = mountSwitcher()
    await flushPromises()

    await wrapper.find('[data-test="profile-switcher-button"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="profile-switcher-empty"]').exists()).toBe(true)
    // "All servers" is still selectable even with zero profiles.
    expect(wrapper.find('[data-test="profile-option-all"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="profile-option-dev"]').exists()).toBe(false)
  })

  it('surfaces a set-active failure without changing the label', async () => {
    setActiveProfile.mockResolvedValue({ success: false, error: 'unknown profile' })
    const wrapper = mountSwitcher()
    await flushPromises()
    await wrapper.find('[data-test="profile-switcher-button"]').trigger('click')
    await flushPromises()

    await wrapper.find('[data-test="profile-option-dev"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="profile-switcher-active"]').text()).toBe('All servers')
  })
})
