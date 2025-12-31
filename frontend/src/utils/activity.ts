/**
 * Activity log utility functions
 * Extracted for reuse across Activity.vue and ActivityWidget.vue
 */

// Activity type labels
const typeLabels: Record<string, string> = {
  'tool_call': 'Tool Call',
  'policy_decision': 'Policy Decision',
  'quarantine_change': 'Quarantine Change',
  'server_change': 'Server Change'
}

// Activity type icons
const typeIcons: Record<string, string> = {
  'tool_call': '🔧',
  'policy_decision': '🛡️',
  'quarantine_change': '⚠️',
  'server_change': '🔄'
}

// Status labels
const statusLabels: Record<string, string> = {
  'success': 'Success',
  'error': 'Error',
  'blocked': 'Blocked'
}

// Status badge CSS classes (DaisyUI)
const statusClasses: Record<string, string> = {
  'success': 'badge-success',
  'error': 'badge-error',
  'blocked': 'badge-warning'
}

// Intent operation type icons
const intentIcons: Record<string, string> = {
  'read': '📖',
  'write': '✏️',
  'destructive': '⚠️'
}

// Intent badge classes
const intentClasses: Record<string, string> = {
  'read': 'badge-info',
  'write': 'badge-warning',
  'destructive': 'badge-error'
}

/**
 * Format activity type for display
 */
export const formatType = (type: string): string => {
  return typeLabels[type] || type
}

/**
 * Get icon for activity type
 */
export const getTypeIcon = (type: string): string => {
  return typeIcons[type] || '📋'
}

/**
 * Format status for display
 */
export const formatStatus = (status: string): string => {
  return statusLabels[status] || status
}

/**
 * Get badge CSS class for status
 */
export const getStatusBadgeClass = (status: string): string => {
  return statusClasses[status] || 'badge-ghost'
}

/**
 * Get icon for intent operation type
 */
export const getIntentIcon = (operationType: string): string => {
  return intentIcons[operationType] || '❓'
}

/**
 * Get badge CSS class for intent operation type
 */
export const getIntentBadgeClass = (operationType: string): string => {
  return intentClasses[operationType] || 'badge-ghost'
}

/**
 * Format timestamp for display
 */
export const formatTimestamp = (timestamp: string): string => {
  return new Date(timestamp).toLocaleString()
}

/**
 * Format relative time (e.g., "5m ago")
 */
export const formatRelativeTime = (timestamp: string): string => {
  const now = Date.now()
  const time = new Date(timestamp).getTime()
  const diff = now - time

  if (diff < 1000) return 'Just now'
  if (diff < 60000) return `${Math.floor(diff / 1000)}s ago`
  if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`
  if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`
  return `${Math.floor(diff / 86400000)}d ago`
}

/**
 * Format duration in milliseconds for display
 */
export const formatDuration = (ms: number): string => {
  if (ms < 1000) return `${Math.round(ms)}ms`
  return `${(ms / 1000).toFixed(2)}s`
}

/**
 * Filter activities based on filter criteria
 */
export interface ActivityFilterOptions {
  type?: string
  server?: string
  status?: string
}

export interface ActivityRecord {
  id: string
  type: string
  server_name?: string
  status: string
  timestamp: string
  [key: string]: unknown
}

export const filterActivities = (
  activities: ActivityRecord[],
  filters: ActivityFilterOptions
): ActivityRecord[] => {
  let result = activities

  if (filters.type) {
    result = result.filter(a => a.type === filters.type)
  }
  if (filters.server) {
    result = result.filter(a => a.server_name === filters.server)
  }
  if (filters.status) {
    result = result.filter(a => a.status === filters.status)
  }

  return result
}

/**
 * Paginate activities
 */
export const paginateActivities = (
  activities: ActivityRecord[],
  page: number,
  pageSize: number
): ActivityRecord[] => {
  const start = (page - 1) * pageSize
  return activities.slice(start, start + pageSize)
}

/**
 * Calculate total pages
 */
export const calculateTotalPages = (totalItems: number, pageSize: number): number => {
  return Math.ceil(totalItems / pageSize)
}
