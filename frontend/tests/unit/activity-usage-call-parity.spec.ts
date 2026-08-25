import { describe, it, expect } from 'vitest'
import { compactSummaryParts } from '../../src/utils/activity'
import { usageHeadline } from '../../src/utils/usageFormat'

// Audit finding F1 (#1046): the Activity Log header said "70 calls" while the
// Usage tab said "42 tool calls", same instance, same 24 hours, ~20s apart.
// Both numbers were real; neither label was.

describe('Activity header counts events and calls apart (F1/F24)', () => {
  it('labels the row total "events" and prints the call count beside it', () => {
    const parts = compactSummaryParts({
      total_count: 70,
      call_count: 42,
      error_count: 6,
      blocked_count: 0,
      rejected_count: 0,
    })
    expect(parts.map(p => p.label)).toEqual(['70 events', '42 calls', '6 errors'])
  })

  it('never calls a system-start row a call', () => {
    // First-run instance: one System Start row and nothing else. The header
    // used to read "1 call".
    const parts = compactSummaryParts({ total_count: 1, call_count: 0 })
    expect(parts.map(p => p.label)).toEqual(['1 event', '0 calls'])
  })

  it('collapses to one segment when every row IS a call', () => {
    const parts = compactSummaryParts({ total_count: 12, call_count: 12 })
    expect(parts.map(p => p.label)).toEqual(['12 calls'])
    expect(parts[0]).toMatchObject({ key: 'total', status: '' })
  })

  it('keeps the event total clickable and the call count inert', () => {
    // Clicking a count drives the STATUS filter, and no status selects "calls".
    const [events, calls] = compactSummaryParts({ total_count: 70, call_count: 42 })
    expect(events).toMatchObject({ key: 'total', status: '', filterable: true })
    expect(calls).toMatchObject({ key: 'calls', filterable: false })
  })

  it('falls back to events-only rather than guessing when call_count is absent', () => {
    const parts = compactSummaryParts({ total_count: 70, error_count: 6 })
    expect(parts.map(p => p.label)).toEqual(['70 events', '6 errors'])
  })
})

describe('Usage tiles read the server total, not a client-side sum (F1)', () => {
  // The tiles used to be `tools.reduce((s, t) => s + t.calls, 0)`, which is a
  // lifetime-cumulative rollup of UPSTREAM tools only, truncated to top-N —
  // three ways to disagree with the histogram printed directly beneath it.
  const usage = {
    total_calls: 25,
    total_errors: 6,
    tools: [
      { calls: 13, errors: 6 },
      { calls: 6, errors: 0 },
    ],
    other: { tools_folded: 4, calls: 4, total_resp_bytes: 0 },
    timeline: [
      { calls: 20, errors: 5 },
      { calls: 5, errors: 1 },
    ],
  }

  it('takes calls and errors from the response', () => {
    expect(usageHeadline(usage)).toMatchObject({ calls: 25, errors: 6 })
  })

  it('agrees with the timeline it renders', () => {
    const barCalls = usage.timeline.reduce((s, b) => s + b.calls, 0)
    const barErrors = usage.timeline.reduce((s, b) => s + b.errors, 0)
    expect(usageHeadline(usage)).toMatchObject({ calls: barCalls, errors: barErrors })
  })

  it('computes the error rate over the same denominator', () => {
    expect(usageHeadline(usage).errorRate).toBe('24.0')
  })

  it('is 0.0% rather than NaN on an empty window', () => {
    expect(usageHeadline({ total_calls: 0, total_errors: 0 })).toMatchObject({
      calls: 0,
      errors: 0,
      errorRate: '0.0',
    })
  })

  it('handles a missing payload', () => {
    expect(usageHeadline(null)).toMatchObject({ calls: 0, errors: 0, errorRate: '0.0' })
  })
})
