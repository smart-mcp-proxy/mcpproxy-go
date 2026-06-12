import { describe, it, expect } from 'vitest'
import { selectQuarantinedTools } from '../../src/utils/toolQuarantine'
import type { ToolApproval } from '../../src/types'

// MCP-2081 (Spec 032): the tool-quarantine banner must key off `changed`
// (rug-pull) tools — NOT baseline `pending` tools — and must be suppressed
// entirely while the server-level Security Quarantine banner is showing.

function approval(tool_name: string, status: ToolApproval['status']): ToolApproval {
  return { server_name: 'srv', tool_name, status, hash: `h-${tool_name}`, description: `${tool_name} desc` }
}

describe('selectQuarantinedTools (MCP-2081)', () => {
  it('does NOT surface the banner for baseline pending tools alone', () => {
    const approvals = [approval('a', 'pending'), approval('b', 'pending')]
    expect(selectQuarantinedTools(approvals, false)).toEqual([])
  })

  it('surfaces only the changed tool when a rug-pull is detected', () => {
    const approvals = [approval('a', 'approved'), approval('b', 'changed')]
    const result = selectQuarantinedTools(approvals, false)
    expect(result.map((t) => t.tool_name)).toEqual(['b'])
  })

  it('includes residual pending tools alongside a changed tool (batch clear)', () => {
    const approvals = [approval('a', 'changed'), approval('b', 'pending'), approval('c', 'approved')]
    const result = selectQuarantinedTools(approvals, false)
    expect(result.map((t) => t.tool_name).sort()).toEqual(['a', 'b'])
  })

  it('suppresses the tool-quarantine banner entirely while the server is quarantined', () => {
    // Even a changed tool is hidden: the operator approves the server first.
    const approvals = [approval('a', 'changed'), approval('b', 'pending')]
    expect(selectQuarantinedTools(approvals, true)).toEqual([])
  })

  it('returns empty for no approvals or all-approved tools', () => {
    expect(selectQuarantinedTools([], false)).toEqual([])
    expect(selectQuarantinedTools(null, false)).toEqual([])
    expect(selectQuarantinedTools([approval('a', 'approved')], false)).toEqual([])
  })
})
