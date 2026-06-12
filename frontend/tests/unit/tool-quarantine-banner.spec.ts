import { describe, it, expect } from 'vitest'
import { selectQuarantinedTools } from '@/utils/toolQuarantine'
import type { ToolApproval } from '@/types'

// MCP-2101 (Spec 032, parent MCP-2081): the per-server Tool-Quarantine banner
// must stop nagging for freshly-`pending` baseline tools and must never show
// alongside the server-level Security Quarantine banner.
//
// Trust model (confirmed on MCP-2081): approving a *server* promotes its
// baseline `pending` tools to `approved` on the backend. So a baseline
// `pending` tool is NOT a reason to surface a tool-level banner — only a
// `changed` tool (a rug-pull) is.

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

describe('selectQuarantinedTools (MCP-2101)', () => {
  it('suppresses the tool banner entirely while the server is quarantined', () => {
    // Even a rug-pull `changed` tool must not surface a SECOND banner while the
    // server-level Security Quarantine banner is up — operator approves the
    // server first, then the backend promotes baseline pending→approved.
    const tools = [
      approval({ tool_name: 'a', status: 'changed' }),
      approval({ tool_name: 'b', status: 'pending' }),
    ]
    expect(selectQuarantinedTools(tools, true)).toEqual([])
  })

  it('does NOT surface freshly-pending baseline tools (the core fix)', () => {
    // Not quarantined, no changed tool → baseline `pending` tools alone must
    // not raise the banner. Pre-fix this returned the pending tools.
    const tools = [
      approval({ tool_name: 'a', status: 'pending' }),
      approval({ tool_name: 'b', status: 'pending' }),
      approval({ tool_name: 'c', status: 'approved' }),
    ]
    expect(selectQuarantinedTools(tools, false)).toEqual([])
  })

  it('surfaces a `changed` (rug-pull) tool when the server is not quarantined', () => {
    const changed = approval({ tool_name: 'rugpull', status: 'changed' })
    const tools = [changed, approval({ tool_name: 'fine', status: 'approved' })]
    expect(selectQuarantinedTools(tools, false)).toEqual([changed])
  })

  it('once a change has surfaced, also includes residual pending tools', () => {
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
