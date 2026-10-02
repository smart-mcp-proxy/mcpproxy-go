import type { ReviewScan, ReviewTool, ServerReviewResponse } from '@/types'

// Pure presentation rules for the review screen. The strings are the shared
// vocabulary of Spec 109 (fix-review-screen): the macOS app (ReviewPresentation
// in ReviewQueueView.swift) and `mcpproxy review show` use the same sentences.

export type ScanBannerSeverity = 'error' | 'warning' | 'success' | 'info'
export type ScanBannerAction = 'rescan' | 'scan-now' | 'none'

export interface ScanBanner {
  severity: ScanBannerSeverity
  text: string
  action: ScanBannerAction
}

function plural(count: number, one: string, many: string): string {
  return count === 1 ? one : many
}

/**
 * The scan line of the review screen. Returns null when the payload carries no
 * scan at all. The risk score is shown only for a scan that covers every
 * captured definition as it is now (`coverage: current`); a missing coverage
 * (an older core) reads as `none`.
 */
export function scanBanner(scan: ReviewScan | undefined | null, definitionsCaptured: boolean): ScanBanner | null {
  if (!scan) return null
  const coverage = definitionsCaptured ? (scan.coverage || 'none') : 'not_captured'
  switch (coverage) {
    case 'current': {
      const severity: ScanBannerSeverity = scan.verdict === 'dangerous' ? 'error' : scan.verdict === 'clean' ? 'success' : 'warning'
      const tools = scan.tools_scanned ?? 0
      return {
        severity,
        text: `Baseline scan: ${scan.verdict} · risk ${scan.risk_score ?? 0}/100 · covers all ${tools} ${plural(tools, 'tool', 'tools')}`,
        action: 'none',
      }
    }
    case 'stale': {
      const names = scan.unscanned_tools ?? []
      const count = names.length
      const what = count === 1 ? '1 tool definition changed or was added' : `${count} tool definitions changed or were added`
      const list = count > 0 ? ` (${names.join(', ')})` : ''
      return { severity: 'warning', text: `Scan out of date: ${what} after the last scan${list}. Last result: ${scan.verdict}.`, action: 'rescan' }
    }
    case 'not_captured':
      return { severity: 'warning', text: 'Scan not checked against tool definitions: they have not been captured yet.', action: 'none' }
    case 'tools_not_scanned':
      return { severity: 'warning', text: 'The last scan did not analyse tool definitions (0 exported).', action: 'rescan' }
    case 'scanning':
      return { severity: 'info', text: 'Scan in progress…', action: 'none' }
    default:
      return { severity: 'warning', text: 'Not scanned yet.', action: 'scan-now' }
  }
}

export interface ReviewHeadline {
  state: 'review' | 'approved'
  title: string
  subtitle: string
}

const needsReview = (tool: ReviewTool) => tool.approval_status === 'pending' || tool.approval_status === 'changed'

/** Heading and subtitle: a server that is not quarantined and has nothing pending reads as approved. */
export function reviewHeadline(review: ServerReviewResponse): ReviewHeadline {
  const name = review.server.name
  if (review.server.quarantined) {
    return { state: 'review', title: `Review ${name}`, subtitle: 'Review tool definitions before changing what agents can call.' }
  }
  const tools = review.tools
  const pending = tools.filter(needsReview).length
  if (pending > 0) {
    return {
      state: 'review',
      title: `Review ${name}`,
      subtitle: `${pending} ${plural(pending, 'tool needs', 'tools need')} review. Agents cannot call ${plural(pending, 'it', 'them')} until approved.`,
    }
  }
  if (tools.length === 0) {
    return { state: 'review', title: `Review ${name}`, subtitle: 'Review tool definitions before changing what agents can call.' }
  }
  const blocked = tools.filter(t => t.disabled).length
  const summary = `All ${tools.length} ${plural(tools.length, 'tool', 'tools')} approved${blocked > 0 ? ` (${blocked} blocked)` : ''}.`
  return { state: 'approved', title: `${name} is approved`, subtitle: `${summary} New or changed tools come back here for review.` }
}

export type ToolControl = 'allow-toggle' | 'approve-reject' | 'approved' | 'blocked'

/** Which control a tool row gets: the quarantine checkbox, Approve/Reject, or a plain state. */
export function toolState(tool: ReviewTool, quarantined: boolean): ToolControl {
  if (quarantined) return 'allow-toggle'
  if (tool.approval_status === 'approved') return tool.disabled ? 'blocked' : 'approved'
  return 'approve-reject'
}
