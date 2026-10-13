import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { HEALTH_STATUS_LABELS } from '@/types/contracts'
import { ACTIVITY_VIEW_LABELS } from '@/utils/activity'
import { TIER_LABELS, TOOL_APPROVAL_LABELS, tierLabel, toolApprovalLabel } from '@/utils/toolQuarantine'

// Spec 109-m (FR-090, T144, M3): the six terminology enums, decoded from the
// ONE golden internal/contracts/testdata/terminology.json that Go
// (TestSpec109TerminologyGolden) and XCTest (Spec109TerminologyTests) also read.
// Every constant is matched as an exact generated line, never by substring.

interface Family {
  values: string[]
  labels?: Record<string, string>
  rank?: Record<string, number>
  retired?: string[]
  cli?: string[]
}
const golden = JSON.parse(
  readFileSync(resolve(__dirname, '../../../internal/contracts/testdata/terminology.json'), 'utf8'),
) as Record<string, Family | string>
const family = (name: string) => golden[name] as Family

const contractsTs = readFileSync(resolve(__dirname, '../../src/types/contracts.ts'), 'utf8').split(/\r?\n/)
const lineSet = new Set(contractsTs)

const camel = (v: string) => v.split('_').map(w => w[0].toUpperCase() + w.slice(1)).join('')

// golden family -> [generated const prefix, generated union type]
const TS_FAMILIES: Record<string, [string, string]> = {
  health_status: ['HealthStatus', 'HealthStatusValue'],
  attention_kind: ['AttentionKind', 'AttentionKind'],
  tier: ['Tier', 'Tier'],
  tool_approval: ['ToolApproval', 'ToolApprovalState'],
  activity_view: ['ActivityView', 'ActivityView'],
  client_presence: ['ClientPresence', 'ClientPresenceState'],
}

function unionMembers(typeName: string): string[] {
  const start = contractsTs.indexOf(`export type ${typeName} =`)
  expect(start, `export type ${typeName} is generated`).toBeGreaterThanOrEqual(0)
  const members: string[] = []
  for (let i = start + 1; i < contractsTs.length; i++) {
    const m = /^ {2}\| typeof (\w+);?$/.exec(contractsTs[i])
    if (!m) break
    members.push(m[1])
    if (contractsTs[i].endsWith(';')) break
  }
  return members
}

describe('terminology.json against the generated contracts.ts', () => {
  for (const [name, [prefix, typeName]] of Object.entries(TS_FAMILIES)) {
    it(`${name}: one exact const line per value and an exact union`, () => {
      const values = family(name).values
      expect(values.length).toBeGreaterThan(0)
      for (const v of values) {
        expect(lineSet.has(`export const ${prefix}${camel(v)} = '${v}' as const;`), `${prefix}${camel(v)}`).toBe(true)
      }
      expect(unionMembers(typeName)).toEqual(values.map(v => `${prefix}${camel(v)}`))
    })
  }
})

describe('terminology.json against the Web label helpers', () => {
  it('health status labels equal the generated table', () => {
    expect(HEALTH_STATUS_LABELS).toEqual(family('health_status').labels)
  })

  it('tool review state labels equal the golden and never use a retired name', () => {
    const f = family('tool_approval')
    expect(TOOL_APPROVAL_LABELS).toEqual(f.labels)
    for (const v of f.values) expect(toolApprovalLabel(v)).toBe(f.labels![v])
    for (const retired of f.retired!) {
      expect(Object.values(TOOL_APPROVAL_LABELS)).not.toContain(retired)
    }
  })

  it('tier labels equal the golden; a missing tier reads Unannotated', () => {
    const f = family('tier')
    expect(TIER_LABELS).toEqual(f.labels)
    for (const v of f.values) expect(tierLabel(v)).toBe(f.labels![v])
    expect(tierLabel(undefined)).toBe('Unannotated')
  })

  it('activity view tab labels equal the golden', () => {
    const f = family('activity_view')
    expect(Object.keys(ACTIVITY_VIEW_LABELS)).toEqual(f.values)
    expect(ACTIVITY_VIEW_LABELS).toEqual(f.labels)
  })

  it('the CLI view list is the web list without sessions', () => {
    const f = family('activity_view')
    expect(f.cli).toEqual(f.values.filter(v => v !== 'sessions'))
  })

  it('attention ranks ascend along the value order', () => {
    const f = family('attention_kind')
    const ranks = f.values.map(v => f.rank![v])
    expect(ranks).toEqual([...ranks].sort((a, b) => a - b))
    expect(new Set(ranks).size).toBe(ranks.length)
  })
})
