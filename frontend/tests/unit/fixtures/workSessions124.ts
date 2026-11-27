// Spec 124 PR-3: shared fixtures for the work-session list and timeline tests.

/** A failed, ended session as an administrator reads it. */
export const WS_FAILED = {
  id: 'ws-aaa111',
  token_name: 'nightly-bot',
  client_id: 'cursor',
  client_name: 'cursor-agent',
  project_name: 'payments',
  surface: 'mcp',
  profile: 'work-readonly',
  profile_source: 'pin',
  status: 'ended',
  started_at: '2026-11-26T22:00:00.000Z',
  last_activity_at: '2026-11-26T22:41:05.000Z',
  ended_at: '2026-11-26T22:41:05.000Z',
  duration_ms: 2_465_000,
  attempts: 120,
  failures: 7,
  policy_blocks: 2,
  stored_failures: 5,
  first_failure_at: '2026-11-26T22:12:00.000Z',
  last_failure: {
    at: '2026-11-26T22:40:00.250Z',
    error_class: 'auth',
    error_code: 'MCPX_OAUTH_REFRESH_FAILED',
    server: 'github',
    tool: 'list_issues',
    reason: 'The upstream server needs a login.',
  },
  connection_count: 3,
  truncated_start_at: '2026-11-26T22:10:00.000Z',
}

/** A clean, active REST run. */
export const WS_OK = {
  id: 'ws-bbb222',
  surface: 'rest',
  run_name: 'nightly-2026-11-26',
  status: 'active',
  started_at: '2026-11-27T08:00:00.000Z',
  last_activity_at: '2026-11-27T08:00:12.000Z',
  duration_ms: 12_000,
  attempts: 4,
  failures: 0,
  policy_blocks: 0,
  stored_failures: 0,
}

/** What a scoped caller reads: no profile, connection count or truncated marker. */
export const WS_SCOPED = {
  id: 'ws-ccc333',
  token_name: 'ro-bot',
  status: 'ended',
  started_at: '2026-11-26T10:00:00.000Z',
  last_activity_at: '2026-11-26T10:05:00.000Z',
  ended_at: '2026-11-26T10:05:00.000Z',
  duration_ms: 300_000,
  attempts: 9,
  failures: 1,
  policy_blocks: 1,
  stored_failures: 1,
  last_failure: { at: '2026-11-26T10:04:00.000Z', error_class: 'proxy_policy', policy_block: true },
}

const call = (id: string, at: string, extra: Record<string, unknown> = {}) => ({
  id,
  type: 'tool_call',
  status: 'success',
  timestamp: at,
  server_name: 'github',
  tool_name: 'list_issues',
  duration_ms: 42,
  request_id: `req-${id}`,
  ...extra,
})

/** One page of a timeline positioned at failure 2 of 5 (row index 14). */
export function timelinePage(overrides: Record<string, unknown> = {}) {
  return {
    session: WS_FAILED,
    rows: [
      { index: 10, kind: 'session_start', at: '2026-11-26T22:00:00.000Z' },
      { index: 11, kind: 'call', at: '2026-11-26T22:11:58.100Z', activity: call('a11', '2026-11-26T22:11:58.100Z') },
      {
        index: 12,
        kind: 'call',
        at: '2026-11-26T22:12:00.000Z',
        failed: true,
        activity: call('a12', '2026-11-26T22:12:00.000Z', { status: 'error', error_message: 'connection refused', error_class: 'network' }),
      },
      {
        index: 13,
        kind: 'upstream_event',
        at: '2026-11-26T22:39:59.650Z',
        activity: {
          id: 'e13',
          type: 'upstream_event',
          status: 'error',
          timestamp: '2026-11-26T22:39:59.650Z',
          server_name: 'github',
          // A problem event carries its fixed text as the row's error.
          error_message: 'The token refresh failed.',
          metadata: {
            kind: 'oauth_refresh_failed',
            code: 'MCPX_OAUTH_REFRESH_FAILED',
            count: 3,
            first_at: '2026-11-26T22:39:59.650Z',
            last_at: '2026-11-26T22:40:00.125Z',
          },
        },
      },
      {
        index: 14,
        kind: 'call',
        at: '2026-11-26T22:40:00.250Z',
        failed: true,
        activity: call('a14', '2026-11-26T22:40:00.250Z', {
          status: 'error',
          error_message: 'The upstream server needs a login.',
          error_class: 'auth',
        }),
      },
      {
        index: 15,
        kind: 'policy_decision',
        at: '2026-11-26T22:40:01.000Z',
        failed: true,
        policy_block: true,
        bodies_removed: true,
        activity: call('a15', '2026-11-26T22:40:01.000Z', { type: 'policy_decision', status: 'blocked', tool_name: 'delete_repo' }),
      },
      { index: 16, kind: 'session_end', at: '2026-11-26T22:41:05.000Z' },
    ],
    position: 14,
    total_rows: 17,
    failure_index: 2,
    failure_total: 5,
    lifetime_failures: 7,
    earlier_failures_not_stored: true,
    has_before: true,
    has_after: false,
    ...overrides,
  }
}
