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
  const read = (root: unknown, key: string): unknown =>
    key.split('.').reduce<unknown>(
      (node, part) => (node && typeof node === 'object' ? (node as Record<string, unknown>)[part] : undefined),
      root
    )
  return Object.keys(locks).filter((key) => JSON.stringify(read(doc, key)) !== JSON.stringify(read(stored, key)))
}
