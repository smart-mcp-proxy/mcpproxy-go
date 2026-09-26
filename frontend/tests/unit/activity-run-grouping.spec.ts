import { describe, it, expect } from 'vitest'
import {
  formatRunDuration,
  formatRunSpan,
  groupActivityRuns,
  quarantineBatchSummary,
  runDurationRange,
} from '@/utils/activity'

// Audit finding F5 (#1046): twelve consecutive `everything:echo` calls produced
// twelve rows, each repeating the same three-line timestamp, the same reason and
// the same word "Success". A real export was 1479 calls in 809 runs, with runs
// up to 100×. The table is scanned, so a hundred identical rows cost a hundred
// rows of attention and carry one row of information.
//
// The compression is only safe if it can never hide an outcome. That is the
// property most of these tests are about.

const call = (over: Record<string, unknown> = {}) => ({
  id: `id-${Math.random().toString(36).slice(2)}`,
  type: 'tool_call',
  server_name: 'everything',
  tool_name: 'echo',
  status: 'success',
  timestamp: '2026-08-21T10:00:00Z',
  duration_ms: 4,
  ...over,
})

describe('groupActivityRuns', () => {
  it('folds consecutive identical calls into one run', () => {
    const rows = Array.from({ length: 12 }, () => call())
    const runs = groupActivityRuns(rows)

    expect(runs).toHaveLength(1)
    expect(runs[0].count).toBe(12)
    expect(runs[0].lead).toBe(rows[0])
    expect(runs[0].rows).toHaveLength(12)
  })

  it('NEVER mixes outcomes: a status change splits the run', () => {
    const rows = [
      call({ id: 'a' }),
      call({ id: 'b' }),
      call({ id: 'c', status: 'error' }),
      call({ id: 'd' }),
      call({ id: 'e' }),
    ]
    const runs = groupActivityRuns(rows)

    expect(runs.map(r => r.count)).toEqual([2, 1, 2])
    expect(runs[1].lead.status).toBe('error')
    // The failure is its own row: "echo ×5" could never swallow it.
    expect(runs.some(r => r.count > 1 && r.lead.status === 'error')).toBe(false)
  })

  it('only folds rows that are ADJACENT', () => {
    const rows = [
      call({ id: 'a' }),
      call({ id: 'b', tool_name: 'add' }),
      call({ id: 'c' }),
    ]
    expect(groupActivityRuns(rows).map(r => r.count)).toEqual([1, 1, 1])
  })

  it('keeps different servers and different tools apart', () => {
    const rows = [
      call({ id: 'a', server_name: 'memory' }),
      call({ id: 'b', server_name: 'everything' }),
      call({ id: 'c', server_name: 'everything', tool_name: 'add' }),
    ]
    expect(groupActivityRuns(rows).map(r => r.count)).toEqual([1, 1, 1])
  })

  it('never folds away a sensitive-data marker', () => {
    const rows = [
      call({ id: 'a' }),
      call({ id: 'b', has_sensitive_data: true }),
      call({ id: 'c' }),
    ]
    expect(groupActivityRuns(rows).map(r => r.count)).toEqual([1, 1, 1])
  })

  it('never folds a code_execution sub-call into a direct call of the same tool', () => {
    const rows = [call({ id: 'a' }), call({ id: 'b', parent_id: 'exec-1' })]
    expect(groupActivityRuns(rows).map(r => r.count)).toEqual([1, 1])
  })

  // The identity has to cover EVERY field the collapsed line prints, not just
  // the outcome. These three were folds the first version of the key allowed.

  it('never folds a write under a read lead', () => {
    // call_tool_read and call_tool_write against the same tool produce records
    // identical in type, server, tool and status. The Intent column would then
    // print "📖 read" on behalf of a run containing writes.
    const rows = [
      call({ id: 'a', metadata: { intent: { operation_type: 'read', reason: 'same' } } }),
      call({ id: 'b', metadata: { intent: { operation_type: 'write', reason: 'same' } } }),
      call({ id: 'c', metadata: { intent: { operation_type: 'write', reason: 'same' } } }),
    ]
    const runs = groupActivityRuns(rows)
    expect(runs.map(r => r.count)).toEqual([1, 2])
    expect((runs[1].lead.metadata as any).intent.operation_type).toBe('write')
  })

  it('never folds a critical detection under a low-severity lead', () => {
    // The Sensitive badge prints the severity glyph and the detection count, so
    // the boolean alone is not what the row displays.
    const rows = [
      call({ id: 'a', has_sensitive_data: true, max_severity: 'low', detection_types: ['high_entropy'] }),
      call({ id: 'b', has_sensitive_data: true, max_severity: 'critical', detection_types: ['cloud_credentials'] }),
    ]
    expect(groupActivityRuns(rows).map(r => r.count)).toEqual([1, 1])
  })

  it('never folds two rows that differ only in how many detections they carry', () => {
    const rows = [
      call({ id: 'a', has_sensitive_data: true, max_severity: 'high', detection_types: ['api_token'] }),
      call({ id: 'b', has_sensitive_data: true, max_severity: 'high', detection_types: ['api_token', 'private_key'] }),
    ]
    expect(groupActivityRuns(rows).map(r => r.count)).toEqual([1, 1])
  })

  it('is collision-safe against separator characters inside free-form fields', () => {
    // A delimiter-joined key would make these two agree; the fields are
    // free-form (a server may be named anything the user typed).
    const rows = [
      call({ id: 'a', server_name: 'my server', tool_name: 'echo' }),
      call({ id: 'b', server_name: 'my', tool_name: 'server echo' }),
    ]
    expect(groupActivityRuns(rows).map(r => r.count)).toEqual([1, 1])
  })

  it('keeps two different config changes apart even though both carry no tool', () => {
    const rows = [
      { id: 'a', type: 'config_change', status: 'success', metadata: { action: 'server_added' } },
      { id: 'b', type: 'config_change', status: 'success', metadata: { action: 'server_removed' } },
      { id: 'c', type: 'config_change', status: 'success', metadata: { action: 'server_removed' } },
    ]
    expect(groupActivityRuns(rows).map(r => r.count)).toEqual([1, 2])
  })

  it('reports when folded calls declared different reasons instead of letting one speak for all', () => {
    const rows = [
      call({ id: 'a', metadata: { intent: { operation_type: 'read', reason: 'poll the echo endpoint' } } }),
      call({ id: 'b', metadata: { intent: { operation_type: 'read', reason: 'poll the echo endpoint' } } }),
    ]
    expect(groupActivityRuns(rows)[0].reasonsVary).toBe(false)

    const varied = [
      call({ id: 'a', metadata: { intent: { operation_type: 'read', reason: 'poll the echo endpoint' } } }),
      call({ id: 'b', metadata: { intent: { operation_type: 'read', reason: 'confirm the server is alive' } } }),
    ]
    const run = groupActivityRuns(varied)[0]
    // Reasons vary call to call in real logs, so keying on them would stop the
    // compression working; the run says so rather than fold silently.
    expect(run.count).toBe(2)
    expect(run.reasonsVary).toBe(true)
  })

  it('disabled: one run per row, in order', () => {
    const rows = Array.from({ length: 5 }, () => call())
    const runs = groupActivityRuns(rows, false)

    expect(runs).toHaveLength(5)
    expect(runs.every(r => r.count === 1)).toBe(true)
    expect(runs.map(r => r.lead)).toEqual(rows)
  })

  it('preserves order and loses no row', () => {
    const rows = [
      call({ id: 'a' }),
      call({ id: 'b' }),
      call({ id: 'c', status: 'error' }),
      call({ id: 'd', tool_name: 'add' }),
    ]
    const flattened = groupActivityRuns(rows).flatMap(r => r.rows)
    expect(flattened).toEqual(rows)
  })

  it('handles an empty list', () => {
    expect(groupActivityRuns([])).toEqual([])
  })
})

// Spec 109-k (activity-scope-filters), acceptance scenario 6, and the
// verified zcode review finding on this fix: a tool_quarantine_change run
// folds WITHOUT agreeing on tool_name (unlike every other type above), so a
// server's batch of per-tool baseline approvals reads as "N tools approved"
// instead of 14 near-identical rows — but only within a bounded window, so
// two genuinely separate same-server, same-status actions hours apart never
// silently merge into one summary line just because nothing of a different
// type happened to land between them.
describe('groupActivityRuns — tool_quarantine_change batch fold', () => {
  const quarantine = (over: Record<string, unknown> = {}) => ({
    id: `qc-${Math.random().toString(36).slice(2)}`,
    type: 'tool_quarantine_change',
    server_name: 'filesystem',
    tool_name: 'some_tool',
    status: 'approved',
    timestamp: '2026-08-21T10:00:00Z',
    ...over,
  })

  it('folds 14 per-tool approvals (different tool_name each) into one run', () => {
    const rows = Array.from({ length: 14 }, (_, i) => quarantine({ id: `qc-${i}`, tool_name: `tool_${i}` }))
    const runs = groupActivityRuns(rows)
    expect(runs).toHaveLength(1)
    expect(runs[0].count).toBe(14)
    expect(quarantineBatchSummary(runs[0].lead, runs[0].count)).toBe('filesystem: 14 tools approved')
  })

  it('does NOT fold two same-server, same-status batches more than 5 minutes apart', () => {
    const rows = [
      quarantine({ id: 'a', tool_name: 'x', timestamp: '2026-08-21T10:00:00Z' }),
      quarantine({ id: 'b', tool_name: 'y', timestamp: '2026-08-21T10:01:00Z' }),
      // A second, later action on the same server — nothing of a different
      // type happened to land between them, so identity/adjacency alone
      // would otherwise merge it into the first run.
      quarantine({ id: 'c', tool_name: 'z', timestamp: '2026-08-21T10:30:00Z' }),
    ]
    const runs = groupActivityRuns(rows)
    expect(runs).toHaveLength(2)
    expect(runs[0].count).toBe(2)
    expect(runs[1].count).toBe(1)
    expect(runs[1].lead.id).toBe('c')
  })

  it('does fold within the 5-minute window, even with a gap between individual records', () => {
    const rows = [
      quarantine({ id: 'a', tool_name: 'x', timestamp: '2026-08-21T10:00:00Z' }),
      quarantine({ id: 'b', tool_name: 'y', timestamp: '2026-08-21T10:04:00Z' }),
    ]
    expect(groupActivityRuns(rows)).toHaveLength(1)
  })

  it('still requires server and status to agree, exactly like the identity fields for every other type', () => {
    const rows = [
      quarantine({ id: 'a', tool_name: 'x', server_name: 'filesystem' }),
      quarantine({ id: 'b', tool_name: 'y', server_name: 'github' }),
      quarantine({ id: 'c', tool_name: 'z', server_name: 'filesystem', status: 'blocked' }),
    ]
    const runs = groupActivityRuns(rows)
    expect(runs).toHaveLength(3)
  })

  it('a lone tool_quarantine_change record is not folded (no "1 tools" label)', () => {
    const runs = groupActivityRuns([quarantine({ id: 'a', tool_name: 'read' })])
    expect(runs).toHaveLength(1)
    expect(runs[0].count).toBe(1)
  })
})

describe('what a folded row prints', () => {
  it('reports one duration when the run agreed, and a range when it did not', () => {
    expect(formatRunDuration([call({ duration_ms: 4 }), call({ duration_ms: 4 })])).toBe('4ms')
    expect(formatRunDuration([call({ duration_ms: 3 }), call({ duration_ms: 9 })])).toBe('3ms–9ms')
  })

  it('has nothing to say when no member timed itself', () => {
    expect(runDurationRange([call({ duration_ms: undefined })])).toBeNull()
    expect(formatRunDuration([call({ duration_ms: undefined })])).toBe('')
  })

  it('names the span a run covers, and stays quiet about an instant', () => {
    const rows = [
      call({ timestamp: '2026-08-21T10:04:00Z' }),
      call({ timestamp: '2026-08-21T10:00:00Z' }),
    ]
    expect(formatRunSpan(rows)).toBe('over 4m')

    // A single row is not a span, and neither is a burst inside one second.
    expect(formatRunSpan([rows[0]])).toBe('')
    expect(
      formatRunSpan([
        call({ timestamp: '2026-08-21T10:00:00.100Z' }),
        call({ timestamp: '2026-08-21T10:00:00.400Z' }),
      ])
    ).toBe('')
  })
})
