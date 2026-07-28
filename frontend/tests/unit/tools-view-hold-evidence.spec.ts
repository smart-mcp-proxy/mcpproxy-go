import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import Tools from '@/views/Tools.vue'
import api from '@/services/api'

// Spec 088 T017 (FR-008, FR-009, FR-012) — the global Tools page lists held
// tools from every server, so it is the surface an operator hits first after a
// scan-gated hold. `GET /api/v1/tools` already carries the spec-086 `held_*`
// fields (internal/httpapi/server.go), but the page dropped them on the floor.
//
// The row rendering stays COMPACT on purpose (one chip cluster in the Approval
// cell): the full evidence badge with descriptions and report links belongs to
// ServerDetail. What the row must still get right:
//   - a threat hold and a fail-closed coverage hold are visually distinct and
//     the coverage hold is never worded as a verdict (FR-008);
//   - TPA signature ids are never traded away for heuristic ones by the
//     compact cap, and the overflow count only counts delivered signals
//     (FR-009);
//   - released tools and pre-086 records render exactly as before (FR-012).

vi.mock('@/services/api', () => ({
  default: { getGlobalTools: vi.fn(), setToolEnabled: vi.fn() },
}))

const globalStubs = { CollapsibleHintsPanel: { template: '<div />' } }

function mountView() {
  return mount(Tools, { global: { plugins: [createPinia()], stubs: globalStubs } })
}

function mockTools(tools: Record<string, unknown>[]) {
  ;(api.getGlobalTools as any).mockResolvedValue({
    success: true,
    data: {
      stats: { total: tools.length, enabled: tools.length, disabled: 0, pending_approval: 0 },
      tools,
    },
  })
}

async function rowFor(wrapper: ReturnType<typeof mountView>, name: string) {
  const rows = wrapper.findAll('[data-test="tool-row"]')
  const row = rows.find(r => r.text().includes(name))
  expect(row, `row for ${name}`).toBeTruthy()
  return row!
}

describe('Tools view — compact hold evidence on held rows', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('renders the reason icon, the TPA id and an overflow count on a scan-findings hold', async () => {
    mockTools([
      {
        name: 'read_notes',
        server_name: 'notes',
        description: 'poisoned',
        approval_status: 'changed',
        disabled: false,
        config_denied: false,
        usage: 0,
        held_reason: 'scan_findings',
        held_verdict: 'dangerous',
        held_signals: [
          'phrase.injection',
          'tpa.TPA-2026-0001.hidden_instruction',
          'unicode.bidi_override',
        ],
      },
    ])

    const wrapper = mountView()
    await flushPromises()

    const row = await rowFor(wrapper, 'read_notes')
    const evidence = row.find('[data-test="tool-hold-evidence"]')
    expect(evidence.exists()).toBe(true)

    // TPA signature wins the single compact slot even though the heuristic
    // signal was delivered first.
    const signals = evidence.findAll('[data-test="tool-hold-signal"]')
    expect(signals.map(s => s.text())).toEqual(['TPA-2026-0001'])

    // Overflow counts only the two collapsed heuristic ids.
    expect(evidence.find('[data-test="tool-hold-signal-more"]').text()).toBe('+2')

    // Threat tone, and the accessible label names the reason.
    expect(evidence.classes().join(' ')).toContain('text-error')
    expect(evidence.text()).toContain('Scan found threats')
  })

  it('presents a coverage hold as a precaution, never as a threat verdict', async () => {
    mockTools([
      {
        name: 'list_files',
        server_name: 'fs',
        description: '',
        approval_status: 'pending',
        disabled: false,
        config_denied: false,
        usage: 0,
        held_reason: 'scan_coverage',
        held_verdict: 'clean',
        held_signals: [],
      },
    ])

    const wrapper = mountView()
    await flushPromises()

    const evidence = (await rowFor(wrapper, 'list_files')).find('[data-test="tool-hold-evidence"]')
    expect(evidence.exists()).toBe(true)
    expect(evidence.text()).toContain('Scan could not complete')
    expect(evidence.text()).not.toContain('Scan found threats')
    expect(evidence.classes().join(' ')).toContain('text-warning')
    expect(evidence.classes().join(' ')).not.toContain('text-error')
    // No signals delivered → no chips and no overflow claim.
    expect(evidence.findAll('[data-test="tool-hold-signal"]').length).toBe(0)
    expect(evidence.find('[data-test="tool-hold-signal-more"]').exists()).toBe(false)
  })

  it('never collapses a TPA id away while a heuristic id is still shown', async () => {
    mockTools([
      {
        name: 'exec',
        server_name: 'shell',
        description: '',
        approval_status: 'changed',
        disabled: false,
        config_denied: false,
        usage: 0,
        held_reason: 'scan_findings',
        held_verdict: 'dangerous',
        held_signals: [
          'phrase.injection',
          'tpa.TPA-2026-0001.hidden_instruction',
          'tpa.TPA-2026-0002.tool_shadowing',
        ],
      },
    ])

    const wrapper = mountView()
    await flushPromises()

    const evidence = (await rowFor(wrapper, 'exec')).find('[data-test="tool-hold-evidence"]')
    const labels = evidence.findAll('[data-test="tool-hold-signal"]').map(s => s.text())
    expect(labels).toEqual(['TPA-2026-0001', 'TPA-2026-0002'])
    expect(evidence.find('[data-test="tool-hold-signal-more"]').text()).toBe('+1')
  })

  it('shows no evidence chrome for released tools or records without hold evidence', async () => {
    mockTools([
      // Approved tool that still carries evidence from an earlier hold: stale,
      // must not be shown (FR-012).
      {
        name: 'approved_tool',
        server_name: 'notes',
        description: '',
        approval_status: 'approved',
        disabled: false,
        config_denied: false,
        usage: 0,
        held_reason: 'scan_findings',
        held_verdict: 'dangerous',
        held_signals: ['tpa.TPA-2026-0001.hidden_instruction'],
      },
      // Pre-086 held record: no evidence fields at all, renders unchanged.
      {
        name: 'legacy_pending',
        server_name: 'notes',
        description: '',
        approval_status: 'pending',
        disabled: false,
        config_denied: false,
        usage: 0,
      },
    ])

    const wrapper = mountView()
    await flushPromises()

    expect(
      (await rowFor(wrapper, 'approved_tool')).find('[data-test="tool-hold-evidence"]').exists(),
    ).toBe(false)
    expect(
      (await rowFor(wrapper, 'legacy_pending')).find('[data-test="tool-hold-evidence"]').exists(),
    ).toBe(false)
  })

  it('names an unknown hold reason without claiming a verdict', async () => {
    mockTools([
      {
        name: 'future_tool',
        server_name: 'notes',
        description: '',
        approval_status: 'pending',
        disabled: false,
        config_denied: false,
        usage: 0,
        held_reason: 'policy_review',
        held_verdict: '',
        held_signals: [],
      },
    ])

    const wrapper = mountView()
    await flushPromises()

    const evidence = (await rowFor(wrapper, 'future_tool')).find('[data-test="tool-hold-evidence"]')
    expect(evidence.exists()).toBe(true)
    expect(evidence.text()).toContain('policy_review')
    expect(evidence.text()).not.toContain('Scan found threats')
    expect(evidence.classes().join(' ')).not.toContain('text-error')
  })
})
