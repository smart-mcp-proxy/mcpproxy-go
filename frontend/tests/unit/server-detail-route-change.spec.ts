// Spec 088 — reused-route regression coverage (Codex impl-review F2/F3):
// ServerDetail is NOT remounted when navigating /servers/A → /servers/B, so
// everything onMounted preloads (the latest scan report powering hold-evidence
// report links, FR-011) must also reload in the serverName watcher, and
// per-server UI state (the trust-mode restart notice, FR-004) must reset.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

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
      getScanReport: vi.fn((name: string) =>
        ok({ job_id: `scan-${name}-1`, risk_score: 0, findings: [] })
      ),
      getScanStatus: vi.fn((name: string) =>
        ok({ id: `scan-${name}-1`, status: 'completed', scan_pass: 1 })
      ),
      startScan: vi.fn(() => ok({ id: 'scan-x' })),
      getServerLogs: vi.fn(() => ok({ logs: [] })),
      discoverServerTools: vi.fn(() => ok({})),
      patchServer: vi.fn(() => ok({ message: 'ok', restart_required: true })),
    },
  }
})

async function mountDetail(tab: string) {
  const api = (await import('@/services/api')).default
  const ServerDetail = (await import('@/views/ServerDetail.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/servers/:serverName', component: { template: '<div/>' } },
      { path: '/security/scans/:jobId', component: { template: '<div/>' } },
    ],
  })
  await router.push(`/servers/alpha?tab=${tab}`)
  await router.isReady()
  const wrapper = mount(ServerDetail, {
    props: { serverName: 'alpha' },
    global: { plugins: [createPinia(), router] },
  })
  await flushPromises()
  await flushPromises()
  return { wrapper, api }
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
})

describe('ServerDetail — navigating between server routes without remount', () => {
  it('reloads the scan report for the new server (report links stay live, FR-011)', async () => {
    const { wrapper, api } = await mountDetail('tools')
    const reportCalls = (api.getScanReport as ReturnType<typeof vi.fn>).mock.calls
    expect(reportCalls.some(c => c[0] === 'alpha')).toBe(true)

    await wrapper.setProps({ serverName: 'beta' })
    await flushPromises()
    await flushPromises()

    const calls = (api.getScanReport as ReturnType<typeof vi.fn>).mock.calls
    expect(calls.some(c => c[0] === 'beta')).toBe(true)
  })

  it('resets the trust-mode restart notice from the previous server (FR-004)', async () => {
    const { wrapper } = await mountDetail('config')

    // Trigger a save on alpha whose response demands a restart.
    await wrapper.find('[data-test="trust-mode-option-scan"] input[type="radio"]').setValue(true)
    await flushPromises()
    expect(wrapper.find('[data-test="trust-mode-restart-notice"]').exists()).toBe(true)

    await wrapper.setProps({ serverName: 'beta' })
    await flushPromises()
    await flushPromises()

    expect(wrapper.find('[data-test="trust-mode-restart-notice"]').exists()).toBe(false)
  })
})
