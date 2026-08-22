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
  statusPresentation,
  intentPresentation,
  compactSummaryParts,
  activeFilterChips,
  activeFilterCount,
  activityDetailsText,
  showParentBadgeInTypeColumn,
  shortId,
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

// Status treatment: success is the norm and must NOT shout. Colour is reserved
// for the rows that need a human; everything else is quiet.
describe('statusPresentation', () => {
  it('renders success as plain muted text — no pill, no green', () => {
    const p = statusPresentation('success')
    expect(p.label).toBe('Success')
    expect(p.pill).toBe(false)
    expect(p.tone).toBe('muted')
    expect(p.className).not.toContain('badge')
    expect(p.className).not.toContain('success')
    expect(p.className).toContain('text-base-content/60')
  })

  it('keeps a red pill for error', () => {
    const p = statusPresentation('error')
    expect(p).toMatchObject({ label: 'Error', pill: true, tone: 'error' })
    expect(p.className).toContain('badge-error')
  })

  it('keeps an amber pill for blocked', () => {
    const p = statusPresentation('blocked')
    expect(p).toMatchObject({ label: 'Blocked', pill: true, tone: 'warning' })
    expect(p.className).toContain('badge-warning')
  })

  it('gives rejected a NEUTRAL pill, not the old info blue', () => {
    const p = statusPresentation('rejected')
    expect(p).toMatchObject({ label: 'Rejected', pill: true, tone: 'neutral' })
    expect(p.className).toContain('badge-ghost')
    expect(p.className).not.toContain('badge-info')
  })

  it('treats any other status as a neutral oddity, keeping its raw label', () => {
    const p = statusPresentation('timeout')
    expect(p).toMatchObject({ label: 'timeout', pill: true, tone: 'neutral' })
    expect(p.className).toContain('badge-ghost')
  })

  it('only ever colours statuses that need attention', () => {
    const coloured = ['success', 'error', 'blocked', 'rejected', 'weird']
      .filter(s => statusPresentation(s).tone === 'error' || statusPresentation(s).tone === 'warning')
    expect(coloured).toEqual(['error', 'blocked'])
  })
})

// Intent: the REASON is the payload, the operation type is a glyph in front of
// it. The old cell showed a coloured `read` pill and hid the reason in a tooltip.
describe('intentPresentation', () => {
  it('renders glyph + reason and a full hover title', () => {
    const p = intentPresentation({ operation_type: 'read', reason: 'list open issues' })
    expect(p.present).toBe(true)
    expect(p.icon).toBe('📖')
    expect(p.label).toBe('read')
    expect(p.reason).toBe('list open issues')
    expect(p.title).toBe('read — list open issues')
  })

  it('uses the pencil glyph for write and the warning glyph for destructive', () => {
    expect(intentPresentation({ operation_type: 'write' }).icon).toBe('✏️')
    expect(intentPresentation({ operation_type: 'destructive' }).icon).toBe('⚠️')
  })

  it('falls back to just the glyph when no reason was declared', () => {
    const p = intentPresentation({ operation_type: 'write' })
    expect(p.present).toBe(true)
    expect(p.reason).toBe('')
    expect(p.title).toBe('write')
  })

  it('ignores a whitespace-only reason', () => {
    expect(intentPresentation({ operation_type: 'read', reason: '   ' }).reason).toBe('')
  })

  it('is absent when no operation type was declared (caller renders a dash)', () => {
    expect(intentPresentation(undefined).present).toBe(false)
    expect(intentPresentation(null).present).toBe(false)
    expect(intentPresentation({}).present).toBe(false)
    expect(intentPresentation({ reason: 'orphan reason' }).present).toBe(false)
  })
})

// Compact header strip: total plus only the counts that want attention.
describe('compactSummaryParts', () => {
  it('shows the total plus non-zero attention counts', () => {
    const parts = compactSummaryParts({
      total_count: 54,
      error_count: 6,
      blocked_count: 1,
      rejected_count: 0,
    })
    expect(parts.map(p => p.label)).toEqual(['54 calls', '6 errors', '1 blocked'])
    expect(parts.map(p => p.tone)).toEqual(['muted', 'error', 'warning'])
  })

  it('omits zero counts entirely rather than printing a reassuring "0 errors"', () => {
    const parts = compactSummaryParts({ total_count: 12, error_count: 0, blocked_count: 0 })
    expect(parts).toHaveLength(1)
    expect(parts[0]).toMatchObject({ key: 'total', label: '12 calls', status: '' })
  })

  it('gives rejected a neutral tone', () => {
    const parts = compactSummaryParts({ total_count: 3, rejected_count: 2 })
    expect(parts[1]).toMatchObject({ key: 'rejected', label: '2 rejected', tone: 'neutral' })
  })

  it('singularises', () => {
    expect(compactSummaryParts({ total_count: 1, error_count: 1 }).map(p => p.label))
      .toEqual(['1 call', '1 error'])
  })

  it('carries the status each segment filters to', () => {
    const parts = compactSummaryParts({ total_count: 9, error_count: 1, blocked_count: 1 })
    expect(parts.map(p => p.status)).toEqual(['', 'error', 'blocked'])
  })

  it('returns nothing when the summary has not loaded', () => {
    expect(compactSummaryParts(null)).toEqual([])
    expect(compactSummaryParts(undefined)).toEqual([])
  })
})

// Compact-strip active-filter summary: what is narrowing the list is visible
// even when the filter grid itself is collapsed.
describe('activeFilterChips', () => {
  it('is empty when nothing is filtered', () => {
    expect(activeFilterChips({})).toEqual([])
    expect(activeFilterCount({})).toBe(0)
  })

  it('emits one chip per selected type, icon included', () => {
    const chips = activeFilterChips({ types: ['tool_call', 'preflight'] })
    expect(chips.map(c => c.label)).toEqual(['🔧 Tool Call', '🛫 Preflight'])
    expect(chips.map(c => c.kind)).toEqual(['type', 'type'])
    expect(chips.map(c => c.value)).toEqual(['tool_call', 'preflight'])
  })

  it('always includes the sub-call chip, shortening the correlation id', () => {
    const chips = activeFilterChips({ parentId: 'req-parent-abcdefgh' })
    expect(chips).toHaveLength(1)
    expect(chips[0].kind).toBe('parent')
    expect(chips[0].label).toBe(`↳ Sub-calls of ${shortId('req-parent-abcdefgh')}`)
    expect(chips[0].title).toContain('req-parent-abcdefgh')
  })

  it('labels the single-value filters', () => {
    const chips = activeFilterChips({
      server: 'github',
      status: 'error',
      authType: 'agent',
      agentName: 'claude',
      sensitiveData: 'true',
      severity: 'critical',
      session: 'sess-0123456789',
      sessionLabel: 'Claude Code · 14:32',
      startDate: '',
      endDate: '',
    })
    expect(chips.map(c => c.label)).toEqual([
      'Server: github',
      'Status: error',
      'Auth: 🤖 Agent',
      'Agent: claude',
      'Sensitive: ⚠️ Detected',
      'Severity: critical',
      'Session: Claude Code · 14:32',
    ])
  })

  it('falls back to a shortened session id when no label resolved', () => {
    const chips = activeFilterChips({ session: 'sess-0123456789' })
    expect(chips[0].label).toBe(`Session: ${shortId('sess-0123456789')}`)
  })

  it('counts every active filter for the Filters toggle badge', () => {
    expect(
      activeFilterCount({ types: ['tool_call', 'preflight'], server: 'github', parentId: 'req-1' })
    ).toBe(4)
  })

  it('keys are unique so the chips can drive a v-for', () => {
    const chips = activeFilterChips({ types: ['tool_call', 'preflight'], server: 'a', status: 'error' })
    expect(new Set(chips.map(c => c.key)).size).toBe(chips.length)
  })
})

// De-duplication of the code_execution marker: one line, once.
describe('code_execution marker placement', () => {
  const PARENT_WITH_TOOL_NAME = {
    type: 'internal_tool_call',
    tool_name: 'code_execution',
    metadata: { internal_tool_name: 'code_execution' },
  }

  it('reads the Details text a row will print', () => {
    expect(activityDetailsText(PARENT_WITH_TOOL_NAME)).toBe('code_execution')
    expect(activityDetailsText({ type: 'server_change', metadata: { action: 'enabled' } })).toBe('enabled')
    expect(activityDetailsText({ type: 'tool_call' })).toBe('')
    expect(activityDetailsText(null)).toBe('')
  })

  it('drops the Type-column badge when Details already says code_execution', () => {
    expect(showParentBadgeInTypeColumn(PARENT_WITH_TOOL_NAME)).toBe(false)
  })

  it('keeps the Type-column badge when Details would say nothing', () => {
    expect(
      showParentBadgeInTypeColumn({
        type: 'internal_tool_call',
        metadata: { internal_tool_name: 'code_execution' },
      })
    ).toBe(true)
  })

  it('never badges a non-parent row', () => {
    expect(showParentBadgeInTypeColumn({ type: 'tool_call', tool_name: 'code_execution' })).toBe(false)
    expect(showParentBadgeInTypeColumn(null)).toBe(false)
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
