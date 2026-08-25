import { describe, it, expect } from 'vitest'
import {
  CALL_STATUSES,
  OTHER_STATUS,
  isOtherStatus,
  statusBucketSum,
  statusBucketTiles,
  activeFilterChips,
} from '@/utils/activity'

// Audit finding F2 (#1046): the Activity Log's own filter tiles did not sum.
// The row read `Total (24h) 42 · Success 15 · Errors 4 · Blocked 0 · Rejected 0`
// — four buckets adding to 19 under a denominator of 42. The four ARE the
// tool-call status vocabulary, but the activity log is wider than tool calls:
// a quarantine change stores its action in `status`, a policy decision its
// verdict. Those rows were in the total and in no bucket.
//
// The summary endpoint now returns the residual as `other_count` and the tiles
// render it, so the row is a partition. These tests are what keeps it one.

const summary = (over: Record<string, number> = {}) => ({
  period: '24h',
  total_count: 42,
  call_count: 30,
  success_count: 15,
  error_count: 4,
  blocked_count: 0,
  rejected_count: 0,
  other_count: 23,
  ...over,
})

describe('Activity status tiles — a partition, not a selection', () => {
  it('sums to the denominator printed above it', () => {
    const s = summary()
    expect(statusBucketSum(s)).toBe(s.total_count)
  })

  it('names the residual rather than dropping it', () => {
    const tiles = statusBucketTiles(summary())
    const other = tiles.find(t => t.status === OTHER_STATUS)

    expect(other).toBeDefined()
    expect(other!.count).toBe(23)
    expect(other!.label).toBe('Other / internal')
    // It has to explain itself: "Other 23" with no hover is a new question.
    expect(other!.title).toMatch(/quarantine|policy|bookkeeping/i)
  })

  it('omits the Other tile when there is no residual, and still sums', () => {
    const s = summary({ total_count: 19, other_count: 0 })
    const tiles = statusBucketTiles(s)

    expect(tiles.map(t => t.status)).toEqual([...CALL_STATUSES])
    expect(statusBucketSum(s)).toBe(19)
  })

  it('keeps the four vocabulary tiles even at zero — absent reads as untracked', () => {
    const tiles = statusBucketTiles(summary({ error_count: 0, blocked_count: 0, other_count: 27 }))
    expect(tiles.map(t => t.status)).toContain('error')
    expect(tiles.map(t => t.status)).toContain('blocked')
  })

  it('spends colour only on the outcomes that want a human', () => {
    const tiles = statusBucketTiles(summary())
    const tone = (status: string) => tiles.find(t => t.status === status)!.tone

    expect(tone('error')).toBe('error')
    expect(tone('blocked')).toBe('warning')
    // Success is the norm and the residual is bookkeeping: neither shouts.
    expect(tone('success')).toBe('muted')
    expect(tone(OTHER_STATUS)).toBe('neutral')
  })

  it('renders nothing at all without a summary', () => {
    expect(statusBucketTiles(null)).toEqual([])
    expect(statusBucketSum(undefined)).toBe(0)
  })
})

describe('the Other bucket as a filter', () => {
  it('matches exactly the statuses outside the tool-call vocabulary', () => {
    for (const status of CALL_STATUSES) {
      expect(isOtherStatus(status)).toBe(false)
    }
    expect(isOtherStatus('tool_auto_approved')).toBe(true)
    expect(isOtherStatus('allow')).toBe(true)
    // A record with no status at all is not a tool-call outcome either.
    expect(isOtherStatus('')).toBe(true)
    expect(isOtherStatus(undefined)).toBe(true)
  })

  it('reads as words in the active-filter chip, not as a raw sentinel', () => {
    const [chip] = activeFilterChips({ status: OTHER_STATUS })
    expect(chip.label).toBe('Status: other / internal')
  })
})
