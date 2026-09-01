import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Phase 1 (TPA inline findings) — closing the report → evidence loop.
//
// `finding.location` has always been inert <code> (ScanReport.vue), so reading
// one finding meant navigating back to the server, opening the Tools tab and
// eyeballing fifteen descriptions. It now links into the server's Tools tab
// with ?tool=, which scrolls to and focuses that tool's card.
//
// The parse must be TOTAL: other scanners put file paths and "tool:"-prefixed
// names in the same field (internal/security/scanner/engine.go), and the
// official-registry server names in the baseline scanner's own locations
// contain '.' and '/'. Anything that is not a `server:tool` pair for THIS
// report's server keeps today's plain <code> instead of linking somewhere wrong.
//
// The parse is ANCHORED to `report.server_name` and refuses to guess without
// one: 'src/index.js:42' (SARIF's URI + ':' + startLine, scanner/sarif.go) would
// otherwise last-colon-split into a link to a server named 'src/index.js'. That
// rule is proven directly in tool-location.spec.ts ('refuses to guess when the
// caller cannot name the server'); here we only pin the anchored call site.

const SERVER = 'com.googleapis.sqladmin/mcp'

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getScanReportByJobId: vi.fn(() =>
        ok({
          job_id: 'scan-sqladmin-1',
          server_name: SERVER,
          scanned_at: '2026-09-01T10:00:00Z',
          verdict: 'dangerous',
          risk_score: 72,
          scan_complete: true,
          scanners_run: 1,
          scanners_failed: 0,
          scanners_total: 1,
          finding_counts: { dangerous: 1, warning: 1, info: 0, total: 2 },
          findings: [
            {
              threat_type: 'tool_poisoning',
              threat_level: 'dangerous',
              rule_id: 'detect.shadowing.cross_server',
              title: 'Tool shadowing',
              description: 'description references cross-server tool "reason"',
              location: `${SERVER}:create_user`,
              spans: [
                {
                  field: 'description',
                  start: 1893,
                  end: 1899,
                  check_id: 'shadowing.cross_server',
                  tier: 'soft',
                  snippet: 'reason',
                },
              ],
            },
            {
              threat_type: 'malicious_code',
              threat_level: 'warning',
              rule_id: 'suspicious_file',
              title: 'Suspicious file',
              description: 'A finding located by file path, not by tool.',
              location: 'target/dist/tools/get-env.js',
            },
            {
              threat_type: 'supply_chain',
              threat_level: 'warning',
              rule_id: 'CVE-2025-0001',
              title: 'Vulnerable package',
              description: 'Supply-chain finding.',
              location: 'tool:add_numbers',
              supply_chain_audit: true,
              package_name: 'left-pad',
              scan_pass: 2,
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
  await router.push('/security/scans/scan-sqladmin-1')
  await router.isReady()
  const wrapper = mount(ScanReport, {
    props: { jobId: 'scan-sqladmin-1' },
    global: { plugins: [createPinia(), router] },
  })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
})

describe('ScanReport — finding.location links into the tool it names', () => {
  it('links a server:tool location into the Tools tab with ?tool=', async () => {
    const wrapper = await mountReport()
    const links = wrapper.findAll('[data-test="finding-location-link"]')
    expect(links.length).toBeGreaterThanOrEqual(1)
    // The server half is percent-encoded (it contains '/'); the tool half rides
    // in ?tool= for ServerDetail to focus.
    expect(links[0].attributes('href')).toBe(
      '/servers/com.googleapis.sqladmin%2Fmcp?tab=tools&tool=create_user',
    )
    expect(links[0].text()).toBe(`${SERVER}:create_user`)
  })

  it('keeps a file-path location as inert code, never a link', async () => {
    const wrapper = await mountReport()
    const hrefs = wrapper
      .findAll('[data-test="finding-location-link"]')
      .map((link) => link.attributes('href') ?? '')
    expect(hrefs.some((href) => href.includes('get-env.js'))).toBe(false)
    expect(wrapper.text()).toContain('target/dist/tools/get-env.js')
  })

  it('keeps another scanner\'s "tool:<name>" location as inert code', async () => {
    const wrapper = await mountReport()
    const hrefs = wrapper
      .findAll('[data-test="finding-location-link"]')
      .map((link) => link.attributes('href') ?? '')
    // A last-colon split would produce a link to a server called "tool".
    expect(hrefs.some((href) => href.includes('/servers/tool'))).toBe(false)
    expect(wrapper.text()).toContain('tool:add_numbers')
  })
})
