// Spec 109 FR-016 / T009 / T021: every tabbed Web UI view reads its tab from
// `?tab=` on mount and writes it with `router.replace` on change, keeping
// other query parameters. ServerDetail already read `?tab=` on mount; this
// pins the write-back half (and that it survives alongside another query
// param), plus the same contract for Settings' tabs.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory, createMemoryHistory } from 'vue-router'

const ok = <T,>(data: T) => Promise.resolve({ success: true, data })

vi.mock('@/services/api', () => {
  const server = (name: string) => ({
    name,
    protocol: 'stdio',
    enabled: true,
    connected: true,
    quarantined: false,
    tool_count: 0,
    trust_mode: 'manual',
    security_scan: {
      status: 'clean',
      risk_score: 0,
      last_scan_at: '2026-07-28T10:00:00Z',
      scanners_run: 1,
      scanners_failed: 0,
      scanners_total: 1,
    },
  })
  return {
    default: {
      getServers: vi.fn(() => ok({ servers: [server('alpha'), server('beta')] })),
      getToolApprovals: vi.fn(() => ok({ tools: [], count: 0 })),
      getServerTools: vi.fn(() => ok({ tools: [] })),
      getSecurityOverview: vi.fn(() => ok({ scanners_enabled: 0, docker_available: true })),
      getScanReport: vi.fn(() => ok({ job_id: 'scan-alpha-1', risk_score: 0, findings: [] })),
      getScanStatus: vi.fn(() => ok({ id: 'scan-alpha-1', status: 'completed', scan_pass: 1 })),
      startScan: vi.fn(() => ok({ id: 'scan-x' })),
      getServerLogs: vi.fn(() => ok({ logs: [] })),
      discoverServerTools: vi.fn(() => ok({})),
      patchServer: vi.fn(() => ok({ message: 'ok', restart_required: true })),
      getConfig: vi.fn(() => ok({ config: { server_edition: {} } })),
      getStatus: vi.fn(() => ok({})),
      validateConfig: vi.fn(),
      applyConfig: vi.fn(),
    },
  }
})

async function mountServerDetail(path: string) {
  const ServerDetail = (await import('@/views/ServerDetail.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/servers/:serverName', component: { template: '<div/>' } },
      { path: '/security/scans/:jobId', component: { template: '<div/>' } },
    ],
  })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(ServerDetail, {
    props: { serverName: 'alpha' },
    global: { plugins: [createPinia(), router] },
  })
  await flushPromises()
  await flushPromises()
  return { wrapper, router }
}

async function mountSettings(path = '/settings') {
  const Settings = (await import('@/views/Settings.vue')).default
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/settings', name: 'settings', component: Settings, meta: { title: 'Settings' } },
    ],
  })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(Settings, {
    global: {
      plugins: [router],
      stubs: { VueMonacoEditor: true, ConnectModal: true },
    },
  })
  await flushPromises()
  return { wrapper, router }
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
})

describe('ServerDetail tab <-> ?tab= (Spec 109 FR-016)', () => {
  it('writes the query param when a tab is clicked, keeping other params', async () => {
    const { wrapper, router } = await mountServerDetail('/servers/alpha?tab=tools&focus=endpoint')

    const logsTab = wrapper.findAll('.tab').find((n) => n.text().includes('Logs'))
    expect(logsTab).toBeTruthy()
    await logsTab!.trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.query.tab).toBe('logs')
    expect(router.currentRoute.value.query.focus).toBe('endpoint')
  })

  // Review round 8, finding 3: <router-view :key="systemStore.authEpoch"> in
  // App.vue means navigating from one /servers/:serverName to another does
  // NOT remount ServerDetail (same route record, same component instance) —
  // onMounted never runs again. The existing props.serverName watch already
  // resets tool/log/scan state for exactly that reason, but never re-read
  // `?tab=`, so a stale tab (e.g. Security, left over from the previous
  // server) kept showing for the new server even though its URL carries no
  // `?tab=` at all.
  it('re-reads ?tab= on an in-place serverName change, resetting to the default when absent', async () => {
    const { wrapper, router } = await mountServerDetail('/servers/alpha?tab=security')

    const securityTab = wrapper.find('[data-test="security-tab"]')
    expect(securityTab.classes()).toContain('tab-active')

    // Simulate the SPA navigation App.vue's <router-view> performs without a
    // remount: the route changes, and the parent hands the component a new
    // serverName prop, but the ServerDetail instance itself is never
    // recreated (no unmount/mount here either).
    await router.push('/servers/beta')
    await wrapper.setProps({ serverName: 'beta' })
    await flushPromises()
    await flushPromises()

    const toolsTab = wrapper.find('[data-test="security-tab"]')
    expect(toolsTab.classes()).not.toContain('tab-active')
    expect(wrapper.find('.tab.tab-active').text()).toContain('Tools')
  })

  it('re-reads ?tab= on an in-place serverName change, honoring the new URL', async () => {
    const { wrapper, router } = await mountServerDetail('/servers/alpha?tab=tools')

    await router.push('/servers/beta?tab=logs')
    await wrapper.setProps({ serverName: 'beta' })
    await flushPromises()
    await flushPromises()

    expect(wrapper.find('.tab.tab-active').text()).toContain('Logs')
  })
})

describe('Settings tab <-> ?tab= (Spec 109 FR-016)', () => {
  it('reads the tab from ?tab= on mount', async () => {
    const { wrapper } = await mountSettings('/settings?tab=advanced')
    expect(wrapper.find('[data-test="settings-tab-advanced"]').classes()).toContain('tab-active')
  })

  it('writes ?tab= when a tab is clicked, keeping other query params', async () => {
    const { wrapper, router } = await mountSettings('/settings?focus=api_key')

    await wrapper.find('[data-test="settings-tab-advanced"]').trigger('click')
    await flushPromises()

    expect(router.currentRoute.value.query.tab).toBe('advanced')
    expect(router.currentRoute.value.query.focus).toBe('api_key')
  })
})
