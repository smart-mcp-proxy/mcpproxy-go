import type { Server } from '@/types'
import { deriveTrustModeState, TRUST_MODES } from '@/utils/trustMode'

// Facts that used to live on the server card. They moved out of the Servers
// row (Spec 134 FR-020) and are shown on the first screen of server detail
// (ServerFacts.vue); the logic is unchanged from ServerCard.vue.

export function quarantineToolCount(server: Server): number {
  const q = server.quarantine
  if (!q) return 0
  return (q.pending_count ?? 0) + (q.changed_count ?? 0)
}

/** "3 tools pending approval", "2 changed tools — re-review needed", "1 pending, 2 changed". */
export function toolQuarantineSummary(server: Server): string {
  const q = server.quarantine
  if (!q) return ''
  const pending = q.pending_count ?? 0
  const changed = q.changed_count ?? 0
  if (pending + changed === 0) return ''
  const noun = (n: number) => (n === 1 ? 'tool' : 'tools')
  if (changed > 0 && pending > 0) return `${pending} pending, ${changed} changed`
  if (changed > 0) return `${changed} changed ${noun(changed)} — re-review needed`
  return `${pending} ${noun(pending)} pending approval`
}

export function securityBadgeText(server: Server): string {
  const scan = server.security_scan
  if (!scan) return ''
  const held = quarantineToolCount(server)
  if (scan.status === 'clean' && held > 0) return `Clean scan · ${held} tool${held !== 1 ? 's' : ''} held`
  switch (scan.status) {
    case 'clean': return 'Clean'
    case 'warnings': {
      const count = scan.finding_counts?.warning ?? 0
      return `${count} warning${count !== 1 ? 's' : ''}`
    }
    case 'dangerous': return 'Dangerous'
    case 'failed': return 'Scan failed'
    case 'scanning': return 'Scanning…'
    default: return ''
  }
}

/** #1065: while a server is quarantined the verdict is subordinated to the review ask. */
export function quarantineScanNote(server: Server): string {
  const scan = server.security_scan
  const status = scan?.status
  if (!scan || !status || status === 'not_scanned') return ''
  switch (status) {
    case 'scanning': return 'Scanning… — still needs review'
    case 'failed': return 'Last scan could not complete — still needs review'
    case 'clean': return 'Last scan: clean — still needs review'
    case 'warnings': {
      const n = scan.finding_counts?.warning ?? 0
      return `Last scan: ${n} warning${n !== 1 ? 's' : ''} — needs review`
    }
    case 'dangerous': return 'Last scan: dangerous findings — needs review'
    default: return ''
  }
}

export function securityLine(server: Server): string {
  return server.quarantined ? quarantineScanNote(server) : securityBadgeText(server)
}

/** GH #938: a clean scan with held tools reads amber, not green. */
export function securityTone(server: Server): string {
  if (server.quarantined) return 'text-warning'
  if (securityBadgeText(server) === '') return ''
  const scan = server.security_scan
  if (scan?.status === 'clean' && quarantineToolCount(server) > 0) return 'text-warning'
  switch (scan?.status) {
    case 'clean': return 'text-success'
    case 'warnings': return 'text-warning'
    case 'dangerous':
    case 'failed': return 'text-error'
    default: return 'text-base-content/60'
  }
}

export interface TrustBadge {
  label: string
  title: string
  tone: string
  invalid: boolean
  /** The mode's meaning (and, for an unrecognised value, the raw configured value) as plain text. */
  detail: string
}

export function trustBadge(server: Server): TrustBadge {
  const state = deriveTrustModeState(server.trust_mode)
  const meta = TRUST_MODES.find(m => m.mode === state.effective) ?? TRUST_MODES[TRUST_MODES.length - 1]
  let tone = 'text-base-content/50'
  if (state.isInvalid || state.effective === 'auto') tone = 'text-warning'
  else if (state.effective === 'scan') tone = 'text-info'
  const title = state.isInvalid
    ? `Trust mode: configured value "${state.raw}" is not recognized — using ${meta.label} (fail closed). ${meta.description}`
    : `${state.isDefault ? `Trust mode: ${meta.label} (default)` : `Trust mode: ${meta.label}`} — ${meta.description}`
  const detail = state.isInvalid
    ? `Configured value "${state.raw}" is not recognized; using ${meta.label} (fail closed). ${meta.description}`
    : `${state.isDefault ? 'Default. ' : ''}${meta.description}`
  return { label: meta.label, title, tone, invalid: state.isInvalid, detail }
}

export function lastCallText(iso: string | undefined, nowMs: number): string {
  if (!iso) return 'never'
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return 'never'
  const minutes = Math.floor((nowMs - then) / 60000)
  if (minutes < 1) return 'just now'
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

/** Activity for this server, last 24 h; errors only when there are any. */
export function serverActivityLink(name: string, errors24h: number): string {
  const base = `/activity?server=${encodeURIComponent(name)}&from=-24h`
  return errors24h > 0 ? `${base}&status=error` : base
}
