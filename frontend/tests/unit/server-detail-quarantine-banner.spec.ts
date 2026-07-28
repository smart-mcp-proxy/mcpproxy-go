import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Spec 088 US3 (T020) — the server-level Security-Quarantine banner renders the
// `deriveQuarantineBannerState` output instead of one generic sentence, so an
// operator can tell from the banner ALONE which of the four situations applies
// (FR-013), sees the latest scan outcome when one exists (FR-014), and is
// offered a scan when nothing has ever been scanned (FR-015).
//
// The derivation itself is unit-tested exhaustively in
// `quarantine-banner-states.spec.ts`; this spec covers the WIRING: state
// attribute, copy, scan-summary row, and the four actions (approve keeps the
// existing gated modal flow, view-report opens the latest report route,
// retry-scan / run-scan go through the existing startScan path).

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
              quarantined: true,
              tool_count: 1,
              ...serverExtra,
            },
          ],
        })
      ),
      getToolApprovals: vi.fn(() => ok({ tools: [], count: 0 })),
      getToolDiff: vi.fn(() => ok({})),
      getServerTools: vi.fn(() => ok({ tools: [] })),
      getSecurityOverview: vi.fn(() => ok({ scanners_enabled: 0, docker_available: true })),
      listScanners: vi.fn(() => ok([])),
      getScanReport: vi.fn(() =>
        scanReportPayload
          ? ok(scanReportPayload)
          : Promise.resolve({ success: false, error: 'no report' })
      ),
      getScanStatus: vi.fn(() => ok({ id: 'scan-github-1', status: 'completed', scan_pass: 1 })),
      startScan: vi.fn(() => ok({ id: 'scan-github-2' })),
      getServerLogs: vi.fn(() => ok({ logs: [] })),
      discoverServerTools: vi.fn(() => ok({})),
      approveTools: vi.fn(() => ok({ approved: 1 })),
      blockTools: vi.fn(() => ok({ blocked: 1 })),
      patchServer: vi.fn(() => ok({ message: 'ok' })),
    },
  }
})

async function mountDetail() {
  const api = (await import('@/services/api')).default
  const ServerDetail = (await import('@/views/ServerDetail.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/servers/:serverName', component: { template: '<div/>' } },
      { path: '/security/scans/:jobId', component: { template: '<div/>' } },
    ],
  })
  await router.push('/servers/github?tab=tools')
  await router.isReady()
  const wrapper = mount(ServerDetail, {
    props: { serverName: 'github' },
    global: { plugins: [createPinia(), router] },
  })
  await flushPromises()
  await flushPromises()
  return { wrapper, api }
}

function scan(status: string, extra: Record<string, unknown> = {}) {
  return {
    security_scan: {
      status,
      risk_score: 0,
      last_scan_at: '2026-07-28T10:00:00Z',
      finding_counts: { dangerous: 0, warning: 0, info: 0, total: 0 },
      scanners_run: 1,
      scanners_failed: 0,
      scanners_total: 1,
      ...extra,
    },
  }
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
  serverExtra = {}
  scanReportPayload = { job_id: 'scan-github-1', risk_score: 0, findings: [] }
})

describe('ServerDetail — quarantine banner states (Spec 088 T020, FR-013)', () => {
  const cases: Array<{ label: string; extra: Record<string, unknown>; state: string }> = [
    { label: 'scan running', extra: { trust_mode: 'scan', ...scan('scanning') }, state: 'scan-running' },
    { label: 'scan failed', extra: { trust_mode: 'scan', ...scan('failed') }, state: 'scan-failed' },
    { label: 'verdict blocked', extra: { trust_mode: 'scan', ...scan('dangerous') }, state: 'scan-blocked' },
    { label: 'awaiting manual review', extra: { trust_mode: 'manual', ...scan('clean') }, state: 'manual-review' },
    { label: 'never scanned', extra: { trust_mode: 'manual' }, state: 'manual-review' },
  ]

  it.each(cases)('$label renders data-state=$state with distinct copy', async ({ extra, state }) => {
    serverExtra = extra
    const { wrapper } = await mountDetail()
    const el = wrapper.find('[data-test="quarantine-banner-state"]')
    expect(el.exists()).toBe(true)
    expect(el.attributes('data-state')).toBe(state)
    expect(wrapper.find('[data-test="quarantine-banner-headline"]').text().trim().length).toBeGreaterThan(0)
    expect(wrapper.find('[data-test="quarantine-banner-detail"]').text().trim().length).toBeGreaterThan(0)
  })

  it('never renders the banner for a server that is not quarantined', async () => {
    serverExtra = { quarantined: false, ...scan('dangerous') }
    const { wrapper } = await mountDetail()
    expect(wrapper.find('[data-test="security-quarantine-banner"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="quarantine-banner-state"]').exists()).toBe(false)
  })

  it('presents a failed scan without threat styling (never a verdict)', async () => {
    serverExtra = { trust_mode: 'scan', ...scan('failed') }
    const { wrapper } = await mountDetail()
    const banner = wrapper.find('[data-test="security-quarantine-banner"]')
    expect(banner.classes()).not.toContain('alert-error')
    expect(banner.text().toLowerCase()).toMatch(/not a threat verdict/)
  })
})

describe('ServerDetail — quarantine banner scan summary (Spec 088 T020, FR-014)', () => {
  it('shows verdict, risk score and finding counts when a scan summary exists', async () => {
    serverExtra = {
      trust_mode: 'scan',
      ...scan('dangerous', { risk_score: 82, finding_counts: { dangerous: 2, warning: 3, info: 1, total: 6 } }),
    }
    const { wrapper } = await mountDetail()
    const summary = wrapper.find('[data-test="quarantine-scan-summary"]')
    expect(summary.exists()).toBe(true)
    const text = summary.text()
    expect(text).toContain('dangerous')
    expect(text).toContain('82')
    expect(text).toContain('2')
    expect(text).toContain('3')
  })

  it('shows no summary row when the payload carries no security_scan field', async () => {
    serverExtra = { trust_mode: 'manual' }
    const { wrapper } = await mountDetail()
    expect(wrapper.find('[data-test="quarantine-scan-summary"]').exists()).toBe(false)
  })
})

describe('ServerDetail — quarantine banner actions (Spec 088 T020, FR-014/FR-015)', () => {
  it('offers approval through the existing gated approve flow', async () => {
    serverExtra = { trust_mode: 'manual' }
    const { wrapper } = await mountDetail()
    const approve = wrapper.find('[data-test="quarantine-action-approve"]')
    expect(approve.exists()).toBe(true)
    await approve.trigger('click')
    await flushPromises()
    // No scan has run → the existing "No Security Scan Run" confirmation opens.
    expect(wrapper.text()).toContain('No Security Scan Run')
  })

  it('offers a Run-scan CTA when nothing has ever been scanned, starting a scan', async () => {
    serverExtra = { trust_mode: 'manual' }
    scanReportPayload = null
    const { wrapper, api } = await mountDetail()
    const cta = wrapper.find('[data-test="quarantine-action-run-scan"]')
    expect(cta.exists()).toBe(true)
    await cta.trigger('click')
    await flushPromises()
    expect(api.startScan).toHaveBeenCalledWith('github')
  })

  it('offers a retry on a failed scan, and no run-scan CTA', async () => {
    serverExtra = { trust_mode: 'scan', ...scan('failed') }
    const { wrapper, api } = await mountDetail()
    expect(wrapper.find('[data-test="quarantine-action-run-scan"]').exists()).toBe(false)
    const retry = wrapper.find('[data-test="quarantine-action-retry-scan"]')
    expect(retry.exists()).toBe(true)
    await retry.trigger('click')
    await flushPromises()
    expect(api.startScan).toHaveBeenCalledWith('github')
  })

  it('links to the latest scan report when a verdict exists', async () => {
    serverExtra = { trust_mode: 'scan', ...scan('dangerous', { risk_score: 82 }) }
    const { wrapper } = await mountDetail()
    const link = wrapper.find('[data-test="quarantine-action-view-report"]')
    expect(link.exists()).toBe(true)
    expect(link.attributes('href')).toContain('/security/scans/scan-github-1')
  })

  it('offers no action at all while a scan is running', async () => {
    serverExtra = { trust_mode: 'scan', ...scan('scanning') }
    const { wrapper } = await mountDetail()
    expect(wrapper.find('[data-test="quarantine-action-approve"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="quarantine-action-run-scan"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="quarantine-action-retry-scan"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="quarantine-action-view-report"]').exists()).toBe(false)
  })
})
