import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Spec 088 US4 (T022) — the per-server Security tab and its Scan Now action are
// available on a DEFAULT install: the deterministic offline baseline scanner
// (spec 077) always runs in-process, so gating the whole surface behind
// `hasEnabledScanners()` (optional Docker deep scanners) hid a capability that
// exists (FR-016). Docker absence no longer disables Scan Now either — it only
// means the optional deep scanners are skipped, which the report presents as
// SKIPPED, never as a failure (FR-017).
//
// Every fixture here reports ZERO enabled scanners and no Docker, i.e. the
// default install that previously had no reachable scan surface at all.

let serverExtra: Record<string, unknown>
let scanReportPayload: Record<string, unknown> | null

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getServers: vi.fn(() =>
        ok({
          servers: [
            {
              name: 'github',
              protocol: 'stdio',
              enabled: true,
              connected: true,
              quarantined: false,
              tool_count: 1,
              ...serverExtra,
            },
          ],
        })
      ),
      getToolApprovals: vi.fn(() => ok({ tools: [], count: 0 })),
      getToolDiff: vi.fn(() => ok({})),
      getServerTools: vi.fn(() => ok({ tools: [] })),
      // Default install: no optional scanner enabled, no Docker.
      getSecurityOverview: vi.fn(() => ok({ scanners_enabled: 0, docker_available: false })),
      listScanners: vi.fn(() => ok([])),
      getScanReport: vi.fn(() =>
        scanReportPayload
          ? ok(scanReportPayload)
          : Promise.resolve({ success: false, error: 'no report' })
      ),
      getScanStatus: vi.fn(() => ok({ id: 'scan-github-1', status: 'completed', scan_pass: 1 })),
      startScan: vi.fn(() => ok({ id: 'scan-github-2' })),
      cancelScan: vi.fn(() => ok({})),
      getServerLogs: vi.fn(() => ok({ logs: [] })),
      discoverServerTools: vi.fn(() => ok({})),
      approveTools: vi.fn(() => ok({ approved: 1 })),
      blockTools: vi.fn(() => ok({ blocked: 1 })),
      patchServer: vi.fn(() => ok({ message: 'ok' })),
    },
  }
})

async function mountDetail(tab: 'security' | 'tools' = 'security') {
  const api = (await import('@/services/api')).default
  const ServerDetail = (await import('@/views/ServerDetail.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/servers/:serverName', component: { template: '<div/>' } },
      { path: '/security/scans/:jobId', component: { template: '<div/>' } },
    ],
  })
  await router.push(`/servers/github?tab=${tab}`)
  await router.isReady()
  const wrapper = mount(ServerDetail, {
    props: { serverName: 'github' },
    global: { plugins: [createPinia(), router] },
  })
  await flushPromises()
  await flushPromises()
  return { wrapper, api }
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
  serverExtra = {}
  scanReportPayload = null
})

describe('ServerDetail — Security tab is un-gated (Spec 088 T022, FR-016)', () => {
  it('renders the Security tab with zero enabled deep scanners', async () => {
    const { wrapper } = await mountDetail('tools')
    expect(wrapper.find('[data-test="security-tab"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('renders Scan Now, enabled, when Docker is unavailable', async () => {
    const { wrapper } = await mountDetail()
    const button = wrapper.find('[data-test="scan-button"]')
    expect(button.exists()).toBe(true)
    expect(button.attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('starts a baseline scan from Scan Now on a default install', async () => {
    const { wrapper, api } = await mountDetail()
    await wrapper.find('[data-test="scan-button"]').trigger('click')
    await flushPromises()
    expect(api.startScan).toHaveBeenCalledWith('github')
    wrapper.unmount()
  })

  it('keeps the tab status dot rendering when the overview reports zero scanners', async () => {
    serverExtra = {
      security_scan: {
        status: 'warnings',
        risk_score: 30,
        last_scan_at: '2026-07-28T10:00:00Z',
        finding_counts: { dangerous: 0, warning: 2, info: 0, total: 2 },
        scanners_run: 1,
        scanners_failed: 0,
        scanners_total: 1,
      },
    }
    scanReportPayload = { job_id: 'scan-github-1', risk_score: 30, findings: [] }
    const { wrapper } = await mountDetail('tools')
    const dot = wrapper.find('[data-test="security-tab-dot"]')
    expect(dot.exists()).toBe(true)
    expect(dot.classes()).toContain('bg-warning')
    wrapper.unmount()
  })

  it('still offers the never-scanned empty state', async () => {
    const { wrapper } = await mountDetail()
    expect(wrapper.text()).toContain('No Security Scan')
    wrapper.unmount()
  })
})

describe('ServerDetail — skipped deep scanners (Spec 088 T022, FR-017)', () => {
  const skipped = { enabled: false, ran: false, available: false, skipped_scanners: ['ramparts', 'cisco'] }

  it('presents skipped optional scanners as skipped, not as an error', async () => {
    scanReportPayload = {
      job_id: 'scan-github-1',
      risk_score: 0,
      findings: [],
      deep_scan: skipped,
    }
    serverExtra = {
      security_scan: {
        status: 'clean',
        risk_score: 0,
        last_scan_at: '2026-07-28T10:00:00Z',
        finding_counts: { dangerous: 0, warning: 0, info: 0, total: 0 },
        scanners_run: 1,
        scanners_failed: 0,
        scanners_total: 1,
        deep_scan: skipped,
      },
    }
    const { wrapper } = await mountDetail()
    const note = wrapper.find('[data-test="deep-scan-skipped"]')
    expect(note.exists()).toBe(true)
    expect(note.text().toLowerCase()).toContain('skipped')
    expect(note.text()).toContain('ramparts')
    expect(note.classes()).not.toContain('alert-error')
    expect(note.text().toLowerCase()).not.toContain('failed')
    wrapper.unmount()
  })

  it('renders the skipped note from the server summary alone (no report loaded yet)', async () => {
    scanReportPayload = null
    serverExtra = {
      security_scan: {
        status: 'clean',
        risk_score: 0,
        last_scan_at: '2026-07-28T10:00:00Z',
        finding_counts: { dangerous: 0, warning: 0, info: 0, total: 0 },
        scanners_run: 1,
        scanners_failed: 0,
        scanners_total: 1,
        deep_scan: skipped,
      },
    }
    const { wrapper } = await mountDetail()
    expect(wrapper.find('[data-test="deep-scan-skipped"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('renders no note when nothing was skipped', async () => {
    scanReportPayload = {
      job_id: 'scan-github-1',
      risk_score: 0,
      findings: [],
      deep_scan: { enabled: true, ran: true, available: true },
    }
    const { wrapper } = await mountDetail()
    expect(wrapper.find('[data-test="deep-scan-skipped"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
