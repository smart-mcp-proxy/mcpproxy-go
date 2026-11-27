import { describe, it, expect } from 'vitest'
import { toolExceptions, hasToolException, serverSectionRank, groupToolsByServer } from '@/utils/toolsGrouping'
import type { GlobalTool } from '@/types/api'

const tool = (over: Partial<GlobalTool>): GlobalTool => ({
  name: 't', server_name: 's', description: '', approval_status: 'approved', disabled: false, config_denied: false, usage: 0, tier: 'read', ...over,
} as GlobalTool)

describe('toolExceptions (FR-056)', () => {
  it('a read-only, approved, enabled tool has no label', () => {
    expect(toolExceptions(tool({}))).toEqual([])
    expect(hasToolException(tool({}))).toBe(false)
  })
  it.each([
    [{ tier: 'write' }, ['Writes']],
    [{ tier: 'destructive' }, ['Destructive']],
    [{ tier: undefined }, ['Unannotated']],
    [{ approval_status: 'pending' }, ['New, needs review']],
    [{ approval_status: 'changed' }, ['Changed, needs review']],
    [{ disabled: true }, ['Disabled']],
    [{ config_denied: true, disabled: true }, ['Denied by config']],
    [{ tier: 'write', approval_status: 'changed', disabled: true }, ['Writes', 'Changed, needs review', 'Disabled']],
  ] as const)('labels %j', (over, labels) => {
    expect(toolExceptions(tool(over as Partial<GlobalTool>)).map(e => e.label)).toEqual(labels)
  })
})

describe('section order (FR-055)', () => {
  it('ranks not usable, usable, disabled', () => {
    expect(serverSectionRank({ status: 'error' })).toBe(0)
    expect(serverSectionRank({ quarantined: true })).toBe(0)
    expect(serverSectionRank({ usable: false })).toBe(0)
    expect(serverSectionRank({ status: 'ready', usable: true })).toBe(1)
    expect(serverSectionRank(undefined)).toBe(1)
    expect(serverSectionRank({ enabled: false })).toBe(2)
  })
  it('sorts by rank then name and builds sections only from the rows given', () => {
    const info: Record<string, any> = { b: { status: 'error' }, c: { enabled: false } }
    const groups = groupToolsByServer(
      [tool({ server_name: 'z' }), tool({ server_name: 'c' }), tool({ server_name: 'b' }), tool({ server_name: 'a' }), tool({ server_name: 'a', name: 'u' })],
      name => info[name],
    )
    expect(groups.map(g => g.server)).toEqual(['b', 'a', 'z', 'c'])
    expect(groups.find(g => g.server === 'a')!.tools).toHaveLength(2)
  })
})
