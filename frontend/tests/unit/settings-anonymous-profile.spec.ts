import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import AnonymousProfileSetting from '@/components/settings/AnonymousProfileSetting.vue'
import api from '@/services/api'
import { GUARD_REFUSAL, WORK_FULL, WORK_RO, apiError } from './fixtures/profiles108i'

vi.mock('@/services/api', () => ({
  default: {
    hasAPIKey: vi.fn(() => true),
    getProfiles: vi.fn(),
    setAnonymousProfile: vi.fn(),
    patchConfig: vi.fn(),
  },
}))

const stub = { template: '<div />' }

async function mountSetting(requireMcpAuth: boolean, url = '/settings') {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/settings', name: 'settings', component: stub }, { path: '/profiles', name: 'profiles', component: stub }] })
  await router.push(url)
  await router.isReady()
  const wrapper = mount(AnonymousProfileSetting, { props: { requireMcpAuth }, global: { plugins: [router] }, attachTo: document.body })
  await flushPromises()
  return { wrapper, router }
}

describe('Settings -> Security: Anonymous callers (Spec 108-i T098a)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [WORK_RO, WORK_FULL], anonymous_profile: '' })
    ;(api.setAnonymousProfile as any).mockResolvedValue({ success: true, data: {} })
  })

  it('takes its options from GET /profiles, with Unconfined first', async () => {
    const { wrapper } = await mountSetting(false)
    const options = wrapper.get('[data-test="anonymous-profile-select"]').findAll('option')
    expect(options.map(option => option.text())).toEqual(['Unconfined — all servers', 'Work (work-ro)', 'Work Full (work-full)'])
    expect(options.map(option => option.attributes('value'))).toEqual(['', 'work-ro', 'work-full'])
  })

  it('preselects the current anonymous_profile', async () => {
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [WORK_RO, WORK_FULL], anonymous_profile: 'work-full' })
    const { wrapper } = await mountSetting(false)
    expect((wrapper.get('[data-test="anonymous-profile-select"]').element as HTMLSelectElement).value).toBe('work-full')
    expect(wrapper.get('[data-test="anonymous-profile-save"]').attributes('disabled')).toBeDefined()
  })

  it('saves a profile through PATCH /config {anonymous_profile}, and Unconfined sends ""', async () => {
    const { wrapper } = await mountSetting(false)
    await wrapper.get('[data-test="anonymous-profile-select"]').setValue('work-ro')
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [WORK_RO, WORK_FULL], anonymous_profile: 'work-ro' })
    await wrapper.get('[data-test="anonymous-profile-save"]').trigger('click')
    await flushPromises()
    expect(api.setAnonymousProfile).toHaveBeenLastCalledWith('work-ro')
    expect(wrapper.get('[data-test="anonymous-profile-saved"]').text()).toContain('Work')

    await wrapper.get('[data-test="anonymous-profile-select"]').setValue('')
    await wrapper.get('[data-test="anonymous-profile-save"]').trigger('click')
    await flushPromises()
    expect(api.setAnonymousProfile).toHaveBeenLastCalledWith('')
  })

  it('explains differently with require_mcp_auth on and off', async () => {
    const off = await mountSetting(false)
    expect(off.wrapper.get('[data-test="anonymous-profile-explanation"]').text()).toContain('Callers that send no credential get this profile.')
    expect(off.wrapper.get('[data-test="anonymous-profile-explanation"]').text()).toContain('binding guard')
    const on = await mountSetting(true)
    expect(on.wrapper.get('[data-test="anonymous-profile-explanation"]').text()).toBe('Anonymous callers are refused because authentication is required. This setting applies only if you turn authentication off.')
  })

  it('a 409 shows GuardRefusal with both fixes', async () => {
    ;(api.setAnonymousProfile as any).mockRejectedValue(apiError(GUARD_REFUSAL.error, GUARD_REFUSAL))
    const { wrapper } = await mountSetting(false)
    await wrapper.get('[data-test="anonymous-profile-select"]').setValue('work-full')
    await wrapper.get('[data-test="anonymous-profile-save"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="guard-refusal"]').text()).toContain('reachable without authentication')
    expect(wrapper.findAll('[data-test^="guard-fix-"]')).toHaveLength(2)
    expect(wrapper.find('[data-test="anonymous-profile-saved"]').exists()).toBe(false)
  })

  it('?focus=anonymous_profile&value=x preselects without saving, and focuses the control', async () => {
    const { wrapper } = await mountSetting(false, '/settings?tab=security&focus=anonymous_profile&value=work-full')
    const select = wrapper.get('[data-test="anonymous-profile-select"]')
    expect((select.element as HTMLSelectElement).value).toBe('work-full')
    expect(document.activeElement).toBe(select.element)
    expect(api.setAnonymousProfile).not.toHaveBeenCalled()
    expect(api.patchConfig).not.toHaveBeenCalled()
    // The operator confirms it with Save.
    expect(wrapper.get('[data-test="anonymous-profile-save"]').attributes('disabled')).toBeUndefined()
  })

  it('offers "Create a profile first" when there are none', async () => {
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [] })
    const { wrapper } = await mountSetting(false)
    expect(wrapper.get('[data-test="anonymous-profile-create-link"]').text()).toBe('Create a profile first')
  })
})
