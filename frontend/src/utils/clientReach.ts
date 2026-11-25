import type { ClientPresence } from '@/types/api'
import { credentialLabel, tierPhrase } from '@/utils/profiles'

// Spec 134 FR-060: what the Clients table says about a client's reach, from the
// same facts the profile chip uses (ProfileChip.vue). Reach is a configured
// binding, not effective access: "All servers" is printed only where the page
// printed it before (a client credential with no profile, or a credential that
// cannot be bound: admin key, revoked, expired, unknown), never for a row with
// no record, whose reach is not known.
export type ReachKind = 'unknown' | 'all' | 'profile' | 'missing'

export interface ClientReach {
  kind: ReachKind
  text: string
  /** Secondary fact printed beside the text (the credential kind, or the lock). */
  note: string
}

interface ProfileLike { title?: string; max_tier?: string; tool_counts?: { write?: number; destructive?: number } }

/** A row with nothing to bind: observed identity, custom client whose record is gone, or a supported client that is neither installed nor connected and holds no credential. */
export function clientHasNoRecord(c: Pick<ClientPresence, 'kind' | 'credential_state' | 'installed' | 'connected'>): boolean {
  if (c.kind === 'other') return true
  const credentialless = !c.credential_state || c.credential_state === 'none' || c.credential_state === 'unknown'
  if (c.kind === 'custom') return c.credential_state === 'none'
  return credentialless && !c.installed && !c.connected
}

export function clientReach(c: ClientPresence, profileFor: (name: string) => ProfileLike | undefined): ClientReach {
  if (clientHasNoRecord(c)) return { kind: 'unknown', text: '—', note: '' }
  if (c.credential_state !== 'client') return { kind: 'all', text: 'All servers', note: credentialLabel(c.credential_state) }
  const locked = c.profile_mode === 'locked' || c.profile_source === 'pin'
  if (!c.profile) return { kind: 'all', text: 'All servers', note: credentialLabel(c.credential_state) }
  if (c.profile_missing) return { kind: 'missing', text: `${c.profile} (missing — deny-all)`, note: credentialLabel(c.credential_state) }
  const profile = profileFor(c.profile)
  const parts = [c.profile_title || profile?.title || c.profile]
  const tier = tierPhrase(profile?.max_tier, profile?.tool_counts)
  if (tier) parts.push(tier)
  parts.push(locked ? 'locked by credential' : 'switchable')
  return { kind: 'profile', text: parts.join(' · '), note: credentialLabel(c.credential_state) }
}

/** Supported apps that are positively not detected fold into one line (FR-060). */
export function isUndetectedSupported(c: ClientPresence): boolean {
  return c.kind === 'supported' && c.state === 'not_installed' && !c.connected && !c.installed &&
    (!c.credential_state || c.credential_state === 'none' || c.credential_state === 'unknown') && !c.token_name &&
    (c.credentials ?? []).length === 0
}

export function clientStateLabel(value: string): string { return value.replaceAll('_', ' ') }
