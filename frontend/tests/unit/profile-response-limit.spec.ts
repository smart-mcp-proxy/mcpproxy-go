import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import ProfileEditor from '@/views/ProfileEditor.vue'
import api from '@/services/api'
import { makeClient, makeProfile } from './fixtures/profiles108i'

vi.mock('@/services/api', () => ({
  default: {
    hasAPIKey: vi.fn(() => true),
    getProfile: vi.fn(),
    getProfiles: vi.fn(),
    getProfileEffectiveTools: vi.fn(),
    updateProfile: vi.fn(),
    tryProfile: vi.fn(),
    renameProfile: vi.fn(),
    deleteProfile: vi.fn(),
    getServers: vi.fn(),
    getClients: vi.fn(),
    setClientBinding: vi.fn(),
  },
}))

const stub = { template: '<div />' }

async function mountEditor(profile: Record<string, any>) {
  ;(api.getProfile as any).mockResolvedValue(profile)
  ;(api.getProfiles as any).mockResolvedValue({ profiles: [profile] })
  ;(api.updateProfile as any).mockImplementation(async (_name: string, cfg: any) => ({ profile: { ...profile, ...cfg }, warnings: [] }))
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/profiles', name: 'profiles', component: stub },
      { path: '/profiles/:name', name: 'profile-editor', component: ProfileEditor, props: true },
      { path: '/tokens', name: 'tokens', component: stub },
    ],
  })
  await router.push('/profiles/script')
  await router.isReady()
  const wrapper = mount(ProfileEditor, { props: { name: 'script' }, global: { plugins: [router] }, attachTo: document.body })
  await flushPromises()
  return wrapper
}

async function save(wrapper: any): Promise<Record<string, any>> {
  await wrapper.get('[data-test="profile-form"]').trigger('submit')
  await flushPromises()
  const calls = (api.updateProfile as any).mock.calls
  return calls[calls.length - 1][1]
}

// Issue #2027: a profile's own tool_response_limit.
describe('Profile editor tool response limit', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.hasAPIKey as any).mockReturnValue(true)
    ;(api.getProfileEffectiveTools as any).mockResolvedValue({ profile: 'script', tools: [], counts: { visible: 0, hidden: 0 }, stale_classifications: [] })
    ;(api.getServers as any).mockResolvedValue({ success: true, data: { servers: [{ name: 'github' }] } })
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [makeClient('cursor')], warnings: [] } })
  })

  it('keeps a stored "no limit" (0) when another field is saved', async () => {
    const wrapper = await mountEditor(makeProfile('script', { servers: ['github'], effective_servers: ['github'], tool_response_limit: 0 }))
    expect((wrapper.get('[data-test="profile-response-limit-none"]').element as HTMLInputElement).checked).toBe(true)
    await wrapper.get('[data-test="profile-title"]').setValue('Script')
    const body = await save(wrapper)
    expect(body.tool_response_limit).toBe(0)
  })

  it('keeps a stored custom limit and shows it', async () => {
    const wrapper = await mountEditor(makeProfile('script', { servers: ['github'], effective_servers: ['github'], tool_response_limit: 500000 }))
    expect((wrapper.get('[data-test="profile-response-limit-value"]').element as HTMLInputElement).value).toBe('500000')
    await wrapper.get('[data-test="profile-title"]').setValue('Script')
    expect((await save(wrapper)).tool_response_limit).toBe(500000)
  })

  it('Inherit omits the key, No limit sends 0, Custom sends the number', async () => {
    const wrapper = await mountEditor(makeProfile('script', { servers: ['github'], effective_servers: ['github'] }))
    expect((wrapper.get('[data-test="profile-response-limit-inherit"]').element as HTMLInputElement).checked).toBe(true)
    expect(wrapper.find('[data-test="profile-response-limit-value"]').exists()).toBe(false)

    await wrapper.get('[data-test="profile-response-limit-none"]').setValue(true)
    expect((await save(wrapper)).tool_response_limit).toBe(0)

    await wrapper.get('[data-test="profile-response-limit-custom"]').setValue(true)
    await wrapper.get('[data-test="profile-response-limit-value"]').setValue('75000')
    expect((await save(wrapper)).tool_response_limit).toBe(75000)

    await wrapper.get('[data-test="profile-response-limit-inherit"]').setValue(true)
    expect('tool_response_limit' in (await save(wrapper))).toBe(false)
  })

  it('an emptied Custom value on a stored "no limit" profile stays dirty and is refused', async () => {
    const wrapper = await mountEditor(makeProfile('script', { servers: ['github'], effective_servers: ['github'], tool_response_limit: 0 }))
    await wrapper.get('[data-test="profile-response-limit-custom"]').setValue(true)
    await wrapper.get('[data-test="profile-response-limit-value"]').setValue('')
    expect(wrapper.get('[data-test="profile-editor-save"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-test="profile-form"]').trigger('submit')
    await flushPromises()
    expect(api.updateProfile).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="profile-response-limit"]').text()).toContain('Enter a whole number')
  })

  it('refuses an empty custom limit instead of saving it as "no limit"', async () => {
    const wrapper = await mountEditor(makeProfile('script', { servers: ['github'], effective_servers: ['github'] }))
    await wrapper.get('[data-test="profile-response-limit-custom"]').setValue(true)
    await wrapper.get('[data-test="profile-response-limit-value"]').setValue('')
    await wrapper.get('[data-test="profile-form"]').trigger('submit')
    await flushPromises()
    expect(api.updateProfile).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="profile-response-limit"]').text()).toContain('Enter a whole number')
  })
})
