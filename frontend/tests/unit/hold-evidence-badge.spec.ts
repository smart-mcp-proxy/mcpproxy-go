import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory, type Router } from 'vue-router'
import HoldEvidenceBadge from '@/components/HoldEvidenceBadge.vue'
import { parseHoldEvidence } from '@/utils/holdEvidence'
import { scanReportPath } from '@/utils/serverRoute'

// Spec 088 T014 (FR-008/FR-009/FR-011/FR-012).
//
// HoldEvidenceBadge is the single presentation of spec 086's `held_*` evidence
// for every Web UI surface that lists or examines a held tool. The invariants
// under test are the ones an operator's decision hangs on:
//
//   * a coverage hold ("the scan could not run") is a PRECAUTION and must be
//     visually distinct from a findings hold ("the scan matched signatures") —
//     never dressed up as a threat verdict (FR-008);
//   * TPA-YYYY-NNNN signature ids come first and are NEVER collapsed away while
//     generic heuristic ids are still on screen; the overflow count reports only
//     received-but-collapsed signals (FR-009);
//   * the scan-report link carries the FULL RAW signal strings as repeatable
//     `?signal=` params — display labels shorten to the TPA id, the query never
//     does, or ScanReport.vue cannot intersect `findings[].signals` (research D4);
//   * a record with no hold evidence renders NOTHING — no empty chrome on
//     approved/released tools and on pre-086 records (FR-012).

const TPA_1 = 'tpa.TPA-2026-0001.hidden_instruction'
const TPA_2 = 'tpa.TPA-2026-0002.tool_shadowing'
const TPA_3 = 'tpa.TPA-2026-0003.exfiltration_channel'
const TPA_4 = 'tpa.TPA-2026-0004.sensitive_file_read'
const TPA_5 = 'tpa.TPA-2026-0005.instruction_override'

function makeRouter(): Router {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'home', component: { template: '<div/>' } },
      { path: '/security/scans/:jobId', name: 'scan-report', component: { template: '<div/>' } },
    ],
  })
}

function mountBadge(props: Record<string, unknown>) {
  return mount(HoldEvidenceBadge, {
    props,
    global: { plugins: [makeRouter()] },
  })
}

const badge = '[data-test="hold-evidence-badge"]'
const reasonPill = '[data-test="hold-reason-pill"]'
const verdictBadge = '[data-test="hold-verdict-badge"]'
const tpaChip = '[data-test="hold-tpa-chip"]'
const heuristicChip = '[data-test="hold-heuristic-chip"]'
const overflow = '[data-test="hold-signal-overflow"]'
const reportLink = '[data-test="hold-evidence-report-link"]'

describe('HoldEvidenceBadge — renders nothing without evidence (FR-012)', () => {
  it('renders nothing when the evidence prop is absent', () => {
    const wrapper = mountBadge({})
    expect(wrapper.find(badge).exists()).toBe(false)
    expect(wrapper.text()).toBe('')
  })

  it('renders nothing for a released/approved record (no held_* fields)', () => {
    const wrapper = mountBadge({
      evidence: { tool_name: 'create_issue', status: 'approved' },
      reportPath: scanReportPath('scan-github-1'),
    })
    expect(wrapper.find(badge).exists()).toBe(false)
    expect(wrapper.text()).toBe('')
    // Crucially: no dangling report link on a tool that is no longer held.
    expect(wrapper.find(reportLink).exists()).toBe(false)
  })

  it('renders nothing when held_reason is empty even if signals arrived', () => {
    const wrapper = mountBadge({
      evidence: { held_reason: '  ', held_verdict: 'warnings', held_signals: [TPA_1] },
    })
    expect(wrapper.find(badge).exists()).toBe(false)
    expect(wrapper.text()).toBe('')
  })
})

describe('HoldEvidenceBadge — reason pill distinguishes threat from precaution (FR-008)', () => {
  it('presents scan_findings as a threat finding', () => {
    const wrapper = mountBadge({
      evidence: { held_reason: 'scan_findings', held_verdict: 'dangerous', held_signals: [TPA_1] },
    })
    expect(wrapper.find(badge).exists()).toBe(true)
    const pill = wrapper.find(reasonPill)
    expect(pill.exists()).toBe(true)
    expect(pill.text()).toContain('Scan found threats')
  })

  it('presents scan_coverage as a precaution, never as a threat verdict', () => {
    const wrapper = mountBadge({
      evidence: { held_reason: 'scan_coverage', held_verdict: 'clean', held_signals: [] },
    })
    const pill = wrapper.find(reasonPill)
    expect(pill.exists()).toBe(true)
    expect(pill.text()).toContain('Scan could not complete')
    expect(pill.text().toLowerCase()).not.toContain('threat')
  })

  it('styles the two reasons distinctly (a precaution must not read as a finding)', () => {
    const findings = mountBadge({
      evidence: { held_reason: 'scan_findings', held_verdict: 'dangerous' },
    }).find(reasonPill)
    const coverage = mountBadge({
      evidence: { held_reason: 'scan_coverage', held_verdict: 'clean' },
    }).find(reasonPill)

    expect(findings.classes()).not.toEqual(coverage.classes())
    // The threat tone owns the error styling; the precaution must not borrow it.
    expect(findings.classes()).toContain('badge-error')
    expect(coverage.classes()).not.toContain('badge-error')
  })

  it('names an unknown reason from a newer backend without claiming a verdict', () => {
    const wrapper = mountBadge({ evidence: { held_reason: 'policy_hold' } })
    const pill = wrapper.find(reasonPill)
    expect(pill.text()).toContain('policy_hold')
    expect(pill.classes()).not.toContain('badge-error')
  })
})

describe('HoldEvidenceBadge — verdict badge (FR-009)', () => {
  it('renders the delivered verdict as received', () => {
    const wrapper = mountBadge({
      evidence: { held_reason: 'scan_findings', held_verdict: 'dangerous' },
    })
    const v = wrapper.find(verdictBadge)
    expect(v.exists()).toBe(true)
    expect(v.text()).toContain('dangerous')
  })

  it('omits the verdict badge when no verdict was delivered', () => {
    const wrapper = mountBadge({ evidence: { held_reason: 'scan_coverage' } })
    expect(wrapper.find(badge).exists()).toBe(true)
    expect(wrapper.find(verdictBadge).exists()).toBe(false)
  })
})

describe('HoldEvidenceBadge — signal chips (FR-009)', () => {
  it('orders TPA signatures before heuristics and labels them with the TPA id', () => {
    const wrapper = mountBadge({
      evidence: {
        held_reason: 'scan_findings',
        held_verdict: 'dangerous',
        // Producer order puts the generic heuristic first; display must not.
        held_signals: ['phrase.injection', TPA_1],
      },
    })

    const chips = wrapper.findAll('[data-test="hold-tpa-chip"], [data-test="hold-heuristic-chip"]')
    expect(chips).toHaveLength(2)
    expect(chips[0].attributes('data-test')).toBe('hold-tpa-chip')
    expect(chips[0].text()).toBe('TPA-2026-0001')
    // The full raw check id stays reachable as a tooltip.
    expect(chips[0].attributes('title')).toBe(TPA_1)
    expect(chips[1].attributes('data-test')).toBe('hold-heuristic-chip')
    expect(chips[1].text()).toBe('phrase.injection')
  })

  it('collapses heuristics beyond the display cap with a "+N more" indicator', () => {
    const wrapper = mountBadge({
      evidence: {
        held_reason: 'scan_findings',
        held_verdict: 'warnings',
        held_signals: [TPA_1, 'h.one', 'h.two', 'h.three', 'h.four', 'h.five'],
      },
    })

    expect(wrapper.findAll(tpaChip)).toHaveLength(1)
    // cap = 3 → one TPA chip + two heuristic chips, three heuristics collapsed.
    expect(wrapper.findAll(heuristicChip)).toHaveLength(2)
    expect(wrapper.find(overflow).text()).toContain('+3 more')
  })

  it('never collapses a TPA signature while heuristics are still shown', () => {
    const wrapper = mountBadge({
      evidence: {
        held_reason: 'scan_findings',
        held_verdict: 'dangerous',
        held_signals: [TPA_1, TPA_2, TPA_3, TPA_4, TPA_5, 'h.one', 'h.two'],
      },
    })

    expect(wrapper.findAll(tpaChip)).toHaveLength(5)
    expect(wrapper.findAll(heuristicChip)).toHaveLength(0)
    expect(wrapper.find(overflow).text()).toContain('+2 more')
  })

  it('shows no overflow indicator when everything delivered is visible', () => {
    const wrapper = mountBadge({
      evidence: { held_reason: 'scan_findings', held_signals: [TPA_1, 'h.one'] },
    })
    expect(wrapper.find(overflow).exists()).toBe(false)
  })

  it('renders no chip row when the hold delivered no signals (coverage hold)', () => {
    const wrapper = mountBadge({
      evidence: { held_reason: 'scan_coverage', held_verdict: 'clean', held_signals: [] },
    })
    expect(wrapper.find(badge).exists()).toBe(true)
    expect(wrapper.findAll(tpaChip)).toHaveLength(0)
    expect(wrapper.findAll(heuristicChip)).toHaveLength(0)
  })
})

describe('HoldEvidenceBadge — scan report link (FR-011, research D4)', () => {
  const signals = [TPA_1, 'phrase.injection', TPA_2]

  it('links to the report with repeatable ?signal= params carrying FULL raw ids', async () => {
    const router = makeRouter()
    const wrapper = mount(HoldEvidenceBadge, {
      props: {
        evidence: {
          held_reason: 'scan_findings',
          held_verdict: 'dangerous',
          held_signals: signals,
        },
        reportPath: scanReportPath('scan-github-1781284446323229000'),
      },
      global: { plugins: [router] },
    })

    const link = wrapper.find(reportLink)
    expect(link.exists()).toBe(true)

    const href = link.attributes('href') as string
    expect(href.startsWith('/security/scans/scan-github-1781284446323229000')).toBe(true)
    expect(href.match(/[?&]signal=/g)).toHaveLength(3)

    // Round-trip through the router: every delivered signal arrives at
    // ScanReport.vue in FULL, in delivered order — no shortened TPA labels.
    const query = router.resolve(href).query.signal
    expect(query).toEqual(signals)
    expect(href).not.toContain('signal=TPA-2026-0001&')
  })

  it('keeps a "/"-containing (percent-encoded) report path intact', () => {
    const router = makeRouter()
    const path = scanReportPath('scan-com.pulsemcp/google-flights-1781284446323229000')
    const wrapper = mount(HoldEvidenceBadge, {
      props: {
        evidence: { held_reason: 'scan_findings', held_signals: [TPA_1] },
        reportPath: path,
      },
      global: { plugins: [router] },
    })

    const href = wrapper.find(reportLink).attributes('href') as string
    const resolved = router.resolve(href)
    expect(resolved.name).toBe('scan-report')
    expect(resolved.params.jobId).toBe('scan-com.pulsemcp/google-flights-1781284446323229000')
    // vue-router collapses a single repeated param to a scalar; normalise.
    expect([resolved.query.signal].flat()).toEqual([TPA_1])
  })

  it('renders the link without query params when the hold delivered no signals', () => {
    const wrapper = mountBadge({
      evidence: { held_reason: 'scan_coverage', held_verdict: 'clean' },
      reportPath: scanReportPath('scan-github-1'),
    })
    const href = wrapper.find(reportLink).attributes('href') as string
    expect(href).toBe('/security/scans/scan-github-1')
  })

  it('renders NO link when no report path is available (caller owns the Run-scan CTA)', () => {
    const wrapper = mountBadge({
      evidence: { held_reason: 'scan_findings', held_signals: [TPA_1] },
    })
    expect(wrapper.find(badge).exists()).toBe(true)
    expect(wrapper.find(reportLink).exists()).toBe(false)
    // Signals are still fully visible without a report to link to.
    expect(wrapper.findAll(tpaChip)).toHaveLength(1)
  })

  it('renders NO link when reportPath is null/empty', () => {
    expect(
      mountBadge({
        evidence: { held_reason: 'scan_findings', held_signals: [TPA_1] },
        reportPath: null,
      }).find(reportLink).exists()
    ).toBe(false)
    expect(
      mountBadge({
        evidence: { held_reason: 'scan_findings', held_signals: [TPA_1] },
        reportPath: '',
      }).find(reportLink).exists()
    ).toBe(false)
  })
})

describe('HoldEvidenceBadge — accepts an already-parsed HoldEvidence', () => {
  it('renders identically whether given raw fields or a parsed evidence object', () => {
    const raw = {
      held_reason: 'scan_findings',
      held_verdict: 'dangerous',
      held_signals: [TPA_1, 'phrase.injection'],
    }
    const parsed = parseHoldEvidence(raw)
    expect(parsed).not.toBeNull()

    const fromParsed = mountBadge({ evidence: parsed })
    expect(fromParsed.find(badge).exists()).toBe(true)
    expect(fromParsed.find(reasonPill).text()).toContain('Scan found threats')
    expect(fromParsed.find(verdictBadge).text()).toContain('dangerous')
    expect(fromParsed.findAll(tpaChip)).toHaveLength(1)
    expect(fromParsed.findAll(heuristicChip)).toHaveLength(1)
    expect(fromParsed.text()).toBe(mountBadge({ evidence: raw }).text())
  })
})
