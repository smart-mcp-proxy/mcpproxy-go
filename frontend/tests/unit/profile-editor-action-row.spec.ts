import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import ProfileEditor from '@/views/ProfileEditor.vue'
import api from '@/services/api'
import { makeClient, makeProfile } from './fixtures/profiles108i'

// Spec 134 PR 8 (FR-052): one action row after both the policy form and the
// tool rules, in normal flow, still submitting the form on Enter.

vi.mock('@/services/api', () => ({
  default: {
    hasAPIKey: vi.fn(() => true), getProfile: vi.fn(), getProfiles: vi.fn(), getProfileEffectiveTools: vi.fn(),
    updateProfile: vi.fn(), tryProfile: vi.fn(), renameProfile: vi.fn(), deleteProfile: vi.fn(),
    getServers: vi.fn(), getClients: vi.fn(), setClientBinding: vi.fn(),
  },
}))

const stub = { template: '<div />' }
const P = makeProfile('work-ro', { title: 'Work', servers: ['github'], effective_servers: ['github'], max_tier: 'read' })

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

describe('Profile editor action row (FR-052)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.getProfile as any).mockResolvedValue(P)
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [P] })
    ;(api.getProfileEffectiveTools as any).mockResolvedValue({ profile: 'work-ro', tools: [], counts: { visible: 0, hidden: 0 } })
    ;(api.updateProfile as any).mockImplementation(async (_n: string, cfg: any) => ({ profile: { ...P, ...cfg }, warnings: [] }))
    ;(api.getServers as any).mockResolvedValue({ success: true, data: { servers: [{ name: 'github' }] } })
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [makeClient('cursor')], warnings: [] } })
  })

  it('sits after both the form and the tool rules, in normal flow', async () => {
    const w = await mountEditor()
    const footer = w.get('[data-test="profile-editor-footer"]').element as HTMLElement
    const form = w.get('[data-test="profile-form"]').element
    expect(form.compareDocumentPosition(footer) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    const tools = w.find('[data-test="profile-tool-table"]').exists()
      ? w.get('[data-test="profile-tool-table"]').element
      : w.get('[data-test="profile-tools-region"]').element
    expect(tools.compareDocumentPosition(footer) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(footer.className).not.toMatch(/\b(sticky|fixed|absolute)\b/)
  })

  it('keeps Save as a submit button of the form, and Discard', async () => {
    const w = await mountEditor()
    const form = w.get('[data-test="profile-form"]')
    const save = w.get('[data-test="profile-editor-save"]')
    expect(save.attributes('type')).toBe('submit')
    expect(save.attributes('form')).toBe(form.attributes('id'))
    expect(form.attributes('id')).toBeTruthy()
    expect(w.find('[data-test="profile-editor-discard"]').exists()).toBe(true)
    await w.get('[data-test="profile-title"]').setValue('New title')
    expect(w.get('[data-test="profile-save-status"]').text()).toBe('Unsaved changes')
    await form.trigger('submit')
    await flushPromises()
    expect(api.updateProfile).toHaveBeenCalled()
  })

  it('a refused custom response limit brings its message into view', async () => {
    const scroll = vi.fn()
    ;(Element.prototype as any).scrollIntoView = scroll
    const w = await mountEditor()
    await w.get('[data-test="profile-response-limit-custom"]').setValue(true)
    await w.get('[data-test="profile-response-limit-value"]').setValue('')
    await w.get('[data-test="profile-form"]').trigger('submit')
    await flushPromises()
    expect(w.get('[data-test="profile-response-limit"]').text()).toContain('whole number')
    expect(scroll).toHaveBeenCalled()
    const field = w.get('[data-test="profile-response-limit-value"]')
    expect(field.attributes('aria-invalid')).toBe('true')
    expect(w.get(`#${field.attributes('aria-describedby')}`).text()).toContain('whole number')
    // Focus lands in the invalid field itself, so the next Tab stays on it.
    expect(document.activeElement).toBe(w.get('[data-test="profile-response-limit-value"]').element)
    // Correcting the value clears the stale invalid state.
    await w.get('[data-test="profile-response-limit-value"]').setValue('20000')
    await flushPromises()
    expect(w.get('[data-test="profile-response-limit-value"]').attributes('aria-invalid')).toBeUndefined()
    delete (Element.prototype as any).scrollIntoView
  })
})
