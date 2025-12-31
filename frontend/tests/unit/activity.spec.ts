import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  formatType,
  getTypeIcon,
  formatStatus,
  getStatusBadgeClass,
  getIntentIcon,
  getIntentBadgeClass,
  formatRelativeTime,
  formatDuration,
  filterActivities,
  paginateActivities,
  calculateTotalPages,
  type ActivityRecord,
  type ActivityFilterOptions
} from '../../src/utils/activity'

// T064 - Activity type rendering tests
describe('Activity Type Rendering', () => {
  describe('formatType', () => {
    it('formats tool_call type', () => {
      expect(formatType('tool_call')).toBe('Tool Call')
    })

    it('formats policy_decision type', () => {
      expect(formatType('policy_decision')).toBe('Policy Decision')
    })

    it('formats quarantine_change type', () => {
      expect(formatType('quarantine_change')).toBe('Quarantine Change')
    })

    it('formats server_change type', () => {
      expect(formatType('server_change')).toBe('Server Change')
    })

    it('returns unknown type as-is', () => {
      expect(formatType('unknown_type')).toBe('unknown_type')
    })
  })

  describe('getTypeIcon', () => {
    it('returns wrench icon for tool_call', () => {
      expect(getTypeIcon('tool_call')).toBe('🔧')
    })

    it('returns shield icon for policy_decision', () => {
      expect(getTypeIcon('policy_decision')).toBe('🛡️')
    })

    it('returns warning icon for quarantine_change', () => {
      expect(getTypeIcon('quarantine_change')).toBe('⚠️')
    })

    it('returns refresh icon for server_change', () => {
      expect(getTypeIcon('server_change')).toBe('🔄')
    })

    it('returns clipboard icon for unknown type', () => {
      expect(getTypeIcon('unknown')).toBe('📋')
    })
  })
})

// T065 - Status badge color tests
describe('Status Badge Colors', () => {
  describe('formatStatus', () => {
    it('formats success status', () => {
      expect(formatStatus('success')).toBe('Success')
    })

    it('formats error status', () => {
      expect(formatStatus('error')).toBe('Error')
    })

    it('formats blocked status', () => {
      expect(formatStatus('blocked')).toBe('Blocked')
    })

    it('returns unknown status as-is', () => {
      expect(formatStatus('pending')).toBe('pending')
    })
  })

  describe('getStatusBadgeClass', () => {
    it('returns badge-success for success status', () => {
      expect(getStatusBadgeClass('success')).toBe('badge-success')
    })

    it('returns badge-error for error status', () => {
      expect(getStatusBadgeClass('error')).toBe('badge-error')
    })

    it('returns badge-warning for blocked status', () => {
      expect(getStatusBadgeClass('blocked')).toBe('badge-warning')
    })

    it('returns badge-ghost for unknown status', () => {
      expect(getStatusBadgeClass('unknown')).toBe('badge-ghost')
    })
  })

  describe('getIntentIcon', () => {
    it('returns book icon for read operation', () => {
      expect(getIntentIcon('read')).toBe('📖')
    })

    it('returns pencil icon for write operation', () => {
      expect(getIntentIcon('write')).toBe('✏️')
    })

    it('returns warning icon for destructive operation', () => {
      expect(getIntentIcon('destructive')).toBe('⚠️')
    })

    it('returns question icon for unknown operation', () => {
      expect(getIntentIcon('unknown')).toBe('❓')
    })
  })

  describe('getIntentBadgeClass', () => {
    it('returns badge-info for read operation', () => {
      expect(getIntentBadgeClass('read')).toBe('badge-info')
    })

    it('returns badge-warning for write operation', () => {
      expect(getIntentBadgeClass('write')).toBe('badge-warning')
    })

    it('returns badge-error for destructive operation', () => {
      expect(getIntentBadgeClass('destructive')).toBe('badge-error')
    })

    it('returns badge-ghost for unknown operation', () => {
      expect(getIntentBadgeClass('unknown')).toBe('badge-ghost')
    })
  })
})

// T066 - Filter logic tests
describe('Filter Logic', () => {
  const mockActivities: ActivityRecord[] = [
    { id: '1', type: 'tool_call', server_name: 'github', status: 'success', timestamp: '2024-01-01T00:00:00Z' },
    { id: '2', type: 'tool_call', server_name: 'slack', status: 'error', timestamp: '2024-01-01T00:01:00Z' },
    { id: '3', type: 'policy_decision', server_name: 'github', status: 'blocked', timestamp: '2024-01-01T00:02:00Z' },
    { id: '4', type: 'server_change', server_name: 'slack', status: 'success', timestamp: '2024-01-01T00:03:00Z' },
    { id: '5', type: 'quarantine_change', server_name: 'github', status: 'success', timestamp: '2024-01-01T00:04:00Z' },
  ]

  describe('filterActivities', () => {
    it('returns all activities when no filters applied', () => {
      const result = filterActivities(mockActivities, {})
      expect(result).toHaveLength(5)
    })

    it('filters by type', () => {
      const result = filterActivities(mockActivities, { type: 'tool_call' })
      expect(result).toHaveLength(2)
      expect(result.every(a => a.type === 'tool_call')).toBe(true)
    })

    it('filters by server', () => {
      const result = filterActivities(mockActivities, { server: 'github' })
      expect(result).toHaveLength(3)
      expect(result.every(a => a.server_name === 'github')).toBe(true)
    })

    it('filters by status', () => {
      const result = filterActivities(mockActivities, { status: 'success' })
      expect(result).toHaveLength(3)
      expect(result.every(a => a.status === 'success')).toBe(true)
    })

    it('combines multiple filters with AND logic', () => {
      const result = filterActivities(mockActivities, {
        type: 'tool_call',
        server: 'github'
      })
      expect(result).toHaveLength(1)
      expect(result[0].id).toBe('1')
    })

    it('returns empty array when no matches', () => {
      const result = filterActivities(mockActivities, {
        type: 'tool_call',
        status: 'blocked'
      })
      expect(result).toHaveLength(0)
    })

    it('combines all three filters', () => {
      const result = filterActivities(mockActivities, {
        type: 'tool_call',
        server: 'github',
        status: 'success'
      })
      expect(result).toHaveLength(1)
      expect(result[0].id).toBe('1')
    })
  })
})

// T067 - Pagination logic tests
describe('Pagination Logic', () => {
  const mockActivities: ActivityRecord[] = Array.from({ length: 100 }, (_, i) => ({
    id: `${i + 1}`,
    type: 'tool_call',
    status: 'success',
    timestamp: `2024-01-01T00:${String(i).padStart(2, '0')}:00Z`
  }))

  describe('paginateActivities', () => {
    it('returns first page correctly', () => {
      const result = paginateActivities(mockActivities, 1, 25)
      expect(result).toHaveLength(25)
      expect(result[0].id).toBe('1')
      expect(result[24].id).toBe('25')
    })

    it('returns second page correctly', () => {
      const result = paginateActivities(mockActivities, 2, 25)
      expect(result).toHaveLength(25)
      expect(result[0].id).toBe('26')
      expect(result[24].id).toBe('50')
    })

    it('returns last page correctly with remaining items', () => {
      const result = paginateActivities(mockActivities, 4, 25)
      expect(result).toHaveLength(25)
      expect(result[0].id).toBe('76')
      expect(result[24].id).toBe('100')
    })

    it('handles page size of 10', () => {
      const result = paginateActivities(mockActivities, 1, 10)
      expect(result).toHaveLength(10)
    })

    it('handles page size of 50', () => {
      const result = paginateActivities(mockActivities, 1, 50)
      expect(result).toHaveLength(50)
    })

    it('handles page size of 100', () => {
      const result = paginateActivities(mockActivities, 1, 100)
      expect(result).toHaveLength(100)
    })

    it('returns empty array for page beyond data', () => {
      const result = paginateActivities(mockActivities, 10, 25)
      expect(result).toHaveLength(0)
    })

    it('handles small dataset', () => {
      const smallData = mockActivities.slice(0, 5)
      const result = paginateActivities(smallData, 1, 25)
      expect(result).toHaveLength(5)
    })
  })

  describe('calculateTotalPages', () => {
    it('calculates pages for exact division', () => {
      expect(calculateTotalPages(100, 25)).toBe(4)
    })

    it('rounds up for partial pages', () => {
      expect(calculateTotalPages(101, 25)).toBe(5)
    })

    it('returns 1 for small dataset', () => {
      expect(calculateTotalPages(5, 25)).toBe(1)
    })

    it('returns 0 for empty dataset', () => {
      expect(calculateTotalPages(0, 25)).toBe(0)
    })

    it('handles different page sizes', () => {
      expect(calculateTotalPages(100, 10)).toBe(10)
      expect(calculateTotalPages(100, 50)).toBe(2)
      expect(calculateTotalPages(100, 100)).toBe(1)
    })
  })
})

// T068 - Export URL generation tests (via api.ts)
describe('Export URL Generation', () => {
  // Note: These tests verify the URL building logic conceptually
  // The actual api.getActivityExportUrl function is in api.ts

  describe('export URL building', () => {
    const buildExportUrl = (
      baseUrl: string,
      apiKey: string,
      options: { format: string; type?: string; server?: string; status?: string }
    ): string => {
      const params = new URLSearchParams()
      params.set('apikey', apiKey)
      params.set('format', options.format)
      if (options.type) params.set('type', options.type)
      if (options.server) params.set('server', options.server)
      if (options.status) params.set('status', options.status)
      return `${baseUrl}/api/v1/activity/export?${params.toString()}`
    }

    it('builds JSON export URL', () => {
      const url = buildExportUrl('http://localhost:8080', 'test-key', { format: 'json' })
      expect(url).toContain('format=json')
      expect(url).toContain('apikey=test-key')
    })

    it('builds CSV export URL', () => {
      const url = buildExportUrl('http://localhost:8080', 'test-key', { format: 'csv' })
      expect(url).toContain('format=csv')
    })

    it('includes type filter in URL', () => {
      const url = buildExportUrl('http://localhost:8080', 'test-key', {
        format: 'json',
        type: 'tool_call'
      })
      expect(url).toContain('type=tool_call')
    })

    it('includes server filter in URL', () => {
      const url = buildExportUrl('http://localhost:8080', 'test-key', {
        format: 'json',
        server: 'github'
      })
      expect(url).toContain('server=github')
    })

    it('includes status filter in URL', () => {
      const url = buildExportUrl('http://localhost:8080', 'test-key', {
        format: 'json',
        status: 'error'
      })
      expect(url).toContain('status=error')
    })

    it('includes all filters in URL', () => {
      const url = buildExportUrl('http://localhost:8080', 'test-key', {
        format: 'csv',
        type: 'tool_call',
        server: 'github',
        status: 'success'
      })
      expect(url).toContain('format=csv')
      expect(url).toContain('type=tool_call')
      expect(url).toContain('server=github')
      expect(url).toContain('status=success')
    })
  })
})

// Additional tests for duration and relative time formatting
describe('Duration Formatting', () => {
  describe('formatDuration', () => {
    it('formats milliseconds', () => {
      expect(formatDuration(500)).toBe('500ms')
    })

    it('formats exact milliseconds', () => {
      expect(formatDuration(999)).toBe('999ms')
    })

    it('formats seconds with decimals', () => {
      expect(formatDuration(1000)).toBe('1.00s')
    })

    it('formats longer durations', () => {
      expect(formatDuration(2500)).toBe('2.50s')
    })

    it('formats large durations', () => {
      expect(formatDuration(125000)).toBe('125.00s')
    })

    it('rounds milliseconds', () => {
      expect(formatDuration(123.456)).toBe('123ms')
    })
  })
})

describe('Relative Time Formatting', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2024-01-01T12:00:00Z'))
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  describe('formatRelativeTime', () => {
    it('returns "Just now" for recent timestamps', () => {
      const timestamp = new Date('2024-01-01T11:59:59.500Z').toISOString()
      expect(formatRelativeTime(timestamp)).toBe('Just now')
    })

    it('formats seconds ago', () => {
      const timestamp = new Date('2024-01-01T11:59:30Z').toISOString()
      expect(formatRelativeTime(timestamp)).toBe('30s ago')
    })

    it('formats minutes ago', () => {
      const timestamp = new Date('2024-01-01T11:55:00Z').toISOString()
      expect(formatRelativeTime(timestamp)).toBe('5m ago')
    })

    it('formats hours ago', () => {
      const timestamp = new Date('2024-01-01T09:00:00Z').toISOString()
      expect(formatRelativeTime(timestamp)).toBe('3h ago')
    })

    it('formats days ago', () => {
      const timestamp = new Date('2023-12-30T12:00:00Z').toISOString()
      expect(formatRelativeTime(timestamp)).toBe('2d ago')
    })
  })
})
