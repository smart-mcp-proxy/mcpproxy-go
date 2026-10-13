import { describe, it, expect } from 'vitest'
import {
  formatType,
  getTypeIcon,
  formatPreflightSummary,
  preflightReasonRollup,
  preflightPerTool,
  isPreflightActivity,
} from '../../src/utils/activity'

/**
 * Spec 098 (T023) — the Web UI activity view must render a `preflight` record
 * with its verdict instead of an empty row (US3 acceptance 3).
 *
 * A preflight is set-scoped: server_name and tool_name are empty by
 * construction and everything readable lives in `metadata`
 * ({verdict, ids_count, reasons{code:count}, per_tool[{id,status,reason?}]}).
 */

const blockedMetadata = {
  verdict: 'blocked',
  ids_count: 4,
  reasons: { server_disabled: 2, tool_changed: 1 },
  per_tool: [
    { id: 'ctl:echo', status: 'ready' },
    { id: 'gh:sync', status: 'unavailable', reason: 'server_disabled' },
    { id: 'gh:close', status: 'unavailable', reason: 'server_disabled' },
    { id: 'slack:post', status: 'unavailable', reason: 'tool_changed' },
  ],
}

describe('preflight activity type', () => {
  it('labels the preflight type', () => {
    expect(formatType('preflight')).toBe('Preflight')
  })

  it('gives the preflight type its own icon', () => {
    expect(getTypeIcon('preflight')).toBe('🛫')
    expect(getTypeIcon('preflight')).not.toBe(getTypeIcon('unknown_type'))
  })

  it('recognises preflight records', () => {
    expect(isPreflightActivity({ type: 'preflight' })).toBe(true)
    expect(isPreflightActivity({ type: 'tool_call' })).toBe(false)
    expect(isPreflightActivity(null)).toBe(false)
  })
})

describe('preflightReasonRollup', () => {
  it('orders by count desc, then reason asc, for a stable line', () => {
    expect(preflightReasonRollup(blockedMetadata)).toEqual([
      { reason: 'server_disabled', count: 2 },
      { reason: 'tool_changed', count: 1 },
    ])

    expect(
      preflightReasonRollup({ reasons: { server_disabled: 1, hash_mismatch: 1, not_found: 1 } })
    ).toEqual([
      { reason: 'hash_mismatch', count: 1 },
      { reason: 'not_found', count: 1 },
      { reason: 'server_disabled', count: 1 },
    ])
  })

  it('is empty for a record without reasons', () => {
    expect(preflightReasonRollup({ verdict: 'ready' })).toEqual([])
    expect(preflightReasonRollup(undefined)).toEqual([])
  })
})

describe('formatPreflightSummary', () => {
  it('names the verdict, the count and the reason rollup', () => {
    expect(formatPreflightSummary(blockedMetadata)).toBe(
      'blocked (4 tools): server_disabled x2, tool_changed x1'
    )
  })

  it('renders an all-ready run without reasons', () => {
    expect(formatPreflightSummary({ verdict: 'ready', ids_count: 2, reasons: {} })).toBe(
      'ready (2 tools)'
    )
  })

  it('does not pluralize a single tool', () => {
    expect(formatPreflightSummary({ verdict: 'ready', ids_count: 1 })).toBe('ready (1 tool)')
  })

  it('caps the rollup so a 100-id run cannot blow up the column', () => {
    const summary = formatPreflightSummary({
      verdict: 'blocked',
      ids_count: 5,
      reasons: {
        server_disabled: 1,
        tool_changed: 1,
        not_found: 1,
        hash_mismatch: 1,
        oauth_required: 1,
      },
    })
    expect(summary).toContain('+2 more')
    expect(summary.match(/ x1/g)).toHaveLength(3)
  })

  it('falls back to per_tool length when ids_count is absent', () => {
    expect(
      formatPreflightSummary({
        verdict: 'unknown_ids',
        per_tool: [{ id: 'a:1', status: 'unavailable', reason: 'not_found' }],
      })
    ).toBe('unknown_ids (1 tool): not_found x1')
  })

  it('is empty for metadata that carries no verdict', () => {
    expect(formatPreflightSummary({})).toBe('')
    expect(formatPreflightSummary(undefined)).toBe('')
    expect(formatPreflightSummary({ intent: { operation_type: 'read' } })).toBe('')
  })
})

describe('preflightPerTool', () => {
  it('keeps the requested order and carries the reason only when unavailable', () => {
    const perTool = preflightPerTool(blockedMetadata)
    expect(perTool.map(t => t.id)).toEqual(['ctl:echo', 'gh:sync', 'gh:close', 'slack:post'])
    expect(perTool[0].reason).toBeUndefined()
    expect(perTool[1].reason).toBe('server_disabled')
  })

  it('tolerates a record without per-tool detail', () => {
    expect(preflightPerTool({ verdict: 'ready' })).toEqual([])
    expect(preflightPerTool(undefined)).toEqual([])
  })

  it('drops malformed entries instead of rendering undefined rows', () => {
    expect(
      preflightPerTool({ per_tool: ['nope', null, { id: 'ok:1', status: 'ready' }] })
    ).toEqual([{ id: 'ok:1', status: 'ready' }])
  })
})
