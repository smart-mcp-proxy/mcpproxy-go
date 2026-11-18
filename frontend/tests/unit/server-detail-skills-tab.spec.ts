// Spec 125 PR-4a: the Skills tab of ServerDetail is offered when skills are on
// and the server supports them (or has records), never to a tenant principal,
// and follows ?tab=skills like every other tab.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import api from '@/services/api'
import { makeListing, makeSkill } from './fixtures/skills125'

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

  it('falls back to Tools when a skills change turns the open tab off', async () => {
    const { wrapper, router } = await mountServerDetail('/servers/alpha?tab=skills')
    expect(wrapper.find('[data-test="skills-panel"]').exists()).toBe(true)
    ;(api.getServerSkills as any).mockResolvedValue({ success: true, data: makeListing([], { enabled: false }) })
    window.dispatchEvent(new CustomEvent('mcpproxy:skills-changed', { detail: { server: 'alpha' } }))
    await flushPromises(); await flushPromises()
    expect(wrapper.find('[data-test="skills-tab"]').exists()).toBe(false)
    expect(router.currentRoute.value.query.tab).toBe('tools')
    wrapper.unmount()
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
})
