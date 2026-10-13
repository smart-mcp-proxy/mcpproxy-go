import { formatDateTimeShort } from '@/utils/datetime'
import type { CredentialIssuer } from '@/types/api'

// Spec 115 UI vocabulary for worker credentials (client credentials and agent
// tokens issued for a task): who issued them, how they are bound, how long the
// lease runs, and their state. One place, so the Clients rows, the Tokens rows
// and the profile editor cannot drift (research D12).

// A lease is a credential whose lifetime at issue is at most 24 h: its expiry
// is the planned task end, not a warning (UI-005).
export const LEASE_THRESHOLD_MS = 24 * 60 * 60 * 1000

export interface LifecycleFields {
  revoked?: boolean
  revoked_at?: string | null
  expires_at?: string | null
  created_at?: string | null
  lease?: boolean
  credential_state?: string
}

function ms(value: string | null | undefined): number | null {
  if (!value) return null
  const t = new Date(value).getTime()
  return Number.isNaN(t) ? null : t
}

// "45 min", "2 h 5 min", "3 d" — a compact remaining-time phrase.
export function durationText(remainingMs: number): string {
  const minutes = Math.max(0, Math.round(remainingMs / 60000))
  if (minutes < 1) return 'less than a minute'
  if (minutes < 60) return `${minutes} min`
  const hours = Math.floor(minutes / 60)
  const rest = minutes % 60
  if (hours < 24) return rest ? `${hours} h ${rest} min` : `${hours} h`
  const days = Math.floor(hours / 24)
  return `${days} d`
}

// Whether the credential's lifetime at issue makes it a lease. The server's
// `lease` field wins; created/expires are the fallback for older payloads.
export function isLease(c: LifecycleFields): boolean {
  if (typeof c.lease === 'boolean') return c.lease
  const created = ms(c.created_at)
  const expires = ms(c.expires_at)
  return created !== null && expires !== null && expires - created <= LEASE_THRESHOLD_MS
}

// "Lease ends in 45 min" while running, "Lease ended" afterwards; '' for a
// credential that is not a lease (the caller keeps today's expiry text).
export function leaseText(c: LifecycleFields, now: number = Date.now()): string {
  if (!isLease(c)) return ''
  const expires = ms(c.expires_at)
  if (expires === null) return ''
  if (expires <= now) return 'Lease ended'
  return `Lease ends in ${durationText(expires - now)}`
}

// "via MCP · api_key", "via Web UI", "via CLI", "via REST", "via macOS app";
// '' for a record with no issuer (minted before Spec 115, or by connect).
export function issuerText(issuer: CredentialIssuer | null | undefined): string {
  if (!issuer) return ''
  switch (issuer.surface) {
    case 'mcp': return issuer.actor_kind ? `via MCP · ${issuer.actor_kind}` : 'via MCP'
    case 'web': return 'via Web UI'
    case 'cli': return 'via CLI'
    case 'macos': return 'via macOS app'
    case 'api': return 'via REST'
    default: return issuer.surface ? `via ${issuer.surface}` : ''
  }
}

// "Locked to X", "Switchable from X" for a client; "Pinned to X" for a token.
export function bindingText(kind: 'client' | 'token', profile: string | undefined, mode?: string): string {
  if (!profile) return kind === 'client' && mode === 'switchable' ? 'All servers (switchable)' : ''
  if (kind === 'token') return `Pinned to ${profile}`
  return mode === 'switchable' ? `Switchable from ${profile}` : `Locked to ${profile}`
}

export interface StateBadge { label: string; tone: 'success' | 'neutral' | 'error'; detail: string }

// The state, revoked over expired (data-model §2). A revoked credential names
// when; an ended lease is an outcome (neutral), not an error.
export function stateBadge(c: LifecycleFields, now: number = Date.now()): StateBadge {
  const revoked = c.revoked || c.credential_state === 'revoked'
  if (revoked) {
    const at = c.revoked_at ? formatDateTimeShort(c.revoked_at) : ''
    return { label: 'Revoked', tone: 'error', detail: at ? `Revoked ${at}` : 'Revoked' }
  }
  const expires = ms(c.expires_at)
  if ((expires !== null && expires <= now) || c.credential_state === 'expired') {
    return isLease(c)
      ? { label: 'Lease ended', tone: 'neutral', detail: 'Lease ended' }
      : { label: 'Expired', tone: 'neutral', detail: 'Expired' }
  }
  return { label: 'Active', tone: 'success', detail: 'Active' }
}

// Today's expiring-soon rule (72 h) applies to non-lease credentials only: a
// lease running out is the plan, never a warning (UI-005).
export function expiringSoon(c: LifecycleFields, now: number = Date.now()): boolean {
  if (isLease(c) || c.revoked) return false
  const expires = ms(c.expires_at)
  if (expires === null || expires <= now) return false
  return expires - now < 72 * 60 * 60 * 1000
}

export const PURPOSE_LABEL = 'Stated purpose — not enforced'
