import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { summarizeToolChange } from '../../src/utils/reviewPresentation'
import type { ReviewTool } from '../../src/types/api'

// Spec 120 FR-007: the Go structural gate (internal/runtime/tool_contract_delta.go)
// and the Web review differ (summarizeToolChange / schemaLines) share one
// fixture. The Go table test pins the exact signals; this test pins that every
// case the gate HOLDS produces at least one review line, so a held change is
// never invisible on the review screen. The Web differ is top-level only and
// may report a nested change as "Parameter x changed"; that is a line too.
interface Case {
  name: string
  before?: unknown; after?: unknown; before_text?: string; after_text?: string
  before_output?: unknown; after_output?: unknown; before_output_text?: string; after_output_text?: string
  signals: string[]
  /** Held by the Go gate but not representable by the Web differ (JS number precision); the chip carries the signal. */
  web_invisible?: boolean
}

const fixture = JSON.parse(readFileSync(resolve(__dirname, '../../../internal/runtime/testdata/contract_delta_cases.json'), 'utf8')) as { cases: Case[] }

const parse = (raw: unknown, text: string | undefined): unknown => {
  if (text !== undefined) {
    if (text.trim() === '') return undefined
    try { return JSON.parse(text) } catch { return text }
  }
  return raw
}

function tool(c: Case): ReviewTool {
  return {
    name: 't', description: 'd', approval_status: 'changed',
    input_schema: parse(c.after, c.after_text),
    output_schema: parse(c.after_output, c.after_output_text),
    previous: { description: 'd', input_schema: parse(c.before, c.before_text), output_schema: parse(c.before_output, c.before_output_text) },
  } as unknown as ReviewTool
}

describe('contract_delta_cases.json against the Web review differ', () => {
  it('has the held and the passing cases', () => {
    expect(fixture.cases.length).toBeGreaterThan(30)
    expect(fixture.cases.some(c => c.signals.length > 0)).toBe(true)
    expect(fixture.cases.some(c => c.signals.length === 0)).toBe(true)
  })

  for (const c of fixture.cases) {
    if (!c.signals.length || c.web_invisible) continue
    const schemaSignal = c.signals.some(s => s.startsWith('GATE-SCHEMA-SHAPE') || s.startsWith('GATE-OUTPUT-SCHEMA'))
    if (!schemaSignal) continue
    it(`held: ${c.name} is visible on the review screen`, () => {
      const sections = summarizeToolChange(tool(c))
      const lines = sections.flatMap(s => s.lines)
      expect(lines.length, `no review line for ${c.name}`).toBeGreaterThan(0)
    })
  }
})
