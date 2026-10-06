import type { ApiError } from '@/services/api'
import type { BindingRef, GuardFix } from '@/types/api'

// Spec 108-i: the words the Web UI uses for profile concepts. One place, so the
// editor, the cards, the chip and the explainer cannot drift from the
// Terminology table (spec.md) or from the CLI's output.

export type Tier = 'read' | 'write' | 'destructive'

// Max tool tier, as the editor's segmented control labels it.
export const MAX_TIER_OPTIONS: Array<{ value: '' | Tier; label: string }> = [
  { value: 'read', label: 'Read' },
  { value: 'write', label: '+ Write' },
  { value: 'destructive', label: '+ Destructive' },
  { value: '', label: 'No cap' },
]

// The short form used in a chip: "Work · Read-only · locked by credential".
export function tierPhrase(maxTier: string | undefined): string {
  switch (maxTier) {
    case 'read': return 'Read-only'
    case 'write': return 'Read + write'
    case 'destructive': return 'Everything'
    default: return ''
  }
}

export const UNANNOTATED_OPTIONS: Array<{ value: '' | 'deny' | 'as_write' | 'as_read'; label: string }> = [
  { value: 'deny', label: 'Hide' },
  { value: 'as_write', label: 'Treat as write' },
  { value: 'as_read', label: 'Treat as read' },
]

export function unannotatedLabel(value: string | undefined): string {
  return UNANNOTATED_OPTIONS.find(option => option.value === value)?.label ?? 'Default'
}

// How a client credential is bound, in words (Terminology: Locked / Switchable).
export function modeLabel(mode: string | undefined): string {
  return mode === 'locked' ? 'Locked' : 'Switchable'
}

// Why a tool is hidden or not callable, in words: the FR-010 reasons plus the
// rest of profile.AccessReasons that a view-as listing and the explainer can
// return (Spec 108-j J7). One map for the editor table, Tools view-as and the
// Activity "Why?" text; utils/accessReason.ts adds the row-dependent word.
export function reasonText(reason: string | undefined): string {
  switch (reason) {
    case 'server_not_in_profile': return 'Server not in profile'
    case 'denied_by_rule': return 'Denied by rule'
    case 'unannotated_hidden': return 'Unannotated — classify'
    case 'above_tier_cap': return 'Above tier cap'
    case 'credential': return 'Credential revoked or expired'
    case 'profile': return 'Profile missing — denied everything'
    case 'server_in_scope': return "Server outside the token's scope"
    case 'token_permission': return 'Token permission'
    case 'global_gate': return 'Blocked by a global setting'
    case 'server_state': return 'Server disabled or not connected'
    case 'tool_approval': return 'Needs review'
    case '':
    case undefined: return 'Visible'
    default: return reason.replaceAll('_', ' ')
  }
}

// How a call's or session's profile was resolved, in words (Spec 108-j J10).
// `none` and an empty source carry no suffix.
export function profileSourceLabel(source: string | undefined): string {
  switch (source) {
    case 'pin': return 'locked by credential'
    case 'binding': return 'switchable'
    case 'url': return 'from URL'
    case 'session': return 'switched in session'
    case 'anonymous': return 'anonymous'
    default: return ''
  }
}

// Spec 108-j J8 (FR-046): who a blocked Activity record is explained for, in
// order: the client, else the token, else an anonymous caller, else the profile
// the call was recorded under. Nothing to explain for (null) means no "Why?".
export interface ExplainSubject { kind: 'client' | 'token' | 'profile' | 'anonymous'; name?: string }
export function explainSubjectForRecord(record: {
  client_id?: string
  token_name?: string
  profile?: string
  profile_source?: string
}): ExplainSubject | null {
  if (record.client_id) return { kind: 'client', name: record.client_id }
  if (record.token_name) return { kind: 'token', name: record.token_name }
  if (record.profile_source === 'anonymous') return { kind: 'anonymous' }
  if (record.profile) return { kind: 'profile', name: record.profile }
  return null
}

// Spec 108-j J9: what a blocked record can offer. `allow` edits a rule or the
// tier of one tool (the editor opens focused on it); `open` is the profile
// itself (code execution and management are profile switches, and a server outside
// the profile is added in the profile editor, not on a tool row).
export function blockedProfileAction(reason: string | undefined): 'allow' | 'open' | null {
  switch (reason) {
    case 'profile_tier':
    case 'profile_rule':
    case 'profile_unannotated':
      return 'allow'
    case 'profile_code_execution':
    case 'profile_management':
    case 'profile_server_scope':
      return 'open'
    default:
      return null
  }
}

export const STEP_LABELS: Record<string, string> = {
  credential: 'Credential',
  profile: 'Profile',
  server_in_scope: 'Server in scope',
  tool_rule: 'Tool rule',
  tier_cap: 'Tier cap',
  token_permission: 'Token permission',
  global_gate: 'Global gate',
  server_state: 'Server state',
  tool_approval: 'Tool approval',
}

export function credentialLabel(state: string | undefined): string {
  switch (state) {
    case 'client': return 'Client credential'
    case 'admin_key': return 'Admin key'
    case 'none': return 'No credential'
    case 'revoked': return 'Revoked'
    case 'expired': return 'Expired'
    default: return 'Unknown'
  }
}

// The fix-button text for a row without a usable client credential
// (labels.json credential_cta, identical on macOS). Only a client that holds
// the admin key is an "upgrade"; a revoked or expired credential is a
// "reconnect"; an installed client that never connected (none, or the
// stat-only unknown) is simply "Connect". A client credential needs no button.
export function credentialCta(state: string | undefined): string {
  switch (state) {
    case 'client': return ''
    case 'admin_key': return 'Upgrade to client credential'
    case 'revoked':
    case 'expired': return 'Reconnect'
    default: return 'Connect'
  }
}

// --- errors ----------------------------------------------------------------

export function errorMessage(err: unknown, fallback = 'Something went wrong'): string {
  if (err instanceof Error && err.message) return err.message
  if (typeof err === 'string' && err) return err
  return fallback
}

export function isGuardRefusal(err: unknown): err is ApiError & { bindings?: BindingRef[]; fixes?: GuardFix[] } {
  return (err as ApiError | undefined)?.code === 'binding_bypassable_without_auth'
}

export function isForbidden(err: unknown): boolean {
  return (err as ApiError | undefined)?.status === 403
}

// A 403 anywhere reads as "not an administrator", never as a lost API key.
export function describeError(err: unknown, fallback?: string): string {
  if (isForbidden(err)) return 'Requires an administrator'
  return errorMessage(err, fallback)
}

// "server:tool" of an effective-tools row.
export function toolKey(row: { server: string; tool: string }): string {
  return `${row.server}:${row.tool}`
}

// A data-test/DOM-safe form of "server:tool": profile-tool-row-<server>__<tool>.
export function toolRowId(row: { server: string; tool: string }): string {
  return `${row.server}__${row.tool}`
}

// --- Try it hits -------------------------------------------------------------

export interface TryHitRow { key: string; server: string; tool: string; description: string }

function text(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

// One POST /profiles/try hit as a row. The real shape is the retrieve_tools hit
// {score, tool: {name: "server:tool", server_name, description, annotations}},
// the one the CLI's tryResultRow reads. A flat {server, name, description} item
// is accepted too. Only string fields are read, so an unexpected value can
// never print as "[object Object]"; an unnamed hit gets an empty key.
export function tryHitRow(item: Record<string, unknown>): TryHitRow {
  const nested = typeof item.tool === 'object' && item.tool !== null ? (item.tool as Record<string, unknown>) : null
  const t = nested ?? item
  let name = text(t.name) || text(item.tool)
  let server = text(t.server_name) || text(t.server) || text(item.server_name) || text(item.server)
  const colon = name.indexOf(':')
  if (colon > 0) {
    if (!server) server = name.slice(0, colon)
    name = name.slice(colon + 1)
  }
  return {
    key: name ? (server ? `${server}:${name}` : name) : '',
    server,
    tool: name,
    description: text(t.description) || text(item.description),
  }
}
