/**
 * Quarantine-banner state derivation (spec 088 US3 — FR-013/FR-014/FR-015).
 *
 * A quarantined server can be in four very different situations, and today the
 * banner says the same generic thing about all of them. This module derives the
 * situation from the facts the server payload ALREADY carries — nothing else is
 * available to the page:
 *
 *   `trust_mode`     raw configured value, resolved fail-closed by
 *                    `effectiveTrustMode()` (mirror of the backend accessor);
 *   `quarantined`    the admin flag;
 *   `security_scan`  the latest scan summary — **absent from the payload
 *                    entirely when no scan has ever run** (`GetScanSummary`
 *                    returns nil server-side). Absence is the no-scan signal;
 *                    the legacy `not_scanned` status string is tolerated as an
 *                    alias but is never what the CTA keys on.
 *
 * Two facts are deliberately NOT derivable here and must never appear in the
 * copy (research D2, Codex R1-1/R2-1):
 *
 *   - admission-window provenance — whether a verdict came from the original
 *     admission scan or a later re-scan (`HasApprovalBaseline` is server-
 *     internal), so the banner never says "when it was added";
 *   - eligibility for automatic approval — the page cannot know whether this
 *     server would still be auto-approved, so no copy promises it.
 *
 * A `failed` scan is an INCOMPLETE scan, not a threat verdict: it is presented
 * as a fail-closed precaution with a retry, mirroring the `scan_coverage` hold
 * presentation in `holdEvidence.ts`.
 */

import { effectiveTrustMode } from '@/utils/trustMode'

/** The four situations the banner distinguishes (data-model derivation table). */
export type QuarantineBannerStateName = 'scan-running' | 'scan-failed' | 'scan-blocked' | 'manual-review'

/**
 * Actions the banner suggests. The component owns the labels, routes and
 * handlers; this module only says WHICH make sense for the situation.
 */
export type QuarantineBannerAction = 'approve' | 'view-report' | 'retry-scan' | 'run-scan'

/** Styling intent — `precaution` exists so a failed scan is never styled as a threat. */
export type QuarantineBannerTone = 'info' | 'precaution' | 'warning' | 'threat' | 'neutral'

/** The only scan-summary fields the derivation reads. */
export interface QuarantineBannerScanSummary {
  status?: string
}

/**
 * Structural input — every server projection in the app (generated
 * `contracts.Server` and the hand-written `api.ts` server type) satisfies it.
 */
export interface QuarantineBannerServer {
  trust_mode?: string
  quarantined?: boolean
  security_scan?: QuarantineBannerScanSummary
}

export interface QuarantineBannerState {
  state: QuarantineBannerStateName
  headline: string
  detail: string
  tone: QuarantineBannerTone
  /** Suggested next steps, most relevant first. May be empty (scan running). */
  actions: QuarantineBannerAction[]
}

/** Scan statuses that carry an actual verdict (i.e. a report worth opening). */
const VERDICT_STATUSES = ['clean', 'warnings', 'dangerous'] as const

/** Verdicts that keep a `scan`-mode server quarantined. */
const BLOCKING_VERDICTS: readonly string[] = ['dangerous', 'warnings']

const STATUS_SCANNING = 'scanning'
const STATUS_FAILED = 'failed'
/** Legacy/back-compat alias for "no scan has run" — absence is the real signal. */
const STATUS_NOT_SCANNED = 'not_scanned'

function scanStatus(summary: QuarantineBannerScanSummary | null | undefined): string {
  return summary?.status?.trim() ?? ''
}

/**
 * True when the payload carries a real scan summary. The field is omitted when
 * no scan has ever run; an empty status (or the `not_scanned` alias) is treated
 * the same way rather than rendered as a mystery state.
 */
function hasScanSummary(summary: QuarantineBannerScanSummary | null | undefined): boolean {
  if (summary === null || summary === undefined) return false
  const status = scanStatus(summary)
  return status !== '' && status !== STATUS_NOT_SCANNED
}

function hasVerdict(summary: QuarantineBannerScanSummary | null | undefined): boolean {
  return (VERDICT_STATUSES as readonly string[]).includes(scanStatus(summary))
}

/**
 * Derive the banner for a server, or null when it is not quarantined (no banner
 * at all). First match wins, in the documented priority order.
 */
export function deriveQuarantineBannerState(
  server: QuarantineBannerServer | null | undefined,
): QuarantineBannerState | null {
  if (!server?.quarantined) return null

  const mode = effectiveTrustMode(server.trust_mode)
  const summary = server.security_scan
  const status = scanStatus(summary)

  // 1. A scan is running under the scan gate — neutral, no action demanded and
  //    no promise about what the verdict will unlock.
  if (mode === 'scan' && status === STATUS_SCANNING) {
    return {
      state: 'scan-running',
      headline: 'Security scan in progress',
      detail:
        'A security scan is running for this quarantined server. Its result determines the next step — nothing changes until the scan settles.',
      tone: 'info',
      actions: [],
    }
  }

  // 2. The scan could not complete — a precaution in EVERY trust mode, never a
  //    threat verdict (no verdict was produced, so there is no report to open).
  if (status === STATUS_FAILED) {
    return {
      state: 'scan-failed',
      headline: 'Security scan could not complete',
      detail:
        'The scan did not finish, so this server stays quarantined as a precaution — this is not a threat verdict. Retry the scan, or review the server and approve it manually.',
      tone: 'precaution',
      actions: ['retry-scan', 'approve'],
    }
  }

  // 3. The latest verdict was non-clean under the scan gate.
  if (mode === 'scan' && BLOCKING_VERDICTS.includes(status)) {
    return {
      state: 'scan-blocked',
      headline: 'Scan verdict blocked automatic approval',
      detail:
        'The latest security scan returned a non-clean verdict, so this server was not approved automatically. Review the findings in the scan report before approving it.',
      tone: status === 'dangerous' ? 'threat' : 'warning',
      actions: ['view-report', 'approve'],
    }
  }

  // 4. Everything else: awaiting manual review. Offer the report when a verdict
  //    exists, and a scan when nothing has ever been scanned (FR-015).
  const actions: QuarantineBannerAction[] = ['approve']
  if (hasVerdict(summary)) {
    actions.push('view-report')
  } else if (!hasScanSummary(summary)) {
    actions.push('run-scan')
  }

  return {
    state: 'manual-review',
    headline: 'Awaiting manual review',
    detail: hasScanSummary(summary)
      ? 'This server stays quarantined until you review its tools and approve it. Check the latest security scan summary before approving.'
      : 'This server stays quarantined until you review its tools and approve it. No security scan has run yet — you can run one first to inform your decision.',
    tone: 'neutral',
    actions,
  }
}
