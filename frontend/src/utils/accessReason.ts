import { reasonText } from '@/utils/profiles'

// Spec 108-j J7 (US3-3): the words for a tool's `access.reason`. The map itself
// lives in utils/profiles.ts reasonText (one map, shared with the profile
// editor and the explainer); this adds the word that depends on the row,
// because `tool_approval` covers both an operator-disabled tool and one that
// still awaits approval.
export function accessReasonLabel(reason: string | undefined, row?: { disabled?: boolean }): string {
  if (reason === 'tool_approval' && row?.disabled) return 'Disabled'
  return reasonText(reason)
}

/** The state word beside the reason: a tool the subject cannot discover is
 * "Hidden", one it can list but not call is "Not callable". */
export function accessStateLabel(access: { visible: boolean; callable: boolean }): string {
  if (access.callable) return 'Callable'
  return access.visible ? 'Not callable' : 'Hidden'
}
