import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// The Findings section groups by threat_type. It used to iterate a fixed list
// and silently drop any type not in it — so a detect-engine exfiltration
// finding (detect.ThreatExfiltration, e.g. "copy the content of ~/.ssh" in a
// tool description) was counted in the risk score and badges above but never
// rendered. Exfiltration now has its own group; any type the UI does not know
// falls into "Other Findings" instead of vanishing.

const SERVER = 'leaky-server'

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getScanReportByJobId: vi.fn(() =>
        ok({
          job_id: 'scan-leaky-1',
          server_name: SERVER,
          scanned_at: '2026-09-23T10:00:00Z',
          verdict: 'dangerous',
          risk_score: 80,
          scan_complete: true,
          scanners_run: 1,
          scanners_failed: 0,
          scanners_total: 1,
          finding_counts: { dangerous: 1, warning: 1, info: 0, total: 2 },
          findings: [
            {
              threat_type: 'exfiltration',
              threat_level: 'dangerous',
              rule_id: 'detect.exfil.secret_path',
              title: 'Secret exfiltration phrase',
              description: 'Tool description asks to copy the content of ~/.ssh',
              location: `${SERVER}:read_notes`,
            },
            {
              threat_type: 'some_future_type',
              threat_level: 'warning',
              rule_id: 'future.rule',
              title: 'Finding of an unknown category',
              description: 'A threat type this UI build has never heard of.',
              location: `${SERVER}:other_tool`,
            },
          ],
        })
      ),
      getServers: vi.fn(() => ok({ servers: [{ name: SERVER, health: { admin_state: 'enabled' } }] })),
      getScanFiles: vi.fn(() => ok({ files: [], total_files: 0, has_more: false })),
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
  await router.push('/security/scans/scan-leaky-1')
  await router.isReady()
  const wrapper = mount(ScanReport, {
    props: { jobId: 'scan-leaky-1' },
    global: { plugins: [createPinia(), router] },
  })
  await flushPromises()
  return wrapper
}

function groupTitles(wrapper: Awaited<ReturnType<typeof mountReport>>): string[] {
  return wrapper.findAll('[data-test="finding-group"]').map((g) => g.attributes('data-threat-type') ?? '')
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
})

describe('ScanReport — every finding lands in a rendered group', () => {
  it('renders an exfiltration finding under its own open group', async () => {
    const wrapper = await mountReport()
    const group = wrapper.find('[data-test="finding-group"][data-threat-type="exfiltration"]')
    expect(group.exists()).toBe(true)
    expect(group.text()).toContain('Data Exfiltration')
    expect(group.text()).toContain('copy the content of ~/.ssh')
    // Dangerous category — open by default like the other hard-signal groups.
    expect(group.classes()).toContain('collapse-open')
  })

  it('folds an unknown threat type into "Other Findings"', async () => {
    const wrapper = await mountReport()
    const group = wrapper.find('[data-test="finding-group"][data-threat-type="uncategorized"]')
    expect(group.exists()).toBe(true)
    expect(group.text()).toContain('Other Findings')
    expect(group.text()).toContain('has never heard of')
    expect(groupTitles(wrapper)).not.toContain('some_future_type')
  })

  it('renders as many findings as the report counts', async () => {
    const wrapper = await mountReport()
    const rendered = wrapper
      .findAll('[data-test="finding-group"]')
      .reduce((n, g) => n + Number(g.find('.collapse-title .badge').text()), 0)
    expect(rendered).toBe(2)
  })
})
