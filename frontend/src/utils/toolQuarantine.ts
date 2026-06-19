import type { ToolApproval } from '@/types'

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
 * `auto_approve_tool_changes: true` (per-server) or `quarantine_enabled: false` (global).
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
 * non-quarantined while its tools stay pending and blocked, leaving the
 * operator no way to approve them.
 */
export function selectQuarantinedTools(
  toolApprovals: ToolApproval[],
  serverQuarantined: boolean,
): ToolApproval[] {
  if (serverQuarantined) return []
  return toolApprovals.filter((t) => t.status === 'changed' || t.status === 'pending')
}
