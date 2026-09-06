import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// UX audit F09. The Force Approve confirmation counted a DIFFERENT number from
// the one the gate is built on.
//
// `hasUnresolvedCritical` — the predicate that disables Approve and reveals
// Force Approve — reads `finding_counts.dangerous`, which mirrors the backend
// gate exactly (internal/security/scanner/service.go `isBlockingFinding`:
// only a HARD-tier baseline finding blocks, and the 409 reads "server has N
// dangerous (hard-tier) finding(s)"). The confirm() text read raw
// `summary.critical` instead, a severity bucket the gate does not consult. On
// the F09 repro — a duplicate endpoint producing two hard-tier
// detect.shadowing.cross_server findings — the report carried
// {critical: 0, high: 2, dangerous: 2}, so the dialog offered to bypass the
// gate "despite 0 critical finding(s)" while the backend was refusing on two.
//
// Same number, same word as the backend, or the sentence is not about the gate
// the user is standing at.

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
      securityApproveServer: vi.fn(() => ok({})),
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

const originalConfirm = window.confirm

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
})

afterEach(() => {
  window.confirm = originalConfirm
})

describe('ScanReport — Force Approve names the number the gate blocks on', () => {
  it('counts the dangerous (hard-tier) findings, not the critical severity bucket', async () => {
    const wrapper = await mountReport()
    const prompts: string[] = []
    window.confirm = vi.fn((message?: string) => {
      prompts.push(message ?? '')
      return false
    }) as unknown as typeof window.confirm

    await wrapper.get('[data-test="scan-report-force-approve"]').trigger('click')
    await flushPromises()

    expect(prompts).toHaveLength(1)
    expect(prompts[0]).toContain('2 dangerous finding(s)')
    expect(prompts[0]).not.toContain('0 critical')
  })
})
