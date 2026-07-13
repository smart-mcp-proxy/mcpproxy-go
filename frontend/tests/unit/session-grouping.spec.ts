import { describe, it, expect } from 'vitest'
import {
  buildWorkSessionIndex,
  groupKeyOf,
  resolveSessionFilter,
  matchesSessionFilter,
} from '@/utils/sessionGrouping'
import type { ActivityRecord, MCPSession } from '@/types/api'

const rec = (p: Partial<ActivityRecord>): ActivityRecord =>
  ({ status: 'success', timestamp: '2026-07-13T05:30:00Z', ...p }) as ActivityRecord

// The bug this file exists for (Spec 082 SC-002).
//
// One opencode connection, transport session `mcp-session-…858cea`, wrote four
// activity records. The first two named built-ins that did not mark the session
// as worked, so they carry no work_session_id; the last two carry `ws-…000abe`.
// Grouping by `work_session_id || session_id` put them in two different buckets
// and the session picker showed ONE connection as TWO sessions — labelled with
// the tails of two different ids, `58cea` and `00abe`.
describe('work-session grouping', () => {
  const SID = 'mcp-session-9958d45e-015f-4c16-8d9a-74792c858cea'
  const WSID = 'ws-a3f505743e000abe'

  const opencodeRun: ActivityRecord[] = [
    rec({ session_id: SID, tool_name: 'list_registries' }), // no work_session_id
    rec({ session_id: SID, tool_name: 'upstream_servers' }), // no work_session_id
    rec({ session_id: SID, work_session_id: WSID, tool_name: 'retrieve_tools' }),
    rec({ session_id: SID, work_session_id: WSID, tool_name: 'echo' }),
  ]

  it('folds records with no work session into their connection’s work session', () => {
    const index = buildWorkSessionIndex(opencodeRun, [])
    const keys = new Set(opencodeRun.map(a => groupKeyOf(a, index)))

    expect(keys).toEqual(new Set([WSID]))
  })

  it('learns the mapping from the sessions API too, not only from sibling records', () => {
    // The orphaned rows on their own — the work session is only known because
    // /api/v1/sessions reports it for this transport session.
    const orphans = opencodeRun.slice(0, 2)
    const sessions = [{ id: SID, work_session_id: WSID } as MCPSession]

    const index = buildWorkSessionIndex(orphans, sessions)

    expect(orphans.map(a => groupKeyOf(a, index))).toEqual([WSID, WSID])
  })

  it('falls back to the transport session when no work session is known anywhere', () => {
    // Pre-082 rows, and clients whose work session was never resolved.
    const legacy = [rec({ session_id: 'sess-legacy', tool_name: 'echo' })]
    const index = buildWorkSessionIndex(legacy, [])

    expect(groupKeyOf(legacy[0], index)).toBe('sess-legacy')
  })

  it('keeps genuinely different work sessions apart', () => {
    // Same client reconnecting into DIFFERENT work (e.g. a different project):
    // two transport sessions, two work sessions. These must not be merged.
    const rows = [
      rec({ session_id: 'sess-a', work_session_id: 'ws-project-one' }),
      rec({ session_id: 'sess-b', work_session_id: 'ws-project-two' }),
    ]
    const index = buildWorkSessionIndex(rows, [])

    expect(rows.map(a => groupKeyOf(a, index))).toEqual(['ws-project-one', 'ws-project-two'])
  })

  it('groups a work session that spans several connections (the reconnect case)', () => {
    const rows = [
      rec({ session_id: 'sess-1', work_session_id: 'ws-same' }),
      rec({ session_id: 'sess-2', work_session_id: 'ws-same' }),
      rec({ session_id: 'sess-2' }), // orphan on the second connection
    ]
    const index = buildWorkSessionIndex(rows, [])

    expect(new Set(rows.map(a => groupKeyOf(a, index)))).toEqual(new Set(['ws-same']))
  })

  it('tolerates records with no session id at all', () => {
    const index = buildWorkSessionIndex([rec({ tool_name: 'echo' })], [])
    expect(groupKeyOf(rec({ tool_name: 'echo' }), index)).toBe('')
  })
})

// Sessions page → "View Activity" links with the row's TRANSPORT id, because
// that is what the Sessions table is a list of. The Activity page groups and
// filters by WORK session. So the link arrived carrying an id that matched no
// group and no dropdown option: an empty log and a blank Session picker, even
// though the session plainly had activity.
describe('session filter from a deep link', () => {
  const SID = 'mcp-session-edb5dc8a-84a6-48d0-a0a6-b03b3aa08ebd'
  const WSID = 'ws-3c571af4004fba33'

  const rows: ActivityRecord[] = [
    rec({ session_id: SID, work_session_id: WSID, tool_name: 'retrieve_tools' }),
    rec({ session_id: SID, work_session_id: WSID, tool_name: 'call_tool_read' }),
  ]
  const index = buildWorkSessionIndex(rows, [])

  it('translates a transport id into the work session the picker is keyed by', () => {
    expect(resolveSessionFilter(SID, index)).toBe(WSID)
  })

  it('leaves a work-session id alone (the dropdown’s own values must survive)', () => {
    expect(resolveSessionFilter(WSID, index)).toBe(WSID)
  })

  it('leaves an unknown id alone rather than dropping the filter', () => {
    expect(resolveSessionFilter('sess-never-seen', index)).toBe('sess-never-seen')
    expect(resolveSessionFilter('', index)).toBe('')
  })

  it('matches the session’s rows whether the filter is the transport or work id', () => {
    for (const filter of [SID, WSID]) {
      expect(rows.filter(a => matchesSessionFilter(a, filter, index))).toHaveLength(2)
    }
  })

  // The index is built from data that arrives asynchronously. Before it lands,
  // a transport-id filter must still show the connection's rows rather than an
  // empty log that then blinks into existence.
  it('matches on the raw transport id even before the index is populated', () => {
    const empty = new Map<string, string>()
    expect(rows.filter(a => matchesSessionFilter(a, SID, empty))).toHaveLength(2)
  })

  it('does not match rows from a different session', () => {
    const other = rec({ session_id: 'sess-other', work_session_id: 'ws-other' })
    expect(matchesSessionFilter(other, SID, index)).toBe(false)
    expect(matchesSessionFilter(other, WSID, index)).toBe(false)
  })
})
