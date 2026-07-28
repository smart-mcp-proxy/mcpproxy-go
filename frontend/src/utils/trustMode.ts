/**
 * Per-server trust mode (spec 086 backend, spec 088 Web UI).
 *
 * A server's `trust_mode` governs BOTH behaviors an operator cares about:
 *  - tool changes: auto = trusted unscanned, scan = auto-approved only on a
 *    clean offline scan verdict (otherwise held), manual = every change held;
 *  - new-server admission: auto = admitted without quarantine or scan,
 *    scan = quarantined on add with a fail-closed automatic scan,
 *    manual = quarantined on add for human review (secure default).
 *
 * The raw value delivered by `GET /api/v1/servers` may be absent (unset), a
 * value migrated from the legacy `auto_approve_tool_changes` / `skip_quarantine`
 * flags (indistinguishable from an explicit one — the UI attempts no provenance
 * labeling, spec 088 FR-001), or an invalid hand-edited string.
 *
 * Derivation mirrors the backend accessor `ServerConfig.EffectiveTrustMode()`
 * (`internal/config/config.go`) for DISPLAY only: matching is case-sensitive and
 * untrimmed, and anything unrecognized fails closed to `manual`. The UI writes
 * `trust_mode` only — never the legacy fields (FR-004/FR-005).
 */

export type TrustMode = 'auto' | 'scan' | 'manual'

const RECOGNIZED: readonly TrustMode[] = ['auto', 'scan', 'manual'] as const

function isTrustMode(raw: string): raw is TrustMode {
  return (RECOGNIZED as readonly string[]).includes(raw)
}

/**
 * Fail-closed resolution of a raw configured trust-mode value.
 * Unset, empty, mis-cased ("Scan"), padded (" scan ") and unknown values all
 * resolve to `manual`.
 */
export function effectiveTrustMode(raw?: string): TrustMode {
  if (raw && isTrustMode(raw)) return raw
  return 'manual'
}

export interface TrustModeState {
  /** The configured value exactly as delivered (undefined when the field is omitted). */
  raw?: string
  /** Fail-closed resolution used for every behavioral decision in the UI. */
  effective: TrustMode
  /** No value configured — the UI presents `manual` as the default selection. */
  isDefault: boolean
  /** A non-empty but unrecognized value — the UI shows raw + effective (US1 scenario 4). */
  isInvalid: boolean
}

export function deriveTrustModeState(raw?: string): TrustModeState {
  const isDefault = raw === undefined || raw === ''
  const isInvalid = !isDefault && !isTrustMode(raw)
  return {
    raw,
    effective: effectiveTrustMode(raw),
    isDefault,
    isInvalid,
  }
}

export interface TrustModeMeta {
  mode: TrustMode
  /** Compact label for the selector option and the server-list badge. */
  label: string
  /** What the mode does to TOOL CHANGES on an existing server. */
  description: string
  /** What the mode does to a NEW server at add time. */
  admissionNote: string
  /** Least-safe mode: selection must be confirmed with a warning first (FR-003). */
  warnsOnSelect: boolean
}

/** Ordered metadata for the tri-mode selector, server-list badge and warnings. */
export const TRUST_MODES: readonly TrustModeMeta[] = [
  {
    mode: 'auto',
    label: 'Auto',
    description:
      'Tool changes from this server are trusted and approved without any security scan — a rug-pull risk if the server is later compromised.',
    admissionNote:
      'At add time the server is admitted straight away: no quarantine and no scan.',
    warnsOnSelect: true,
  },
  {
    mode: 'scan',
    label: 'Scan',
    description:
      'Tool changes are approved automatically only when the offline security scan returns a clean verdict; anything else is held for your review.',
    admissionNote:
      'At add time the server is quarantined and scanned automatically; a scan that is not clean — or cannot complete — keeps it quarantined (fail closed).',
    warnsOnSelect: false,
  },
  {
    mode: 'manual',
    label: 'Manual',
    description:
      'Every tool change is held for your review — nothing is approved automatically.',
    admissionNote:
      'At add time the server is quarantined until you approve it — the secure default.',
    warnsOnSelect: false,
  },
] as const
