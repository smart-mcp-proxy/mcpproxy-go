import { describe, it, expect } from 'vitest'
import {
  formatLatencyBound,
  neverCompleted,
  partitionUsageTools,
} from '@/utils/usageFormat'

// Audit finding F22 (#1046): "Calls per tool", "Token sinks" and "Errors &
// latency" all listed `everything:doesnotexist` and `broken-remote:whatever` as
// first-class tools with 100% error rates. Those names came from FAILED calls —
// a typo the agent made, and a server that was never reachable. No such tool
// exists on any upstream, and charting them invents a catalog out of mistakes.
//
// The same finding: every row of the p50/p95 table read exactly "10 ms" while
// the Activity Log showed 3/4/5 ms for the same calls. A percentile read off a
// histogram is the upper BOUND of a bucket, and the first bucket used to be
// everything under 10ms — which is where local MCP servers live.

const tool = (over: Record<string, number> = {}) => ({
  server: 'everything',
  tool: 'echo',
  calls: 10,
  errors: 0,
  ...over,
})

describe('unresolved tool names', () => {
  it('a name that never completed a call is not a tool', () => {
    expect(neverCompleted(tool({ calls: 2, errors: 2 }))).toBe(true)
    // Only blocked or shed attempts: nothing ever executed.
    expect(neverCompleted(tool({ calls: 0, errors: 0 }))).toBe(true)
  })

  it('one success is enough to vouch for it, however bad the rest', () => {
    expect(neverCompleted(tool({ calls: 100, errors: 99 }))).toBe(false)
  })

  it('splits the list without losing or duplicating a row', () => {
    const tools = [
      tool({ calls: 10, errors: 0 }),
      tool({ calls: 2, errors: 2 }),
      tool({ calls: 6, errors: 1 }),
    ]
    const { completed, unresolved } = partitionUsageTools(tools)

    expect(completed).toHaveLength(2)
    expect(unresolved).toHaveLength(1)
    expect(completed.length + unresolved.length).toBe(tools.length)
  })

  it('is self-correcting: a name that later succeeds rejoins the charts', () => {
    const failing = tool({ calls: 3, errors: 3 })
    expect(partitionUsageTools([failing]).completed).toHaveLength(0)

    const recovered = { ...failing, calls: 4, errors: 3 }
    expect(partitionUsageTools([recovered]).completed).toHaveLength(1)
  })
})

describe('latency percentiles are bounds, not measurements', () => {
  it('renders a bounded bucket as an upper bound', () => {
    expect(formatLatencyBound(5, false)).toBe('≤ 5 ms')
    expect(formatLatencyBound(2500, false)).toBe('≤ 2.5 s')
  })

  it('inverts for the unbounded overflow bucket', () => {
    // The histogram stops at 10s; a slower call is only known to be past it, so
    // "≤ 10 s" would be a false reading of the same number.
    expect(formatLatencyBound(10000, true)).toBe('> 10.0 s')
  })

  it('says nothing rather than "0 ms" when there is no data', () => {
    expect(formatLatencyBound(0)).toBe('—')
    expect(formatLatencyBound(null)).toBe('—')
    expect(formatLatencyBound(undefined)).toBe('—')
  })
})
