/**
 * Health status utility functions
 * Extracted for consistent health checks across the application
 */

import type { HealthStatus, Server } from '@/types'

/**
 * Health level constants matching Go backend (internal/health/constants.go)
 */
export const HealthLevel = {
  Healthy: 'healthy',
  Degraded: 'degraded',
  Unhealthy: 'unhealthy',
} as const

/**
 * Admin state constants matching Go backend (internal/health/constants.go)
 */
export const AdminState = {
  Enabled: 'enabled',
  Disabled: 'disabled',
  Quarantined: 'quarantined',
} as const

/**
 * Action constants matching Go backend (internal/health/constants.go)
 */
export const HealthAction = {
  None: '',
  Login: 'login',
  Restart: 'restart',
  Enable: 'enable',
  Approve: 'approve',
  ViewLogs: 'view_logs',
  SetSecret: 'set_secret',
  Configure: 'configure',
} as const

/**
 * Check if a health status indicates a healthy server.
 * Uses health.level as the source of truth.
 *
 * @param health - The health status object (may be undefined)
 * @param legacyConnected - Fallback value when health is not available
 * @returns true if the server is considered healthy
 */
export function isHealthy(health: HealthStatus | undefined, legacyConnected: boolean): boolean {
  if (health) {
    return health.level === HealthLevel.Healthy
  }
  // Fallback to legacy connected field if health is not available
  return legacyConnected
}

/**
 * Check if a server is considered "connected" using health.level as source of truth.
 * Falls back to legacy connected field for backward compatibility.
 *
 * This is the canonical function to use when determining if a server is operational.
 *
 * @param server - The server object
 * @returns true if the server is connected/healthy
 */
export function isServerConnected(server: Server): boolean {
  return isHealthy(server.health, server.connected)
}

/**
 * Get the appropriate badge class for a health level
 *
 * @param level - The health level
 * @returns DaisyUI badge class
 */
export function getHealthBadgeClass(level: string): string {
  switch (level) {
    case HealthLevel.Healthy:
      return 'badge-success'
    case HealthLevel.Degraded:
      return 'badge-warning'
    case HealthLevel.Unhealthy:
      return 'badge-error'
    default:
      return 'badge-ghost'
  }
}

/**
 * Get the appropriate badge class for an admin state
 *
 * @param state - The admin state
 * @returns DaisyUI badge class
 */
export function getAdminStateBadgeClass(state: string): string {
  switch (state) {
    case AdminState.Enabled:
      return 'badge-success'
    case AdminState.Disabled:
      return 'badge-ghost'
    case AdminState.Quarantined:
      return 'badge-warning'
    default:
      return 'badge-ghost'
  }
}
