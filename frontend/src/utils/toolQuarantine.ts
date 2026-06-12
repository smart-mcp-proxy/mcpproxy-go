// MCP-2081 (Spec 032): decide which tools surface the tool-level Quarantine
// banner + per-tool Approve UI on the server-detail page.
//
// The trust model (confirmed): a freshly-discovered baseline tool starts
// `pending`. That is NOT a rug-pull — when the operator approves the server,
// the backend promotes those baseline `pending` records to `approved`. So
// baseline `pending` tools must never raise the tool-quarantine banner on their
// own, and the tool-level banner must never compete with the server-level
// Security Quarantine banner.
//
// The only thing that legitimately needs per-tool attention is a `changed`
// tool (its description / input schema / output schema hash drifted from the
// approved version — i.e. a rug-pull). Once a `changed` tool exists, any
// residual `pending` tools are surfaced alongside it so the operator can clear
// the whole batch in one pass.

import type { ToolApproval } from '@/types'

/**
 * Select the tool-approval records that should drive the tool-quarantine
 * banner and approval list.
 *
 * @param approvals       the server's tool-approval records
 * @param serverQuarantined whether the server itself is under Security Quarantine
 * @returns the subset to surface (empty = no tool-quarantine banner)
 */
export function selectQuarantinedTools(
  approvals: ToolApproval[] | null | undefined,
  serverQuarantined: boolean | null | undefined
): ToolApproval[] {
  if (!approvals || approvals.length === 0) return []

  // Server-level Security Quarantine takes precedence: the operator approves
  // the server first, and the backend then promotes baseline pending tools to
  // approved. Suppress the competing tool-level banner entirely.
  if (serverQuarantined) return []

  // Baseline `pending` tools are a fresh-install artifact, not a rug-pull, so
  // they must not raise the banner on their own. Only a `changed` tool triggers
  // it; once it does, residual `pending` tools are included for batch approval.
  const hasChanged = approvals.some((t) => t.status === 'changed')
  if (!hasChanged) return []

  return approvals.filter((t) => t.status === 'changed' || t.status === 'pending')
}
