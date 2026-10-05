import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import ProfileEditor from '@/views/ProfileEditor.vue'
import ProfileTryPanel from '@/components/profiles/ProfileTryPanel.vue'
import api from '@/services/api'
import { tryHitRow } from '@/utils/profiles'
import { makeClient, makeProfile } from './fixtures/profiles108i'

// Spec 108 FR-041 / US4-2 (fix-usertest-web T169, audit F-05). The editor's
// counts and Try it told the truth about the SAVED profile only:
//  - Try it printed "[object Object]" for every hit (POST /profiles/try returns
//    {score, tool: {name, server_name, description}}, the panel read a flat item);
//  - the visible/hidden counts did not follow unsaved edits and said nothing.

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

const REAL_RESULT = {
  results: [
    { score: 1.2, tool: { name: 'memory:open_nodes', server_name: 'memory', description: 'Open nodes' } },
    { score: 0.9, tool: { name: 'memory:search_nodes', server_name: 'memory', description: 'Search nodes' } },
  ],
  hidden_by_profile: 1,
  hidden: [{ server: 'memory', tool: 'read_graph', reason: 'denied_by_rule' }],
}

describe('tryHitRow', () => {
  it('reads the nested retrieve_tools hit', () => {
    expect(tryHitRow({ score: 1, tool: { name: 'memory:read_graph', server_name: 'memory', description: 'Read the graph' } })).toEqual({
      key: 'memory:read_graph', server: 'memory', tool: 'read_graph', description: 'Read the graph',
    })
  })
  it('does not double the server when the nested name is bare', () => {
    expect(tryHitRow({ tool: { name: 'read_graph', server_name: 'memory' } }).key).toBe('memory:read_graph')
  })
  it('still reads a flat item', () => {
    expect(tryHitRow({ server: 'github', name: 'search_code', description: 'Search' })).toEqual({
      key: 'github:search_code', server: 'github', tool: 'search_code', description: 'Search',
    })
  })
  it('gives garbage an empty key and never an object string', () => {
    expect(tryHitRow({ tool: {} }).key).toBe('')
    const inputs: Array<Record<string, unknown>> = [
      {}, { tool: {} }, { tool: { name: { a: 1 } } }, { name: ['x'], server: {} }, { tool: null }, { tool: 5, description: {} },
    ]
    for (const input of inputs) {
      const row = tryHitRow(input)
      expect(JSON.stringify(row)).not.toContain('[object')
    }
  })
})

describe('ProfileTryPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  async function run(dirty: boolean, result: unknown = REAL_RESULT) {
    ;(api.tryProfile as any).mockResolvedValue(result)
    const wrapper = mount(ProfileTryPanel, { props: { draft: () => ({ servers: ['memory'] }) as any, dirty } })
    await wrapper.get('[data-test="profile-try-query"]').setValue('read the knowledge graph')
    await wrapper.get('[data-test="profile-try-run"]').trigger('submit')
    await flushPromises()
    return wrapper
  }

  it('renders the real hit shape as readable rows and the hidden reason in words', async () => {
    const wrapper = await run(true)
    const items = wrapper.get('[data-test="profile-try-list"]').findAll('li')
    expect(items).toHaveLength(2)
    expect(items.map(li => li.get('code').text())).toEqual(['memory:open_nodes', 'memory:search_nodes'])
    expect(items[0].text()).toContain('Open nodes')
    expect(items[1].text()).toContain('Search nodes')
    expect(wrapper.text()).not.toContain('[object Object]')
    expect(wrapper.get('[data-test="profile-try-hidden"]').text()).toContain('memory:read_graph — Denied by rule')
  })

  it('labels a garbage hit instead of printing an object', async () => {
    const wrapper = await run(false, { results: [{ tool: {} }], hidden_by_profile: 0, hidden: [] })
    expect(wrapper.get('[data-test="profile-try-list"]').text()).toContain('(unnamed tool)')
    expect(wrapper.text()).not.toContain('[object')
  })

  it('says whether it uses unsaved edits', async () => {
    const dirty = mount(ProfileTryPanel, { props: { draft: () => ({}) as any, dirty: true } })
    expect(dirty.get('[data-test="profile-try-source"]').text()).toBe('Uses your unsaved edits')
    const clean = mount(ProfileTryPanel, { props: { draft: () => ({}) as any, dirty: false } })
    expect(clean.get('[data-test="profile-try-source"]').text()).toBe('Uses the saved profile')
  })
})

describe('ProfileEditor counts under unsaved edits', () => {
  const SAVED = makeProfile('work-readonly', {
    title: 'Work Readonly',
    servers: ['memory'],
    effective_servers: ['memory'],
    max_tier: 'read',
    unannotated: 'deny',
    code_execution: false,
    management_tools: false,
    switchable_to: [],
    used_by: { clients: [], tokens: [], anonymous_profile: false },
  })
  const ROWS = [
    { server: 'memory', tool: 'read_graph', intrinsic_tier: 'read', profile_tier: 'read', access: { visible: true, callable: true, reason: '' }, classification_stale: false },
  ]

  async function mountEditor() {
    const stub = { template: '<div />' }
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/profiles', name: 'profiles', component: stub },
        { path: '/profiles/:name', name: 'profile-editor', component: ProfileEditor, props: true },
        { path: '/tokens', name: 'tokens', component: stub },
      ],
    })
    await router.push('/profiles/work-readonly')
    await router.isReady()
    const wrapper = mount(ProfileEditor, { props: { name: 'work-readonly' }, global: { plugins: [router] }, attachTo: document.body })
    await flushPromises()
    return wrapper
  }

  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.hasAPIKey as any).mockReturnValue(true)
    ;(api.getProfile as any).mockResolvedValue(SAVED)
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [SAVED] })
    ;(api.getProfileEffectiveTools as any).mockResolvedValue({ profile: 'work-readonly', tools: ROWS, counts: { visible: 3, hidden: 6 }, stale_classifications: [] })
    ;(api.getServers as any).mockResolvedValue({ success: true, data: { servers: [{ name: 'memory' }, { name: 'filesystem' }] } })
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [makeClient('cursor')], warnings: [] } })
    ;(api.tryProfile as any).mockResolvedValue(REAL_RESULT)
  })

  const counts = (w: Awaited<ReturnType<typeof mountEditor>>) => w.get('[data-test="profile-tool-counts"]').text().replace(/\s+/g, ' ')

  it('labels the counts as the saved profile while the draft differs, and clears on Discard', async () => {
    const wrapper = await mountEditor()
    expect(counts(wrapper)).toBe('3 visible · 6 hidden')
    expect(wrapper.find('[data-test="profile-tool-unsaved-note"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="profile-try-source"]').text()).toBe('Uses the saved profile')

    await wrapper.get('[data-test="profile-server-memory"]').setValue(false)
    await flushPromises()
    expect(counts(wrapper)).toBe('Saved profile: 3 visible · 6 hidden')
    const note = wrapper.get('[data-test="profile-tool-unsaved-note"]')
    expect(note.text()).toContain('Counts and access show the saved profile. Save to update them, or use Try it below to test your unsaved edits.')
    expect(wrapper.get('[data-test="profile-try-source"]').text()).toBe('Uses your unsaved edits')

    await wrapper.get('[data-test="profile-editor-discard"]').trigger('click')
    await flushPromises()
    expect(counts(wrapper)).toBe('3 visible · 6 hidden')
    expect(wrapper.find('[data-test="profile-tool-unsaved-note"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="profile-try-source"]').text()).toBe('Uses the saved profile')
    expect(api.updateProfile).not.toHaveBeenCalled()
  })
})
