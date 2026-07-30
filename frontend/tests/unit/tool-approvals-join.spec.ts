import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { joinHoldEvidence } from '@/utils/holdEvidence'
import type { ToolApproval } from '@/types'

// Spec 088 (T006, research D1, Codex plan-review R2/F1) — hold-evidence join.
//
// The tool-approvals panel must show WHY a tool is held, but the two candidate
// payloads each have half the answer:
//   GET .../tools/export  — DURABLE approval records (pending/blocked tools
//                           survive a disconnected server and an empty index)
//                           but deliberately carries no held_* fields
//                           (internal/httpapi/server.go).
//   GET .../tools         — carries held_reason/held_verdict/held_signals but is
//                           inventory-based, so it silently omits tools whose
//                           server is disconnected.
//
// Therefore: export stays the RECORD SOURCE and /tools is best-effort
// ENRICHMENT joined by tool name (bridging `tool_name` ↔ `name`). Every export
// record must survive the join, and a failed enrichment fetch must never drop a
// durable record — no Promise.all that rejects the whole call.

const TPA_HIDDEN = 'tpa.TPA-2026-0001.hidden_instruction'

function record(overrides: Partial<ToolApproval> = {}): ToolApproval {
  return {
    server_name: 'github',
    tool_name: 'create_issue',
    status: 'pending',
    hash: 'h1',
    description: 'Create an issue',
    ...overrides,
  }
}

describe('joinHoldEvidence (spec 088 T006)', () => {
  it('joins held_* onto the matching record, bridging tool_name ↔ name', () => {
    const records = [record()]
    const joined = joinHoldEvidence(records, [
      {
        name: 'create_issue',
        held_reason: 'scan_findings',
        held_verdict: 'dangerous',
        held_signals: [TPA_HIDDEN],
      },
    ])

    expect(joined).toHaveLength(1)
    expect(joined[0].held_reason).toBe('scan_findings')
    expect(joined[0].held_verdict).toBe('dangerous')
    expect(joined[0].held_signals).toEqual([TPA_HIDDEN])
    // Durable record fields survive untouched.
    expect(joined[0].tool_name).toBe('create_issue')
    expect(joined[0].status).toBe('pending')
  })

  it('keeps EVERY export record even when the enrichment inventory is missing tools', () => {
    const records = [
      record({ tool_name: 'create_issue' }),
      record({ tool_name: 'delete_repo', status: 'changed' }),
      record({ tool_name: 'list_issues', status: 'approved' }),
    ]

    const joined = joinHoldEvidence(records, [
      { name: 'delete_repo', held_reason: 'scan_coverage', held_verdict: 'clean' },
    ])

    expect(joined.map((t) => t.tool_name)).toEqual(['create_issue', 'delete_repo', 'list_issues'])
    expect(joined[1].held_reason).toBe('scan_coverage')
  })

  it('leaves a record with no join partner completely unchanged', () => {
    const original = record({ tool_name: 'orphan' })
    const joined = joinHoldEvidence([original], [{ name: 'someone_else', held_reason: 'scan_findings' }])

    expect(joined[0]).toBe(original)
    expect(joined[0].held_reason).toBeUndefined()
    expect(joined[0].held_verdict).toBeUndefined()
    expect(joined[0].held_signals).toBeUndefined()
  })

  it('leaves a record unchanged when its partner carries no evidence (approved tool)', () => {
    const original = record({ tool_name: 'create_issue', status: 'approved' })
    const joined = joinHoldEvidence([original], [{ name: 'create_issue' }])
    expect(joined[0]).toBe(original)
    expect(joined[0].held_reason).toBeUndefined()
  })

  it('returns ALL export records unchanged when the enrichment list is null/undefined', () => {
    const records = [record({ tool_name: 'a' }), record({ tool_name: 'b' })]
    expect(joinHoldEvidence(records, null)).toEqual(records)
    expect(joinHoldEvidence(records, undefined)).toEqual(records)
    expect(joinHoldEvidence(records, [])).toEqual(records)
  })

  it('returns an empty list when there are no export records', () => {
    expect(joinHoldEvidence(null, [{ name: 'x', held_reason: 'scan_findings' }])).toEqual([])
    expect(joinHoldEvidence(undefined, null)).toEqual([])
  })

  it('does not mutate the input records or share the signals array', () => {
    const original = record()
    const signals = [TPA_HIDDEN]
    const joined = joinHoldEvidence([original], [
      { name: 'create_issue', held_reason: 'scan_findings', held_signals: signals },
    ])

    expect(original.held_reason).toBeUndefined()
    expect(joined[0].held_signals).not.toBe(signals)
    joined[0].held_signals!.push('mutated')
    expect(signals).toEqual([TPA_HIDDEN])
  })
})

describe('api.getToolApprovals — export records + best-effort evidence enrichment', () => {
  const exportBody = {
    success: true,
    data: {
      tools: [
        {
          server_name: 'github',
          tool_name: 'create_issue',
          status: 'pending',
          hash: 'h1',
          description: 'Create an issue',
        },
        {
          server_name: 'github',
          tool_name: 'list_issues',
          status: 'approved',
          hash: 'h2',
          description: 'List issues',
          disabled: true,
        },
      ],
      count: 2,
    },
  }

  const toolsBody = {
    success: true,
    data: {
      tools: [
        {
          name: 'create_issue',
          held_reason: 'scan_findings',
          held_verdict: 'dangerous',
          held_signals: [TPA_HIDDEN, 'phrase.injection'],
        },
      ],
    },
  }

  let fetchMock: ReturnType<typeof vi.fn>

  function stubFetch(toolsHandler: (url: string) => unknown) {
    fetchMock = vi.fn(async (url: string) => {
      if (url.includes('/tools/export')) {
        // Fresh copy per call — a real fetch parses a new body every time, and
        // the API service assigns the joined list back onto response.data.
        return { ok: true, status: 200, json: async () => structuredClone(exportBody) }
      }
      return toolsHandler(url)
    })
    vi.stubGlobal('fetch', fetchMock)
  }

  beforeEach(() => {
    vi.resetModules()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.resetModules()
  })

  async function freshApi() {
    const mod = await import('@/services/api')
    return mod.default
  }

  it('fetches the export records and the tools inventory, and returns joined records', async () => {
    stubFetch(() => ({ ok: true, status: 200, json: async () => structuredClone(toolsBody) }))
    const api = await freshApi()

    const res = await api.getToolApprovals('github')

    expect(res.success).toBe(true)
    const urls = fetchMock.mock.calls.map((c) => c[0])
    expect(urls).toContain('/api/v1/servers/github/tools/export')
    expect(urls).toContain('/api/v1/servers/github/tools')

    const tools = res.data!.tools
    expect(tools).toHaveLength(2)
    expect(tools[0].tool_name).toBe('create_issue')
    expect(tools[0].held_reason).toBe('scan_findings')
    expect(tools[0].held_verdict).toBe('dangerous')
    expect(tools[0].held_signals).toEqual([TPA_HIDDEN, 'phrase.injection'])
    // Record with no evidence partner keeps its existing shape…
    expect(tools[1].held_reason).toBeUndefined()
    // …including the existing enabled/disabled normalization.
    expect(tools[1].disabled).toBe(true)
    expect(tools[1].enabled).toBe(false)
    expect(tools[0].disabled).toBe(false)
    expect(tools[0].enabled).toBe(true)
  })

  it('returns every export record unchanged when the enrichment request fails (HTTP error)', async () => {
    stubFetch(() => ({ ok: false, status: 500, statusText: 'Server Error', json: async () => ({}) }))
    const api = await freshApi()

    const res = await api.getToolApprovals('github')

    expect(res.success).toBe(true)
    expect(res.data!.tools).toHaveLength(2)
    expect(res.data!.tools[0].held_reason).toBeUndefined()
  })

  it('returns every export record unchanged when the enrichment fetch rejects outright', async () => {
    stubFetch(() => {
      throw new Error('network down')
    })
    const api = await freshApi()

    const res = await api.getToolApprovals('github')

    expect(res.success).toBe(true)
    expect(res.data!.tools.map((t) => t.tool_name)).toEqual(['create_issue', 'list_issues'])
  })

  it('percent-encodes a "/"-containing server name on both requests', async () => {
    stubFetch(() => ({ ok: true, status: 200, json: async () => structuredClone(toolsBody) }))
    const api = await freshApi()

    await api.getToolApprovals('io.github.owner/repo')

    const urls = fetchMock.mock.calls.map((c) => c[0])
    expect(urls).toContain('/api/v1/servers/io.github.owner%2Frepo/tools/export')
    expect(urls).toContain('/api/v1/servers/io.github.owner%2Frepo/tools')
  })
})
