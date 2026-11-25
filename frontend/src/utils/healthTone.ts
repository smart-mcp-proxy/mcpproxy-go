import { HEALTH_STATUS_LABELS, type HealthStatusValue } from '@/types/contracts'

// Spec 134 FR-003: the colour tone of a server status. Colour only; the words
// come from HEALTH_STATUS_LABELS (Spec 109). Unknown values are neutral.
export type StatusTone = 'ok' | 'review' | 'error' | 'busy' | 'off' | 'neutral'

const TONES: Record<HealthStatusValue, StatusTone> = {
  ready: 'ok',
  needs_review: 'review',
  connecting: 'busy',
  disabled: 'off',
  sign_in_required: 'error',
  needs_secret: 'error',
  needs_config: 'error',
  error: 'error',
}

export function isKnownHealthStatus(status: string | null | undefined): status is HealthStatusValue {
  return !!status && Object.prototype.hasOwnProperty.call(HEALTH_STATUS_LABELS, status)
}

/**
 * Tone of a status. `level` (Spec 109: a severity signal for colouring only,
 * never printed) raises the tone of `ready` when it is worse than the status
 * implies: a usable but degraded server keeps a warning (review tone), an
 * unhealthy one the error tone. The words stay the Spec 109 label.
 */
export function healthTone(status: string | null | undefined, level?: string | null): StatusTone {
  if (!isKnownHealthStatus(status)) return 'neutral'
  if (status === 'ready') {
    if (level === 'unhealthy') return 'error'
    if (level === 'degraded') return 'review'
  }
  return TONES[status]
}

/** The Spec 109 label for a known status, else the raw value (FR-003). */
export function healthPillText(status: string): string {
  return isKnownHealthStatus(status) ? HEALTH_STATUS_LABELS[status] : status
}
