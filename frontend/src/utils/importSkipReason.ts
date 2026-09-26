// Human-readable labels for the config-import skip reasons the backend emits
// (internal/configimport: SkippedServer.Reason / SkipReasonSelfReference).
// The raw reason strings are stable API values, not copy — nothing maps them
// to text a user should read, so a skip surfaced them verbatim (e.g. the
// literal string "self_reference") or, worse, mislabeled every skip reason
// as "already configured" regardless of why the server was actually skipped.
const SKIP_REASON_LABELS: Record<string, string> = {
  self_reference: 'connects to mcpproxy itself',
  already_exists: 'already configured',
  filtered_out: 'not selected',
}

/**
 * Human-readable label for one import skip reason. Falls back to the raw
 * reason string for any value the backend adds later that this map hasn't
 * caught up with yet, so nothing renders blank.
 */
export function skipReasonLabel(reason: string | undefined | null): string {
  if (!reason) return 'skipped'
  return SKIP_REASON_LABELS[reason] ?? reason
}
