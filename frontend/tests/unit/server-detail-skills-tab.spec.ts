// Spec 125 PR-4a: the Skills tab of ServerDetail is offered when skills are on
// and the server supports them (or has records), never to a tenant principal,
// and follows ?tab=skills like every other tab.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import api from '@/services/api'
import { connectSkillsSSE, makeListing, makeSkill } from './fixtures/skills125'

const ok = <T,>(data: T) => Promise.resolve({ success: true, data })

vi.mock('@/services/api', () => {
  const server = (name: string) => ({
    name, protocol: 'stdio', enabled: true, connected: true, quarantined: false, tool_count: 0, trust_mode: 'manual',
    security_scan: { status: 'clean', risk_score: 0, last_scan_at: '2026-07-28T10:00:00Z', scanners_run: 1, scanners_failed: 0, scanners_total: 1 },
  })
  return { default: {
    hasAPIKey: vi.fn(() => true),
    getServers: vi.fn(() => ok({ servers: [server('alpha'), server('beta')] })),
    getToolApprovals: vi.fn(() => ok({ tools: [], count: 0 })),
    getServerTools: vi.fn(() => ok({ tools: [] })),
    getSecurityOverview: vi.fn(() => ok({ scanners_enabled: 0, docker_available: true })),
    getScanReport: vi.fn(() => ok({ job_id: 'scan-alpha-1', risk_score: 0, findings: [] })),
    getScanStatus: vi.fn(() => ok({ id: 'scan-alpha-1', status: 'completed', scan_pass: 1 })),
    getServerLogs: vi.fn(() => ok({ logs: [] })),
    discoverServerTools: vi.fn(() => ok({})),
    getConfig: vi.fn(() => ok({ config: { server_edition: {} } })),
    getStatus: vi.fn(() => ok({})),
    getServerSkills: vi.fn(),
    getSkillContent: vi.fn(),
    refreshServerSkills: vi.fn(), approveSkills: vi.fn(), blockSkills: vi.fn(), unblockSkills: vi.fn(),
  } }
})

let emitSkillsChanged: (server: string | string[]) => void = () => { throw new Error('not connected') }

async function mountServerDetail(path: string, tenant = false) {
  const pinia = createPinia()
  setActivePinia(pinia)
  if (tenant) {
    ;(api.hasAPIKey as any).mockReturnValue(false)
    const auth = useAuthStore()
    auth.isTeamsEdition = true
    auth.user = { role: 'user', email: 'u@example.com' } as any
  } else {
    ;(api.hasAPIKey as any).mockReturnValue(true)
  }
  const ServerDetail = (await import('@/views/ServerDetail.vue')).default
  const router = createRouter({ history: createWebHistory(), routes: [{ path: '/servers/:serverName', component: { template: '<div/>' } }] })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(ServerDetail, { props: { serverName: 'alpha' }, global: { plugins: [pinia, router] } })
  await flushPromises(); await flushPromises()
  // Round-3 finding 1: skills.changed reaches the view through the real store's re-dispatch of the core's frame.
  emitSkillsChanged = await connectSkillsSSE(api)
  return { wrapper, router }
}

beforeEach(() => {
  vi.clearAllMocks()
  ;(api.getServerSkills as any).mockResolvedValue({ success: true, data: makeListing([makeSkill('hf')]) })
})

describe('ServerDetail Skills tab (Spec 125)', () => {
  it('shows the tab when skills are enabled and supported', async () => {
    const { wrapper } = await mountServerDetail('/servers/alpha')
    expect(wrapper.find('[data-test="skills-tab"]').exists()).toBe(true)
    expect(api.getServerSkills).toHaveBeenCalledWith('alpha')
  })

  it('hides the tab when enable_skills is off', async () => {
    ;(api.getServerSkills as any).mockResolvedValue({ success: true, data: makeListing([], { enabled: false }) })
    const { wrapper } = await mountServerDetail('/servers/alpha')
    expect(wrapper.find('[data-test="skills-tab"]').exists()).toBe(false)
  })

  it('hides the tab when the server neither supports skills nor has records', async () => {
    ;(api.getServerSkills as any).mockResolvedValue({ success: true, data: makeListing([], { supported: false }) })
    const { wrapper } = await mountServerDetail('/servers/alpha')
    expect(wrapper.find('[data-test="skills-tab"]').exists()).toBe(false)
  })

  it('hides the tab when the listing request fails', async () => {
    ;(api.getServerSkills as any).mockResolvedValue({ success: false, error: 'nope' })
    const { wrapper } = await mountServerDetail('/servers/alpha')
    expect(wrapper.find('[data-test="skills-tab"]').exists()).toBe(false)
  })

  it('does not even request the listing for a tenant principal', async () => {
    const { wrapper } = await mountServerDetail('/servers/alpha', true)
    expect(api.getServerSkills).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="skills-tab"]').exists()).toBe(false)
  })

  it('opens on ?tab=skills and writes ?tab=skills when the tab is clicked', async () => {
    const first = await mountServerDetail('/servers/alpha?tab=skills')
    expect(first.wrapper.find('[data-test="skills-tab"]').classes()).toContain('tab-active')
    expect(first.wrapper.find('[data-test="skills-panel"]').exists()).toBe(true)
    const { wrapper, router } = await mountServerDetail('/servers/alpha?tab=tools&focus=x')
    await wrapper.get('[data-test="skills-tab"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.tab).toBe('skills')
    expect(router.currentRoute.value.query.focus).toBe('x')
    expect(wrapper.find('[data-test="skills-panel"]').exists()).toBe(true)
  })

  it('reads Needs review from the URL and updates only its filter parameter', async () => {
    ;(api.getServerSkills as any).mockResolvedValue({ success: true, data: makeListing([makeSkill('new'), makeSkill('approved', { status: 'approved' })]) })
    const { wrapper, router } = await mountServerDetail('/servers/alpha?tab=skills&state=needs-review&focus=x')
    const all = wrapper.get('[data-test="skills-filter-all"]')
    const needsReview = wrapper.get('[data-test="skills-filter-needs-review"]')
    expect(needsReview.attributes('aria-pressed')).toBe('true')
    expect(wrapper.find('[data-test="skill-row-approved"]').exists()).toBe(false)
    await all.trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ tab: 'skills', focus: 'x' })
    expect(all.attributes('aria-pressed')).toBe('true')
    expect(wrapper.find('[data-test="skill-row-approved"]').exists()).toBe(true)
    await needsReview.trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ tab: 'skills', state: 'needs-review', focus: 'x' })
    await wrapper.findAll('button.tab').find(tab => tab.text().startsWith('Tools ('))!.trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ tab: 'tools', focus: 'x' })
    await wrapper.get('[data-test="skills-tab"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="skills-filter-all"]').attributes('aria-pressed')).toBe('true')
    wrapper.unmount()
  })

  it('treats an unknown Skills filter as All skills', async () => {
    const { wrapper } = await mountServerDetail('/servers/alpha?tab=skills&state=unknown')
    expect(wrapper.get('[data-test="skills-filter-all"]').attributes('aria-pressed')).toBe('true')
    expect(wrapper.find('[data-test="skill-row-hf"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('re-checks the tab for the next server on an in-place serverName change', async () => {
    const { wrapper } = await mountServerDetail('/servers/alpha')
    expect(wrapper.find('[data-test="skills-tab"]').exists()).toBe(true)
    ;(api.getServerSkills as any).mockResolvedValue({ success: true, data: makeListing([], { enabled: false }) })
    await wrapper.setProps({ serverName: 'beta' })
    await flushPromises(); await flushPromises()
    expect(api.getServerSkills).toHaveBeenLastCalledWith('beta')
    expect(wrapper.find('[data-test="skills-tab"]').exists()).toBe(false)
  })

  // Review finding 5: ?tab=skills never leaves an empty area.
  it('?tab=skills on a server without skills falls back to Tools and writes ?tab=tools', async () => {
    ;(api.getServerSkills as any).mockResolvedValue({ success: true, data: makeListing([], { supported: false }) })
    const { wrapper, router } = await mountServerDetail('/servers/alpha?tab=skills&focus=x')
    expect(wrapper.find('[data-test="skills-tab"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="skills-panel"]').exists()).toBe(false)
    expect(router.currentRoute.value.query.tab).toBe('tools')
    expect(router.currentRoute.value.query.focus).toBe('x')
  })

  it('?tab=skills with the feature off falls back to Tools', async () => {
    ;(api.getServerSkills as any).mockResolvedValue({ success: true, data: makeListing([], { enabled: false }) })
    const { wrapper, router } = await mountServerDetail('/servers/alpha?tab=skills')
    expect(wrapper.find('[data-test="skills-panel"]').exists()).toBe(false)
    expect(router.currentRoute.value.query.tab).toBe('tools')
  })

  it('?tab=skills when the listing request fails falls back to Tools', async () => {
    ;(api.getServerSkills as any).mockResolvedValue({ success: false, error: 'nope' })
    const { router } = await mountServerDetail('/servers/alpha?tab=skills')
    expect(router.currentRoute.value.query.tab).toBe('tools')
  })

  it('?tab=skills for a tenant principal falls back to Tools without a request', async () => {
    const { wrapper, router } = await mountServerDetail('/servers/alpha?tab=skills', true)
    expect(api.getServerSkills).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="skills-panel"]').exists()).toBe(false)
    expect(router.currentRoute.value.query.tab).toBe('tools')
  })

  it('does not render the panel before the visibility check settles', async () => {
    let release: (v: unknown) => void = () => {}
    ;(api.getServerSkills as any).mockReturnValueOnce(new Promise(r => { release = r }))
    const { wrapper, router } = await mountServerDetail('/servers/alpha?tab=skills')
    expect(wrapper.find('[data-test="skills-panel"]').exists()).toBe(false)
    expect(router.currentRoute.value.query.tab).toBe('skills')
    release({ success: true, data: makeListing([makeSkill('hf')]) }); await flushPromises(); await flushPromises()
    expect(wrapper.find('[data-test="skills-tab"]').classes()).toContain('tab-active')
    expect(wrapper.find('[data-test="skills-panel"]').exists()).toBe(true)
  })

  it('rechecks an open Skills tab only when a bulk change includes its server', async () => {
    const { wrapper, router } = await mountServerDetail('/servers/alpha?tab=skills')
    vi.mocked(api.getServerSkills).mockClear()
    emitSkillsChanged(['beta', 'gamma'])
    await flushPromises()
    expect(api.getServerSkills).not.toHaveBeenCalled()
    vi.mocked(api.getServerSkills).mockResolvedValue({ success: true, data: makeListing([], { enabled: false }) })
    emitSkillsChanged(['beta', 'alpha'])
    await flushPromises()
    expect(router.currentRoute.value.query.tab).toBe('tools')
    wrapper.unmount()
  })

  it('falls back to Tools when a skills change turns the open tab off', async () => {
    const { wrapper, router } = await mountServerDetail('/servers/alpha?tab=skills')
    expect(wrapper.find('[data-test="skills-panel"]').exists()).toBe(true)
    ;(api.getServerSkills as any).mockResolvedValue({ success: true, data: makeListing([], { enabled: false }) })
    emitSkillsChanged('alpha')
    await flushPromises(); await flushPromises()
    expect(wrapper.find('[data-test="skills-tab"]').exists()).toBe(false)
    expect(router.currentRoute.value.query.tab).toBe('tools')
    wrapper.unmount()
  })

  // Round-2 finding 3: a failed background re-check keeps the open review.
  it('a failed re-check after a skills change (409 or 500) keeps the tab and the open panel', async () => {
    const { wrapper, router } = await mountServerDetail('/servers/alpha?tab=skills')
    expect(wrapper.find('[data-test="skills-panel"]').exists()).toBe(true)
    for (const failure of [
      { success: false, error: 'server_replaced: x', errorCode: 'server_replaced' },
      { success: false, error: 'internal error' },
    ]) {
      ;(api.getServerSkills as any).mockResolvedValue(failure)
      emitSkillsChanged('alpha')
      await flushPromises(); await flushPromises()
      expect(wrapper.find('[data-test="skills-tab"]').exists()).toBe(true)
      expect(wrapper.find('[data-test="skills-panel"]').exists()).toBe(true)
      expect(router.currentRoute.value.query.tab).toBe('skills')
    }
    // A thrown request is a failure too.
    ;(api.getServerSkills as any).mockRejectedValue(new Error('network'))
    emitSkillsChanged('alpha')
    await flushPromises(); await flushPromises()
    expect(wrapper.find('[data-test="skills-panel"]').exists()).toBe(true)
    wrapper.unmount()
  })

  // Round-2 finding 11: an agent-token listing (no `supported`) offers no tab.
  it('hides the tab for an agent-shaped listing without `supported`', async () => {
    ;(api.getServerSkills as any).mockResolvedValue({ success: true, data: { enabled: true, server: 'alpha', skills: [{ path: 'hf', status: 'approved', files: [] }] } })
    const { wrapper, router } = await mountServerDetail('/servers/alpha?tab=skills')
    expect(wrapper.find('[data-test="skills-tab"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="skills-panel"]').exists()).toBe(false)
    expect(router.currentRoute.value.query.tab).toBe('tools')
  })

  it('an in-place change to a server that offers skills honours its ?tab=skills (not judged by the previous server)', async () => {
    ;(api.getServerSkills as any).mockResolvedValue({ success: true, data: makeListing([], { enabled: false }) })
    const { wrapper, router } = await mountServerDetail('/servers/alpha')
    ;(api.getServerSkills as any).mockResolvedValue({ success: true, data: makeListing([makeSkill('hf')]) })
    await router.push('/servers/beta?tab=skills')
    await wrapper.setProps({ serverName: 'beta' })
    await flushPromises(); await flushPromises()
    expect(wrapper.find('[data-test="skills-tab"]').classes()).toContain('tab-active')
    expect(router.currentRoute.value.query.tab).toBe('skills')
  })
  // Round-4 finding 2: a late visibility answer never writes ?tab= into a URL
  // that is no longer this view's: not after unmount, not after a server switch.
  it('a check that answers after unmount writes nothing into the next URL', async () => {
    let release: (v: unknown) => void = () => {}
    ;(api.getServerSkills as any).mockReturnValueOnce(new Promise(r => { release = r }))
    const { wrapper, router } = await mountServerDetail('/servers/alpha')
    wrapper.unmount()
    // The next view (here a fresh page of the same server) asks for the Skills tab.
    await router.push('/servers/alpha?tab=skills&focus=x')
    const replace = vi.spyOn(router, 'replace')
    release({ success: true, data: makeListing([], { supported: false }) })
    await flushPromises(); await flushPromises()
    expect(replace).not.toHaveBeenCalled()
    expect(router.currentRoute.value.fullPath).toBe('/servers/alpha?tab=skills&focus=x')
  })

  it('a check that answers after a server switch writes nothing into the new server URL', async () => {
    const pending: Array<(v: unknown) => void> = []
    ;(api.getServerSkills as any).mockImplementation(() => new Promise(r => { pending.push(r) }))
    const { wrapper, router } = await mountServerDetail('/servers/alpha')
    await router.push('/servers/beta?tab=skills')
    await wrapper.setProps({ serverName: 'beta' })
    await flushPromises()
    const replace = vi.spyOn(router, 'replace')
    // alpha's late answer says "no skills"; beta's check is still in flight.
    pending[0]({ success: true, data: makeListing([], { supported: false }) })
    await flushPromises(); await flushPromises()
    expect(replace).not.toHaveBeenCalled()
    expect(router.currentRoute.value.fullPath).toBe('/servers/beta?tab=skills')
    // The same holds once alpha's view is gone and beta's answer arrives late.
    wrapper.unmount()
    await router.push('/servers/beta?tab=skills&next=1')
    for (const r of pending.slice(1)) r({ success: true, data: makeListing([], { supported: false }) })
    await flushPromises(); await flushPromises()
    expect(replace).not.toHaveBeenCalled()
    expect(router.currentRoute.value.fullPath).toBe('/servers/beta?tab=skills&next=1')
  })

  // Round-3 finding 3: overlapping background re-checks are ordered by request:
  // only an answer newer than the last applied one changes the tab.
  for (const order of ['older answer first', 'newer answer first'] as const) {
    it(`two overlapping re-checks keep the newer answer (${order})`, async () => {
      const { wrapper, router } = await mountServerDetail('/servers/alpha?tab=skills')
      expect(wrapper.find('[data-test="skills-panel"]').exists()).toBe(true)
      const pending: Array<(v: unknown) => void> = []
      ;(api.getServerSkills as any).mockImplementation(() => new Promise(r => { pending.push(r) }))
      emitSkillsChanged('alpha') // older: will say "unsupported"
      emitSkillsChanged('alpha') // newer: will say "supported"
      await flushPromises()
      // Each event reaches ServerDetail's listener first, then SkillsPanel's reload:
      // requests are [check 1, reload 1, check 2, reload 2].
      expect(api.getServerSkills).toHaveBeenCalledTimes(1 + 1 + 4)
      const [older, newer] = [pending[0], pending[2]]
      const unsupported = { success: true, data: makeListing([], { supported: false }) }
      const supported = { success: true, data: makeListing([makeSkill('hf')]) }
      if (order === 'older answer first') { older(unsupported); await flushPromises(); newer(supported) }
      else { newer(supported); await flushPromises(); older(unsupported) }
      pending[1](supported); pending[3](supported)
      await flushPromises(); await flushPromises()
      // The newer answer ("supported") is the one that stands either way.
      expect(wrapper.find('[data-test="skills-tab"]').exists()).toBe(true)
      if (order === 'newer answer first') {
        // The older answer came after it and was dropped: the open review was never unmounted.
        expect(wrapper.find('[data-test="skills-panel"]').exists()).toBe(true)
        expect(router.currentRoute.value.query.tab).toBe('skills')
      }
      wrapper.unmount()
    })
  }
})
