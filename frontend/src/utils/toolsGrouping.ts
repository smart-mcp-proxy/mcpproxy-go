import type { GlobalTool } from '@/types/api'
import { toolApprovalLabel } from '@/utils/toolQuarantine'
import { formatDate } from '@/utils/datetime'

// Spec 134 FR-055/FR-056: what the grouped Tools list derives from the rows the
// server returned to this caller. Nothing here reads the fleet list: a server
// that has no returned tool has no section, and a count is a count of returned
// rows.

export type ToolExceptionKey =
  | 'writes'
  | 'destructive'
  | 'unannotated'
  | 'pending'
  | 'changed'
  | 'disabled'
  | 'config_denied'

export interface ToolException {
  key: ToolExceptionKey
  label: string
  tone: 'warning' | 'error' | 'review' | 'neutral'
  /** Plain words for the title: annotation-accurate, no claim about access. */
  title: string
}

/** The tier the server returned; a row without one is unannotated (Spec 109 X11). */
export const toolTier = (tool: GlobalTool): string => tool.tier || 'unannotated'

/**
 * The exception labels of a row (FR-056), in display order. A read-only,
 * approved, enabled tool has none. The words are the returned tier and state,
 * never a guess: "Writes" is the write tier, not proof that this client can
 * call the tool.
 */
export function toolExceptions(tool: GlobalTool): ToolException[] {
  const out: ToolException[] = []
  const tier = toolTier(tool)
  if (tier === 'write') out.push({ key: 'writes', label: 'Writes', tone: 'warning', title: 'Tier: write (from the tool annotations)' })
  else if (tier === 'destructive') out.push({ key: 'destructive', label: 'Destructive', tone: 'error', title: 'Tier: destructive (the tool is annotated destructive)' })
  else if (tier === 'unannotated') out.push({ key: 'unannotated', label: 'Unannotated', tone: 'neutral', title: 'The tool carries no annotations, so its tier is unknown' })
  if (tool.approval_status === 'pending') out.push({ key: 'pending', label: toolApprovalLabel('pending'), tone: 'review', title: 'Held until it is approved' })
  else if (tool.approval_status === 'changed') out.push({ key: 'changed', label: toolApprovalLabel('changed'), tone: 'review', title: 'Held until the change is approved' })
  if (tool.config_denied) out.push({ key: 'config_denied', label: 'Denied by config', tone: 'error', title: 'Denied by the layered configuration' })
  else if (tool.disabled) out.push({ key: 'disabled', label: 'Disabled', tone: 'neutral', title: 'Switched off for this tool' })
  return out
}

export const hasToolException = (tool: GlobalTool): boolean => toolExceptions(tool).length > 0

/** What a section header may know about a server: only what this caller was sent. */
export interface SectionServerInfo {
  enabled?: boolean
  quarantined?: boolean
  status?: string | null
  level?: string | null
  usable?: boolean | null
}

/** 0 = not usable, 1 = usable (or unknown), 2 = disabled (same order as Servers). */
export function serverSectionRank(info?: SectionServerInfo): 0 | 1 | 2 {
  if (!info) return 1
  if (info.enabled === false || info.status === 'disabled') return 2
  if (info.quarantined) return 0
  if (info.usable === false) return 0
  if (info.status && info.status !== 'ready') return 0
  return 1
}

export interface ToolGroup {
  server: string
  rank: 0 | 1 | 2
  tools: GlobalTool[]
}

/** Sections ordered not usable, usable, disabled, then by name. Tools keep their order. */
export function groupToolsByServer(
  tools: GlobalTool[],
  infoFor: (server: string) => SectionServerInfo | undefined,
): ToolGroup[] {
  const map = new Map<string, ToolGroup>()
  for (const tool of tools) {
    let group = map.get(tool.server_name)
    if (!group) {
      group = { server: tool.server_name, rank: serverSectionRank(infoFor(tool.server_name)), tools: [] }
      map.set(tool.server_name, group)
    }
    group.tools.push(tool)
  }
  return [...map.values()].sort((a, b) => a.rank - b.rank || a.server.localeCompare(b.server))
}


/** Relative time for "Last used": minutes, hours, days, then a date. */
export function formatRelativeTime(ts: string): string {
  const diff = Date.now() - new Date(ts).getTime()
  if (diff < 60_000) return 'just now'
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`
  if (diff < 30 * 86_400_000) return `${Math.floor(diff / 86_400_000)}d ago`
  return formatDate(ts)
}
