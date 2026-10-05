import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import ProfileEditor from '@/views/ProfileEditor.vue'
import api from '@/services/api'
import { useProfilesStore } from '@/stores/profiles'
import { WORK_FULL, GUARD_REFUSAL, apiError, makeClient, makeProfile } from './fixtures/profiles108i'

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
const FULL = makeProfile('work-ro', {
  title: 'Work Read-only',
  description: 'Read-only view',
  servers: ['github', 'notion'],
  effective_servers: ['github', 'notion'],
  max_tier: 'read',
  unannotated: 'deny',
  tools: { allow: ['notion:update_page'], deny: ['github:*secret*'], classify: { 'github:search_code': 'read', 'github:list_issues': 'write' } },
  code_execution: false,
  management_tools: false,
  switchable_to: ['work-full'],
  used_by: { clients: [{ id: 'cursor', mode: 'locked' }], tokens: ['ci'], anonymous_profile: false },
})

// effective_tools.json (T003) rows, plus an allowed write tool.
const ROWS = [
  { server: 'github', tool: 'list_issues', intrinsic_tier: 'read', profile_tier: 'read', access: { visible: true, callable: true, reason: '' }, classification_stale: true },
  { server: 'github', tool: 'create_issue', intrinsic_tier: 'write', profile_tier: 'write', access: { visible: false, callable: false, reason: 'above_tier_cap' }, classification_stale: false },
  { server: 'github', tool: 'search_code', intrinsic_tier: 'unannotated', profile_tier: 'read', access: { visible: true, callable: true, reason: '' }, classification_stale: false },
  { server: 'github', tool: 'other', intrinsic_tier: 'unannotated', profile_tier: 'write', access: { visible: false, callable: false, reason: 'unannotated_hidden' }, classification_stale: false },
]

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/profiles', name: 'profiles', component: stub },
      { path: '/profiles/:name', name: 'profile-editor', component: ProfileEditor, props: true },
      { path: '/tokens', name: 'tokens', component: stub },
    ],
  })
}

async function mountEditor(query = '') {
  const router = makeRouter()
  await router.push(`/profiles/work-ro${query}`)
  await router.isReady()
  const wrapper = mount(ProfileEditor, { props: { name: 'work-ro' }, global: { plugins: [router] }, attachTo: document.body })
  await flushPromises()
  return { wrapper, router }
}

function lastPut(): Record<string, any> {
  const calls = (api.updateProfile as any).mock.calls
  return calls[calls.length - 1][1]
}

describe('Profile policy editor (Spec 108-i T091, FR-041, FR-005)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.hasAPIKey as any).mockReturnValue(true)
    ;(api.getProfile as any).mockResolvedValue(FULL)
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [FULL, WORK_FULL] })
    ;(api.getProfileEffectiveTools as any).mockResolvedValue({ profile: 'work-ro', tools: ROWS, counts: { visible: 2, hidden: 2 }, stale_classifications: ['github:gone'] })
    ;(api.updateProfile as any).mockImplementation(async (_name: string, cfg: any) => ({ profile: { ...FULL, ...cfg }, warnings: [] }))
    ;(api.getServers as any).mockResolvedValue({ success: true, data: { servers: [{ name: 'github' }, { name: 'notion' }, { name: 'slack' }] } })
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [makeClient('cursor')], warnings: [] } })
  })

  it('round-trips every FR-041 field into the PUT body with the REST names and omission semantics', async () => {
    const { wrapper } = await mountEditor()
    // Nothing edited: the round trip is the stored document (no dirty state).
    expect(wrapper.get('[data-test="profile-editor-save"]').attributes('disabled')).toBeDefined()

    await wrapper.get('[data-test="profile-title"]').setValue('Renamed')
    await wrapper.get('[data-test="profile-description"]').setValue('New text')
    await wrapper.get('[data-test="profile-server-slack"]').setValue(true)
    await wrapper.get('[data-test="profile-tier-write"]').setValue(true)
    await wrapper.get('[data-test="profile-unannotated-as_read"]').setValue(true)
    await wrapper.get('[data-test="profile-code-execution-on"]').setValue(true)
    await wrapper.get('[data-test="profile-management-tools-inherit"]').setValue(true)
    await wrapper.get('[data-test="profile-switch-none"]').setValue(true)
    await wrapper.get('[data-test="profile-form"]').trigger('submit')
    await flushPromises()

    expect(api.updateProfile).toHaveBeenCalledTimes(1)
    const body = lastPut()
    expect(body).toEqual({
      name: 'work-ro',
      title: 'Renamed',
      description: 'New text',
      servers: ['github', 'notion', 'slack'],
      max_tier: 'write',
      unannotated: 'as_read',
      tools: FULL.tools,
      code_execution: true,
      switchable_to: [],
    })
    // Inherit leaves the key out entirely; "None" is an empty list.
    expect('management_tools' in body).toBe(false)
  })

  it('code execution Inherit omits the key and switchable "Not set" omits switchable_to', async () => {
    const { wrapper } = await mountEditor()
    await wrapper.get('[data-test="profile-code-execution-inherit"]').setValue(true)
    await wrapper.get('[data-test="profile-switch-unset"]').setValue(true)
    await wrapper.get('[data-test="profile-form"]').trigger('submit')
    await flushPromises()
    const body = lastPut()
    expect('code_execution' in body).toBe(false)
    expect('switchable_to' in body).toBe(false)
    expect(body.management_tools).toBe(false)
  })

  it('renders the per-tool table with the access reason in words', async () => {
    const { wrapper } = await mountEditor()
    const cap = wrapper.get('[data-test="profile-tool-row-github__create_issue"]')
    expect(cap.text()).toContain('Hidden')
    expect(cap.text()).toContain('Above tier cap')
    expect(wrapper.get('[data-test="profile-tool-row-github__other"]').text()).toContain('Unannotated — classify')
    expect(wrapper.get('[data-test="profile-tool-row-github__search_code"]').text()).toContain('Visible')
    expect(wrapper.get('[data-test="profile-tool-counts"]').text()).toContain('2 visible')
  })

  it('the Allow and Deny toggles edit the draft tools.allow / tools.deny', async () => {
    const { wrapper } = await mountEditor()
    await wrapper.get('[data-test="tool-allow-github__create_issue"]').setValue(true)
    await wrapper.get('[data-test="tool-deny-github__search_code"]').setValue(true)
    expect(wrapper.get('[data-test="profile-save-status"]').text()).toBe('Unsaved changes')
    await wrapper.get('[data-test="profile-form"]').trigger('submit')
    await flushPromises()
    expect(lastPut().tools.allow).toEqual(['notion:update_page', 'github:create_issue'])
    expect(lastPut().tools.deny).toEqual(['github:*secret*', 'github:search_code'])
    expect(wrapper.get('[data-test="tool-allow-github__create_issue"]').attributes('aria-label')).toBe('Allow github:create_issue in Work Read-only')
  })

  it('offers Classify only on unannotated rows and writes tools.classify', async () => {
    const { wrapper } = await mountEditor()
    expect(wrapper.find('[data-test="tool-classify-github__create_issue"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="tool-classify-github__list_issues"]').exists()).toBe(false)
    const select = wrapper.get('[data-test="tool-classify-github__other"]')
    await select.setValue('read')
    await wrapper.get('[data-test="profile-form"]').trigger('submit')
    await flushPromises()
    expect(lastPut().tools.classify).toEqual({ ...FULL.tools!.classify, 'github:other': 'read' })
  })

  it('shows the stale classification marker exactly and can remove the classification', async () => {
    const { wrapper } = await mountEditor()
    const marker = wrapper.get('[data-test="profile-stale-github__list_issues"]')
    expect(marker.text()).toBe('classification ignored — tool is now annotated')
    await wrapper.get('[data-test="tool-remove-classification-github__list_issues"]').trigger('click')
    await wrapper.get('[data-test="profile-form"]').trigger('submit')
    await flushPromises()
    expect(lastPut().tools.classify).toEqual({ 'github:search_code': 'read' })
  })

  it('Try it posts the UNSAVED draft to /profiles/try and renders the hidden list', async () => {
    ;(api.tryProfile as any).mockResolvedValue({ results: [{ score: 1, tool: { name: 'github:search_code', server_name: 'github', description: 'Search' } }], hidden_by_profile: 2, hidden: [{ server: 'github', tool: 'create_issue', reason: 'above_tier_cap' }] })
    const { wrapper } = await mountEditor()
    await wrapper.get('[data-test="profile-tier-destructive"]').setValue(true)
    await wrapper.get('[data-test="profile-try-query"]').setValue('issue')
    await wrapper.get('[data-test="profile-try-run"]').trigger('submit')
    await flushPromises()
    expect(api.tryProfile).toHaveBeenCalledTimes(1)
    const call = (api.tryProfile as any).mock.calls[0][0]
    expect(call.query).toBe('issue')
    expect(call.profile.max_tier).toBe('destructive')
    expect(api.updateProfile).not.toHaveBeenCalled()
    const results = wrapper.get('[data-test="profile-try-results"]')
    expect(results.attributes('aria-live')).toBe('polite')
    expect(results.text()).toContain('Hidden by profile: 2')
    expect(results.text()).toContain('github:create_issue')
    expect(results.text()).toContain('Above tier cap')
  })

  it('Rename lists what moves before posting the rename', async () => {
    ;(api.renameProfile as any).mockResolvedValue({ profile: { ...FULL, name: 'work-ro2' }, moved: { clients: ['cursor'], tokens: ['ci'] } })
    const { wrapper, router } = await mountEditor()
    // Opening the dialog refreshes the list, so the impact list never reads a stale one.
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [FULL, makeProfile('other', { switchable_to: ['work-ro'] })] })
    await wrapper.get('[data-test="profile-rename"]').trigger('click')
    await flushPromises()
    const impact = wrapper.get('[data-test="profile-rename-dialog"] [data-test="profile-impact"]')
    expect(impact.text()).toContain('This moves:')
    expect(impact.get('[data-test="impact-client-cursor"]').text()).toContain('locked')
    expect(impact.get('[data-test="impact-token-ci"]').exists()).toBe(true)
    expect(impact.get('[data-test="impact-switchable-other"]').exists()).toBe(true)
    expect(api.renameProfile).not.toHaveBeenCalled()
    await wrapper.get('[data-test="profile-rename-input"]').setValue('work-ro2')
    await wrapper.get('[data-test="profile-rename-submit"]').trigger('submit')
    await flushPromises()
    expect(api.renameProfile).toHaveBeenCalledWith('work-ro', 'work-ro2')
    expect(router.currentRoute.value.params.name).toBe('work-ro2')
  })

  it('a rename never refetches the old name when its events arrive (no 404 console noise)', async () => {
    ;(api.renameProfile as any).mockImplementation(async () => {
      // The server emits profiles.changed and client.binding_changed while the
      // rename is still in flight: the open editor must not reload the old name.
      window.dispatchEvent(new CustomEvent('mcpproxy:profiles.changed'))
      window.dispatchEvent(new CustomEvent('mcpproxy:client.binding_changed'))
      window.dispatchEvent(new CustomEvent('mcpproxy:profiles.changed'))
      await Promise.resolve()
      return { profile: { ...FULL, name: 'work-ro2' }, moved: { clients: ['cursor'], tokens: ['ci'] } }
    })
    ;(api.getProfile as any).mockImplementation(async (name: string) => {
      if (name !== 'work-ro') return { ...FULL, name }
      return FULL
    })
    const { wrapper, router } = await mountEditor()
    ;(api.getProfile as any).mockClear()
    await wrapper.get('[data-test="profile-rename"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="profile-rename-input"]').setValue('work-ro2')
    await wrapper.get('[data-test="profile-rename-submit"]').trigger('submit')
    await flushPromises()
    expect(router.currentRoute.value.params.name).toBe('work-ro2')
    const names = (api.getProfile as any).mock.calls.map((call: any[]) => call[0])
    expect(names).not.toContain('work-ro')
  })

  it('a failed rename re-enables reloads for the unchanged name', async () => {
    ;(api.renameProfile as any).mockRejectedValue(apiError('name already exists', { field: 'new_name', status: 409 }))
    const { wrapper } = await mountEditor()
    await wrapper.get('[data-test="profile-rename"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="profile-rename-input"]').setValue('taken')
    await wrapper.get('[data-test="profile-rename-submit"]').trigger('submit')
    await flushPromises()
    ;(api.getProfile as any).mockClear()
    window.dispatchEvent(new CustomEvent('mcpproxy:profiles.changed'))
    await flushPromises()
    expect(api.getProfile).toHaveBeenCalledWith('work-ro')
  })

  it('Delete of an in-use profile requires a target and hides force for the anonymous_profile', async () => {
    ;(api.deleteProfile as any).mockResolvedValue({ deleted: 'work-ro', moved: { clients: ['cursor'], tokens: ['ci'] } })
    const { wrapper } = await mountEditor()
    useProfilesStore().profiles = [FULL, WORK_FULL]
    await wrapper.get('[data-test="profile-delete"]').trigger('click')
    await flushPromises()
    const dialog = wrapper.get('[data-test="profile-delete-dialog"]')
    expect(dialog.get('[data-test="profile-delete-target"]').exists()).toBe(true)
    expect(dialog.get('[data-test="profile-delete-confirm"]').attributes('disabled')).toBeDefined()
    expect(dialog.find('[data-test="profile-delete-force"]').exists()).toBe(true)
    await dialog.get('[data-test="profile-delete-target"]').setValue('work-full')
    await dialog.get('[data-test="profile-delete-confirm"]').trigger('click')
    await flushPromises()
    expect(api.deleteProfile).toHaveBeenCalledWith('work-ro', { reassign_to: 'work-full' })
  })

  it('does not offer force when the profile is the anonymous_profile', async () => {
    ;(api.getProfile as any).mockResolvedValue({ ...FULL, used_by: { clients: [], tokens: [], anonymous_profile: true } })
    const { wrapper } = await mountEditor()
    useProfilesStore().profiles = [FULL, WORK_FULL]
    await wrapper.get('[data-test="profile-delete"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="profile-delete-force"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="profile-delete-target"]').exists()).toBe(true)
  })

  it('?focus=server:tool rings the row, sets aria-current, focuses its Allow toggle and announces it', async () => {
    const { wrapper } = await mountEditor('?focus=github:create_issue')
    const row = wrapper.get('[data-test="profile-tool-row-github__create_issue"]')
    expect(row.attributes('aria-current')).toBe('true')
    expect(row.classes()).toContain('ring-2')
    expect(document.activeElement).toBe(wrapper.get('[data-test="tool-allow-github__create_issue"]').element)
    expect(wrapper.get('[data-test="profile-focus-announce"]').text()).toBe('Focused github:create_issue')
  })

  it('offers to add the server when the focused tool\'s server is not in the profile', async () => {
    ;(api.getProfileEffectiveTools as any).mockResolvedValue({ profile: 'work-ro', tools: [], counts: { visible: 0, hidden: 0 } })
    const { wrapper } = await mountEditor('?focus=slack:post_message')
    const notice = wrapper.get('[data-test="profile-focus-add-server"]')
    expect(notice.text()).toContain('Add slack to this profile')
    await notice.get('button').trigger('click')
    expect(wrapper.get('[data-test="profile-server-slack"]').element.checked).toBe(true)
  })

  it('a 409 guard on save shows GuardRefusal and keeps the draft', async () => {
    ;(api.updateProfile as any).mockRejectedValue(apiError(GUARD_REFUSAL.error, GUARD_REFUSAL))
    const { wrapper } = await mountEditor()
    await wrapper.get('[data-test="profile-title"]').setValue('Edited')
    await wrapper.get('[data-test="profile-form"]').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[data-test="guard-refusal"]').text()).toContain('reachable without authentication')
    expect(wrapper.findAll('[data-test^="guard-fix-"]')).toHaveLength(2)
    expect((wrapper.get('[data-test="profile-title"]').element as HTMLInputElement).value).toBe('Edited')
    expect(wrapper.get('[data-test="profile-save-status"]').text()).toBe('Unsaved changes')
  })

  it('a refused save moves focus to the refusal and scrolls it clear of the sticky footer', async () => {
    const scroll = vi.fn()
    ;(Element.prototype as any).scrollIntoView = scroll
    ;(api.updateProfile as any).mockRejectedValue(apiError(GUARD_REFUSAL.error, GUARD_REFUSAL))
    const { wrapper } = await mountEditor()
    await wrapper.get('[data-test="profile-title"]').setValue('Edited')
    await wrapper.get('[data-test="profile-form"]').trigger('submit')
    await flushPromises()
    const refusal = wrapper.get('[data-test="guard-refusal"]').element
    expect(document.activeElement).toBe(refusal)
    expect(scroll).toHaveBeenCalledWith(expect.objectContaining({ block: 'center' }))
    expect(wrapper.get('[data-test="guard-refusal"]').element.parentElement?.className).toContain('scroll-mb')
    delete (Element.prototype as any).scrollIntoView
  })

  it('a 400 marks the field the validator names', async () => {
    ;(api.updateProfile as any).mockRejectedValue(apiError('max_tier must be one of read, write, destructive', { field: 'max_tier', status: 400 }))
    const { wrapper } = await mountEditor()
    await wrapper.get('[data-test="profile-tier-write"]').setValue(true)
    await wrapper.get('[data-test="profile-form"]').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[data-test="profile-max-tier"]').text()).toContain('max_tier must be one of')
  })

  it('a profiles.changed event with a dirty draft shows the notice and keeps the draft', async () => {
    const { wrapper } = await mountEditor()
    await wrapper.get('[data-test="profile-title"]').setValue('Edited')
    ;(api.getProfile as any).mockResolvedValue({ ...FULL, title: 'Changed elsewhere' })
    window.dispatchEvent(new CustomEvent('mcpproxy:profiles.changed'))
    await flushPromises()
    expect(wrapper.get('[data-test="profile-changed-elsewhere"]').text()).toContain('This profile changed elsewhere')
    expect((wrapper.get('[data-test="profile-title"]').element as HTMLInputElement).value).toBe('Edited')
    await wrapper.get('[data-test="profile-reload"]').trigger('click')
    await flushPromises()
    expect((wrapper.get('[data-test="profile-title"]').element as HTMLInputElement).value).toBe('Changed elsewhere')
  })

  it('a clean draft silently refetches on profiles.changed', async () => {
    const { wrapper } = await mountEditor()
    ;(api.getProfile as any).mockResolvedValue({ ...FULL, title: 'Changed elsewhere' })
    window.dispatchEvent(new CustomEvent('mcpproxy:profiles.changed'))
    await flushPromises()
    expect(wrapper.find('[data-test="profile-changed-elsewhere"]').exists()).toBe(false)
    expect((wrapper.get('[data-test="profile-title"]').element as HTMLInputElement).value).toBe('Changed elsewhere')
  })

  it('drops the answer of a save for a profile the operator has already left', async () => {
    let finish!: (value: unknown) => void
    ;(api.updateProfile as any).mockReturnValue(new Promise(resolve => { finish = resolve }))
    const { wrapper } = await mountEditor()
    await wrapper.get('[data-test="profile-title"]').setValue('Edited')
    await wrapper.get('[data-test="profile-form"]').trigger('submit')
    // Navigate to another profile while the PUT is in flight.
    ;(api.getProfile as any).mockResolvedValue(makeProfile('other', { title: 'Other profile', servers: ['github'] }))
    await wrapper.setProps({ name: 'other' })
    await flushPromises()
    expect((wrapper.get('[data-test="profile-title"]').element as HTMLInputElement).value).toBe('Other profile')
    finish({ profile: { ...FULL, title: 'Edited' }, warnings: [] })
    await flushPromises()
    // The late answer for work-ro did not replace the page that is showing "other".
    expect((wrapper.get('[data-test="profile-title"]').element as HTMLInputElement).value).toBe('Other profile')
    expect(wrapper.get('h1').text()).toBe('Other profile')
  })

  it('shows Profile not found for a 404', async () => {
    ;(api.getProfile as any).mockRejectedValue(apiError('profile not found', { status: 404 }))
    const { wrapper } = await mountEditor()
    expect(wrapper.get('[data-test="profile-editor-not-found"]').text()).toContain('Profile not found')
  })
})
