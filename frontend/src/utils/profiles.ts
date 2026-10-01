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

// Why a tool is hidden, in words (FR-010 reasons).
export function reasonText(reason: string | undefined): string {
  switch (reason) {
    case 'server_not_in_profile': return 'Server not in profile'
    case 'denied_by_rule': return 'Denied by rule'
    case 'unannotated_hidden': return 'Unannotated — hidden'
    case 'above_tier_cap': return 'Above tier cap'
    case '':
    case undefined: return 'Visible'
    default: return reason.replaceAll('_', ' ')
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

// The fix-button text for a failed step.
export function credentialCta(state: string | undefined): string {
  return state === 'revoked' || state === 'expired' ? 'Reconnect' : 'Upgrade to client credential'
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
