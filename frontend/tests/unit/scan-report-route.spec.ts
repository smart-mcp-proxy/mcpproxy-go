import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import { scanReportPath } from '@/utils/serverRoute'

// MCP-2125 (Defect B of MCP-2123): scan ids embed the raw upstream server name,
// so official-registry servers whose names contain '/' (e.g.
// "com.pulsemcp/google-flights") produce a scan id like
// "scan-com.pulsemcp/google-flights-1781284446323229000". The scan-report route
// is a single `:jobId` segment, so an unencoded '/' splits the path and falls
// through to the catch-all 404. The id MUST be percent-encoded; vue-router v4
// decodes the param back on read (same class as MCP-1112 / serverDetailPath).

const SLASH_SCAN_ID = 'scan-com.pulsemcp/google-flights-1781284446323229000'

describe('scanReportPath (MCP-2125)', () => {
  it('percent-encodes a "/"-containing scan id into a single path segment', () => {
    expect(scanReportPath(SLASH_SCAN_ID)).toBe(
      '/security/scans/scan-com.pulsemcp%2Fgoogle-flights-1781284446323229000'
    )
  })

  it('leaves a plain scan id untouched (no "/" to encode)', () => {
    expect(scanReportPath('scan-github-123')).toBe('/security/scans/scan-github-123')
  })
})

describe('scan-report route round-trip (MCP-2125)', () => {
  it('decodes the encoded "/" back into the jobId param (no 404)', async () => {
    const router = createRouter({
      history: createWebHistory(),
      routes: [
        { path: '/security/scans/:jobId', name: 'scan-report', component: { template: '<div/>' } },
        { path: '/:pathMatch(.*)*', name: 'not-found', component: { template: '<div>404</div>' } },
      ],
    })
    await router.push(scanReportPath(SLASH_SCAN_ID))
    await router.isReady()
    // It must match scan-report (NOT the catch-all 404)...
    expect(router.currentRoute.value.name).toBe('scan-report')
    // ...and the param must be decoded back to the original scan id.
    expect(router.currentRoute.value.params.jobId).toBe(SLASH_SCAN_ID)
  })
})

// ---------------------------------------------------------------------------
// Spec 088 T018 (FR-011): hold evidence links into this report with repeatable
// `?signal=` query params carrying the FULL raw check ids (e.g.
// "tpa.TPA-2026-0001.hidden_instruction"). ScanReport intersects them EXACTLY
// with `findings[].signals` — display labels may shorten to the TPA id, the
// query never does. Linking is best effort: hold evidence carries no report or
// finding identifier, so zero matches must degrade to plain, unannotated report
// rendering — never a claim that something was (or should have been) found.
// ---------------------------------------------------------------------------

const TPA_SIGNAL = 'tpa.TPA-2026-0001.hidden_instruction'

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getScanReportByJobId: vi.fn(() =>
        ok({
          job_id: 'scan-github-1',
          server_name: 'github',
          scanned_at: '2026-07-28T10:00:00Z',
          verdict: 'dangerous',
          risk_score: 72,
          scan_complete: true,
          scanners_run: 1,
          scanners_failed: 0,
          scanners_total: 1,
          finding_counts: { dangerous: 1, warning: 2, info: 0, total: 3 },
          findings: [
            {
              threat_type: 'tool_poisoning',
              threat_level: 'dangerous',
              rule_id: 'tpa-hidden-instruction',
              title: 'Hidden instruction in tool description',
              description: 'Hidden instruction block found in the tool description.',
              signals: ['tpa.TPA-2026-0001.hidden_instruction', 'phrase.injection'],
            },
            {
              threat_type: 'prompt_injection',
              threat_level: 'warning',
              rule_id: 'exfiltration-phrase',
              title: 'Exfiltration phrasing',
              description: 'Suspicious exfiltration phrasing.',
              signals: ['phrase.exfiltration'],
            },
            {
              threat_type: 'malicious_code',
              threat_level: 'warning',
              rule_id: 'no-signal-finding',
              title: 'Finding without signals',
              description: 'A finding that carries no deterministic check ids.',
            },
          ],
        })
      ),
      getServers: vi.fn(() =>
        ok({ servers: [{ name: 'github', health: { admin_state: 'quarantined' } }] })
      ),
      getScanFiles: vi.fn(() => ok({ files: [], total_files: 0, has_more: false })),
    },
  }
})

async function mountReport(query = '') {
  const ScanReport = (await import('@/views/ScanReport.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/security', name: 'security', component: { template: '<div/>' } },
      { path: '/security/scans/:jobId', name: 'scan-report', component: { template: '<div/>' } },
      { path: '/servers/:serverName', name: 'server-detail', component: { template: '<div/>' } },
    ],
  })
  await router.push('/security/scans/scan-github-1' + query)
  await router.isReady()
  const wrapper = mount(ScanReport, {
    props: { jobId: 'scan-github-1' },
    global: { plugins: [createPinia(), router] },
  })
  await flushPromises()
  return wrapper
}

describe('ScanReport — ?signal= finding highlighting (spec 088 T018 / FR-011)', () => {
  let scrollSpy: ReturnType<typeof vi.fn>
  let originalScrollIntoView: typeof Element.prototype.scrollIntoView

  beforeEach(() => {
    setActivePinia(createPinia())
    originalScrollIntoView = Element.prototype.scrollIntoView
    scrollSpy = vi.fn()
    Element.prototype.scrollIntoView = scrollSpy
  })

  afterEach(() => {
    Element.prototype.scrollIntoView = originalScrollIntoView
  })

  it('highlights the finding whose signals contain the passed raw signal', async () => {
    const wrapper = await mountReport(`?signal=${encodeURIComponent(TPA_SIGNAL)}`)
    const highlighted = wrapper.findAll('[data-test="finding-highlighted"]')
    expect(highlighted).toHaveLength(1)
    expect(highlighted[0].text()).toContain('tpa-hidden-instruction')
  })

  it('highlights every finding matched by repeatable ?signal= params', async () => {
    const wrapper = await mountReport(
      `?signal=${encodeURIComponent(TPA_SIGNAL)}&signal=${encodeURIComponent('phrase.exfiltration')}`
    )
    const highlighted = wrapper.findAll('[data-test="finding-highlighted"]')
    expect(highlighted).toHaveLength(2)
    // The signal-less finding is never highlighted.
    expect(wrapper.text()).toContain('no-signal-finding')
    expect(highlighted.map((el) => el.text()).join(' ')).not.toContain('no-signal-finding')
  })

  it('matches exactly — a shortened TPA label or a prefix highlights nothing', async () => {
    const shortened = await mountReport('?signal=TPA-2026-0001')
    expect(shortened.find('[data-test="finding-highlighted"]').exists()).toBe(false)

    const prefix = await mountReport(`?signal=${encodeURIComponent('tpa.TPA-2026-0001')}`)
    expect(prefix.find('[data-test="finding-highlighted"]').exists()).toBe(false)
  })

  it('renders the report unchanged when no ?signal= param is present', async () => {
    const wrapper = await mountReport()
    expect(wrapper.find('[data-test="finding-highlighted"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="signal-highlight-note"]').exists()).toBe(false)
    expect(scrollSpy).not.toHaveBeenCalled()
    // The findings themselves still render normally.
    expect(wrapper.text()).toContain('tpa-hidden-instruction')
  })

  it('makes NO claim when the passed signals match nothing (best-effort link)', async () => {
    const wrapper = await mountReport(`?signal=${encodeURIComponent('tpa.TPA-2099-9999.unknown')}`)
    expect(wrapper.find('[data-test="finding-highlighted"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="signal-highlight-note"]').exists()).toBe(false)
    expect(scrollSpy).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('tpa-hidden-instruction')
  })

  it('scrolls the first matching finding into view on mount', async () => {
    await mountReport(
      `?signal=${encodeURIComponent('phrase.exfiltration')}&signal=${encodeURIComponent(TPA_SIGNAL)}`
    )
    expect(scrollSpy).toHaveBeenCalledTimes(1)
    const target = scrollSpy.mock.instances[0] as HTMLElement
    expect(target.getAttribute('data-test')).toBe('finding-highlighted')
    // First match in report order, not in query order.
    expect(target.textContent).toContain('tpa-hidden-instruction')
  })

  it('notes the highlight only when at least one finding matched', async () => {
    const wrapper = await mountReport(`?signal=${encodeURIComponent(TPA_SIGNAL)}`)
    expect(wrapper.find('[data-test="signal-highlight-note"]').exists()).toBe(true)
  })
})
