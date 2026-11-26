import type { Server } from '@/types'
import { healthTone, type StatusTone } from '@/utils/healthTone'
import { serverDisplayName } from '@/utils/serverRoute'

// Spec 134 FR-012, FR-023, FR-024: the Servers list model. Pure functions, so
// the page, the header parity checks and the unit tests share one definition.

export type ServerChip = 'not-online' | 'all' | 'online' | 'disabled'
export type LegacyServerFilter = 'connected' | 'enabled' | 'quarantined' | 'needs_review'
export type ServerFilter = ServerChip | LegacyServerFilter

export const SERVER_CHIPS: ServerChip[] = ['not-online', 'all', 'online', 'disabled']
export const LEGACY_FILTERS: LegacyServerFilter[] = ['connected', 'enabled', 'quarantined', 'needs_review']

/** Same predicate as the header (HeaderStatus): online means `health.usable`. */
export function isOnline(server: Server): boolean {
  if (server.health) return server.health.usable
  return server.connected && server.enabled && !server.quarantined
}

/** Administratively on. */
export function isEnabledServer(server: Server): boolean {
  // `enabled` is the field Enable/Disable update at once (before the SSE-driven
  // refresh replaces `health`), so the Disabled chip and the Enabled filter
  // never contradict each other in that window. A disabled+quarantined row has
  // enabled=false, as in the header.
  return server.enabled
}

export function isNeedsReview(server: Server): boolean {
  if (server.quarantined) return true
  const q = server.quarantine
  return !!q && (q.pending_count ?? 0) + (q.changed_count ?? 0) > 0
}

export interface ServerCounts {
  total: number
  notOnline: number
  online: number
  disabled: number
  connected: number
  enabled: number
  quarantined: number
  needsReview: number
}

export function countServers(servers: Server[]): ServerCounts {
  const c: ServerCounts = { total: servers.length, notOnline: 0, online: 0, disabled: 0, connected: 0, enabled: 0, quarantined: 0, needsReview: 0 }
  for (const s of servers) {
    const enabled = isEnabledServer(s)
    if (!enabled) c.disabled++
    else if (isOnline(s)) c.online++
    else c.notOnline++
    if (s.connected) c.connected++
    if (s.enabled) c.enabled++
    if (s.quarantined) c.quarantined++
    if (isNeedsReview(s)) c.needsReview++
  }
  return c
}

export function matchesFilter(server: Server, filter: ServerFilter): boolean {
  switch (filter) {
    case 'all': return true
    case 'not-online': return isEnabledServer(server) && !isOnline(server)
    case 'online': return isEnabledServer(server) && isOnline(server)
    case 'disabled': return !isEnabledServer(server)
    case 'connected': return server.connected
    case 'enabled': return server.enabled
    case 'quarantined': return server.quarantined
    case 'needs_review': return isNeedsReview(server)
  }
}

export function matchesSearch(server: Server, query: string): boolean {
  if (!query) return true
  const q = query.toLowerCase()
  return server.name.toLowerCase().includes(q) ||
    !!server.url?.toLowerCase().includes(q) ||
    !!server.command?.toLowerCase().includes(q)
}

export interface FilterInputs {
  /** `?filter=` as in the URL (raw string or undefined). */
  filter?: unknown
  /** `?status=` as in the URL. */
  status?: unknown
  hasQ: boolean
  hasProfile: boolean
  /** 768 px and wider. */
  wide: boolean
  /** Number of "Not online" servers in the list on screen. */
  notOnlineCount: number
}

// A repeated parameter (an array) is not a value, as before.
const asString = (v: unknown): string | undefined => (typeof v === 'string' ? v : undefined)

/**
 * The filter in force. A known `status` wins (old links keep their meaning,
 * `status=all` is the All chip); then a known `filter`; otherwise a bare visit
 * (none of filter, status, q, profile in the URL) is All at 768 px and wider and
 * "Not online" below that when it is not empty; a link carrying only `q` or
 * `profile` is All at every width.
 */
export function resolveFilter(input: FilterInputs): ServerFilter {
  const status = asString(input.status)
  if (status === 'all') return 'all'
  if (status && (LEGACY_FILTERS as string[]).includes(status)) return status as LegacyServerFilter
  const filter = asString(input.filter)
  if (filter && (SERVER_CHIPS as string[]).includes(filter)) return filter as ServerChip
  // Presence, not validity: a valueless `?filter` (null) is still a visit with a filter in the URL.
  const bare = input.filter === undefined && input.status === undefined && !input.hasQ && !input.hasProfile
  if (bare && !input.wide && input.notOnlineCount > 0) return 'not-online'
  return 'all'
}

// ---------------------------------------------------------------------------
// FR-024 default order
// ---------------------------------------------------------------------------

/** The status a row is sorted and drawn by: `health.status`, else derived from the legacy fields. */
export function effectiveStatus(server: Server): string {
  const health = server.health
  if (health?.status) return health.status
  // A payload with health but no status: derive from what it does say.
  if (health) {
    if (health.admin_state === 'disabled') return 'disabled'
    if (health.admin_state === 'quarantined') return 'needs_review'
    if (health.usable) return 'ready'
    return server.connecting ? 'connecting' : 'error'
  }
  if (!server.enabled) return 'disabled'
  if (server.quarantined) return 'needs_review'
  if (server.connected) return 'ready'
  if (server.connecting) return 'connecting'
  return 'error'
}

const TONE_RANK: Record<StatusTone, number> = { error: 0, review: 1, neutral: 2, busy: 3, ok: 4, off: 5 }

export function statusRank(server: Server): number {
  return TONE_RANK[healthTone(effectiveStatus(server))]
}

function cmp(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0
}

/** Total order: tone rank, then display title (case-insensitive), then canonical id. */
export function compareServers(a: Server, b: Server): number {
  return (
    statusRank(a) - statusRank(b) ||
    cmp(serverDisplayName(a).toLowerCase(), serverDisplayName(b).toLowerCase()) ||
    cmp(serverDisplayName(a), serverDisplayName(b)) ||
    cmp(a.name, b.name)
  )
}

export function sortServers(servers: Server[]): Server[] {
  return [...servers].sort(compareServers)
}
