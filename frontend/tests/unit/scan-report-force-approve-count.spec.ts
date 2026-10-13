import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Direct approval from scan reports must enter informed review.
//
const SERVER = 'context7-docs'

const REPORT = {
  job_id: 'scan-dup-1',
  server_name: SERVER,
  scanned_at: '2026-09-05T10:00:00Z',
  verdict: 'dangerous',
  risk_score: 60,
  scan_complete: true,
  scanners_run: 1,
  scanners_failed: 0,
  scanners_total: 1,
  // The shape the live repro produced: the tier-driven counts say two, the
  // severity bucket the old string read says zero.
  finding_counts: { dangerous: 2, warning: 0, info: 0, total: 2 },
  summary: { critical: 0, high: 2, medium: 0, low: 0, total: 2 },
  findings: [
    {
      threat_type: 'tool_poisoning',
      threat_level: 'dangerous',
      rule_id: 'detect.shadowing.cross_server',
      title: 'Tool shadowing',
      description: 'Tool "resolve-library-id" clones server "context7"\'s tool.',
      location: `${SERVER}:resolve-library-id`,
    },
    {
      threat_type: 'tool_poisoning',
      threat_level: 'dangerous',
      rule_id: 'detect.shadowing.cross_server',
      title: 'Tool shadowing',
      description: 'Tool "query-docs" clones server "context7"\'s tool.',
      location: `${SERVER}:query-docs`,
    },
  ],
}

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getScanReportByJobId: vi.fn(() => ok(REPORT)),
      getServers: vi.fn(() =>
        ok({ servers: [{ name: SERVER, health: { admin_state: 'quarantined' } }] })
      ),
      getScanFiles: vi.fn(() => ok({ files: [], total_files: 0, has_more: false })),
      // NOTE: the api service method is `securityApprove` (the STORE action is
      // `securityApproveServer`); mocking the store's name leaves the real call
      // undefined and the toast never fires.
      securityApprove: vi.fn(() => ok({})),
    },
  }
})

async function mountReport() {
  const ScanReport = (await import('@/views/ScanReport.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/security', name: 'security', component: { template: '<div/>' } },
      { path: '/security/scans/:jobId', name: 'scan-report', component: { template: '<div/>' } },
      { path: '/servers/:serverName', name: 'server-detail', component: { template: '<div/>' } },
      { path: '/review/:server', name: 'review', component: { template: '<div/>' } },
    ],
  })
  await router.push('/security/scans/scan-dup-1')
  await router.isReady()
  const wrapper = mount(ScanReport, {
    props: { jobId: 'scan-dup-1' },
    global: { plugins: [createPinia(), router] },
  })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
})

describe('ScanReport — approval requires informed review (Spec 109-g)', () => {
  it('routes a dangerous report to its server review', async () => {
    const wrapper = await mountReport()

    await wrapper.get('[data-test="scan-report-force-approve"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('.modal-open').exists()).toBe(false)
    expect(wrapper.vm.$route.fullPath).toBe(`/review/${SERVER}`)
  })

  it('never calls the approval API from the scan report', async () => {
    const wrapper = await mountReport()

    await wrapper.get('[data-test="scan-report-force-approve"]').trigger('click')
    await flushPromises()

    const api = (await import('@/services/api')).default
    expect(api.securityApprove).not.toHaveBeenCalled()
  })
})
