import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import ProfileEditor from '@/views/ProfileEditor.vue'
import api from '@/services/api'
import { PROFILES_CHANGED_EVENT } from '@/stores/profiles'
import { makeClient, makeProfile } from './fixtures/profiles108i'

// Issue #1446 item 2: edits typed while a save or a load is in flight must not
// be overwritten by the response.

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
const BASE = makeProfile('work-ro', { title: 'Work', servers: ['github'], effective_servers: ['github'] })

async function mountEditor() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/profiles', name: 'profiles', component: stub },
      { path: '/profiles/:name', name: 'profile-editor', component: ProfileEditor, props: true },
      { path: '/tokens', name: 'tokens', component: stub },
    ],
  })
  await router.push('/profiles/work-ro')
  await router.isReady()
  const wrapper = mount(ProfileEditor, { props: { name: 'work-ro' }, global: { plugins: [router] }, attachTo: document.body })
  await flushPromises()
  return wrapper
}

function deferred<T>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((r) => { resolve = r })
  return { promise, resolve }
}

describe('ProfileEditor in-flight edits (#1446)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.hasAPIKey as any).mockReturnValue(true)
    ;(api.getProfile as any).mockResolvedValue(BASE)
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [BASE] })
    ;(api.getProfileEffectiveTools as any).mockResolvedValue({ profile: 'work-ro', tools: [], counts: { visible: 0, hidden: 0 }, stale_classifications: [] })
    ;(api.getServers as any).mockResolvedValue({ success: true, data: { servers: [{ name: 'github' }] } })
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [makeClient('cursor')], warnings: [] } })
  })

  it('keeps a title typed while the save is in flight, still dirty', async () => {
    const wrapper = await mountEditor()
    const put = deferred<any>()
    ;(api.updateProfile as any).mockReturnValue(put.promise)

    await wrapper.get('[data-test="profile-title"]').setValue('First')
    await wrapper.get('[data-test="profile-form"]').trigger('submit')
    await wrapper.get('[data-test="profile-title"]').setValue('Second')
    put.resolve({ profile: { ...BASE, title: 'First' }, warnings: [] })
    await flushPromises()

    expect((wrapper.get('[data-test="profile-title"]').element as HTMLInputElement).value).toBe('Second')
    expect(wrapper.get('[data-test="profile-save-status"]').text()).toBe('Unsaved changes')
    wrapper.unmount()
  })

  it('refreshes an untouched draft from the save response', async () => {
    const wrapper = await mountEditor()
    ;(api.updateProfile as any).mockResolvedValue({ profile: { ...BASE, title: 'Server Title' }, warnings: [] })
    await wrapper.get('[data-test="profile-title"]').setValue('Mine')
    await wrapper.get('[data-test="profile-form"]').trigger('submit')
    await flushPromises()
    expect((wrapper.get('[data-test="profile-title"]').element as HTMLInputElement).value).toBe('Server Title')
    wrapper.unmount()
  })

  it('keeps an edit made while a background reload is in flight', async () => {
    const wrapper = await mountEditor()
    const slow = deferred<any>()
    ;(api.getProfile as any).mockReturnValue(slow.promise)
    window.dispatchEvent(new Event(PROFILES_CHANGED_EVENT)) // not dirty -> silent reload starts
    await flushPromises()
    await wrapper.get('[data-test="profile-title"]').setValue('Typed')
    slow.resolve({ ...BASE, title: 'Changed elsewhere' })
    await flushPromises()
    expect((wrapper.get('[data-test="profile-title"]').element as HTMLInputElement).value).toBe('Typed')
    expect(wrapper.get('[data-test="profile-save-status"]').text()).toBe('Unsaved changes')
    wrapper.unmount()
  })
})
