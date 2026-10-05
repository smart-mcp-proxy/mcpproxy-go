import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setAvailableFeatures } from '@/composables/useScopeQuery'
import { makeClient, makeProfile } from './fixtures/profiles108i'

// Spec 108-j T105/T108 (FR-029, FR-046; link map "Activity row chips", "Blocked
// Activity row"): the Scope column and the drawer's Attribution section, the
// "Allow in profile…" and "Why?" actions on a blocked row.

const getActivitiesMock = vi.hoisted(() => vi.fn())
const getUserActivityMock = vi.hoisted(() => vi.fn())
const explainAccessMock = vi.hoisted(() => vi.fn())

vi.mock('@/services/api', () => ({
  default: {
    getActivities: getActivitiesMock,
    getUserActivity: getUserActivityMock,
    getActivitySummary: vi.fn(() => Promise.resolve({ success: true, data: { period: '24h', total_count: 0, success_count: 0, error_count: 0, blocked_count: 0, rejected_count: 0 } })),
    getSessions: vi.fn(() => Promise.resolve({ success: true, data: { sessions: [] } })),
    getActivityExportUrl: vi.fn(() => 'http://localhost/export'),
    getStatus: vi.fn(() => Promise.resolve({ success: true, data: {} })),
    explainAccess: explainAccessMock,
    getGlobalTools: vi.fn(() => Promise.resolve({ success: true, data: { tools: [] } })),
    getProfiles: vi.fn(() => Promise.resolve({ profiles: [] })),
    getClients: vi.fn(() => Promise.resolve({ success: true, data: { clients: [] } })),
    listAgentTokens: vi.fn(() => Promise.resolve({ success: true, data: { tokens: [] } })),
  },
}))

// The T003 contract fixture: the attribution fields exactly as the backend writes them.
const ATTRIBUTED = JSON.parse(readFileSync(resolve(__dirname, '../../../internal/profile/testdata/contract/activity_attributed.json'), 'utf8'))

const stub = { template: '<div />' }

function record(id: string, extra: Record<string, unknown> = {}) {
  return { id, type: 'tool_call', server_name: 'github', tool_name: 'create_issue', status: 'success', timestamp: '2026-09-30T10:00:00Z', ...extra }
}

async function mountActivity(records: unknown[], options: { tenant?: boolean; profiles?: boolean } = {}) {
  getActivitiesMock.mockImplementation(() => Promise.resolve({ success: true, data: { activities: records, total: records.length, limit: 200, offset: 0 } }))
  getUserActivityMock.mockImplementation(() => Promise.resolve({ success: true, data: { items: records, total: records.length } }))
  const Activity = (await import('@/views/Activity.vue')).default
  const { useProfilesStore } = await import('@/stores/profiles')
  const { useClientsStore } = await import('@/stores/clients')
  const { useAuthStore } = await import('@/stores/auth')
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/activity', name: 'activity', component: Activity },
      { path: '/clients', name: 'clients', component: stub },
      { path: '/tokens', name: 'tokens', component: stub },
      { path: '/profiles/:name', name: 'profile-editor', component: stub },
      { path: '/servers/:serverName', component: stub },
    ],
  })
  await router.push('/activity?view=all')
  await router.isReady()
  if (options.tenant) {
    const auth = useAuthStore()
    auth.isTeamsEdition = true
    auth.user = { id: 'c', email: 'c@x', display_name: 'C', role: 'user', provider: 'oidc', created_at: '', last_login_at: '' } as any
  }
  const profiles = useProfilesStore()
  profiles.profiles = options.profiles === false ? [] : [makeProfile('work-readonly', { title: 'Work', max_tier: 'read' })] as any
  profiles.loaded = true
  useClientsStore().clients = [makeClient('cursor')] as any
  const wrapper = mount(Activity, { global: { plugins: [router] } })
  await flushPromises()
  await flushPromises()
  return { wrapper, router }
}

const href = (router: ReturnType<typeof createRouter>, to: unknown) => router.resolve(to as any).fullPath

describe('Activity attribution chips (Spec 108-j)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    setAvailableFeatures(['profile', 'client', 'token'])
  })

  it('the Scope column shows the client (store name), the profile with how it was resolved, and omits the client\'s own token', async () => {
    const { wrapper } = await mountActivity([record('a1', ATTRIBUTED)])
    expect(wrapper.find('[data-test="activity-scope-col"]').exists()).toBe(true)
    const chips = wrapper.get('[data-test="activity-attribution-a1"]')
    expect(chips.get('[data-test="attribution-client"]').text()).toBe('Cursor')
    expect(chips.get('[data-test="attribution-profile"]').text()).toBe('Work · locked by credential')
    // token_name is client-cursor: the client chip already says that.
    expect(chips.find('[data-test="attribution-token"]').exists()).toBe(false)
  })

  it('an agent token is its own chip', async () => {
    const { wrapper } = await mountActivity([record('a2', { profile: 'work-readonly', profile_source: 'pin', token_name: 'ro-bot' })])
    expect(wrapper.get('[data-test="activity-attribution-a2"] [data-test="attribution-token"]').text()).toBe('ro-bot')
  })

  it('a legacy record with none of the fields reads "unattributed"', async () => {
    const { wrapper } = await mountActivity([record('legacy'), record('a1', ATTRIBUTED)])
    expect(wrapper.get('[data-test="activity-attribution-legacy"]').text()).toBe('unattributed')
  })

  it('a system event is a dash, not "unattributed"', async () => {
    const { wrapper } = await mountActivity([record('sys', { type: 'config_change' }), record('a1', ATTRIBUTED)])
    expect(wrapper.get('[data-test="activity-attribution-sys"]').text()).toBe('—')
  })

  it('a self-reported client_name alone is a muted "~name (reported)", not a link', async () => {
    const { wrapper } = await mountActivity([record('rep', { client_name: 'Cursor' })])
    const chip = wrapper.get('[data-test="activity-attribution-rep"] [data-test="attribution-client-reported"]')
    expect(chip.text()).toBe('~Cursor (reported)')
    expect(chip.element.tagName).not.toBe('A')
  })

  it('the chip links resolve through the link map and the named profile route', async () => {
    const { wrapper, router } = await mountActivity([record('a3', { ...ATTRIBUTED, token_name: 'ro-bot' })])
    const chips = wrapper.get('[data-test="activity-attribution-a3"]')
    const paths = ['client', 'profile', 'token'].map(kind => chips.get(`[data-test="attribution-${kind}"]`).attributes('href'))
    expect(paths[0]).toBe(href(router, { name: 'clients', query: { client: 'cursor' } }))
    expect(paths[1]).toBe('/profiles/work-readonly')
    expect(paths[2]).toBe(href(router, { name: 'tokens', query: { token: 'ro-bot' } }))
  })

  describe('a blocked row', () => {
    const blocked = (extra: Record<string, unknown> = {}) => record('b1', { ...ATTRIBUTED, status: 'blocked', ...extra })
    const openDrawer = async (wrapper: ReturnType<typeof mount>, id = 'b1') => {
      await wrapper.get(`[data-test="activity-open-${id}"]`).trigger('click')
      await flushPromises()
    }

    it('"Allow in profile…" goes to the editor focused on the tool; "Why?" opens the explainer for the client', async () => {
      explainAccessMock.mockResolvedValue({ subject: { kind: 'client', name: 'cursor' }, tool: 'github:create_issue', profile: { name: 'work-readonly', source: 'pin' }, steps: [], verdict: 'hidden', first_failure: 'tier_cap', fixes: [] })
      const { wrapper, router } = await mountActivity([blocked()])
      await openDrawer(wrapper)
      const allow = wrapper.get('[data-test="activity-allow-in-profile-b1"]')
      expect(allow.text()).toBe('Allow in profile…')
      expect(allow.attributes('href')).toBe('/profiles/work-readonly?focus=github:create_issue')
      expect(router.resolve({ name: 'profile-editor', params: { name: 'work-readonly' }, query: { focus: 'github:create_issue' } }).fullPath).toBe(allow.attributes('href'))

      await wrapper.get('[data-test="activity-why-b1"]').trigger('click')
      await flushPromises()
      expect(explainAccessMock).toHaveBeenCalledWith({ tool: 'github:create_issue', client: 'cursor' })
      const dialog = wrapper.get('[data-test="access-explainer"]')
      expect(dialog.text()).toContain('Why is this blocked?')
      expect(dialog.get('[data-test="explain-note"]').text()).toContain('Evaluated against the current configuration')
    })

    it('the drawer panel fits a 390px viewport (full width, capped at 500px)', async () => {
      const { wrapper } = await mountActivity([blocked()])
      await openDrawer(wrapper)
      const classes = wrapper.get('[data-test="activity-detail-panel"]').classes()
      expect(classes).toContain('w-full')
      expect(classes).toContain('max-w-[500px]')
      expect(classes.some(c => /^w-\[\d+px\]$/.test(c))).toBe(false)
    })

    it('Escape while the explainer is open leaves the drawer (and its Why? button) in place', async () => {
      explainAccessMock.mockResolvedValue({ subject: { kind: 'client', name: 'cursor' }, tool: 'github:create_issue', profile: { name: 'work-readonly', source: 'pin' }, steps: [], verdict: 'hidden', first_failure: 'tier_cap', fixes: [] })
      const { wrapper } = await mountActivity([blocked()])
      await openDrawer(wrapper)
      await wrapper.get('[data-test="activity-why-b1"]').trigger('click')
      await flushPromises()
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
      await flushPromises()
      expect(wrapper.find('[data-test="activity-why-b1"]').exists()).toBe(true)
      // With the explainer closed, Escape closes the drawer as before.
      wrapper.getComponent({ name: 'AccessExplainer' }).vm.$emit('close')
      await flushPromises()
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
      await flushPromises()
      expect(wrapper.find('[data-test="activity-why-b1"]').exists()).toBe(false)
    })

    it('the row itself offers "Why?" for a blocked call', async () => {
      explainAccessMock.mockResolvedValue({ subject: { kind: 'client', name: 'cursor' }, tool: 'github:create_issue', profile: { name: 'work-readonly', source: 'pin' }, steps: [], verdict: 'hidden', first_failure: 'tier_cap', fixes: [] })
      const { wrapper } = await mountActivity([blocked()])
      await wrapper.get('[data-test="activity-row-why-b1"]').trigger('click')
      await flushPromises()
      expect(explainAccessMock).toHaveBeenCalledWith({ tool: 'github:create_issue', client: 'cursor' })
    })

    it('profile_code_execution and profile_management read "Open profile…" with no focus', async () => {
      for (const reason of ['profile_code_execution', 'profile_management']) {
        const { wrapper } = await mountActivity([blocked({ block_reason: reason })])
        await openDrawer(wrapper)
        const open = wrapper.get('[data-test="activity-allow-in-profile-b1"]')
        expect(open.text()).toBe('Open profile…')
        expect(open.attributes('href')).toBe('/profiles/work-readonly')
        wrapper.unmount()
      }
    })

    it('a deleted profile replaces the action with muted text', async () => {
      const { wrapper } = await mountActivity([blocked()], { profiles: false })
      await openDrawer(wrapper)
      expect(wrapper.find('[data-test="activity-allow-in-profile-b1"]').exists()).toBe(false)
      expect(wrapper.get('[data-test="activity-profile-missing"]').text()).toBe('Profile work-readonly no longer exists')
    })

    it('a tenant gets neither the action nor "Why?"', async () => {
      const { wrapper } = await mountActivity([blocked()], { tenant: true })
      expect(getUserActivityMock).toHaveBeenCalled()
      await openDrawer(wrapper)
      expect(wrapper.find('[data-test="activity-allow-in-profile-b1"]').exists()).toBe(false)
      expect(wrapper.find('[data-test="activity-row-why-b1"]').exists()).toBe(false)
      expect(wrapper.find('[data-test="activity-why-b1"]').exists()).toBe(false)
    })

    it('the explainer subject: client, else token, else anonymous, else the recorded profile, else no "Why?"', async () => {
      const cases: Array<[string, Record<string, unknown>, Record<string, unknown> | null]> = [
        ['client', { client_id: 'cursor' }, { client: 'cursor' }],
        ['token', { client_id: '', token_name: 'ro-bot' }, { token: 'ro-bot' }],
        ['anonymous', { client_id: '', token_name: '', profile_source: 'anonymous' }, { anonymous: true }],
        ['profile', { client_id: '', token_name: '', profile_source: 'url' }, { profile: 'work-readonly' }],
        ['none', { client_id: '', token_name: '', profile: '', profile_source: '' }, null],
      ]
      for (const [name, extra, expected] of cases) {
        explainAccessMock.mockClear()
        explainAccessMock.mockResolvedValue({ subject: { kind: 'client', name: 'x' }, tool: 'github:create_issue', profile: { name: 'p', source: 'pin' }, steps: [], verdict: 'hidden', first_failure: '', fixes: [] })
        const { wrapper } = await mountActivity([blocked(extra)])
        const why = wrapper.find('[data-test="activity-row-why-b1"]')
        if (expected === null) {
          expect(why.exists(), name).toBe(false)
        } else {
          await why.trigger('click')
          await flushPromises()
          expect(explainAccessMock, name).toHaveBeenCalledWith({ tool: 'github:create_issue', ...expected })
        }
        wrapper.unmount()
      }
    })

    it('a successful call has neither action', async () => {
      const { wrapper } = await mountActivity([record('ok1', ATTRIBUTED)])
      await openDrawer(wrapper, 'ok1')
      expect(wrapper.find('[data-test="activity-allow-in-profile-ok1"]').exists()).toBe(false)
      expect(wrapper.find('[data-test="activity-why-ok1"]').exists()).toBe(false)
    })
  })

  it('the drawer carries an Attribution section with the same chips', async () => {
    const { wrapper } = await mountActivity([record('a1', ATTRIBUTED)])
    await wrapper.get('[data-test="activity-open-a1"]').trigger('click')
    await flushPromises()
    const section = wrapper.get('[data-test="activity-drawer-attribution"]')
    expect(section.text()).toContain('Attribution')
    expect(section.get('[data-test="attribution-client"]').text()).toBe('Cursor')
  })
})
