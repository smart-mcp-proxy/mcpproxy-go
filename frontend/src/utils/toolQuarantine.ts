import type { ToolApproval } from '@/types'

/**
 * Selects the tools that warrant the per-server Tool-Quarantine banner / list
 * (Spec 032, parent MCP-2081, MCP-2101).
 *
 * Trust model (confirmed on MCP-2081): when an operator approves a *server*
 * (lifting the server-level Security Quarantine), the backend promotes that
 * server's baseline `pending` tools to `approved`. A freshly-`pending` baseline
 * tool is therefore NOT a reason to nag the operator with a tool-level banner —
 * only a `changed` tool (a rug-pull) is.
 *
 * Rules:
 *  - While the server is quarantined, suppress the tool banner entirely. The
 *    server-level Security Quarantine banner already covers it and the operator
 *    must approve the server first — never show two banners at once.
 *  - Otherwise the banner keys off `status === 'changed'`. Only once a change
 *    has surfaced do we also include any residual `pending` tools so the
 *    operator can clear them in the same approval pass.
 */
export function selectQuarantinedTools(
  toolApprovals: ToolApproval[],
  serverQuarantined: boolean,
): ToolApproval[] {
  if (serverQuarantined) return []
  const hasChanged = toolApprovals.some((t) => t.status === 'changed')
  if (!hasChanged) return []
  return toolApprovals.filter((t) => t.status === 'changed' || t.status === 'pending')
}
