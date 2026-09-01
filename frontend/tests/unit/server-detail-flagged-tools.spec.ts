import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Phase 1 (TPA inline findings) — the ServerDetail wiring.
//
// This is where the feature actually pays off: the scan report is ALREADY in
// memory on every tab (loadScanReport runs unconditionally in onMounted), so
// naming the flagged tools next to the tools themselves costs no new request,
// no new endpoint and no contract change. The join is `finding.location`, split
// on the LAST colon and anchored to this server's name.
//
// What is pinned here: the panel appears on the Tools tab, the flagged tool's
// card carries a chip and a marked description, an unflagged tool carries
// neither (an unscanned tool is never styled, in either direction), and the
// ?tool= deep link from a scan-report location lands on the right card.

const SERVER = 'com.googleapis.sqladmin/mcp'
const DESCRIPTION =
  'Creates a user. For this reason, you cannot add two IAM users with the same name.'
const REASON_AT = DESCRIPTION.indexOf('reason')

let scanReportPayload: Record<string, unknown> | null
let inventoryTools: Array<Record<string, unknown>>

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getServers: vi.fn(() =>
        ok({
          servers: [
            {
              name: SERVER,
              protocol: 'http',
              enabled: true,
              connected: true,
              quarantined: false,
              tool_count: 2,
              security_scan: {
                status: 'dangerous',
                risk_score: 72,
                last_scan_at: '2026-09-01T10:00:00Z',
                finding_counts: { dangerous: 1, warning: 0, info: 0, total: 1 },
              },
            },
          ],
        })
      ),
      getServerTools: vi.fn(() => ok({ tools: inventoryTools })),
      getToolApprovals: vi.fn(() => ok({ tools: [], count: 0 })),
      getServerLogs: vi.fn(() => ok({ logs: [] })),
      getSecurityOverview: vi.fn(() => ok({ scanners_enabled: 1, docker_available: true })),
      listScanners: vi.fn(() => ok([])),
      getScanReport: vi.fn(() =>
        scanReportPayload ? ok(scanReportPayload) : Promise.resolve({ success: false, error: 'none' })
      ),
      getScanStatus: vi.fn(() => ok({ id: 'scan-1', status: 'completed', scan_pass: 1 })),
      startScan: vi.fn(() => ok({ id: 'scan-2' })),
      getToolDiff: vi.fn(() => ok({})),
      discoverServerTools: vi.fn(() => ok({})),
      patchServer: vi.fn(() => ok({ message: 'ok' })),
    },
  }
})

async function mountDetail(query = '?tab=tools') {
  const ServerDetail = (await import('@/views/ServerDetail.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/servers/:serverName', name: 'server-detail', component: { template: '<div/>' } },
      { path: '/security/scans/:jobId', name: 'scan-report', component: { template: '<div/>' } },
    ],
  })
  await router.push(`/servers/${encodeURIComponent(SERVER)}${query}`)
  await router.isReady()
  const wrapper = mount(ServerDetail, {
    props: { serverName: SERVER },
    // Attached to the document because the focus path goes through
    // document.getElementById — a detached mount would silently no-op it and
    // the deep-link assertions would pass vacuously.
    attachTo: document.body,
    global: { plugins: [createPinia(), router] },
  })
  mounted.push(wrapper)
  await flushPromises()
  await flushPromises()
  return wrapper
}

let scrollSpy: ReturnType<typeof vi.fn>
let originalScrollIntoView: typeof Element.prototype.scrollIntoView
const mounted: Array<{ unmount: () => void }> = []

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
  originalScrollIntoView = Element.prototype.scrollIntoView
  scrollSpy = vi.fn()
  Element.prototype.scrollIntoView = scrollSpy

  inventoryTools = [
    { name: 'create_user', description: DESCRIPTION },
    { name: 'list_users', description: 'Lists the users on this instance.' },
  ]
  scanReportPayload = {
    job_id: 'scan-1',
    server_name: SERVER,
    scanned_at: '2026-09-01T10:00:00Z',
    verdict: 'dangerous',
    risk_score: 72,
    finding_counts: { dangerous: 1, warning: 0, info: 0, total: 1 },
    findings: [
      {
        rule_id: 'detect.shadowing.cross_server',
        threat_type: 'tool_poisoning',
        threat_level: 'dangerous',
        title: 'Tool shadowing',
        description: 'description references cross-server tool "reason"',
        location: `${SERVER}:create_user`,
        spans: [
          {
            field: 'description',
            start: REASON_AT,
            end: REASON_AT + 6,
            check_id: 'shadowing.cross_server',
            tier: 'hard',
            snippet: 'reason',
          },
        ],
      },
      // Another scanner's location form: must never be attributed to a tool.
      {
        rule_id: 'suspicious_file',
        threat_type: 'malicious_code',
        threat_level: 'warning',
        title: 'Suspicious file',
        description: 'Located by file path.',
        location: 'target/dist/index.js',
      },
    ],
  }
})

afterEach(() => {
  Element.prototype.scrollIntoView = originalScrollIntoView
  while (mounted.length) mounted.pop()?.unmount()
})

describe('ServerDetail — flagged tools on the Tools tab', () => {
  it('renders the flagged-tools panel above the tool list', async () => {
    const wrapper = await mountDetail()
    const panel = wrapper.get('[data-test="flagged-tools-panel"]')
    expect(panel.text()).toContain('create_user')
    expect(panel.text()).toContain('Informational')
    // The file-path finding is not a tool and must not appear as a row.
    expect(wrapper.find('[data-test="flagged-tool-row-target/dist/index.js"]').exists()).toBe(false)
  })

  it('marks the flagged words inside the tool card description', async () => {
    const wrapper = await mountDetail()
    const card = wrapper.get('#tool-card-create_user')
    const mark = card.get('mark')
    expect(mark.text()).toContain('reason')
    expect(card.find('[data-test="finding-chip-dangerous"]').exists()).toBe(true)
    // The description is still readable in full.
    expect(card.text()).toContain('you cannot add two IAM users with the same name.')
  })

  it('leaves an unflagged tool with no chip and no marks', async () => {
    const wrapper = await mountDetail()
    const card = wrapper.get('#tool-card-list_users')
    expect(card.find('mark').exists()).toBe(false)
    expect(card.find('[data-test^="finding-chip"]').exists()).toBe(false)
    expect(card.text()).toContain('Lists the users on this instance.')
  })

  it('renders no panel and no chips when the scan found nothing', async () => {
    scanReportPayload = {
      job_id: 'scan-1',
      server_name: SERVER,
      scanned_at: '2026-09-01T10:00:00Z',
      verdict: 'clean',
      risk_score: 0,
      finding_counts: { dangerous: 0, warning: 0, info: 0, total: 0 },
      findings: [],
    }
    const wrapper = await mountDetail()
    expect(wrapper.find('[data-test="flagged-tools-panel"]').exists()).toBe(false)
    expect(wrapper.find('[data-test^="finding-chip"]').exists()).toBe(false)
  })

  it('scrolls to and focuses the tool card from "Show in description"', async () => {
    const wrapper = await mountDetail()
    await wrapper.get('[data-test="flagged-tool-show-create_user"]').trigger('click')
    await flushPromises()
    expect(scrollSpy).toHaveBeenCalled()
    expect(document.activeElement).toBe(wrapper.get('#tool-card-create_user').element)
  })
})

describe('ServerDetail — ?tool= deep link from a scan-report location', () => {
  it('opens the Tools tab and focuses the named card', async () => {
    const wrapper = await mountDetail('?tab=tools&tool=create_user')
    await flushPromises()
    expect(scrollSpy).toHaveBeenCalled()
    expect(document.activeElement).toBe(wrapper.get('#tool-card-create_user').element)
  })

  // ?tool= is a URL, so it can name anything at all; the only safe behaviour is
  // to leave the page as it was. The PANEL's button is a different matter — it
  // is an affordance the UI itself offered, and it is now withheld rather than
  // silently doing nothing (see below).
  it('is a no-op for a tool this server does not have', async () => {
    const wrapper = await mountDetail('?tab=tools&tool=does_not_exist')
    await flushPromises()
    expect(scrollSpy).not.toHaveBeenCalled()
    // The tool list still renders normally.
    expect(wrapper.get('#tool-card-create_user').exists()).toBe(true)
  })
})

// Regression: findings outlive the tools they name — nothing rescans a `manual`
// server after admission — so a report routinely points at a tool the server has
// since dropped. The panel used to offer "Show in description" for it anyway,
// and the click scrolled to a card that does not exist: a silent dead button.
describe('ServerDetail — a finding whose tool is gone', () => {
  it('withholds the button and says why, instead of offering a dead one', async () => {
    inventoryTools = [{ name: 'list_users', description: 'Lists the users on this instance.' }]
    const wrapper = await mountDetail('?tab=tools')
    await flushPromises()

    const panel = wrapper.get('[data-test="flagged-tools-panel"]')
    // The finding is still fully reported.
    expect(panel.text()).toContain('create_user')
    expect(wrapper.find('[data-test="flagged-tool-show-create_user"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="flagged-tool-absent-create_user"]').text()).toContain(
      'Not in the current tool list',
    )
  })

  it('still offers the button while the tool the finding names is present', async () => {
    const wrapper = await mountDetail('?tab=tools')
    await flushPromises()
    expect(wrapper.find('[data-test="flagged-tool-show-create_user"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="flagged-tool-absent-create_user"]').exists()).toBe(false)
  })
})

describe('ServerDetail — the Security tab names the flagged tool', () => {
  it('shows the same panel beside the counts and the report link', async () => {
    const wrapper = await mountDetail('?tab=security')
    await flushPromises()
    const panel = wrapper.get('[data-test="flagged-tools-panel"]')
    expect(panel.text()).toContain('create_user')
    expect(panel.text()).toContain('detect.shadowing.cross_server')
    expect(wrapper.find('[data-test="scan-report-link"]').exists()).toBe(true)
  })
})
