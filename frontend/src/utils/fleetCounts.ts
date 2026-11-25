import type { Server } from '@/types'

// A server is online when the core's unified health says it is usable. An old
// core that predates `health` falls back to the same facts the predicate is
// built from: connected, enabled and not quarantined.
export function isOnline(server: Server): boolean {
  if (server.health) return server.health.usable
  return server.connected && server.enabled && !server.quarantined
}

// Tools offered by servers that are online (spec 134 FR-011/FR-040).
export function usableToolCount(servers: Server[]): number {
  return servers.filter(isOnline).reduce((sum, s) => sum + (s.tool_count ?? 0), 0)
}
