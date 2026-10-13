import type { ToolApproval } from '@/types'
import {
  TierDestructive,
  TierRead,
  TierUnannotated,
  TierUnknown,
  TierWrite,
  type Tier,
} from '@/types/contracts'

/**
 * Selects the tools that warrant the per-server Tool-Quarantine banner / list
 * (Spec 032, parent MCP-2916, MCP-2917).
 *
 * On a live, NON-quarantined server a `pending` (new, never-approved) tool is
 * genuinely blocked by the backend (`checkToolApprovals` → `BlockedTools`) and
 * the Servers page already counts it (`pending_count + changed_count`). The
 * banner must therefore surface both `pending` and `changed` tools so the
 * operator can approve them; banner and count must agree. Pending tools come
 * from tool-level quarantine and can be auto-approved by setting
 * `skip_quarantine: true` (per-server) or `quarantine_enabled: false` (global).
 *
 * Rules:
 *  - While the server is quarantined, suppress the tool banner entirely. The
 *    server-level Security Quarantine banner already covers it and the operator
 *    must approve the server first — never show two banners at once.
 *  - Otherwise surface every tool that is `pending` (awaiting first approval) or
 *    `changed` (a rug-pull), since both are blocked until the operator acts.
 *
 * Note: this intentionally reverses the MCP-2101 "don't nag on a pending
 * baseline" behavior for non-quarantined servers. That trust model assumed
 * approving the server would promote pending→approved, but a server can be
 * non-quarantined (e.g. `skip_quarantine`) while its tools stay pending and
 * blocked, leaving the operator no way to approve them.
 */
export function selectQuarantinedTools(
  toolApprovals: ToolApproval[],
  serverQuarantined: boolean,
): ToolApproval[] {
  if (serverQuarantined) return []
  return toolApprovals.filter((t) => t.status === 'changed' || t.status === 'pending')
}

/**
 * Spec 109 FR-027: the one tool review-state vocabulary. Web, macOS and the CLI
 * all name the raw `approval_status` values the same way; the raw value stays the
 * wire/URL/filter value and is never rendered.
 */
export const TOOL_APPROVAL_LABELS: Record<string, string> = {
  approved: 'Approved',
  pending: 'New, needs review',
  changed: 'Changed, needs review',
}

/** Label for a raw approval status; an unknown value is shown as-is. */
export function toolApprovalLabel(status: string): string {
  return TOOL_APPROVAL_LABELS[status] ?? status
}

/**
 * Spec 109 FR-028/FR-090: the one tool tier vocabulary, keyed by the generated
 * Tier enum. The tier itself is computed by contracts.AnnotationTier; this is
 * display only. macOS (ToolLabels.tierLabel) is pinned to the same words.
 */
export const TIER_LABELS: Record<Tier, string> = {
  [TierRead]: 'Read',
  [TierWrite]: 'Write',
  [TierDestructive]: 'Destructive',
  [TierUnannotated]: 'Unannotated',
  [TierUnknown]: 'Unknown',
}

/** Label for a raw tier; a missing tier reads Unannotated, an unknown value is shown as-is. */
export function tierLabel(tier?: string): string {
  if (!tier) return TIER_LABELS[TierUnannotated]
  return TIER_LABELS[tier as Tier] ?? tier
}
