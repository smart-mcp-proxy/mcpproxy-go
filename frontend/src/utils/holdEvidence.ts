// Hold evidence for scan-gated tool changes (spec 088 FR-008/FR-009/FR-012,
// closing spec 086 FR-018/SC-006 for the Web UI).
//
// Spec 086 attaches three fields to every tool the `trust_mode: scan` gate
// refuses to auto-approve:
//   held_reason  — "scan_findings" (the offline TPA scan returned a non-clean
//                  verdict) or "scan_coverage" (the scan itself could not be
//                  trusted, so the gate failed closed). A coverage hold is a
//                  PRECAUTION and must never be presented as a threat verdict.
//   held_verdict — the baseline verdict at hold time (dangerous | warnings |
//                  clean — "clean" accompanies a coverage hold).
//   held_signals — matched deterministic check ids, e.g.
//                  "tpa.TPA-2026-0001.hidden_instruction" or "phrase.injection".
//
// The stored signal list is a capped review hint: ≤16 ids in producer (first-
// seen) order, with NO overflow count and NO scan-report/finding identifier
// (internal/storage/models.go MaxToolHeldSignals). Every helper here therefore
// operates on the delivered list only and never implies knowledge beyond it —
// an overflow count reports received-but-collapsed items, nothing more.
//
// TPA-YYYY-NNNN ids name known attack signatures; the scanner emits its generic
// heuristics ahead of them, so the display re-orders TPA ids first and never
// collapses one away while heuristic ids are still shown (parity with the CLI
// HELD column, cmd/mcpproxy/tools_cmd.go formatToolHold).

import type { ToolApproval } from '@/types'

/** Backend hold-reason constants (internal/storage/models.go). */
export const HOLD_REASON_SCAN_FINDINGS = 'scan_findings'
export const HOLD_REASON_SCAN_COVERAGE = 'scan_coverage'

/** The raw payload fields carrying hold evidence, on any surface that has them. */
export interface HoldEvidenceFields {
  held_reason?: string
  held_verdict?: string
  held_signals?: string[]
}

/** A matched TPA signature: `id` is the display label, `raw` the full check id. */
export interface TpaSignal {
  /** Extracted signature id, e.g. "TPA-2026-0001" — what the operator reads. */
  id: string
  /** Full signal string, e.g. "tpa.TPA-2026-0001.hidden_instruction" — what
   *  report links must carry so ScanReport can intersect it exactly. */
  raw: string
}

export interface HoldEvidence {
  /** Raw held_reason as delivered (one of the constants above, or unknown). */
  reason: string
  /** Raw held_verdict as delivered; '' when absent. */
  verdict: string
  /** Delivered signal list, blanks dropped and exact duplicates removed. Full
   *  raw strings — report links use these, never the shortened TPA labels. */
  signals: string[]
  /** Matched TPA signatures, deduped by id, in delivered order. */
  tpa: TpaSignal[]
  /** Remaining (heuristic) check ids, deduped, in delivered order. */
  heuristics: string[]
  /** The scan found something: style as a threat. */
  isThreat: boolean
  /** The scan could not complete: style as a precaution, never a threat. */
  isPrecaution: boolean
}

/**
 * A deterministic check id belongs to the TPA signature bundle when it is
 * namespaced `tpa.<TPA-YYYY-NNNN>.<check>`. Anchored on purpose: a bare id or a
 * nested mention must not be promoted to a known-attack claim.
 */
const TPA_SIGNAL_RE = /^tpa\.(TPA-\d{4}-\d{4})\./

/** Trimmed, blank-free, exact-duplicate-free view of a delivered signal list. */
function cleanSignals(signals: string[] | undefined | null): string[] {
  const out: string[] = []
  const seen = new Set<string>()
  for (const raw of signals ?? []) {
    if (typeof raw !== 'string') continue
    const signal = raw.trim()
    if (!signal || seen.has(signal)) continue
    seen.add(signal)
    out.push(signal)
  }
  return out
}

/**
 * Split a delivered signal list into TPA signatures (first) and heuristic ids,
 * deduping each group and preserving delivered order within it. TPA signatures
 * dedupe by signature id, keeping the first raw signal that matched it.
 */
export function orderSignals(signals: string[] | undefined | null): {
  tpa: TpaSignal[]
  heuristics: string[]
} {
  const tpa: TpaSignal[] = []
  const heuristics: string[] = []
  const seen = new Set<string>()

  for (const raw of signals ?? []) {
    if (typeof raw !== 'string') continue
    const signal = raw.trim()
    if (!signal) continue

    const match = TPA_SIGNAL_RE.exec(signal)
    const key = match ? match[1] : signal
    if (seen.has(key)) continue
    seen.add(key)

    if (match) {
      tpa.push({ id: match[1], raw: signal })
    } else {
      heuristics.push(signal)
    }
  }

  return { tpa, heuristics }
}

/**
 * Build the view model for a held tool's evidence, or null when the payload
 * carries none. Records predating spec 086 (and tools held for a non-scan
 * reason) have an empty held_reason and must render exactly as before — no
 * empty evidence chrome — hence the null.
 */
export function parseHoldEvidence(
  source: HoldEvidenceFields | null | undefined,
): HoldEvidence | null {
  const reason = source?.held_reason?.trim() ?? ''
  if (!reason) return null

  const { tpa, heuristics } = orderSignals(source?.held_signals)

  return {
    reason,
    // Delivered order (not display order) and full raw strings — these are what
    // report links pass through as repeatable ?signal= params.
    signals: cleanSignals(source?.held_signals),
    verdict: source?.held_verdict?.trim() ?? '',
    tpa,
    heuristics,
    isThreat: reason === HOLD_REASON_SCAN_FINDINGS,
    isPrecaution: reason === HOLD_REASON_SCAN_COVERAGE,
  }
}

export interface DisplaySignal {
  kind: 'tpa' | 'heuristic'
  /** What the chip shows: the TPA id, or the raw heuristic check id. */
  label: string
  /** Full raw signal string, for report links. */
  raw: string
}

export interface DisplaySignals {
  visible: DisplaySignal[]
  /** How many DELIVERED signals were collapsed — never a claim beyond the list. */
  collapsedCount: number
}

/** Default number of signal chips rendered before collapsing the rest. */
export const DEFAULT_SIGNAL_DISPLAY_CAP = 3

/**
 * Order and cap the signal chips for display. TPA ids are always shown in full:
 * the cap collapses heuristic ids only, so a known-attack signature is never
 * traded away for a generic one (FR-009). The overflow count covers exactly the
 * heuristics that were collapsed.
 */
export function displaySignals(
  evidence: HoldEvidence | null | undefined,
  cap: number = DEFAULT_SIGNAL_DISPLAY_CAP,
): DisplaySignals {
  if (!evidence) return { visible: [], collapsedCount: 0 }

  const visible: DisplaySignal[] = evidence.tpa.map((t) => ({
    kind: 'tpa' as const,
    label: t.id,
    raw: t.raw,
  }))

  const heuristicSlots = Math.max(0, cap - visible.length)
  for (const raw of evidence.heuristics.slice(0, heuristicSlots)) {
    visible.push({ kind: 'heuristic', label: raw, raw })
  }

  return {
    visible,
    collapsedCount: Math.max(0, evidence.heuristics.length - heuristicSlots),
  }
}

export interface ReasonPresentation {
  label: string
  /** Drives styling: a real finding vs a fail-closed precaution. */
  tone: 'threat' | 'precaution' | 'neutral'
  description: string
}

/** Plain-language presentation of the hold reason, or null when no evidence. */
export function reasonPresentation(
  evidence: HoldEvidence | null | undefined,
): ReasonPresentation | null {
  if (!evidence) return null

  if (evidence.isThreat) {
    return {
      label: 'Scan found threats',
      tone: 'threat',
      description:
        'The offline security scan returned a non-clean verdict for this tool definition. Review the matched signatures before approving.',
    }
  }

  if (evidence.isPrecaution) {
    return {
      label: 'Scan could not complete',
      tone: 'precaution',
      description:
        'The security scan could not be completed, so this change was held as a precaution. This is not a threat verdict — retry the scan or review the change manually.',
    }
  }

  // Unknown reason from a newer backend: name it, claim nothing.
  return {
    label: `Held for review (${evidence.reason})`,
    tone: 'neutral',
    description: 'This tool change is held for review by the approval gate.',
  }
}

export interface VerdictPresentation {
  label: string
  tone: 'danger' | 'warning' | 'neutral'
}

/** Badge presentation for the scan verdict, or null when none was delivered. */
export function verdictPresentation(
  verdict: string | undefined | null,
): VerdictPresentation | null {
  const value = verdict?.trim() ?? ''
  if (!value) return null

  switch (value) {
    case 'dangerous':
      return { label: value, tone: 'danger' }
    case 'warnings':
      return { label: value, tone: 'warning' }
    default:
      return { label: value, tone: 'neutral' }
  }
}

/**
 * Enrichment source shape: an entry of the inventory payload returned by
 * GET /api/v1/servers/{id}/tools — the only tool-LIST surface carrying hold
 * evidence (the durable export payload deliberately omits it,
 * internal/httpapi/server.go).
 */
export interface HoldEvidenceSource extends HoldEvidenceFields {
  name: string
}

function hasEvidence(source: HoldEvidenceFields): boolean {
  return !!(source.held_reason || source.held_verdict || source.held_signals?.length)
}

/**
 * Join hold evidence onto durable approval records (spec 088 T006, research D1).
 *
 * The approvals panel reads its records from `GET .../tools/export` because they
 * are durable — pending/blocked tools survive a disconnected server and an empty
 * index — but that payload carries no `held_*` fields. The inventory endpoint
 * has the evidence yet can omit whole tools, so it serves ONLY as best-effort
 * enrichment: every export record survives the join, and a failed enrichment
 * fetch (null/undefined list) returns the records untouched rather than dropping
 * any durable record.
 *
 * The two payloads name the tool differently (`tool_name` vs `name`); the join
 * bridges that. Records without a join partner (or whose partner carries no
 * evidence — e.g. an approved tool) are returned as-is, so surfaces render no
 * empty evidence chrome and never show stale evidence on a released tool.
 */
export function joinHoldEvidence<T extends ToolApproval>(
  records: T[] | null | undefined,
  enrichment: HoldEvidenceSource[] | null | undefined,
): T[] {
  if (!records) return []
  if (!enrichment || enrichment.length === 0) return records

  const byName = new Map<string, HoldEvidenceSource>()
  for (const tool of enrichment) {
    if (!tool?.name || byName.has(tool.name)) continue
    byName.set(tool.name, tool)
  }
  if (byName.size === 0) return records

  return records.map((record) => {
    const match = record?.tool_name ? byName.get(record.tool_name) : undefined
    if (!match || !hasEvidence(match)) return record

    const joined: T = { ...record }
    if (match.held_reason) joined.held_reason = match.held_reason
    if (match.held_verdict) joined.held_verdict = match.held_verdict
    if (match.held_signals?.length) joined.held_signals = [...match.held_signals]
    return joined
  })
}
