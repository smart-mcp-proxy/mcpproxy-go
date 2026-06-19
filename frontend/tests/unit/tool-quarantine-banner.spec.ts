import { describe, it, expect } from 'vitest'
import { selectQuarantinedTools } from '@/utils/toolQuarantine'
import type { ToolApproval } from '@/types'

// MCP-2917 (Spec 032, parent MCP-2916): the per-server Tool-Quarantine banner
// must surface BOTH `pending` (new, never-approved) and `changed` (rug-pull)
// tools whenever the server itself is NOT quarantined, and must never show
// alongside the server-level Security Quarantine banner.
//
// This intentionally reverses the MCP-2101 "don't nag on a pending baseline"
// behavior for non-quarantined servers: on a live, non-quarantined server a
// `pending` tool is genuinely BLOCKED by the backend (checkToolApprovals →
// BlockedTools) and the Servers page already counts it (pending_count +
// changed_count). The banner and the count must agree, so the operator gets
// the Approve/Block buttons to clear pending tools. While the server is still
// quarantined the server-level banner covers everything, so we suppress the
// tool-level banner to avoid two banners at once.

function approval(over: Partial<ToolApproval>): ToolApproval {
  return {
    server_name: 's',
    tool_name: 't',
    status: 'approved',
    hash: 'h',
    description: 'd',
    ...over,
  }
}

describe('selectQuarantinedTools (MCP-2917)', () => {
  it('suppresses the tool banner entirely while the server is quarantined', () => {
    // The server-level Security Quarantine banner already covers it and the
    // operator must approve the server first — never show two banners at once.
    const tools = [
      approval({ tool_name: 'a', status: 'changed' }),
      approval({ tool_name: 'b', status: 'pending' }),
    ]
    expect(selectQuarantinedTools(tools, true)).toEqual([])
  })

  it('surfaces freshly-pending tools on a non-quarantined server (the core fix)', () => {
    // Regression for MCP-2917: an all-`pending`, not-quarantined server used to
    // hide the banner (the old `hasChanged` early-return) even though every
    // tool is genuinely blocked. Now they must surface for approval.
    const a = approval({ tool_name: 'a', status: 'pending' })
    const b = approval({ tool_name: 'b', status: 'pending' })
    const tools = [a, b, approval({ tool_name: 'c', status: 'approved' })]
    const result = selectQuarantinedTools(tools, false)
    expect(result).toContain(a)
    expect(result).toContain(b)
    expect(result).not.toContainEqual(approval({ tool_name: 'c', status: 'approved' }))
  })

  it('surfaces a `changed` (rug-pull) tool when the server is not quarantined', () => {
    const changed = approval({ tool_name: 'rugpull', status: 'changed' })
    const tools = [changed, approval({ tool_name: 'fine', status: 'approved' })]
    expect(selectQuarantinedTools(tools, false)).toEqual([changed])
  })

  it('surfaces both pending and changed tools together when not quarantined', () => {
    const changed = approval({ tool_name: 'rugpull', status: 'changed' })
    const pending = approval({ tool_name: 'leftover', status: 'pending' })
    const tools = [changed, pending, approval({ tool_name: 'fine', status: 'approved' })]
    const result = selectQuarantinedTools(tools, false)
    expect(result).toContain(changed)
    expect(result).toContain(pending)
    expect(result).not.toContainEqual(approval({ tool_name: 'fine', status: 'approved' }))
  })

  it('returns empty when every tool is approved', () => {
    const tools = [
      approval({ tool_name: 'a', status: 'approved' }),
      approval({ tool_name: 'b', status: 'approved' }),
    ]
    expect(selectQuarantinedTools(tools, false)).toEqual([])
  })

  it('returns empty for an empty approval list', () => {
    expect(selectQuarantinedTools([], false)).toEqual([])
    expect(selectQuarantinedTools([], true)).toEqual([])
  })
})
