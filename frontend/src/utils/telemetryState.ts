import type { TelemetryState } from '@/types'

/**
 * Effective telemetry state helpers (Spec 109 FR-044a). The copy here is
 * mirrored verbatim by native/macos/.../State/TelemetryNotice.swift, and both
 * are asserted against internal/telemetry/testdata/effective_state_cases.json
 * so the surfaces cannot drift.
 */

/**
 * How the first-run telemetry slot renders:
 *  - `notice`:  the standard disclosure (telemetry on, or state unknown).
 *  - `off_env`: an environment variable disabled telemetry; say so.
 *  - `hidden`:  the user turned it off in their own config; no nagging.
 */
export type TelemetryNoticeMode = 'notice' | 'off_env' | 'hidden'

export function telemetryNoticeMode(s?: TelemetryState | null): TelemetryNoticeMode {
  // Unknown state (older core, failed fetch) keeps today's notice: failing
  // toward disclosure is the safe direction.
  if (!s || s.enabled) return 'notice'
  if (s.source === 'env') return 'off_env'
  return 'hidden'
}

export function telemetryOffLine(s: TelemetryState): string {
  return `Anonymous usage telemetry is off — disabled by ${s.disabled_by} in the environment. Nothing is sent.`
}

/** Non-null only when an environment variable forces telemetry off. */
export function telemetrySettingLock(s?: TelemetryState | null): { reason: string; value: false } | null {
  if (!s || s.enabled || s.source !== 'env') return null
  return {
    reason: `Off — disabled by ${s.disabled_by} in the environment. Unset it and restart MCPProxy to change this setting.`,
    value: false,
  }
}

/**
 * Locked settings (an environment forces their effective value) that a whole
 * config document would change relative to the stored one. FR-044a: a locked
 * setting must never be saved. The Raw JSON editor posts the entire document,
 * so the per-field lock on the forms does not cover it. Returns the dotted keys
 * whose value in `doc` differs from `stored`.
 */
export function lockedKeysChanged(doc: unknown, stored: unknown, locks: Record<string, unknown>): string[] {
  // The backend decodes the Raw JSON document case-insensitively. It first
  // round-trips the document through a map (sorted keys), so which of several
  // case-variant keys wins is decided by byte order, not by document order, and
  // same-named objects merge. Rather than mirror that, every case-variant
  // reading of a key is collected and the document is refused when ANY of them
  // differs from the stored value: ambiguous input fails toward refusal.
  const readAll = (root: unknown, key: string): unknown[] =>
    key.split('.').reduce<unknown[]>(
      (nodes, part) => {
        const want = part.toLowerCase()
        const next: unknown[] = []
        for (const node of nodes) {
          if (!node || typeof node !== 'object') {
            next.push(undefined)
            continue
          }
          const hits = Object.entries(node as Record<string, unknown>).filter(([k]) => k.toLowerCase() === want)
          if (hits.length === 0) next.push(undefined)
          for (const [, v] of hits) next.push(v)
        }
        return next
      },
      [root],
    )
  return Object.keys(locks).filter((key) => {
    const storedAll = readAll(stored, key)
    const want = JSON.stringify(storedAll[storedAll.length - 1])
    return readAll(doc, key).some((v) => JSON.stringify(v) !== want)
  })
}
