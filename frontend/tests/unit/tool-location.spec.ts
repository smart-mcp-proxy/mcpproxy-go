import { describe, it, expect } from 'vitest'
import type { SecurityScanFinding } from '@/types/api'
import { parseToolLocation, toolFocusPath, groupFindingsByTool } from '@/utils/toolLocation'

// Phase 1 (TPA inline findings) — `finding.location` is the ONLY join key
// between a scan report and the tool list, and it is a free-form string shared
// by scanners that mean different things by it.
//
// The baseline scanner builds it as `server + ":" + tool` (aggregate.go), where
// the server half legitimately contains '.' and '/':
//
//     com.googleapis.sqladmin/mcp:create_user
//
// so a first-colon split silently produces server "com" — a link to a server
// that does not exist. Meanwhile engine.go emits "tool:<name>" (cisco),
// "<targetType>:<targetName>" (yara), "<issueType>:<subject>" (ramparts, where
// the subject is free prose) and bare file paths. Every one of those must come
// back null so the caller falls back to today's inert <code>, rather than
// linking somewhere wrong.

function finding(overrides: Partial<SecurityScanFinding> = {}): SecurityScanFinding {
  return {
    threat_type: 'tool_poisoning',
    threat_level: 'dangerous',
    title: 'Tool poisoning',
    description: 'detected',
    ...overrides,
  }
}

describe('parseToolLocation — the last-colon split', () => {
  it('splits a plain server:tool pair', () => {
    expect(parseToolLocation('github:create_issue', { expectedServer: 'github' })).toEqual({
      server: 'github',
      tool: 'create_issue',
    })
  })

  it('splits on the LAST colon so dotted, slashed server names survive intact', () => {
    expect(
      parseToolLocation('com.googleapis.sqladmin/mcp:create_user', {
        expectedServer: 'com.googleapis.sqladmin/mcp',
      }),
    ).toEqual({
      server: 'com.googleapis.sqladmin/mcp',
      tool: 'create_user',
    })
  })

  it('handles an official-registry name containing both a slash and dots', () => {
    expect(
      parseToolLocation('io.github.owner/repo:search_code', {
        expectedServer: 'io.github.owner/repo',
      }),
    ).toEqual({
      server: 'io.github.owner/repo',
      tool: 'search_code',
    })
  })

  it('handles a server name that itself contains a colon', () => {
    expect(
      parseToolLocation('host:8080:list_files', { expectedServer: 'host:8080' }),
    ).toEqual({
      server: 'host:8080',
      tool: 'list_files',
    })
  })

  it('trims surrounding whitespace', () => {
    expect(
      parseToolLocation('  github:create_issue  ', { expectedServer: 'github' }),
    ).toEqual({
      server: 'github',
      tool: 'create_issue',
    })
  })
})

// Every case here supplies an expectedServer that WOULD match the string's
// leading segment, so each one exercises a shape guard rather than passing
// vacuously on the missing-anchor rule proved in the block above.
describe('parseToolLocation — locations that are NOT a tool', () => {
  it('returns null for a bare file path (engine.go SARIF findings)', () => {
    expect(
      parseToolLocation('target/dist/tools/get-env.js', { expectedServer: 'srv' }),
    ).toBeNull()
    expect(parseToolLocation('config.json', { expectedServer: 'srv' })).toBeNull()
  })

  it('returns null for the cisco scanner "tool:<name>" form on another server', () => {
    expect(parseToolLocation('tool:add_numbers', { expectedServer: 'github' })).toBeNull()
  })

  it('returns null for the SARIF "path:line" form even when anchored', () => {
    expect(
      parseToolLocation('src/index.js:42', { expectedServer: 'srv' }),
    ).toBeNull()
  })

  it('returns null when the subject is prose rather than a tool name', () => {
    expect(
      parseToolLocation('prompt:the system prompt for review', { expectedServer: 'prompt' }),
    ).toBeNull()
    expect(
      parseToolLocation('mcp-server:tool with spaces', { expectedServer: 'mcp-server' }),
    ).toBeNull()
  })

  it('returns null for a Windows path, which last-colon-splits into garbage', () => {
    expect(
      parseToolLocation('C:\\Users\\dev\\server\\index.js', { expectedServer: 'C' }),
    ).toBeNull()
  })

  it('returns null for empty, missing or degenerate values', () => {
    const anchored = { expectedServer: ['github', ''] }
    expect(parseToolLocation(undefined, anchored)).toBeNull()
    expect(parseToolLocation(null, anchored)).toBeNull()
    expect(parseToolLocation('', anchored)).toBeNull()
    expect(parseToolLocation('   ', anchored)).toBeNull()
    expect(parseToolLocation('github', anchored)).toBeNull()
    expect(parseToolLocation('github:', anchored)).toBeNull()
    expect(parseToolLocation(':create_issue', anchored)).toBeNull()
    expect(parseToolLocation(':', anchored)).toBeNull()
  })
})

describe('parseToolLocation — expectedServer is the strongest guard', () => {
  it('accepts only an exact server match', () => {
    expect(
      parseToolLocation('github:create_issue', { expectedServer: 'github' }),
    ).toEqual({ server: 'github', tool: 'create_issue' })
    expect(parseToolLocation('github:create_issue', { expectedServer: 'gitlab' })).toBeNull()
  })

  it('rejects a cisco "tool:<name>" location even on a matching-looking report', () => {
    expect(parseToolLocation('tool:add_numbers', { expectedServer: 'github' })).toBeNull()
  })

  it('accepts any of several candidate names (report name vs route param)', () => {
    expect(
      parseToolLocation('io.github.owner/repo:search_code', {
        expectedServer: ['github', 'io.github.owner/repo'],
      }),
    ).toEqual({ server: 'io.github.owner/repo', tool: 'search_code' })
  })

  it('resolves a server genuinely NAMED "tool" on its own report', () => {
    // The anchor is an exact string match, so a name that collides with another
    // scanner's prefix vocabulary is not penalised for it.
    expect(parseToolLocation('tool:add_numbers', { expectedServer: 'tool' })).toEqual({
      server: 'tool',
      tool: 'add_numbers',
    })
  })

  // Regression: without an anchor there is nothing to distinguish a real
  // server:tool pair from a SARIF `URI + ":" + startLine` location such as
  // 'src/index.js:42' (scanner/sarif.go). The old prefix heuristic accepted both
  // and the caller rendered a confident link to a server that does not exist.
  // Refusing to parse is what makes the caller fall back to today's inert code.
  it('refuses to guess when the caller cannot name the server', () => {
    for (const missing of [undefined, null, '', '   ', []] as const) {
      expect(
        parseToolLocation('github:create_issue', { expectedServer: missing as never }),
      ).toBeNull()
    }
    expect(parseToolLocation('github:create_issue')).toBeNull()
    expect(parseToolLocation('src/index.js:42')).toBeNull()
  })
})

describe('toolFocusPath', () => {
  it('deep-links into the Tools tab with the tool percent-encoded', () => {
    expect(toolFocusPath('github', 'create_issue')).toBe(
      '/servers/github?tab=tools&tool=create_issue',
    )
  })

  it('percent-encodes a server name containing a slash (MCP-1112)', () => {
    expect(toolFocusPath('io.github.owner/repo', 'search_code')).toBe(
      '/servers/io.github.owner%2Frepo?tab=tools&tool=search_code',
    )
  })
})

describe('groupFindingsByTool', () => {
  it('groups by tool and hoists dangerous groups above warnings', () => {
    const groups = groupFindingsByTool(
      [
        finding({ location: 'github:list_issues', threat_level: 'warning', rule_id: 'detect.a' }),
        finding({ location: 'github:create_issue', threat_level: 'dangerous', rule_id: 'detect.b' }),
        finding({ location: 'github:create_issue', threat_level: 'warning', rule_id: 'detect.c' }),
      ],
      'github',
    )
    expect(groups.map((g) => g.tool)).toEqual(['create_issue', 'list_issues'])
    expect(groups[0].level).toBe('dangerous')
    expect(groups[0].findings).toHaveLength(2)
    expect(groups[0].counts).toEqual({ dangerous: 1, warning: 1, info: 0 })
    expect(groups[0].ruleIds).toEqual(['detect.b', 'detect.c'])
    expect(groups[1].level).toBe('warning')
  })

  it('drops findings whose location is not a tool on this server', () => {
    const groups = groupFindingsByTool(
      [
        finding({ location: 'tool:add_numbers' }),
        finding({ location: 'target/dist/index.js' }),
        finding({ location: 'other-server:create_issue' }),
        finding({ location: undefined }),
        finding({ location: 'github:create_issue' }),
      ],
      'github',
    )
    expect(groups.map((g) => g.tool)).toEqual(['create_issue'])
  })

  it('de-duplicates rule ids within a group', () => {
    const groups = groupFindingsByTool(
      [
        finding({ location: 'github:t', rule_id: 'detect.x' }),
        finding({ location: 'github:t', rule_id: 'detect.x' }),
      ],
      'github',
    )
    expect(groups[0].ruleIds).toEqual(['detect.x'])
  })

  it('returns an empty array for no findings — absence is the clean state', () => {
    expect(groupFindingsByTool([], 'github')).toEqual([])
    expect(groupFindingsByTool(null, 'github')).toEqual([])
    expect(groupFindingsByTool(undefined, 'github')).toEqual([])
  })
})
