import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Spec 134 PR 3 (FR-030..FR-035): server detail header, facts, "Review and scans" and the first-screen links.
let serverExtra: Record<string, unknown>
let reviewData: Record<string, unknown>

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getServers: vi.fn(() => ok({ servers: [{ name: 'github', protocol: 'stdio', enabled: true, connected: true, quarantined: false, tool_count: 1, ...serverExtra }] })),
      getToolApprovals: vi.fn(() => ok({ tools: [], count: 0 })),
      getToolDiff: vi.fn(() => ok({})),
      getServerTools: vi.fn(() => ok({ tools: [] })),
      getSecurityOverview: vi.fn(() => ok({ scanners_enabled: 0, docker_available: false })),
      listScanners: vi.fn(() => ok([])),
      getScanReport: vi.fn(() => Promise.resolve({ success: false, error: 'no report' })),
      getScanStatus: vi.fn(() => ok({ id: 's', status: 'completed', scan_pass: 1 })),
      startScan: vi.fn(() => ok({ id: 'scan-2' })),
      cancelScan: vi.fn(() => ok({})),
      getServerLogs: vi.fn(() => ok({ logs: [] })),
      discoverServerTools: vi.fn(() => ok({})),
      approveTools: vi.fn(() => ok({})),
      blockTools: vi.fn(() => ok({})),
      patchServer: vi.fn(() => ok({})),
      getServerReview: vi.fn(() => ok(reviewData)),
      listScanHistory: vi.fn(() => ok({ scans: [], total: 0 })),
      getQueueProgress: vi.fn(() => ok({ status: 'idle' })),
      getServerSkills: vi.fn(() => ok({ enabled: false, supported: false, skills: [] })),
      securityApprove: vi.fn(() => ok({})),
      restartServer: vi.fn(() => ok({})),
      enableServer: vi.fn(() => ok({})),
      disableServer: vi.fn(() => ok({})),
    },
  }
})

async function mountDetail(query = '') {
  const ServerDetail = (await import('@/views/ServerDetail.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/servers/:serverName', component: { template: '<div/>' } },
      { path: '/secrets', component: { template: '<div/>' } },
      { path: '/activity', component: { template: '<div/>' } },
      { path: '/security/scans/:jobId', component: { template: '<div/>' } },
    ],
  })
  await router.push(`/servers/github${query}`)
  await router.isReady()
  const wrapper = mount(ServerDetail, { props: { serverName: 'github' }, global: { plugins: [createPinia(), router] }, attachTo: document.body })
  await flushPromises(); await flushPromises()
  return { wrapper, router }
}

const tool = (name: string, approval_status: string) => ({ name, description: name, tier: 'read', approval_status, disabled: false, scan_verdict: 'clean' })

beforeEach(() => {
  setActivePinia(createPinia())
  document.body.innerHTML = ''
  serverExtra = {}
  reviewData = { server: { name: 'github', transport: 'stdio', quarantined: false, definitions_captured: true }, tools: [] }
})

describe('header (FR-030)', () => {
  it('prints the label through the pill, the reason beside it, and the primary action once', async () => {
    serverExtra = { connected: false, health: { level: 'unhealthy', admin_state: 'enabled', summary: 'Token expired', action: 'login', actions: ['login'], status: 'sign_in_required', usable: false } }
    const { wrapper } = await mountDetail()
    expect(wrapper.get('[data-test="server-status-badge"]').text()).toBe('Sign-in required')
    expect(wrapper.get('[data-test="server-detail-reason"]').text()).toBe('Token expired')
    expect(wrapper.findAll('[data-test="server-detail-primary-action"]')).toHaveLength(1)
    expect(wrapper.get('[data-test="server-detail-primary-action"]').text()).toBe('Sign in')
  })

  it('shows no primary button when health.actions is empty', async () => {
    serverExtra = { health: { level: 'healthy', admin_state: 'enabled', summary: 'Connected', status: 'ready', usable: true, actions: [] } }
    const { wrapper } = await mountDetail()
    expect(wrapper.find('[data-test="server-detail-primary-action"]').exists()).toBe(false)
  })

  it('an approve primary action opens the review part and never approves', async () => {
    const api = (await import('@/services/api')).default
    serverExtra = { quarantined: true, connected: false, health: { level: 'healthy', admin_state: 'quarantined', summary: 'Quarantined for review', status: 'needs_review', usable: false, actions: ['approve'] } }
    reviewData = { server: { name: 'github', transport: 'stdio', quarantined: true, definitions_captured: false }, tools: [] }
    const { wrapper, router } = await mountDetail()
    await wrapper.get('[data-test="server-detail-primary-action"]').trigger('click'); await flushPromises()
    expect(router.currentRoute.value.query.tab).toBe('review')
    expect(api.securityApprove).not.toHaveBeenCalled()
    expect(api.approveTools).not.toHaveBeenCalled()
  })

  it('the Actions menu keeps every present action', async () => {
    const { wrapper } = await mountDetail()
    const text = wrapper.get('[data-test="server-actions-menu"]').element.parentElement!.textContent!
    for (const label of ['Disable', 'Restart', 'Discover Tools', 'Quarantine', 'Refresh']) expect(text).toContain(label)
  })
})

describe('facts on the first screen (FR-020, FR-030)', () => {
  it('shows id vs title, transport state, trust, scan verdict and held counts on every tab', async () => {
    serverExtra = { title: 'GitHub tools', trust_mode: 'manual', quarantine: { pending_count: 2, changed_count: 1, blocked_count: 0 }, security_scan: { status: 'warnings', finding_counts: { dangerous: 0, warning: 3, info: 0 } } }
    for (const tab of ['tools', 'logs', 'config', 'review']) {
      const { wrapper } = await mountDetail(`?tab=${tab}`)
      expect(wrapper.get('[data-test="fact-id"]').text()).toBe('github')
      expect(wrapper.get('[data-test="fact-title"]').text()).toBe('GitHub tools')
      expect(wrapper.get('[data-test="fact-transport"]').text()).toContain('connected')
      expect(wrapper.get('[data-test="server-trust-mode"]').attributes('aria-label')).toContain('Manual')
      expect(wrapper.get('[data-test="fact-scan"]').text()).toContain('3 scan warnings')
      expect(wrapper.get('[data-test="fact-held-counts"]').text()).toBe('2 pending, 1 changed')
      wrapper.unmount()
    }
  })

  it('opens the complete untruncated diagnostic and raw error, and returns focus', async () => {
    const long = 'x'.repeat(600)
    serverExtra = { last_error: `boom ${long}`, health: { level: 'unhealthy', admin_state: 'enabled', summary: 'Error', detail: 'long detail', status: 'error', usable: false, actions: [] } }
    const { wrapper } = await mountDetail()
    expect(wrapper.find('[data-test="diagnostic-value"]').exists()).toBe(false)
    const trigger = wrapper.get('[data-test="open-diagnostic"]')
    await trigger.trigger('click'); await flushPromises()
    const values = wrapper.findAll('[data-test="diagnostic-value"]').map(v => v.text())
    expect(values).toContain(`boom ${long}`)
    expect(values).toContain('long detail')
    await wrapper.get('[data-test="diagnostic-close"]').trigger('click'); await flushPromises()
    expect(wrapper.find('[data-test="diagnostic-value"]').exists()).toBe(false)
  })
})

describe('Review and scans (FR-031)', () => {
  it('?tab=review lands on the review part, ?tab=security on the scan part; both parts exist', async () => {
    const a = await mountDetail('?tab=review')
    expect(a.wrapper.get('[data-test="review-tab"]').classes()).toContain('tab-active')
    expect(a.wrapper.find('[data-test="review-part"] [data-test="review-screen"]').exists()).toBe(true)
    expect(a.wrapper.find('[data-test="scans-part"] [data-test="scan-button"]').exists()).toBe(true)
    a.wrapper.unmount()
    const b = await mountDetail('?tab=security')
    expect(b.wrapper.get('[data-test="review-tab"]').classes()).toContain('tab-active')
    expect(b.router.currentRoute.value.query.tab).toBe('security')
  })

  it('has exactly the five tabs, with no separate Security tab', async () => {
    const { wrapper } = await mountDetail()
    const labels = wrapper.findAll('[role="tab"]').map(t => t.text().replace(/\s+/g, ' '))
    expect(labels.some(l => l.startsWith('Tools'))).toBe(true)
    expect(labels.some(l => l.startsWith('Review and scans'))).toBe(true)
    expect(labels).toContain('Logs')
    expect(labels).toContain('Configuration')
    expect(labels.some(l => /^Security/.test(l))).toBe(false)
  })

  it('?tool= keeps today\'s precedence over ?tab=review', async () => {
    const { wrapper } = await mountDetail('?tab=review&tool=nonexistent')
    expect(wrapper.get('[data-test="review-tab"]').classes()).not.toContain('tab-active')
  })

  it('a scan can still be started from the scan part', async () => {
    const api = (await import('@/services/api')).default
    const { wrapper } = await mountDetail('?tab=security')
    await wrapper.get('[data-test="scan-button"]').trigger('click'); await flushPromises()
    expect(api.startScan).toHaveBeenCalledWith('github')
  })
})

describe('summary lines reveal what is below the first screen (FR-035)', () => {
  it('a held tool line clears a filter that hides it and focuses the tool', async () => {
    serverExtra = { quarantine: { pending_count: 1, changed_count: 0, blocked_count: 0 } }
    reviewData = { server: { name: 'github', transport: 'stdio', quarantined: false, definitions_captured: true }, tools: [tool('a_ok', 'approved'), tool('z_held', 'pending')] }
    const { wrapper, router } = await mountDetail('?tab=tools')
    await wrapper.get('[data-test="attention-held-tools"]').trigger('click'); await flushPromises(); await flushPromises()
    expect(router.currentRoute.value.query.tab).toBe('review')
    const held = wrapper.get('[data-test="review-tool-z_held"]')
    expect(document.activeElement).toBe(held.element)
  })

  it('a hiding filter is cleared before the reveal', async () => {
    serverExtra = { quarantine: { pending_count: 1, changed_count: 0, blocked_count: 0 } }
    reviewData = { server: { name: 'github', transport: 'stdio', quarantined: false, definitions_captured: true }, tools: [tool('a_ok', 'approved'), tool('z_held', 'pending')] }
    const { wrapper } = await mountDetail('?tab=review')
    await wrapper.get('[data-test="review-filter-state"]').setValue('approved'); await flushPromises()
    expect(wrapper.find('[data-test="review-tool-z_held"]').exists()).toBe(false)
    await wrapper.get('[data-test="attention-held-tools"]').trigger('click'); await flushPromises(); await flushPromises()
    expect(wrapper.find('[data-test="review-tool-z_held"]').exists()).toBe(true)
    expect(document.activeElement).toBe(wrapper.get('[data-test="review-tool-z_held"]').element)
  })

  it('a failed scan line opens the scan part', async () => {
    serverExtra = { security_scan: { status: 'failed' } }
    const { wrapper, router } = await mountDetail('?tab=tools')
    await wrapper.get('[data-test="attention-scan"]').trigger('click'); await flushPromises()
    expect(router.currentRoute.value.query.tab).toBe('security')
  })
})
