// Spec 134 FR-020 / FR-030 / FR-035: the facts the Servers list no longer
// shows are on the first screen of server detail. Pure functions so the
// wording and the links are testable. Nothing here invents data: every value
// is read from the server payload the page already has.
import type { Server } from '@/types'
import { deriveTrustModeState, TRUST_MODES } from '@/utils/trustMode'

export interface TrustFact { label: string; title: string; invalid: boolean }

/** Effective trust mode, with the tooltip text the card used (fail closed on an unknown value). */
export function trustFact(server: Pick<Server, 'trust_mode'>): TrustFact {
  const state = deriveTrustModeState(server.trust_mode)
  const meta = TRUST_MODES.find(m => m.mode === state.effective) ?? TRUST_MODES[TRUST_MODES.length - 1]
  if (state.isInvalid) {
    return { label: meta.label, invalid: true, title: `Trust mode: configured value "${state.raw}" is not recognized — using ${meta.label} (fail closed). ${meta.description}` }
  }
  const prefix = state.isDefault ? `Trust mode: ${meta.label} (default)` : `Trust mode: ${meta.label}`
  return { label: meta.label, invalid: false, title: `${prefix} — ${meta.description}` }
}

export interface HeldCounts { pending: number; changed: number; total: number; text: string }

export function heldCounts(server: Pick<Server, 'quarantine'>): HeldCounts {
  const q = server.quarantine
  const pending = q?.pending_count ?? 0
  const changed = q?.changed_count ?? 0
  const total = pending + changed
  let text = ''
  if (changed > 0 && pending > 0) text = `${pending} pending, ${changed} changed`
  else if (changed > 0) text = `${changed} changed ${changed === 1 ? 'tool' : 'tools'}, re-review needed`
  else if (pending > 0) text = `${pending} ${pending === 1 ? 'tool' : 'tools'} pending approval`
  return { pending, changed, total, text }
}

export interface ScanFact { text: string; tone: 'ok' | 'review' | 'error' | 'neutral'; warnings: number }

/** The scan verdict. A clean scan is never presented as approval: a quarantined server or held tools demote it. */
export function scanFact(server: Pick<Server, 'security_scan' | 'quarantined' | 'quarantine'>): ScanFact | null {
  const scan = server.security_scan
  if (!scan || !scan.status || scan.status === 'not_scanned') return null
  const held = heldCounts(server).total
  const warnings = scan.finding_counts?.warning ?? 0
  if (server.quarantined) {
    const note: Record<string, string> = {
      scanning: 'Scanning, still needs review',
      failed: 'Last scan could not complete, still needs review',
      clean: 'Last scan: clean, still needs review',
      warnings: `Last scan: ${warnings} warning${warnings !== 1 ? 's' : ''}, needs review`,
      dangerous: 'Last scan: dangerous findings, needs review',
    }
    return { text: note[scan.status] ?? '', tone: scan.status === 'dangerous' || scan.status === 'failed' ? 'error' : 'review', warnings }
  }
  switch (scan.status) {
    case 'clean':
      return held > 0
        ? { text: `Clean scan, ${held} tool${held !== 1 ? 's' : ''} held`, tone: 'review', warnings }
        : { text: 'Scan clean', tone: 'ok', warnings }
    case 'warnings': return { text: `${warnings} scan warning${warnings !== 1 ? 's' : ''}`, tone: 'review', warnings }
    case 'dangerous': return { text: 'Scan: dangerous findings', tone: 'error', warnings }
    case 'failed': return { text: 'Scan failed', tone: 'error', warnings }
    case 'scanning': return { text: 'Scanning…', tone: 'neutral', warnings }
    default: return null
  }
}

export interface DiagnosticText { label: string; value: string }

/** Everything the first screen's "Full diagnostic" control shows, untruncated and in a stable order. */
export function diagnosticSections(server: Server): DiagnosticText[] {
  const out: DiagnosticText[] = []
  const h = server.health
  if (h?.summary) out.push({ label: 'Summary', value: h.summary })
  if (h?.detail) out.push({ label: 'Detail', value: h.detail })
  if (h?.action) out.push({ label: 'Suggested action', value: h.action })
  const d = server.diagnostic
  if (d) {
    out.push({ label: 'Diagnostic code', value: d.code })
    if (d.cause) out.push({ label: 'Cause', value: d.cause })
    if (d.user_message) out.push({ label: 'Message', value: d.user_message })
    if (d.docs_url) out.push({ label: 'Docs', value: d.docs_url })
  }
  if (server.last_error) out.push({ label: 'Raw error', value: server.last_error })
  return out
}

export type AttentionTarget = 'held-tools' | 'scan' | 'health-action'
export interface AttentionLine { id: AttentionTarget; text: string }

/**
 * FR-035: things to act on that are below the first screen. One summary line
 * each; the page links every line to its target and reveals it.
 */
export function attentionLines(server: Server, opts: { primaryAction?: string; primaryLabel?: string } = {}): AttentionLine[] {
  const lines: AttentionLine[] = []
  const held = heldCounts(server)
  if (held.total > 0) lines.push({ id: 'held-tools', text: `${held.text || `${held.total} tools held`}. Review them` })
  const scan = scanFact(server)
  if (server.security_scan && (server.security_scan.status === 'dangerous' || server.security_scan.status === 'failed' || server.security_scan.status === 'warnings')) {
    lines.push({ id: 'scan', text: `${scan?.text ?? 'Scan needs a look'}. Open scan results` })
  }
  if (opts.primaryAction && opts.primaryLabel) lines.push({ id: 'health-action', text: 'How to fix this' })
  return lines
}
