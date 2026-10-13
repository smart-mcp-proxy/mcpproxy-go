import { describe, it, expect } from 'vitest'
import {
  parseHoldEvidence,
  orderSignals,
  displaySignals,
  reasonPresentation,
  verdictPresentation,
} from '@/utils/holdEvidence'

// Spec 088 (T004, FR-008/FR-009/FR-012) — hold evidence surfacing.
//
// Spec 086 attaches scan-gate evidence to every held tool change:
//   held_reason  — "scan_findings" (the offline scan returned a non-clean
//                  verdict) vs "scan_coverage" (the scan could NOT be trusted,
//                  so the gate failed closed — a precaution, never a threat
//                  claim);
//   held_verdict — the baseline verdict at hold time (dangerous | warnings |
//                  clean; "clean" accompanies a coverage hold);
//   held_signals — the matched deterministic check ids, e.g.
//                  "tpa.TPA-2026-0001.hidden_instruction" or "phrase.injection".
//
// The stored signal list is a capped review hint (≤16, producer order, no
// overflow count — internal/storage/models.go). Everything below therefore
// operates on the DELIVERED list only: no display may claim knowledge beyond it.
// TPA-YYYY-NNNN ids name known attacks and must be ordered first and never be
// collapsed away while generic heuristic ids are still shown (CLI HELD-column
// parity, cmd/mcpproxy/tools_cmd.go formatToolHold).

const TPA_HIDDEN = 'tpa.TPA-2026-0001.hidden_instruction'
const TPA_SHADOW = 'tpa.TPA-2026-0001.tool_shadowing'
const TPA_EXFIL = 'tpa.TPA-2026-0042.exfiltration'

describe('parseHoldEvidence (spec 088 FR-008/FR-012)', () => {
  it('returns null when no hold reason is present (pre-086 records render unchanged)', () => {
    expect(parseHoldEvidence({})).toBeNull()
    expect(parseHoldEvidence({ held_reason: '' })).toBeNull()
    expect(parseHoldEvidence(null)).toBeNull()
    expect(parseHoldEvidence(undefined)).toBeNull()
  })

  it('returns null even when signals arrived without a reason (no evidence chrome)', () => {
    expect(parseHoldEvidence({ held_signals: [TPA_HIDDEN], held_verdict: 'dangerous' })).toBeNull()
  })

  it('marks a scan_findings hold as a threat', () => {
    const evidence = parseHoldEvidence({
      held_reason: 'scan_findings',
      held_verdict: 'dangerous',
      held_signals: [TPA_HIDDEN],
    })
    expect(evidence).not.toBeNull()
    expect(evidence!.reason).toBe('scan_findings')
    expect(evidence!.verdict).toBe('dangerous')
    expect(evidence!.isThreat).toBe(true)
    expect(evidence!.isPrecaution).toBe(false)
  })

  it('marks a scan_coverage + clean-verdict hold as a precaution, never a threat', () => {
    const evidence = parseHoldEvidence({
      held_reason: 'scan_coverage',
      held_verdict: 'clean',
      held_signals: [],
    })
    expect(evidence).not.toBeNull()
    expect(evidence!.isThreat).toBe(false)
    expect(evidence!.isPrecaution).toBe(true)
    expect(evidence!.signals).toEqual([])
  })

  it('does not fabricate a threat claim for an unrecognized reason', () => {
    const evidence = parseHoldEvidence({ held_reason: 'future_reason' })
    expect(evidence).not.toBeNull()
    expect(evidence!.isThreat).toBe(false)
    expect(evidence!.isPrecaution).toBe(false)
  })

  it('keeps the full raw signal strings (report links need them) while deduping and dropping blanks', () => {
    const evidence = parseHoldEvidence({
      held_reason: 'scan_findings',
      held_verdict: 'warnings',
      held_signals: ['phrase.injection', '', 'phrase.injection', TPA_HIDDEN],
    })
    expect(evidence!.signals).toEqual(['phrase.injection', TPA_HIDDEN])
  })

  it('exposes TPA ids ahead of heuristics with the extracted id as the display label', () => {
    const evidence = parseHoldEvidence({
      held_reason: 'scan_findings',
      held_verdict: 'dangerous',
      // Producer order puts heuristics first — display order must not.
      held_signals: ['directive.imperative', TPA_HIDDEN, 'capability.mismatch'],
    })
    expect(evidence!.tpa).toEqual([{ id: 'TPA-2026-0001', raw: TPA_HIDDEN }])
    expect(evidence!.heuristics).toEqual(['directive.imperative', 'capability.mismatch'])
  })
})

describe('orderSignals (spec 088 FR-009)', () => {
  it('extracts the TPA id from a tpa.<ID>.<check> signal and keeps the raw string', () => {
    const { tpa, heuristics } = orderSignals([TPA_EXFIL])
    expect(tpa).toEqual([{ id: 'TPA-2026-0042', raw: TPA_EXFIL }])
    expect(heuristics).toEqual([])
  })

  it('orders every TPA id before every heuristic id, preserving producer order within each group', () => {
    const { tpa, heuristics } = orderSignals([
      'phrase.injection',
      TPA_EXFIL,
      'directive.imperative',
      TPA_HIDDEN,
    ])
    expect(tpa.map((t) => t.id)).toEqual(['TPA-2026-0042', 'TPA-2026-0001'])
    expect(heuristics).toEqual(['phrase.injection', 'directive.imperative'])
  })

  it('dedupes a TPA id matched by several checks, keeping the first raw signal', () => {
    const { tpa } = orderSignals([TPA_HIDDEN, TPA_SHADOW])
    expect(tpa).toEqual([{ id: 'TPA-2026-0001', raw: TPA_HIDDEN }])
  })

  it('dedupes repeated heuristic ids', () => {
    const { heuristics } = orderSignals(['phrase.injection', 'phrase.injection'])
    expect(heuristics).toEqual(['phrase.injection'])
  })

  it('treats anything not matching /^tpa\\.(TPA-\\d{4}-\\d{4})\\./ as a heuristic id', () => {
    const { tpa, heuristics } = orderSignals([
      'TPA-2026-0001', // bare id, no tpa. prefix
      'tpa.TPA-2026-0001', // no trailing check segment
      'phrase.tpa.TPA-2026-0001.x', // not anchored at the start
      'tpa.TPA-26-1.hidden', // malformed id
    ])
    expect(tpa).toEqual([])
    expect(heuristics).toEqual([
      'TPA-2026-0001',
      'tpa.TPA-2026-0001',
      'phrase.tpa.TPA-2026-0001.x',
      'tpa.TPA-26-1.hidden',
    ])
  })

  it('ignores blank entries and handles an empty list', () => {
    expect(orderSignals([])).toEqual({ tpa: [], heuristics: [] })
    expect(orderSignals(['', '  '])).toEqual({ tpa: [], heuristics: [] })
  })
})

describe('displaySignals (spec 088 FR-009 — cap collapses heuristics only)', () => {
  function evidenceWith(signals: string[]) {
    return parseHoldEvidence({
      held_reason: 'scan_findings',
      held_verdict: 'dangerous',
      held_signals: signals,
    })
  }

  it('shows every signal and collapses nothing when the cap is not exceeded', () => {
    const result = displaySignals(evidenceWith([TPA_HIDDEN, 'phrase.injection']), 3)
    expect(result.visible.map((s) => s.label)).toEqual(['TPA-2026-0001', 'phrase.injection'])
    expect(result.collapsedCount).toBe(0)
  })

  it('collapses only heuristics once the cap is reached', () => {
    const result = displaySignals(
      evidenceWith([TPA_HIDDEN, 'phrase.injection', 'directive.imperative', 'capability.mismatch']),
      3,
    )
    expect(result.visible.map((s) => s.label)).toEqual([
      'TPA-2026-0001',
      'phrase.injection',
      'directive.imperative',
    ])
    expect(result.collapsedCount).toBe(1)
  })

  it('never drops a TPA id while heuristic ids are shown — TPA ids survive the cap entirely', () => {
    const result = displaySignals(
      evidenceWith([TPA_HIDDEN, TPA_EXFIL, 'phrase.injection', 'directive.imperative']),
      2,
    )
    const labels = result.visible.map((s) => s.label)
    expect(labels).toEqual(['TPA-2026-0001', 'TPA-2026-0042'])
    expect(labels).not.toContain('phrase.injection')
    // Both heuristics collapsed; no TPA id was traded away for one.
    expect(result.collapsedCount).toBe(2)
  })

  it('counts only received-but-collapsed signals — never beyond the delivered list', () => {
    const result = displaySignals(evidenceWith([TPA_HIDDEN, 'phrase.injection']), 1)
    expect(result.collapsedCount).toBe(1)
    expect(result.visible.length + result.collapsedCount).toBe(2)
  })

  it('labels TPA chips with the extracted id but keeps the full raw signal for report links', () => {
    const result = displaySignals(evidenceWith([TPA_HIDDEN, 'phrase.injection']), 5)
    expect(result.visible[0]).toEqual({ kind: 'tpa', label: 'TPA-2026-0001', raw: TPA_HIDDEN })
    expect(result.visible[1]).toEqual({
      kind: 'heuristic',
      label: 'phrase.injection',
      raw: 'phrase.injection',
    })
  })

  it('returns an empty display for null evidence or an empty signal list', () => {
    expect(displaySignals(null, 3)).toEqual({ visible: [], collapsedCount: 0 })
    expect(
      displaySignals(parseHoldEvidence({ held_reason: 'scan_coverage', held_verdict: 'clean' }), 3),
    ).toEqual({ visible: [], collapsedCount: 0 })
  })
})

describe('presentation helpers (spec 088 FR-008)', () => {
  it('presents a scan_findings hold as a threat', () => {
    const p = reasonPresentation(parseHoldEvidence({ held_reason: 'scan_findings' }))
    expect(p).not.toBeNull()
    expect(p!.tone).toBe('threat')
    expect(p!.label.toLowerCase()).toContain('threat')
  })

  it('presents a scan_coverage hold as an incomplete scan held as a precaution', () => {
    const p = reasonPresentation(parseHoldEvidence({ held_reason: 'scan_coverage' }))
    expect(p).not.toBeNull()
    expect(p!.tone).toBe('precaution')
    expect(p!.label.toLowerCase()).toContain('could not complete')
    expect(p!.label.toLowerCase()).not.toContain('threat')
    expect(p!.description.toLowerCase()).toContain('precaution')
  })

  it('falls back to neutral copy for an unrecognized reason and null for no evidence', () => {
    const p = reasonPresentation(parseHoldEvidence({ held_reason: 'future_reason' }))
    expect(p!.tone).toBe('neutral')
    expect(reasonPresentation(null)).toBeNull()
  })

  it('maps verdict severity onto badge tones', () => {
    expect(verdictPresentation('dangerous')).toEqual({ label: 'dangerous', tone: 'danger' })
    expect(verdictPresentation('warnings')).toEqual({ label: 'warnings', tone: 'warning' })
    expect(verdictPresentation('clean')).toEqual({ label: 'clean', tone: 'neutral' })
    expect(verdictPresentation('')).toBeNull()
    expect(verdictPresentation(undefined)).toBeNull()
  })
})
