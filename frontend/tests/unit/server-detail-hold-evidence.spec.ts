import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Spec 088 US2 (T016) — the per-server tool-quarantine panel surfaces the
// spec-086 hold evidence on every held tool row, and on the changed-tool diff
// sections alongside the before/after comparison (FR-008/FR-009/FR-010/FR-012).
//
// Data sources, per research D1/D4:
//   - list rows: durable `/tools/export` records with `held_*` joined on from
//     the `/tools` inventory (api.getToolApprovals does the join — mocked here
//     the same way so the view sees the production shape);
//   - changed rows: `/tools/{tool}/diff` carries `held_*` DIRECTLY, so a tool
//     missing from the inventory still shows its evidence;
//   - report link: the server's LATEST scan report route (`scanReportPath(job_id)`)
//     with repeatable `?signal=` params carrying the FULL RAW signal strings —
//     never the shortened TPA display labels;
//   - when the server payload carries NO `security_scan` field at all (the
//     backend omits it until a scan has run) there is no report to link to, so
//     the panel offers a Run-scan CTA instead of a dead link (FR-011).

const TPA_SIGNAL = 'tpa.TPA-2026-0001.hidden_instruction'

let serverExtra: Record<string, unknown>
let approvalTools: Array<Record<string, unknown>>
let inventoryTools: Array<Record<string, unknown>>
let diffPayloads: Record<string, Record<string, unknown>>
let scanReportPayload: Record<string, unknown> | null

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  const held = (source: Record<string, unknown>) => {
    const out: Record<string, unknown> = {}
    if (source.held_reason) out.held_reason = source.held_reason
    if (source.held_verdict) out.held_verdict = source.held_verdict
    if (source.held_signals) out.held_signals = source.held_signals
    return out
  }
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
      // Mirrors the production join in api.getToolApprovals: durable export
      // records enriched by tool name from the inventory payload.
      getToolApprovals: vi.fn(() =>
        ok({
          tools: approvalTools.map((record) => {
            const match = inventoryTools.find((t) => t.name === record.tool_name)
            return match ? { ...record, ...held(match) } : record
          }),
          count: approvalTools.length,
        })
      ),
      getToolDiff: vi.fn((_server: string, tool: string) => ok(diffPayloads[tool] ?? {})),
      getServerTools: vi.fn(() => ok({ tools: inventoryTools })),
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

const SCANNED_SERVER = {
  security_scan: {
    status: 'dangerous',
    risk_score: 82,
    last_scan_at: '2026-07-28T10:00:00Z',
    finding_counts: { dangerous: 2, warning: 1, info: 0, total: 3 },
    scanners_run: 1,
    scanners_failed: 0,
    scanners_total: 1,
  },
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
  serverExtra = { ...SCANNED_SERVER }
  approvalTools = []
  inventoryTools = []
  diffPayloads = {}
  scanReportPayload = { job_id: 'scan-github-1', risk_score: 82, findings: [] }
})

describe('ServerDetail — held-tool evidence on the quarantine panel (Spec 088 T016, FR-008/FR-009)', () => {
  beforeEach(() => {
    approvalTools = [{ tool_name: 'create_issue', status: 'pending', description: 'Create an issue' }]
    inventoryTools = [
      {
        name: 'create_issue',
        held_reason: 'scan_findings',
        held_verdict: 'dangerous',
        held_signals: ['phrase.injection', TPA_SIGNAL],
      },
    ]
  })

  it('renders the hold-evidence badge on the held tool row', async () => {
    const { wrapper } = await mountDetail()
    const badge = wrapper.find('[data-test="hold-evidence-badge"]')
    expect(badge.exists()).toBe(true)
    expect(badge.attributes('data-tool')).toBe('create_issue')
  })

  it('names the hold reason in plain language and shows the verdict', async () => {
    const { wrapper } = await mountDetail()
    expect(wrapper.find('[data-test="hold-reason-pill"]').text()).toMatch(/scan found threats/i)
    expect(wrapper.find('[data-test="hold-verdict-badge"]').text()).toContain('dangerous')
  })

  it('shows the matched TPA signature id (SC-002)', async () => {
    const { wrapper } = await mountDetail()
    const chips = wrapper.findAll('[data-test="hold-tpa-chip"]')
    expect(chips).toHaveLength(1)
    expect(chips[0].text()).toBe('TPA-2026-0001')
  })

  it('links to the latest scan report carrying the FULL raw signal strings (research D4)', async () => {
    const { wrapper } = await mountDetail()
    const link = wrapper.find('[data-test="hold-evidence-report-link"]')
    expect(link.exists()).toBe(true)
    const href = link.attributes('href') ?? ''
    expect(href).toContain('/security/scans/scan-github-1')
    expect(decodeURIComponent(href)).toContain(TPA_SIGNAL)
    // The shortened display label must never be what the query carries.
    expect(href).not.toContain('signal=TPA-2026-0001&')
  })

  it('renders a coverage hold as a precaution, never as a threat verdict', async () => {
    inventoryTools = [
      {
        name: 'create_issue',
        held_reason: 'scan_coverage',
        held_verdict: 'clean',
        held_signals: [],
      },
    ]
    const { wrapper } = await mountDetail()
    const pill = wrapper.find('[data-test="hold-reason-pill"]')
    expect(pill.text()).toMatch(/could not complete/i)
    expect(pill.classes()).not.toContain('badge-error')
    expect(wrapper.find('[data-test="hold-tpa-chip"]').exists()).toBe(false)
  })

  it('renders no evidence chrome for records that carry none (FR-012)', async () => {
    inventoryTools = [{ name: 'create_issue' }]
    const { wrapper } = await mountDetail()
    expect(wrapper.find('[data-test="hold-evidence-badge"]').exists()).toBe(false)
    // …and the row itself is untouched.
    expect(wrapper.find('[data-test="tool-quarantine-list"]').text()).toContain('create_issue')
  })

  it('never shows evidence for an approved (released) tool', async () => {
    approvalTools = [{ tool_name: 'create_issue', status: 'approved', description: 'Create an issue' }]
    const { wrapper } = await mountDetail()
    expect(wrapper.find('[data-test="hold-evidence-badge"]').exists()).toBe(false)
  })
})

describe('ServerDetail — held evidence on the changed-tool diff (Spec 088 T016, FR-010)', () => {
  beforeEach(() => {
    approvalTools = [
      {
        tool_name: 'create_issue',
        status: 'changed',
        description: 'after',
      },
    ]
    // Deliberately NO inventory evidence: the diff endpoint is the source here.
    inventoryTools = [{ name: 'create_issue' }]
    diffPayloads = {
      create_issue: {
        previous_description: 'before text',
        current_description: 'after text',
        held_reason: 'scan_findings',
        held_verdict: 'warnings',
        held_signals: [TPA_SIGNAL],
      },
    }
  })

  it('shows the diff payload evidence alongside the before/after sections', async () => {
    const { wrapper } = await mountDetail()
    expect(wrapper.find('[data-test="tool-diff"]').exists()).toBe(true)
    const badge = wrapper.find('[data-test="hold-evidence-badge"]')
    expect(badge.exists()).toBe(true)
    expect(badge.find('[data-test="hold-tpa-chip"]').text()).toBe('TPA-2026-0001')
    expect(badge.find('[data-test="hold-verdict-badge"]').text()).toContain('warnings')
  })
})

describe('ServerDetail — evidence without a scan report (Spec 088 T016, FR-011)', () => {
  beforeEach(() => {
    // The `security_scan` field is OMITTED entirely until a scan has run.
    serverExtra = {}
    scanReportPayload = null
    approvalTools = [{ tool_name: 'create_issue', status: 'pending', description: 'Create an issue' }]
    inventoryTools = [
      {
        name: 'create_issue',
        held_reason: 'scan_findings',
        held_verdict: 'dangerous',
        held_signals: [TPA_SIGNAL],
      },
    ]
  })

  it('still shows the evidence but offers no dead report link', async () => {
    const { wrapper } = await mountDetail()
    expect(wrapper.find('[data-test="hold-evidence-badge"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="hold-tpa-chip"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="hold-evidence-report-link"]').exists()).toBe(false)
  })

  it('offers running a scan instead, which starts one', async () => {
    const { wrapper, api } = await mountDetail()
    const cta = wrapper.find('[data-test="hold-evidence-run-scan"]')
    expect(cta.exists()).toBe(true)
    await cta.trigger('click')
    await flushPromises()
    expect(api.startScan).toHaveBeenCalledWith('github')
  })

  it('does not offer the scan CTA once the server has a scan summary', async () => {
    serverExtra = { ...SCANNED_SERVER }
    scanReportPayload = { job_id: 'scan-github-1', risk_score: 82, findings: [] }
    const { wrapper } = await mountDetail()
    expect(wrapper.find('[data-test="hold-evidence-run-scan"]').exists()).toBe(false)
  })
})
